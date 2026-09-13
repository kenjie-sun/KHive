package voiceclient

import (
	"bufio"
	"context"
	"fmt"
	"github.com/1239t/vowifi-go/engine/sim"
	"github.com/emiago/sipgo/sip"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type khiveAttachAKA struct{}

func (khiveAttachAKA) CalculateAKA([]byte, []byte) (sim.AKAResult, error) {
	return sim.AKAResult{}, fmt.Errorf("unexpected SIM call")
}

type khiveIncomingStore struct {
	khiveSMSStore
	received chan string
}

func (s *khiveIncomingStore) ReceiveSMS(ctx context.Context, dev, imsi string, body []byte) ([]byte, error) {
	s.received <- imsi
	return []byte{2, body[1]}, nil
}
func readKHiveSIP(r *bufio.Reader) (sip.Message, error) {
	var b strings.Builder
	n := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		b.WriteString(line)
		if line == "\r\n" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			n, _ = strconv.Atoi(strings.TrimSpace(val))
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return sip.ParseMessage(append([]byte(b.String()), body...))
}
func TestKHivePortSDeliversSMSAndRepliesOnPortC(t *testing.T) {
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
	incoming, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	store := &khiveIncomingStore{received: make(chan string, 1)}
	c, err := Dial(context.Background(), Config{LocalIP: net.ParseIP("127.0.0.1"), LocalPort: initial.LocalAddr().(*net.TCPAddr).Port, InitialConn: initial, TCPListener: incoming, PCSCFAddr: remote.LocalAddr().String(), PrivateID: "test@example.invalid", PublicURI: "sip:test@example.invalid", HomeDomain: "example.invalid", IMSI: "TEST-SIM", AKA: khiveAttachAKA{}, SkipRegister: true, DeliveryStore: store})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if err := c.signaling.keepalive(); err != nil {
		t.Fatal(err)
	}
	ping := make([]byte, 4)
	if _, err := io.ReadFull(remote, ping); err != nil || string(ping) != "\r\n\r\n" {
		t.Fatalf("keepalive frame=%q error=%v", ping, err)
	}
	if _, err := remote.Write([]byte("\r\n")); err != nil {
		t.Fatal(err)
	}
	push, err := net.Dial("tcp", incoming.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer push.Close()
	_ = push.SetDeadline(time.Now().Add(5 * time.Second))
	request := fmt.Sprintf("MESSAGE sip:test@example.invalid SIP/2.0\r\nVia: SIP/2.0/TCP %s;branch=z9hG4bK-khive-test\r\nFrom: <sip:sender@example.invalid>;tag=remote\r\nP-Asserted-Identity: \"Gateway\" <sip:gateway@example.invalid>\r\nTo: <sip:test@example.invalid>\r\nCall-ID: khive-push\r\nCSeq: 1 MESSAGE\r\nMax-Forwards: 70\r\nContent-Type: application/vnd.3gpp.sms\r\nContent-Length: 2\r\n\r\n", push.LocalAddr().String())
	if _, err := push.Write(append([]byte(request), 1, 42)); err != nil {
		t.Fatal(err)
	}
	msg, err := readKHiveSIP(bufio.NewReader(push))
	if err != nil {
		t.Fatal(err)
	}
	response, ok := msg.(*sip.Response)
	if !ok || response.StatusCode != 202 {
		t.Fatalf("acceptance=%v", msg)
	}
	msg, err = readKHiveSIP(bufio.NewReader(remote))
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := msg.(*sip.Request)
	if !ok || ack.Method != sip.MESSAGE || string(ack.Body()) != string([]byte{2, 42}) || ack.Recipient.User != "gateway" {
		t.Fatalf("RP ack=%v", msg)
	}
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(ack, 202, "Accepted", nil).String())); err != nil {
		t.Fatal(err)
	}
	if imsi := <-store.received; imsi != "TEST-SIM" {
		t.Fatal(imsi)
	}
	done := make(chan error, 1)
	go func() { done <- c.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("SkipRegister close blocked")
	}
}

func TestKHiveEstablishedRegistrationRefreshAndChallengeFailure(t *testing.T) {
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
	msg, err := sip.ParseMessage([]byte("REGISTER sip:example.invalid SIP/2.0\r\nVia: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK-original\r\nFrom: <sip:test@example.invalid>;tag=local\r\nTo: <sip:test@example.invalid>\r\nCall-ID: established\r\nCSeq: 7 REGISTER\r\nExpires: 2\r\nContent-Length: 0\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(context.Background(), Config{LocalIP: net.ParseIP("127.0.0.1"), LocalPort: initial.LocalAddr().(*net.TCPAddr).Port, InitialConn: initial, PCSCFAddr: listener.Addr().String(), PrivateID: "test@example.invalid", PublicURI: "sip:test@example.invalid", HomeDomain: "example.invalid", AKA: khiveAttachAKA{}, SkipRegister: true, RegisterExpiry: 2 * time.Second, RefreshRequest: msg.(*sip.Request)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	reader := bufio.NewReader(remote)
	for i, code := range []int{200, 401} {
		msg, err := readKHiveSIP(reader)
		if err != nil {
			t.Fatal(err)
		}
		req := msg.(*sip.Request)
		if req.Method != sip.REGISTER || req.CallID().Value() != "established" || req.CSeq().SeqNo != uint32(8+i) {
			t.Fatalf("refresh=%v", req)
		}
		if _, err := remote.Write([]byte(sip.NewResponseFromRequest(req, code, "Test", nil).String())); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-c.Done():
		if c.Ready() {
			t.Fatal("401 kept ready")
		}
	case <-time.After(time.Second):
		t.Fatal("refresh failure did not trigger recovery")
	}
}
