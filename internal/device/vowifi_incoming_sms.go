package device

import (
	"context"
	"errors"
	"fmt"
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vohive/internal/smsnotify"
	"github.com/1239t/vohive/pkg/smscodec"
	smspdu "github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
)

func (s vowifiDeliveryStore) ReceiveSMS(ctx context.Context, deviceID, imsi string, body []byte) ([]byte, error) {
	if len(body) < 2 {
		return nil, fmt.Errorf("RP-DATA too short")
	}
	mr := body[1]
	if body[0] != 1 {
		return smscodec.BuildRPError(mr, 97), nil
	}
	_, _, _, raw, err := smscodec.ParseRPDataWithAddresses(body)
	if err != nil {
		return smscodec.BuildRPError(mr, 95), nil
	}
	if err := persistIncomingTPDU(ctx, s.pool, deviceID, imsi, raw, "VoWiFi"); err != nil {
		if errors.Is(err, errInvalidIncomingPDU) {
			return smscodec.BuildRPError(mr, 95), nil
		}
		return nil, err
	}
	return smscodec.BuildRPAck(mr), nil
}

var errInvalidIncomingPDU = errors.New("invalid SMS-DELIVER")

func persistIncomingTPDU(ctx context.Context, pool *Pool, deviceID, imsi string, raw []byte, source string) error {
	if trimmed, ok := smscodec.TrimDeliverTPDUToDeclaredLength(raw); ok {
		raw = trimmed
	}
	decoded, err := smspdu.Unmarshal(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidIncomingPDU, err)
	}
	if decoded.SmsType() == tpdu.SmsStatusReport {
		if decoded.FirstOctet.SRQ() {
			return nil
		} // A command report is not a SUBMIT report.
		return db.StoreSMSStatusReport(ctx, db.SMSStatusReport{IMSI: imsi, Peer: decoded.RA.Number(), TPMR: int(decoded.MR), Status: int(decoded.ST), ServiceCentreAt: decoded.SCTS.Time, DischargedAt: decoded.DT.Time}, raw)
	}
	if decoded.SmsType() != tpdu.SmsDeliver {
		return errInvalidIncomingPDU
	}
	sender, text, at, concat, err := smscodec.DecodeDeliverTPDU(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidIncomingPDU, err)
	}
	result, err := db.StoreIncomingSMS(ctx, db.IncomingSMSFragment{IMSI: imsi, DeviceID: deviceID, Source: source, Sender: sender, Content: text, TPDU: raw, Timestamp: at, Ref: concat.Ref, RefBits: concat.RefBits, Total: concat.Total, Seq: concat.Seq, DCS: int(decoded.DCS), Suppress: smsnotify.ShouldSuppressReceivedSMS(text)})
	if err != nil {
		return err
	}
	notifyStoredSMS(pool, deviceID, source, result)
	return nil
}
func notifyStoredSMS(pool *Pool, deviceID, source string, result db.IncomingSMSResult) {
	if !result.Stored || pool == nil {
		return
	}
	notifier := pool.getNotifier()
	if notifier == nil {
		return
	}
	if withSource, ok := notifier.(SMSSourceNotifier); ok {
		withSource.NotifySMSWithSource(deviceID, result.SMS.Sender, result.SMS.Content, source, result.SMS.Timestamp)
	} else {
		notifier.NotifySMS(deviceID, result.SMS.Sender, result.SMS.Content, result.SMS.Timestamp)
	}
}
