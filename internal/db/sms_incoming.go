package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"strings"
	"sync"
	"time"
)

// SMSIncomingPart is a durable receive journal. Transport retransmissions are
// deduplicated by SIM identity and TPDU bytes, which include the SMSC timestamp.
type SMSIncomingPart struct {
	ID          uint   `gorm:"primaryKey"`
	IMSI        string `gorm:"column:imsi;uniqueIndex:idx_incoming_fingerprint,priority:1;index"`
	Fingerprint string `gorm:"uniqueIndex:idx_incoming_fingerprint,priority:2"`
	GroupID     string `gorm:"index"`
	AssemblyKey string `gorm:"index"`
	Seq         int
	Total       int
	Content     string
	Timestamp   time.Time
	CreatedAt   time.Time `gorm:"index"`
	Complete    bool
}

type IncomingSMSFragment struct {
	IMSI, DeviceID, Sender, LocalPhone, Content string
	TPDU                                        []byte
	FingerprintInput                            []byte
	Timestamp                                   time.Time
	Ref, RefBits, Total, Seq, DCS               int
	Suppress                                    bool
}
type IncomingSMSResult struct {
	Stored, Duplicate, Pending bool
	SMS                        SMS
}

var incomingSMSMu sync.Mutex

// StoreIncomingSMS commits a fragment, and only creates history/contact rows
// when the complete message is available. Returning nil permits an RP-ACK.
func StoreIncomingSMS(ctx context.Context, in IncomingSMSFragment) (IncomingSMSResult, error) {
	if DB == nil {
		return IncomingSMSResult{}, errors.New("SMS database unavailable")
	}
	in.IMSI = strings.TrimSpace(in.IMSI)
	in.Sender = strings.TrimSpace(in.Sender)
	identity := in.TPDU
	if len(in.FingerprintInput) > 0 {
		identity = append([]byte("decoded-qmi:"), in.FingerprintInput...)
	}
	if in.IMSI == "" || len(identity) == 0 || len(identity) > 4096 {
		return IncomingSMSResult{}, errors.New("invalid incoming SMS identity or TPDU")
	}
	if in.Total == 0 {
		in.Total = 1
		in.Seq = 1
	}
	if in.Total < 1 || in.Total > 255 || in.Seq < 1 || in.Seq > in.Total {
		return IncomingSMSResult{}, errors.New("invalid SMS concatenation sequence")
	}
	if in.Total > 1 && in.RefBits != 0 && in.RefBits != 8 && in.RefBits != 16 {
		return IncomingSMSResult{}, errors.New("invalid SMS reference width")
	}
	now := time.Now()
	if in.Timestamp.IsZero() {
		in.Timestamp = now
	}
	fingerprint := sha256.Sum256(identity)
	hash := hex.EncodeToString(fingerprint[:])
	key := fmt.Sprintf("%s|%s|%d|%d|%d|%d", in.IMSI, in.Sender, in.RefBits, in.Ref, in.Total, in.DCS)
	// Resolve metadata before the transaction; SQLite uses one connection.
	iccid := GetICCIDForIMSI(in.IMSI)
	phone := normalizeSMSLocalPhone(in.IMSI, 1, in.LocalPhone, in.Sender, in.LocalPhone)
	incomingSMSMu.Lock()
	defer incomingSMSMu.Unlock()
	var out IncomingSMSResult
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Retain the completed journal for seven days of network retransmissions.
		if err := tx.Where("created_at < ?", now.Add(-7*24*time.Hour)).Delete(&SMSIncomingPart{}).Error; err != nil {
			return err
		}
		var existing SMSIncomingPart
		err := tx.Where("imsi = ? AND fingerprint = ?", in.IMSI, hash).First(&existing).Error
		if err == nil {
			out.Duplicate = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		group := uuid.NewString()
		if in.Total > 1 {
			var candidates []SMSIncomingPart
			err := tx.Where("assembly_key = ? AND complete = ? AND created_at > ? AND timestamp BETWEEN ? AND ?", key, false, now.Add(-24*time.Hour), in.Timestamp.Add(-15*time.Minute), in.Timestamp.Add(15*time.Minute)).Order("id DESC").Limit(512).Find(&candidates).Error
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, candidate := range candidates {
				if seen[candidate.GroupID] {
					continue
				}
				seen[candidate.GroupID] = true
				conflict := false
				for _, part := range candidates {
					if part.GroupID == candidate.GroupID && part.Seq == in.Seq {
						conflict = true
						break
					}
				}
				if !conflict {
					group = candidate.GroupID
					break
				}
			}
		}
		var count int64
		if err := tx.Model(&SMSIncomingPart{}).Where("imsi = ? AND complete = ? AND created_at > ?", in.IMSI, false, now.Add(-24*time.Hour)).Count(&count).Error; err != nil {
			return err
		}
		if count >= 4096 {
			return errors.New("incoming SMS fragment capacity reached")
		}
		part := SMSIncomingPart{IMSI: in.IMSI, Fingerprint: hash, GroupID: group, AssemblyKey: key, Seq: in.Seq, Total: in.Total, Content: in.Content, Timestamp: in.Timestamp, CreatedAt: now}
		if err := tx.Create(&part).Error; err != nil {
			return err
		}
		var parts []SMSIncomingPart
		if err := tx.Where("group_id = ?", group).Order("seq ASC").Find(&parts).Error; err != nil {
			return err
		}
		if len(parts) != in.Total {
			out.Pending = true
			return nil
		}
		var body strings.Builder
		for idx, p := range parts {
			if p.Seq != idx+1 {
				return errors.New("SMS fragment sequence conflict")
			}
			body.WriteString(p.Content)
		}
		if err := tx.Model(&SMSIncomingPart{}).Where("group_id = ?", group).Update("complete", true).Error; err != nil {
			return err
		}
		if in.Suppress {
			return nil
		}
		sms := SMS{IMSI: in.IMSI, ICCID: iccid, Peer: in.Sender, LocalPhone: phone, Sender: in.Sender, Recipient: phone, Content: body.String(), Type: 1, Status: 0, Timestamp: parts[0].Timestamp.Truncate(time.Second)}
		if err := tx.Create(&sms).Error; err != nil {
			return err
		}
		if sms.Peer != "" {
			if err := upsertSMSContactFromSMS(tx, &sms); err != nil {
				return err
			}
		}
		out.Stored = true
		out.SMS = sms
		return nil
	})
	if err != nil {
		return IncomingSMSResult{}, err
	}
	return out, nil
}
