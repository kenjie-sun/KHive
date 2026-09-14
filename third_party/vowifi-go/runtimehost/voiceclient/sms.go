package voiceclient

import (
	"context"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/1239t/swu-go/pkg/logger"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"

	"github.com/1239t/vowifi-go/runtimehost/messaging"
)

const smsContentType = "application/vnd.3gpp.sms"

// SendSMS submits each of parts as a separate SIP MESSAGE (3GPP TS 24.341),
// expecting the immediate 202 Accepted per part, and records delivery
// tracking via DeliveryStore. It does not wait for the delivery report
// (RP-ACK/RP-ERROR) -- that arrives asynchronously as a separate incoming
// MESSAGE and is handled by handleIncomingMessage, matching how vohive's own
// DeliveryStore.MarkSMSDeliveryPartReport is designed to be called well
// after the initial submission returns (see its In-Reply-To/Call-ID/
// rp_mr-plus-time-window correlation cascade).
func (c *Client) SendSMS(ctx context.Context, peer, content string, parts []messaging.SMSPart) (out messaging.SendOutcome, sendErr error) {
	if len(parts) == 0 {
		return out, fmt.Errorf("voiceclient: no parts to send")
	}

	for _, part := range parts {
		if !strings.HasPrefix(part.TargetURI, "sip:") && !strings.HasPrefix(part.TargetURI, "tel:") {
			return out, fmt.Errorf("voiceclient: missing service-centre PSI")
		}
	}

	messageID := uuid.NewString()
	now := time.Now()
	out = messaging.SendOutcome{MessageID: messageID, PartsTotal: len(parts), DeliveryState: "pending"}
	store := c.cfg.DeliveryStore
	if store != nil {
		if err := store.CreateSMSDelivery(messageID, c.cfg.IMSI, c.cfg.DeviceID, peer, content, len(parts), now); err != nil {
			return out, fmt.Errorf("voiceclient: CreateSMSDelivery: %w", err)
		}
	}
	defer func() {
		if sendErr != nil {
			out.DeliveryState = "failed"
		}
		if store == nil {
			return
		}
		if sendErr != nil {
			if status, err := store.GetSMSDeliveryStatus(messageID); err == nil {
				for _, part := range status.Parts {
					if part.State == "pending" {
						_, _ = store.MarkSMSDeliveryPartReport(part.CallID, "", c.cfg.DeviceID, part.RPMR, "failed", 0, 0, sendErr.Error(), time.Now())
					}
				}
			}
			_ = store.UpdateSMSDeliveryState(messageID, "failed", sendErr.Error(), -1, time.Now())
		}
		if status, err := store.GetSMSDeliveryStatus(messageID); err == nil {
			out.DeliveryState = status.State
		}
	}()
	// Persist all part associations before sending any bytes. An early report
	// cannot race creation or make a partially submitted multipart message complete.
	requests := make([]*sip.Request, len(parts))
	for i, part := range parts {
		req, err := c.newRequest(sip.MESSAGE, part.TargetURI, false)
		if err != nil {
			return out, err
		}
		req.AppendHeader(sip.NewHeader("Call-ID", uuid.NewString()))
		req.AppendHeader(sip.NewHeader("Content-Type", smsContentType))

		if store != nil {
			if err := store.UpsertSMSDeliveryPart(messageID, i+1, req.CallID().Value(), int(part.RPMR), "pending", now); err != nil {
				return out, fmt.Errorf("voiceclient: UpsertSMSDeliveryPart: %w", err)
			}
		}
		body := part.Body
		if preparer, ok := store.(messaging.TerminalSubmitPreparer); ok {
			body, err = preparer.PrepareSMSTerminalSubmit(messageID, i+1, body)
			if err != nil {
				return out, fmt.Errorf("voiceclient: prepare terminal report: %w", err)
			}
		}
		req.SetBody(body)
		requests[i] = req
	}
	for i, req := range requests {
		res, err := c.doTransaction(ctx, req)
		if err != nil {
			return out, fmt.Errorf("voiceclient: submit part %d: %w", i+1, err)
		}
		if res.StatusCode != 202 && res.StatusCode != 200 {
			return out, fmt.Errorf("voiceclient: submit part %d: unexpected response %d %s", i+1, res.StatusCode, res.Reason)
		}
		logger.Info("IMS SMS submission accepted", logger.Int("part", i+1), logger.Int("sip_code", res.StatusCode), logger.Int("response_bytes", len(res.Body())))
	}
	return out, nil
}

// rpKind is the outer RP envelope's message type, per 3GPP TS 24.011 --
// just enough to recognize a delivery report and its cause, not a full TPDU
// decode. See the package doc comment for why the TPDU layer itself stays
// in vohive.
type rpKind int

const (
	rpKindUnknown rpKind = iota
	rpKindAck
	rpKindError
)

type deliveryReport struct {
	kind  rpKind
	rpMR  byte
	cause int
}

// classifyRPEnvelope reads the RP-level framing needed to recognize an
// RP-ACK/RP-ERROR and its RP-MR/cause. Message type octet values: 0x02/0x03
// = RP-ACK, 0x04/0x05 = RP-ERROR (MS->Network / Network->MS pairs
// respectively); a delivery report for our own submission arrives as the
// Network->MS variant (0x03 or 0x05), but both are accepted here since the
// direction doesn't affect how we correlate/record it. Cause parsing
// mirrors 3GPP TS 24.011: cause IE is [length][value], value's low 7 bits
// are the cause code.
func classifyRPEnvelope(body []byte) (deliveryReport, error) {
	if len(body) < 2 {
		return deliveryReport{}, fmt.Errorf("voiceclient: RP body too short (%d bytes)", len(body))
	}
	switch body[0] {
	case 0x02, 0x03:
		return deliveryReport{kind: rpKindAck, rpMR: body[1]}, nil
	case 0x04, 0x05:
		if len(body) < 4 {
			return deliveryReport{}, fmt.Errorf("voiceclient: RP-ERROR body too short (%d bytes)", len(body))
		}
		causeIELen := int(body[2])
		if causeIELen <= 0 || 3+causeIELen > len(body) {
			return deliveryReport{}, fmt.Errorf("voiceclient: RP-ERROR cause IE out of range")
		}
		cause := int(body[3] & 0x7F)
		return deliveryReport{kind: rpKindError, rpMR: body[1], cause: cause}, nil
	default:
		return deliveryReport{}, fmt.Errorf("voiceclient: unrecognized RP message type 0x%02x", body[0])
	}
}

// handleIncomingMessage persists SMS data and correlated submit reports before acceptance.
func (c *Client) handleIncomingMessage(req *sip.Request, tx sip.ServerTransaction) {
	ct := req.GetHeader("Content-Type")
	mediaType := ""
	if ct != nil {
		mediaType, _, _ = mime.ParseMediaType(ct.Value())
	}
	if !strings.EqualFold(mediaType, smsContentType) {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 415, "Unsupported Media Type", nil))
		return
	}
	if body := req.Body(); len(body) >= 2 {
		logger.Info("IMS SMS envelope received", logger.Int("rp_type", int(body[0])), logger.Int("rp_mr", int(body[1])), logger.Int("bytes", len(body)))
	}

	if body := req.Body(); len(body) >= 2 && body[0] == 1 {
		c.handleIncomingSMSData(req, tx)
		return
	}
	report, err := classifyRPEnvelope(req.Body())
	if err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}

	if c.cfg.DeliveryStore != nil {
		inReplyTo := ""
		if irt := req.GetHeader("In-Reply-To"); irt != nil {
			inReplyTo = irt.Value()
		}
		callID := ""
		if req.CallID() != nil {
			callID = req.CallID().Value()
		}

		state := "acked"
		if report.kind == rpKindError {
			state = "failed"
		}
		match, err := c.cfg.DeliveryStore.MarkSMSDeliveryPartReport(
			inReplyTo, callID, c.cfg.DeviceID, int(report.rpMR),
			state, 200, report.cause, "", time.Now(),
		)
		if err != nil {
			logger.Warn("IMS SMS report correlation failed", logger.Int("rp_mr", int(report.rpMR)), logger.String("error", err.Error()))
			_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
			return
		}
		if err := c.cfg.DeliveryStore.RecomputeSMSDelivery(match.MessageID, time.Now()); err != nil {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
			return
		}
	} else {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}

	_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
}

func (c *Client) handleIncomingSMSData(req *sip.Request, tx sip.ServerTransaction) {
	receiver, ok := c.cfg.DeliveryStore.(messaging.IncomingSMSReceiver)
	if !ok || req.From() == nil || req.CallID() == nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	reply, err := receiver.ReceiveSMS(ctx, c.cfg.DeviceID, c.cfg.IMSI, req.Body())
	if err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}
	// Persistence precedes both the SIP acceptance and the RP-layer response.
	if err := tx.Respond(sip.NewResponseFromRequest(req, 202, "Accepted", nil)); err != nil {
		return
	}
	target := req.From().Address.String()
	if asserted := req.GetHeader("P-Asserted-Identity"); asserted != nil {
		var address sip.Uri
		params := sip.NewParams()
		if _, err := sip.ParseAddressValue(asserted.Value(), &address, &params); err == nil {
			target = address.String()
		}
	}
	ack, err := c.newRequest(sip.MESSAGE, target, false)
	if err != nil {
		return
	}
	ack.AppendHeader(sip.NewHeader("Content-Type", smsContentType))
	ack.AppendHeader(sip.NewHeader("In-Reply-To", req.CallID().Value()))
	ack.SetBody(reply)
	_, _ = c.doTransaction(ctx, ack)
}
