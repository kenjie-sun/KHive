package modem

import (
	"encoding/hex"
	"fmt"
	"github.com/1239t/vohive/pkg/smscodec"
	"strings"
	"testing"
	"time"
)

func TestKHiveATTerminalReferenceAndPersistenceBoundary(t *testing.T) {
	m := newRunningTestManager(t)
	// This fixture has no command worker; route both priorities to its responder.
	m.cmdChanHigh = m.cmdChan
	prepared, submitted := false, false
	done := make(chan struct{})
	go func() {
		defer close(done)
		respondToCommands(t, m, 3, func(req commandRequest) {
			switch req.cmd {
			case "AT+CMGF=0", "AT+CNMI=2,1,0,1,0":
				req.respChan <- "OK"
			default:
				if !strings.HasPrefix(req.cmd, "AT+CMGS=") {
					t.Errorf("unexpected command %s", req.cmd)
				}
				if !prepared {
					t.Error("bytes sent before persistence")
				}
				pdu, err := hex.DecodeString(strings.TrimSuffix(req.followUp, "\x1a"))
				if err != nil || len(pdu) < 3 || pdu[1]&0x20 == 0 {
					t.Errorf("missing TP-SRR %x %v", pdu, err)
				}
				req.respChan <- "\r\n+CMGS: 73\r\nOK\r\n"
			}
		})
	}()
	err := m.SendSMSWithOptions("+1234", "hello", smscodec.SubmitOptions{RequestStatusReport: true, BeforeSubmit: func(parts int) error {
		if parts != 1 {
			t.Fatalf("parts %d", parts)
		}
		prepared = true
		return nil
	}, OnSubmitted: func(part, ref int) error {
		if part != 1 || ref != 73 {
			t.Fatalf("reference %d %d", part, ref)
		}
		submitted = true
		return nil
	}})
	if err != nil || !submitted {
		t.Fatalf("submitted %v error %v", submitted, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("commands unfinished")
	}
	for _, bad := range []string{"OK", "+CMGS: -1", "+CMGS: 256", "+CMGS: invalid"} {
		if _, err := parseCMGSReference(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestKHiveCDSIStorageParsing(t *testing.T) {
	result := (&Manager{}).formatURC(`+CDSI: "ME",7`)
	if result.CMTIIndex != "7" || result.CMTIStorage != "ME" {
		t.Fatalf("stored report %v", result)
	}
}

func TestKHiveATUnsupportedReportsPreserveSending(t *testing.T) {
	m := newRunningTestManager(t)
	// This fixture has no command worker; route both priorities to its responder.
	m.cmdChanHigh = m.cmdChan
	done := make(chan struct{})
	go func() {
		defer close(done)
		respondToCommands(t, m, 5, func(req commandRequest) {
			switch req.cmd {
			case "AT+CMGF=0", "AT+CNMI=2,1,0,0,0":
				req.respChan <- "OK"
			case "AT+CNMI=2,1,0,1,0", "AT+CNMI=2,1,0,2,0":
				req.errChan <- fmt.Errorf("unsupported")
			default:
				pdu, err := hex.DecodeString(strings.TrimSuffix(req.followUp, "\x1a"))
				if err != nil || len(pdu) < 3 || pdu[1]&0x20 != 0 {
					t.Errorf("unexpected report request %x %v", pdu, err)
				}
				req.respChan <- "OK"
			}
		})
	}()
	err := m.SendSMSWithOptions("+1234", "hello", smscodec.SubmitOptions{RequestStatusReport: true, BeforeSubmit: func(int) error { t.Fatal("tracking enabled despite unsupported reports"); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("commands unfinished")
	}
}

func TestKHiveATReportRoutingFallback(t *testing.T) {
	m := newRunningTestManager(t)
	// This fixture has no command worker; route both priorities to its responder.
	m.cmdChanHigh = m.cmdChan
	done := make(chan []string, 1)
	go func() {
		done <- respondToCommands(t, m, 3, func(req commandRequest) {
			if req.cmd != "AT+CNMI=2,1,0,0,0" {
				req.errChan <- fmt.Errorf("unsupported")
			} else {
				req.respChan <- "OK"
			}
		})
	}()
	m.ConfigureSMSReports()
	select {
	case commands := <-done:
		if len(commands) != 3 || commands[2] != "AT+CNMI=2,1,0,0,0" {
			t.Fatalf("fallback %v", commands)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback unfinished")
	}
}

func TestKHiveATReportRoutingSurvivesRepeatedRestore(t *testing.T) {
	m := newRunningTestManager(t)
	m.cmdChanHigh = m.cmdChan
	done := make(chan []string, 1)
	go func() {
		done <- respondToCommands(t, m, 3, func(req commandRequest) {
			req.respChan <- "OK"
		})
	}()
	for range 3 {
		m.ConfigureSMSReports()
	}
	select {
	case commands := <-done:
		for _, cmd := range commands {
			if cmd != "AT+CNMI=2,1,0,1,0" {
				t.Fatalf("restore disabled status reports: %s", cmd)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("report routing restoration unfinished")
	}
}
