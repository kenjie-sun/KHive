package voiceclient

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"github.com/1239t/vowifi-go/runtimehost/messaging"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const ussdContentType = "application/vnd.3gpp.ussd+xml"

var ussdDialString = regexp.MustCompile(`^[0-9*#+]{1,182}$`)

type ussdXML struct {
	XMLName  xml.Name `xml:"ussd-data"`
	Language string   `xml:"language,omitempty"`
	Text     string   `xml:"ussd-string,omitempty"`
	Error    string   `xml:"error-code,omitempty"`
}
type ussdSession struct {
	id, callID string
	dialog     *sipgo.DialogClientSession
	ready      chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	results    chan messaging.USSDResult
	timer      *time.Timer
	waiting    bool
	closed     bool
	once       sync.Once
}

func encodeUSSD(text string) ([]byte, error) {
	if !utf8.ValidString(text) || len([]rune(text)) > 182 {
		return nil, fmt.Errorf("USSD input must contain at most 182 characters")
	}
	body, err := xml.Marshal(ussdXML{Language: "en", Text: text})
	return append([]byte(xml.Header), body...), err
}
func decodeUSSD(contentType string, body []byte) (ussdXML, string, error) {
	if len(body) > 64*1024 {
		return ussdXML{}, "", fmt.Errorf("USSD body too large")
	}
	typ, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ussdXML{}, "", err
	}
	if typ == "multipart/mixed" {
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for n := 0; n < 8; n++ {
			part, err := reader.NextPart()
			if err != nil {
				return ussdXML{}, "", err
			}
			data, err := io.ReadAll(io.LimitReader(part, 64*1024+1))
			_ = part.Close()
			if err != nil {
				return ussdXML{}, "", err
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), ussdContentType) {
				return decodeUSSD(part.Header.Get("Content-Type"), data)
			}
		}
		return ussdXML{}, "", fmt.Errorf("USSD XML part missing")
	}
	if typ != ussdContentType {
		return ussdXML{}, "", fmt.Errorf("unsupported USSD content type")
	}
	var result ussdXML
	if err := xml.Unmarshal(body, &result); err != nil {
		return result, "", err
	}
	return result, string(body), nil
}
func (c *Client) SendUSSD(ctx context.Context, command string) (*messaging.USSDResult, error) {
	command = strings.TrimSpace(command)
	if !ussdDialString.MatchString(command) {
		return nil, fmt.Errorf("invalid USSD dial string")
	}
	c.ussdMu.Lock()
	if c.ussd != nil {
		c.ussdMu.Unlock()
		return nil, fmt.Errorf("USSD session already active")
	}
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	s := &ussdSession{done: make(chan struct{}), cancel: sessionCancel, id: uuid.NewString(), callID: uuid.NewString(), ready: make(chan struct{}), results: make(chan messaging.USSDResult, 8), waiting: true}
	c.ussd = s
	c.ussdMu.Unlock()
	failed := true
	defer func() {
		if failed {
			c.finishUSSD(s)
		}
	}()
	target := "sip:" + strings.ReplaceAll(command, "#", "%23") + ";phone-context=" + c.cfg.HomeDomain + "@" + c.cfg.HomeDomain + ";user=dialstring"
	req, err := c.newRequest(sip.INVITE, target, false)
	if err != nil {
		return nil, err
	}
	req.AppendHeader(sip.NewHeader("Call-ID", s.callID))
	req.AppendHeader(sip.NewHeader("Recv-Info", "g.3gpp.ussd"))
	req.AppendHeader(sip.NewHeader("Accept", ussdContentType+", application/sdp, multipart/mixed"))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	ipType := "IP4"
	if c.cfg.LocalIP.To4() == nil {
		ipType = "IP6"
	}
	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN %s %s\r\ns=USSD\r\nc=IN %s %s\r\nt=0 0\r\nm=audio 0 RTP/AVP 0\r\n", time.Now().Unix(), ipType, c.cfg.LocalIP, ipType, c.cfg.LocalIP)
	part, _ := writer.CreatePart(textproto.MIMEHeader{"Content-Type": []string{"application/sdp"}})
	_, _ = part.Write([]byte(sdp))
	data, _ := encodeUSSD(command)
	part, _ = writer.CreatePart(textproto.MIMEHeader{"Content-Type": []string{ussdContentType}, "Content-Disposition": []string{"render;handling=optional"}})
	_, _ = part.Write(data)
	_ = writer.Close()
	req.AppendHeader(sip.NewHeader("Content-Type", "multipart/mixed; boundary="+writer.Boundary()))
	req.SetBody(body.Bytes())
	dialog, err := c.ussdDialogs.WriteInvite(sessionCtx, req)
	if err != nil {
		return nil, err
	}
	c.ussdMu.Lock()
	if s.closed {
		c.ussdMu.Unlock()
		_ = dialog.Close()
		return nil, context.Canceled
	}
	s.dialog = dialog
	c.ussdMu.Unlock()
	if err := dialog.WaitAnswer(sessionCtx, sipgo.AnswerOptions{}); err != nil {
		return nil, err
	}
	if err := dialog.WriteAck(sessionCtx, c.ussdDialogRequest(s, sip.ACK)); err != nil {
		return nil, err
	}
	close(s.ready)
	c.ussdMu.Lock()
	if s.closed {
		c.ussdMu.Unlock()
		return nil, context.Canceled
	}
	s.timer = time.AfterFunc(3*time.Minute, func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.CancelUSSD(cancelCtx, s.id)
	})
	c.ussdMu.Unlock()
	failed = false
	return c.waitUSSD(ctx, s)
}
func (c *Client) waitUSSD(ctx context.Context, s *ussdSession) (*messaging.USSDResult, error) {
	defer func() { c.ussdMu.Lock(); s.waiting = false; c.ussdMu.Unlock() }()
	select {
	case result := <-s.results:
		return &result, nil
	case <-s.done:
		select {
		case result := <-s.results:
			return &result, nil
		default:
			return nil, context.Canceled
		}
	case <-c.stopCh:
		return nil, fmt.Errorf("IMS client closed")
	case <-ctx.Done():
		cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.CancelUSSD(cancelCtx, s.id)
		return nil, ctx.Err()
	}
}
func (c *Client) ContinueUSSD(ctx context.Context, sessionID, input string) (*messaging.USSDResult, error) {
	data, err := encodeUSSD(input)
	if err != nil {
		return nil, err
	}
	c.ussdMu.Lock()
	s := c.ussd
	if s == nil || s.id != sessionID || s.closed {
		c.ussdMu.Unlock()
		return nil, fmt.Errorf("USSD session not found")
	}
	if s.waiting {
		c.ussdMu.Unlock()
		return nil, fmt.Errorf("USSD response already pending")
	}
	s.waiting = true
	c.ussdMu.Unlock()
	target := s.dialog.InviteRequest.Recipient
	if contact := s.dialog.InviteResponse.Contact(); contact != nil {
		target = contact.Address
	}
	req := sip.NewRequest(sip.INFO, target)
	req.SetTransport("TCP")
	req.SetDestination(c.cfg.PCSCFAddr)
	req.AppendHeader(sip.NewHeader("Info-Package", "g.3gpp.ussd"))
	req.AppendHeader(sip.NewHeader("Content-Type", ussdContentType))
	req.AppendHeader(sip.NewHeader("Content-Disposition", "Info-Package"))
	req.SetBody(data)
	if c.cfg.SecurityVerify != "" {
		req.AppendHeader(sip.NewHeader("Security-Verify", c.cfg.SecurityVerify))
	}
	res, err := s.dialog.Do(ctx, req)
	if err != nil || res.StatusCode >= 300 {
		c.finishUSSD(s)
		if err == nil {
			err = fmt.Errorf("USSD INFO failed: %d", res.StatusCode)
		}
		return nil, err
	}
	return c.waitUSSD(ctx, s)
}
func (c *Client) CancelUSSD(ctx context.Context, sessionID string) error {
	c.ussdMu.Lock()
	s := c.ussd
	if s == nil || s.id != sessionID {
		c.ussdMu.Unlock()
		return fmt.Errorf("USSD session not found")
	}
	c.ussdMu.Unlock()
	select {
	case <-s.ready:
	default:
		s.cancel()
		select {
		case <-s.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer c.finishUSSD(s)
	select {
	case <-s.ready:
		if s.dialog != nil {
			return s.dialog.WriteBye(ctx, c.ussdDialogRequest(s, sip.BYE))
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stopCh:
		return nil
	}
	return nil
}
func (c *Client) finishUSSD(s *ussdSession) {
	s.once.Do(func() {
		c.ussdMu.Lock()
		s.closed = true
		if s.timer != nil {
			s.timer.Stop()
		}
		if c.ussd == s {
			c.ussd = nil
		}
		dialog := s.dialog
		c.ussdMu.Unlock()
		s.cancel()
		if dialog != nil {
			_ = dialog.Close()
		}
		close(s.done)
	})
}
func (c *Client) handleUSSD(req *sip.Request, tx sip.ServerTransaction) {
	c.ussdMu.Lock()
	s := c.ussd
	valid := s != nil && req.CallID() != nil && req.CallID().Value() == s.callID
	c.ussdMu.Unlock()
	if !valid {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 481, "Call/Transaction Does Not Exist", nil))
		return
	}
	select {
	case <-s.ready:
	case <-c.stopCh:
		return
	case <-time.After(2 * time.Second):
		_ = tx.Respond(sip.NewResponseFromRequest(req, 500, "Dialog Not Ready", nil))
		return
	}
	if _, err := c.ussdDialogs.MatchRequestDialog(req); err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 481, "Call/Transaction Does Not Exist", nil))
		return
	}
	result := messaging.USSDResult{SessionID: s.id, DCS: 15, Status: 1}
	if len(req.Body()) > 0 {
		ct := req.GetHeader("Content-Type")
		if ct == nil {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 415, "Unsupported Media Type", nil))
			return
		}
		decoded, raw, err := decodeUSSD(ct.Value(), req.Body())
		if err != nil {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 400, "Invalid USSD XML", nil))
			return
		}
		result.Text = decoded.Text
		result.RawText = decoded.Text
		result.RawXML = raw
		if decoded.Error != "" {
			result.Status = 4
			result.Text = decoded.Error
		}
	}
	if req.Method == sip.BYE {
		if result.Status != 4 {
			result.Status = 0
		}
		result.SessionID = ""
		if err := s.dialog.ReadBye(req, tx); err != nil {
			return
		}
	} else {
		info := req.GetHeader("Info-Package")
		if info == nil || info.Value() != "g.3gpp.ussd" {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 469, "Bad Info Package", nil))
			return
		}
		if err := tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil)); err != nil {
			return
		}
	}
	select {
	case s.results <- result:
		if req.Method == sip.BYE {
			c.finishUSSD(s)
		}
	default:
		c.finishUSSD(s)
	}
}

// Keep every in-dialog method on the established P-CSCF security association.
func (c *Client) ussdDialogRequest(s *ussdSession, method sip.RequestMethod) *sip.Request {
	target := s.dialog.InviteRequest.Recipient
	if contact := s.dialog.InviteResponse.Contact(); contact != nil {
		target = contact.Address
	}
	req := sip.NewRequest(method, target)
	req.SetDestination(c.cfg.PCSCFAddr)
	req.SetTransport("TCP")
	if c.cfg.SecurityVerify != "" {
		req.AppendHeader(sip.NewHeader("Security-Verify", c.cfg.SecurityVerify))
	}
	return req
}
