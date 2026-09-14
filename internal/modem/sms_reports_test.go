package modem

import (
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/warthog618/sms/encoding/tpdu"
)

func directReportFixture(t *testing.T, smsc bool) (string, string) {
	t.Helper()
	now := time.Date(2026, 9, 14, 13, 32, 14, 0, time.UTC)
	r := tpdu.TPDU{Direction: tpdu.MT, FirstOctet: 2, MR: 73,
		RA: tpdu.Address{Addr: "1234", TOA: 0x91}, SCTS: tpdu.Timestamp{Time: now}, DT: tpdu.Timestamp{Time: now}, ST: 0}
	raw, err := r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte{0}
	if smsc {
		prefix = []byte{3, 0x91, 0x21, 0x43}
	}
	return fmt.Sprintf("+CDS: %d", len(raw)), hex.EncodeToString(append(prefix, raw...))
}

func TestKHiveSMSReportNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		accept smsReportMode
		fail   bool
		want   []string
	}{
		{"direct", smsReportsDirect, false, []string{"AT+CNMI=2,1,0,1,0"}},
		{"stored", smsReportsStored, false, []string{"AT+CNMI=2,1,0,1,0", "AT+CNMI=2,1,0,2,0"}},
		{"ordinary", smsReportsDisabled, false, []string{"AT+CNMI=2,1,0,1,0", "AT+CNMI=2,1,0,2,0", "AT+CNMI=2,1,0,0,0"}},
		{"failed", smsReportsDisabled, true, []string{"AT+CNMI=2,1,0,1,0", "AT+CNMI=2,1,0,2,0", "AT+CNMI=2,1,0,0,0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			mode, err := configureSMSReports(func(cmd string, _ time.Duration) (string, error) {
				calls = append(calls, cmd)
				if !tc.fail && cmd == fmt.Sprintf("AT+CNMI=2,1,0,%d,0", tc.accept) {
					return "OK", nil
				}
				return "", errors.New("unsupported")
			})
			if (err != nil) != tc.fail || mode != tc.accept || !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("mode=%v err=%v commands=%v", mode, err, calls)
			}
		})
	}
}

func TestKHiveDirectSMSReportInterleavesWithCMGS(t *testing.T) {
	for _, smsc := range []bool{false, true} {
		t.Run(fmt.Sprint(smsc), func(t *testing.T) {
			m := newRunningTestManager(t)
			m.port = &timeoutSerialPort{}
			m.cmdChanHigh = m.cmdChan
			header, pdu := directReportFixture(t, smsc)
			saved := make(chan string, 1)
			m.SetPDUCallback(func(raw string) error { saved <- raw; return nil })
			done := make(chan []string, 1)
			go func() {
				done <- respondToCommands(t, m, 1, func(req commandRequest) { req.respChan <- "+CSMS: 0,1,1,1" })
			}()
			for _, line := range []string{header, "+CMGS: 73", pdu, "OK"} {
				m.rxChan <- rxMsg{Data: line}
			}
			req := commandRequest{cmd: "AT+CMGS=23", timeout: time.Second, respChan: make(chan string, 1), errChan: make(chan error, 1)}
			m.handleCommand(req)
			select {
			case got := <-req.respChan:
				if got != "+CMGS: 73" {
					t.Fatalf("report contaminated response: %q", got)
				}
			default:
				t.Fatal("missing CMGS response")
			}
			select {
			case got := <-saved:
				if got != pdu {
					t.Fatal("wrong report persisted")
				}
			case <-time.After(time.Second):
				t.Fatal("report not persisted")
			}
			if got := <-done; !reflect.DeepEqual(got, []string{"AT+CSMS?"}) {
				t.Fatalf("commands %v", got)
			}
		})
	}
}

func TestKHiveDirectSMSReportCompletesAfterCommandOK(t *testing.T) {
	m := newRunningTestManager(t)
	m.port = &timeoutSerialPort{}
	m.cmdChanHigh = m.cmdChan
	header, pdu := directReportFixture(t, false)
	saved := make(chan string, 1)
	m.SetPDUCallback(func(raw string) error { saved <- raw; return nil })
	done := make(chan []string, 1)
	go func() {
		done <- respondToCommands(t, m, 1, func(req commandRequest) { req.respChan <- "+CSMS: 0,1,1,1" })
	}()
	for _, line := range []string{header, "OK"} {
		m.rxChan <- rxMsg{Data: line}
	}
	req := commandRequest{cmd: "AT", timeout: time.Second, respChan: make(chan string, 1), errChan: make(chan error, 1)}
	m.handleCommand(req)
	if !m.consumeDirectSMSReportLine(pdu) {
		t.Fatal("idle PDU continuation was lost")
	}
	select {
	case <-saved:
	case <-time.After(time.Second):
		t.Fatal("report not persisted")
	}
	<-done
}

func TestKHiveDirectSMSReportInvalidFrameDoesNotConsumeResponse(t *testing.T) {
	m := newRunningTestManager(t)
	header, _ := directReportFixture(t, false)
	if !m.consumeDirectSMSReportLine(header) {
		t.Fatal("header not consumed")
	}
	if m.consumeDirectSMSReportLine("+CSQ: 25,99") {
		t.Fatal("unrelated response consumed")
	}
	if m.consumeDirectSMSReportLine("9000") {
		t.Fatal("unrelated APDU response consumed")
	}
	if m.directReportFrame.length == 0 {
		t.Fatal("unrelated APDU response discarded the pending header")
	}
	m.consumeDirectSMSReportLine(header)
	m.directReportFrame.since = time.Now().Add(-6 * time.Second)
	if m.consumeDirectSMSReportLine("001122") || m.directReportFrame.length != 0 {
		t.Fatal("expired frame consumed later input")
	}
	for _, invalid := range []string{"+CDS: 99999", "+CDS: bad", "+CDS: -1"} {
		if !m.consumeDirectSMSReportLine(invalid) || m.directReportFrame.length != 0 {
			t.Fatal("invalid header accepted")
		}
	}
}

func TestKHiveSMSReportAcknowledgementRequiresPersistence(t *testing.T) {
	for _, service := range []string{"0", "1"} {
		var calls []string
		err := persistAndAcknowledgeSMSReport("fixture", func(string) error { calls = append(calls, "persist"); return nil }, func(cmd string, _ time.Duration) (string, error) {
			calls = append(calls, cmd)
			if cmd == "AT+CSMS?" {
				return "+CSMS: " + service + ",1,1,1", nil
			}
			return "OK", nil
		})
		want := []string{"persist", "AT+CSMS?"}
		if service == "1" {
			want = append(want, "AT+CNMA=1")
		}
		if err != nil || !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls=%v err=%v", calls, err)
		}
	}
	called := false
	err := persistAndAcknowledgeSMSReport("fixture", func(string) error { return errors.New("disk full") }, func(string, time.Duration) (string, error) { called = true; return "OK", nil })
	if err == nil || called {
		t.Fatal("unpersisted report acknowledged")
	}
	err = persistAndAcknowledgeSMSReport("fixture", func(string) error { return nil }, func(string, time.Duration) (string, error) { return "invalid", nil })
	if err == nil || !strings.Contains(err.Error(), "saved") {
		t.Fatal("invalid CSMS mode not surfaced")
	}
}
