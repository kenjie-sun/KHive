package modem

import (
	"errors"
	"github.com/1239t/vohive/internal/config"
	"testing"
	"time"
)

func TestKHiveCFUNTraceDistinguishesRawAndParsedOK(t *testing.T) {
	trace := newCFUNTrace("AT+CFUN=1")
	trace.raw([]byte("\r\nO"))
	trace.raw([]byte("K\r\n"))
	if !trace.rawOK || trace.parsedOK {
		t.Fatal("raw framing observation lost")
	}
	trace.line("OK")
	if !trace.parsedOK {
		t.Fatal("parsed OK missing")
	}
	trace.line("+CPIN: READY")
	if trace.readyMS < 0 {
		t.Fatal("readiness timing missing")
	}
	trace.line("subscriber-sensitive-payload")
	if trace.kinds[len(trace.kinds)-1] != "other" {
		t.Fatal("payload retained")
	}
	if newCFUNTrace("AT+CNUM") != nil {
		t.Fatal("unrelated command traced")
	}
}

func TestKHiveSIMRestartEndsOnlyOptedInCFUN(t *testing.T) {
	for _, opt := range []bool{false, true} {
		m, err := New(config.DeviceConfig{ID: "khive-test", ATPort: "/dev/khive-test"})
		if err != nil {
			t.Fatal(err)
		}
		m.port = &timeoutSerialPort{}
		ready := m.SubscribeRDY()
		m.rxChan <- rxMsg{Data: "RDY"}
		if !opt {
			m.rxChan <- rxMsg{Data: "OK"}
		}
		req := commandRequest{cmd: "AT+CFUN=1", finishOnRDY: opt, timeout: time.Second, respChan: make(chan string, 1), errChan: make(chan error, 1)}
		m.handleCommand(req)
		select {
		case <-ready:
		default:
			t.Fatal("RDY not dispatched")
		}
		if opt {
			select {
			case err := <-req.errChan:
				if !errors.Is(err, errSIMFunctionRestarted) {
					t.Fatal(err)
				}
			default:
				t.Fatal("restart was not returned for readback")
			}
		} else {
			select {
			case <-req.respChan:
			default:
				t.Fatal("generic CFUN did not wait for OK")
			}
		}
	}
}
