package modem

import (
	"testing"
	"time"

	"github.com/1239t/vohive/internal/config"
)

func TestKHiveSolicitedResponse(t *testing.T) {
	for _, tc := range []struct {
		cmd, line string
		want      bool
	}{
		{"AT+CPIN?", "+CPIN: READY", true},
		{"AT+QSIMSTAT?", "+QSIMSTAT: 1,1", true},
		{"AT+CEREG?", "+CEREG: 2,5,\"0010\",\"1234\",7", true},
		{"AT+CEREG?", "+CEREG: 5", false},
		{"AT+CEREG?", "+CEREG: 5,\"0010\",\"1234\",7", false},
		{"AT+CPIN?", "RDY", false},
		{"AT+CSQ", "+CPIN: READY", false},
	} {
		if got := isSolicitedATResponse(tc.cmd, tc.line); got != tc.want {
			t.Errorf("%s / %s: %v", tc.cmd, tc.line, got)
		}
	}
}

func TestKHiveQueryDoesNotBroadcastReady(t *testing.T) {
	for _, cmd := range []string{"AT+CPIN?", "AT+QSIMSTAT?"} {
		t.Run(cmd, func(t *testing.T) {
			m, err := New(config.DeviceConfig{ID: "khive-test", ATPort: "/dev/khive-test"})
			if err != nil {
				t.Fatal(err)
			}
			m.port = &timeoutSerialPort{}
			ready := m.SubscribeRDY()
			line := "+CPIN: READY"
			if cmd == "AT+QSIMSTAT?" {
				line = "+QSIMSTAT: 1,1"
			}
			m.rxChan <- rxMsg{Data: line}
			m.rxChan <- rxMsg{Data: "OK"}
			req := commandRequest{cmd: cmd, timeout: time.Second, respChan: make(chan string, 1), errChan: make(chan error, 1)}
			m.handleCommand(req)
			select {
			case got := <-req.respChan:
				if got != line {
					t.Fatalf("response=%q", got)
				}
			default:
				t.Fatal("missing response")
			}
			select {
			case <-ready:
				t.Fatal("query triggered RDY")
			default:
			}
			m.handleURC("RDY")
			select {
			case <-ready:
			default:
				t.Fatal("real RDY was lost")
			}
		})
	}
}
