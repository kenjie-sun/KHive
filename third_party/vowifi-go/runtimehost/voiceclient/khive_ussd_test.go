package voiceclient

import (
	"bufio"
	"context"
	"fmt"
	"github.com/1239t/vowifi-go/runtimehost/messaging"
	"github.com/emiago/sipgo/sip"
	"net"
	"strings"
	"testing"
	"time"
)

func TestKHiveUSSDMenuContinuationAndFinalBYE(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	initial, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	_ = remote.SetDeadline(time.Now().Add(8 * time.Second))
	reader := bufio.NewReader(remote)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c, err := Dial(ctx, Config{LocalIP: net.ParseIP("127.0.0.1"), LocalPort: initial.LocalAddr().(*net.TCPAddr).Port, InitialConn: initial, PCSCFAddr: listener.Addr().String(), PrivateID: "test@example.invalid", PublicURI: "sip:test@example.invalid", HomeDomain: "example.invalid", AKA: khiveAttachAKA{}, SkipRegister: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	results := make(chan *messaging.USSDResult, 1)
	errs := make(chan error, 1)
	go func() { result, err := c.SendUSSD(ctx, "*100#"); results <- result; errs <- err }()
	msg, err := readKHiveSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	invite, ok := msg.(*sip.Request)
	if !ok || invite.Method != sip.INVITE {
		t.Fatalf("invite %v", msg)
	}
	if !strings.Contains(invite.Recipient.String(), "%23;phone-context=example.invalid") || !strings.Contains(string(invite.Body()), "m=audio 0 RTP/AVP") {
		t.Fatalf("invalid USSD INVITE %v", invite)
	}
	response := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	response.To().Params.Add("tag", "khive-server")
	response.AppendHeader(sip.NewHeader("Contact", fmt.Sprintf("<sip:gateway@%s>", remote.LocalAddr())))
	if _, err := remote.Write([]byte(response.String())); err != nil {
		t.Fatal(err)
	}
	msg, err = readKHiveSIP(reader)
	if err != nil || msg.(*sip.Request).Method != sip.ACK {
		t.Fatalf("ACK %v %v", msg, err)
	}
	push := func(method sip.RequestMethod, text string, seq int) {
		t.Helper()
		body, _ := encodeUSSD(text)
		wire := fmt.Sprintf("%s sip:test@example.invalid SIP/2.0\r\nVia: SIP/2.0/TCP %s;branch=z9hG4bK-ussd-%d\r\nFrom: %s\r\nTo: %s\r\nCall-ID: %s\r\nCSeq: %d %s\r\nMax-Forwards: 70\r\nInfo-Package: g.3gpp.ussd\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", method, remote.LocalAddr(), seq, response.To().Value(), invite.From().Value(), invite.CallID().Value(), seq, method, ussdContentType, len(body))
		if _, err := remote.Write(append([]byte(wire), body...)); err != nil {
			t.Fatal(err)
		}
	}
	push(sip.INFO, "1. Balance", 1)
	msg, err = readKHiveSIP(reader)
	if err != nil || msg.(*sip.Response).StatusCode != 200 {
		t.Fatalf("INFO response %v %v", msg, err)
	}
	result := <-results
	if err := <-errs; err != nil || result == nil || result.Status != 1 || result.Text != "1. Balance" || result.SessionID == "" {
		t.Fatalf("menu %+v %v", result, err)
	}
	go func() { next, err := c.ContinueUSSD(ctx, result.SessionID, "1"); results <- next; errs <- err }()
	msg, err = readKHiveSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	info := msg.(*sip.Request)
	if info.Method != sip.INFO || info.Recipient.User != "gateway" || !strings.Contains(string(info.Body()), "<ussd-string>1</ussd-string>") {
		t.Fatalf("continuation %v", info)
	}
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(info, 200, "OK", nil).String())); err != nil {
		t.Fatal(err)
	}
	push(sip.BYE, "Balance: 5", 2)
	msg, err = readKHiveSIP(reader)
	if err != nil || msg.(*sip.Response).StatusCode != 200 {
		t.Fatalf("BYE response %v %v", msg, err)
	}
	result = <-results
	if err := <-errs; err != nil || result == nil || result.Status != 0 || result.Text != "Balance: 5" || result.SessionID != "" {
		t.Fatalf("final %+v %v", result, err)
	}
	_ = remote.Close()
	select {
	case <-c.Done():
		if c.Ready() {
			t.Fatal("closed socket still ready")
		}
	case <-time.After(time.Second):
		t.Fatal("connection loss not surfaced")
	}
}
func TestKHiveUSSDXMLValidation(t *testing.T) {
	body, err := encodeUSSD("1<&中文")
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := decodeUSSD(ussdContentType, body)
	if err != nil || value.Text != "1<&中文" {
		t.Fatalf("roundtrip %+v %v", value, err)
	}
	if _, _, err := decodeUSSD(ussdContentType, []byte("<wrong/>")); err == nil {
		t.Fatal("invalid XML root accepted")
	}
	if _, err := encodeUSSD(strings.Repeat("x", 183)); err == nil {
		t.Fatal("oversized USSD accepted")
	}
}

func TestKHiveUSSDCancelPendingInvite(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	initial, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	_ = remote.SetDeadline(time.Now().Add(5 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, Config{LocalIP: net.ParseIP("127.0.0.1"), LocalPort: initial.LocalAddr().(*net.TCPAddr).Port, InitialConn: initial, PCSCFAddr: listener.Addr().String(), PrivateID: "test@example.invalid", PublicURI: "sip:test@example.invalid", HomeDomain: "example.invalid", AKA: khiveAttachAKA{}, SkipRegister: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	sends := make(chan error, 1)
	go func() { _, err := c.SendUSSD(ctx, "*100#"); sends <- err }()
	reader := bufio.NewReader(remote)
	msg, err := readKHiveSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	invite := msg.(*sip.Request)
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(invite, 100, "Trying", nil).String())); err != nil {
		t.Fatal(err)
	}
	c.ussdMu.Lock()
	id := c.ussd.id
	c.ussdMu.Unlock()
	cancels := make(chan error, 1)
	go func() { cancels <- c.CancelUSSD(ctx, id) }()
	msg, err = readKHiveSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	req := msg.(*sip.Request)
	if req.Method != sip.CANCEL {
		t.Fatalf("cancel=%v", msg)
	}
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(req, 200, "OK", nil).String() + sip.NewResponseFromRequest(invite, 487, "Request Terminated", nil).String())); err != nil {
		t.Fatal(err)
	}
	if err := <-sends; err == nil {
		t.Fatal("cancelled USSD succeeded")
	}
	if err := <-cancels; err != nil {
		t.Fatal(err)
	}
	c.ussdMu.Lock()
	active := c.ussd != nil
	c.ussdMu.Unlock()
	if active {
		t.Fatal("cancel retained session")
	}
}
