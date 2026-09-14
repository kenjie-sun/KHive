package device

import (
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vohive/pkg/smscodec"
	"github.com/1239t/vowifi-go/runtimehost/messaging"
	"github.com/google/uuid"
	"strings"
	"time"
)

// SendSMSTracked adds terminal-report tracking to AT backends. QMI/MBIM retain
// their existing submission contract until their network references are exposed.
func (w *Worker) SendSMSTracked(phone, message string, opts smscodec.SubmitOptions) (messaging.SendOutcome, error) {
	out := messaging.SendOutcome{}
	if db.DB == nil || (w.Backend != nil && w.Backend.Mode() != "at") {
		return out, w.sendSMSWithOptions(phone, message, opts)
	}
	imsi := strings.TrimSpace(w.GetIMSI())
	if imsi == "" {
		return out, w.sendSMSWithOptions(phone, message, opts)
	}
	opts.RequestStatusReport = true
	opts.BeforeSubmit = func(total int) error {
		out = messaging.SendOutcome{MessageID: uuid.NewString(), PartsTotal: total, DeliveryState: "pending"}
		now := time.Now()
		if err := db.CreateSMSDelivery(out.MessageID, imsi, w.ID, strings.TrimSpace(phone), message, total, now); err != nil {
			return err
		}
		for n := 1; n <= total; n++ {
			if err := db.UpsertSMSDeliveryPart(out.MessageID, n, "", -1, "pending", now); err != nil {
				return err
			}
		}
		return nil
	}
	opts.OnSubmitted = func(partNo, reference int) error {
		return db.AssociateATTerminalReference(out.MessageID, partNo, reference)
	}
	err := w.sendSMSWithOptions(phone, message, opts)
	if out.MessageID != "" {
		if err != nil {
			out.DeliveryState = "unconfirmed"
			_ = db.UpdateSMSDeliveryState(out.MessageID, "unconfirmed", "cellular submission incomplete", -1, time.Now())
		} else {
			if recomputeErr := db.RecomputeSMSDelivery(out.MessageID, time.Now()); recomputeErr != nil {
				return out, recomputeErr
			}
			out.DeliveryState = "acked"
		}
	}
	return out, err
}
