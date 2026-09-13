package device

import (
	"bufio"
	"context"
	"fmt"
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vowifi-go/engine/sim"
	"github.com/1239t/vowifi-go/runtimehost/messaging"
	"github.com/1239t/vowifi-go/runtimehost/voiceclient"
	"github.com/emiago/sipgo/sip"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type khiveOutboundAKA struct{}

func (khiveOutboundAKA) CalculateAKA([]byte, []byte) (sim.AKAResult, error) {
	return sim.AKAResult{}, fmt.Errorf("unexpected SIM use")
}
func readKHiveOutboundSIP(r *bufio.Reader) (sip.Message, error) {
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
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(k, "Content-Length") {
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return sip.ParseMessage(append([]byte(b.String()), body...))
}
func TestKHiveOutgoingSMSRealStoreEarlyReportAndPartialFailure(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c, err := voiceclient.Dial(ctx, voiceclient.Config{LocalIP: net.ParseIP("127.0.0.1"), LocalPort: initial.LocalAddr().(*net.TCPAddr).Port, InitialConn: initial, PCSCFAddr: listener.Addr().String(), PrivateID: "test@example.invalid", PublicURI: "sip:test@example.invalid", HomeDomain: "example.invalid", IMSI: "SIM-OUT", DeviceID: "test-device", AKA: khiveOutboundAKA{}, SkipRegister: true, DeliveryStore: vowifiDeliveryStore{}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	outcomes := make(chan messaging.SendOutcome, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := c.SendSMS(ctx, "+1234", "two parts", []messaging.SMSPart{{TargetURI: "tel:+999", RPMR: 41, Body: []byte{0, 41}}, {TargetURI: "tel:+999", RPMR: 42, Body: []byte{0, 42}}})
		outcomes <- out
		errs <- err
	}()
	reader := bufio.NewReader(remote)
	msg, err := readKHiveOutboundSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	first := msg.(*sip.Request)
	if first.Recipient.String() != "tel:+999" || first.To().Address.String() != "tel:+999" {
		t.Fatalf("wrong SC PSI: %v", first)
	}
	var delivery db.SMSDelivery
	if err := db.DB.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	status, err := db.GetSMSDeliveryStatus(delivery.MessageID)
	if err != nil || len(status.Parts) != 2 || status.Parts[0].PartNo != 1 || status.IMSI != "SIM-OUT" {
		t.Fatalf("parts must exist before send: %+v %v", status, err)
	}
	// 100 must not finish the send transaction. Send the RP-ACK before the 202.
	_, err = remote.Write([]byte(sip.NewResponseFromRequest(first, 100, "Trying", nil).String()))
	if err != nil {
		t.Fatal(err)
	}
	report := fmt.Sprintf("MESSAGE sip:test@example.invalid SIP/2.0\r\nVia: SIP/2.0/TCP %s;branch=z9hG4bK-early\r\nFrom: <sip:gw@example.invalid>;tag=gw\r\nTo: <sip:test@example.invalid>\r\nCall-ID: report-early\r\nIn-Reply-To: %s\r\nCSeq: 1 MESSAGE\r\nMax-Forwards: 70\r\nContent-Type: application/vnd.3gpp.sms\r\nContent-Length: 2\r\n\r\n", remote.LocalAddr(), first.CallID().Value())
	if _, err := remote.Write(append([]byte(report), 3, 41)); err != nil {
		t.Fatal(err)
	}
	msg, err = readKHiveOutboundSIP(reader)
	if err != nil || msg.(*sip.Response).StatusCode != 200 {
		t.Fatalf("early report acceptance=%v err=%v", msg, err)
	}
	status, err = db.GetSMSDeliveryStatus(delivery.MessageID)
	if err != nil || status.State != "partial_ack" || status.Acks != 1 {
		t.Fatalf("early report state=%+v err=%v", status, err)
	}
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(first, 202, "Accepted", nil).String())); err != nil {
		t.Fatal(err)
	}
	msg, err = readKHiveOutboundSIP(reader)
	if err != nil {
		t.Fatal(err)
	}
	second := msg.(*sip.Request)
	if _, err := remote.Write([]byte(sip.NewResponseFromRequest(second, 503, "Unavailable", nil).String())); err != nil {
		t.Fatal(err)
	}
	out := <-outcomes
	if err := <-errs; err == nil || out.MessageID != delivery.MessageID || out.DeliveryState != "failed" {
		t.Fatalf("failure result=%+v err=%v", out, err)
	}
	status, err = db.GetSMSDeliveryStatus(out.MessageID)
	if err != nil || status.State != "failed" || status.Acks != 1 || status.Parts[0].State != "acked" || status.Parts[1].State != "failed" {
		t.Fatalf("partial failure=%+v err=%v", status, err)
	}
}
func TestKHiveSMSMissingReportExpiresAfterRestart(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	at := time.Now().Add(-3 * time.Minute)
	if err := db.CreateSMSDelivery("timeout", "SIM-OUT", "test-device", "+1234", "text", 1, at); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSMSDeliveryPart("timeout", 1, "old-call", 1, "pending", at); err != nil {
		t.Fatal(err)
	}
	status, err := db.GetSMSDeliveryStatus("timeout")
	if err != nil || status.State != "unconfirmed" || status.Parts[0].State != "timeout" {
		t.Fatalf("expired=%+v err=%v", status, err)
	}
	// A legacy timeout-only failure must also be corrected on read, while the
	// network submission remains unconfirmed until a real report arrives.
	if err := db.UpdateSMSDeliveryState("timeout", "failed", "submit report timeout", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	status, err = db.GetSMSDeliveryStatus("timeout")
	if err != nil || status.State != "unconfirmed" {
		t.Fatalf("legacy timeout=%+v err=%v", status, err)
	}
	if _, err := db.MarkSMSDeliveryPartReport("old-call", "late-report", "test-device", 1, "acked", 200, 0, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	status, err = db.GetSMSDeliveryStatus("timeout")
	if err != nil || status.State != "acked" || status.Acks != 1 {
		t.Fatalf("late confirmation=%+v err=%v", status, err)
	}
}
