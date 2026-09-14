package db

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SMSStatusReport is a durable inbox, including reports received before association.
// RP submission status is deliberately never changed by this table.
type SMSStatusReport struct {
	ID              string    `gorm:"primaryKey" json:"id"`
	IMSI            string    `gorm:"index" json:"-"`
	Peer            string    `json:"peer"`
	TPMR            int       `gorm:"column:tp_mr;index" json:"tp_mr"`
	Status          int       `json:"tp_status"`
	ServiceCentreAt time.Time `json:"service_centre_at"`
	DischargedAt    time.Time `json:"discharged_at"`
	ReceivedAt      time.Time `json:"received_at"`
	MessageID       string    `gorm:"index" json:"message_id"`
	PartNo          int       `json:"part_no"`
	MatchState      string    `json:"match_state"`
}

// ReserveSMSTerminalReference persists an unused TP-MR before any wire submission.
// References are scoped to a SIM, survive restarts, and cannot wrap onto a recent send.
func ReserveSMSTerminalReference(messageID string, partNo int) (byte, error) {
	if DB == nil {
		return 0, fmt.Errorf("SMS database unavailable")
	}
	var selected byte
	err := DB.Transaction(func(tx *gorm.DB) error {
		var message SMSDelivery
		if err := tx.Where("message_id = ?", messageID).First(&message).Error; err != nil {
			return err
		}
		if strings.TrimSpace(message.IMSI) == "" {
			return fmt.Errorf("missing SIM identity")
		}
		var parts []SMSDeliveryPart
		if err := tx.Joins("JOIN sms_delivery d ON d.message_id = sms_delivery_part.message_id").Where("d.imsi = ? AND sms_delivery_part.sent_at >= ? AND sms_delivery_part.tp_mr IS NOT NULL", message.IMSI, time.Now().Add(-24*time.Hour)).Find(&parts).Error; err != nil {
			return err
		}
		used := [256]bool{}
		for _, p := range parts {
			if p.TPMR != nil && *p.TPMR >= 0 && *p.TPMR < 256 {
				used[*p.TPMR] = true
			}
		}
		for n := 0; n < 256; n++ {
			if !used[n] {
				selected = byte(n)
				r := tx.Model(&SMSDeliveryPart{}).Where("message_id = ? AND part_no = ? AND tp_mr IS NULL", messageID, partNo).Updates(map[string]any{"tp_mr": n, "terminal_state": "pending"})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return fmt.Errorf("missing or already reserved SMS part")
				}
				return nil
			}
		}
		return fmt.Errorf("SMS TP-MR capacity exhausted for this SIM; try later")
	})
	return selected, err
}

// TerminalReportState follows TS 23.040 9.2.3.15. Only 0x00 explicitly proves
// delivery to the recipient; other completed/reserved statuses remain distinct.
func TerminalReportState(status int) string {
	switch {
	case status == 0:
		return "delivered"
	case status < 0x20:
		return "completed"
	case status < 0x40:
		return "temporary_failure"
	case status < 0x80:
		return "failed"
	default:
		return "unknown"
	}
}
func StoreSMSStatusReport(ctx context.Context, report SMSStatusReport, raw []byte) error {
	if DB == nil {
		return fmt.Errorf("SMS database unavailable")
	}
	if report.IMSI == "" || report.Peer == "" || report.TPMR < 0 || report.TPMR > 255 || report.Status < 0 || report.Status > 255 {
		return fmt.Errorf("invalid SMS status report identity")
	}
	report.ID = fmt.Sprintf("%x", sha256.Sum256(append([]byte(report.IMSI+"\x00"), raw...)))
	report.ReceivedAt = time.Now()
	report.MatchState = "unmatched"
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report).Error; err != nil {
			return err
		}
		return matchSMSStatusReport(tx, report.ID)
	})
}
func matchSMSStatusReport(tx *gorm.DB, id string) error {
	var report SMSStatusReport
	if err := tx.First(&report, "id = ?", id).Error; err != nil {
		return err
	}
	if report.MessageID != "" {
		return nil
	}
	var parts []SMSDeliveryPart
	// Exact recipient and home SIM identity: never suffix/prefix-match phone numbers.
	// Keep all candidates, including completed ones, to reject reference-reuse ambiguity.
	err := tx.Joins("JOIN sms_delivery d ON d.message_id = sms_delivery_part.message_id").Where("d.imsi = ? AND d.peer = ? AND sms_delivery_part.tp_mr = ? AND sms_delivery_part.sent_at BETWEEN ? AND ?", report.IMSI, report.Peer, report.TPMR, report.ServiceCentreAt.Add(-24*time.Hour), report.ServiceCentreAt.Add(5*time.Minute)).Find(&parts).Error
	if err != nil {
		return err
	}
	if len(parts) != 1 {
		state := "unmatched"
		if len(parts) > 1 {
			state = "ambiguous"
		}
		return tx.Model(&report).Update("match_state", state).Error
	}
	p := parts[0]
	if err := tx.Model(&report).Updates(map[string]any{"message_id": p.MessageID, "part_no": p.PartNo, "match_state": "matched"}).Error; err != nil {
		return err
	}
	// A late temporary report cannot regress a final outcome; older reports remain in the inbox.
	final := p.TerminalState == "delivered" || p.TerminalState == "failed" || p.TerminalState == "completed"
	if final || (p.TerminalReportAt != nil && report.DischargedAt.Before(*p.TerminalReportAt)) {
		return nil
	}
	return tx.Model(&p).Updates(map[string]any{"terminal_state": TerminalReportState(report.Status), "tp_status": report.Status, "terminal_report_at": report.DischargedAt}).Error
}

// AssociateATTerminalReference uses the modem's returned +CMGS reference, which
// may differ from TP-MR in the submitted PDU. Reconcile reports that arrived early.
func AssociateATTerminalReference(messageID string, partNo, mr int) error {
	if DB == nil || mr < 0 || mr > 255 {
		return fmt.Errorf("invalid AT message reference")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&SMSDeliveryPart{}).Where("message_id = ? AND part_no = ?", messageID, partNo).Updates(map[string]any{"tp_mr": mr, "state": "acked", "terminal_state": "pending"})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return fmt.Errorf("SMS part not found")
		}
		var msg SMSDelivery
		if err := tx.First(&msg, "message_id = ?", messageID).Error; err != nil {
			return err
		}
		var reports []SMSStatusReport
		if err := tx.Where("imsi = ? AND peer = ? AND tp_mr = ? AND message_id = ''", msg.IMSI, msg.Peer, mr).Order("discharged_at asc").Find(&reports).Error; err != nil {
			return err
		}
		for _, report := range reports {
			if err := matchSMSStatusReport(tx, report.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
