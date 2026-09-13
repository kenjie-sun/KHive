package modem

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"github.com/1239t/vohive/pkg/logger"
)

// CFUN diagnostics record timing and framing only, never arbitrary UART
// payloads (which may contain subscriber identifiers or incoming SMS).
type cfunTrace struct {
	mu       sync.Mutex
	started  time.Time
	bytes    int
	tail     []byte
	rawOK    bool
	parsedOK bool
	readyMS  int64
	kinds    []string
}

func newCFUNTrace(command string) *cfunTrace {
	if command != "AT+CFUN=0" && command != "AT+CFUN=1" {
		return nil
	}
	return &cfunTrace{started: time.Now(), readyMS: -1}
}
func (t *cfunTrace) raw(data []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bytes += len(data)
	combined := append(t.tail, data...)
	t.rawOK = t.rawOK || bytes.Contains(combined, []byte("OK"))
	if len(combined) > 1 {
		t.tail = append([]byte(nil), combined[len(combined)-1:]...)
	} else {
		t.tail = append([]byte(nil), combined...)
	}
}
func (t *cfunTrace) line(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	kind := "other"
	switch {
	case line == "OK":
		t.parsedOK = true
		kind = "OK"
	case line == "ERROR":
		kind = "ERROR"
	case line == "+CPIN: READY":
		kind = "CPIN_READY"
		if t.readyMS < 0 {
			t.readyMS = time.Since(t.started).Milliseconds()
		}
	case line == "RDY":
		kind = "RDY"
	case strings.HasPrefix(line, "+CFUN:"):
		kind = "CFUN"
	case strings.HasPrefix(line, "+QIND:"):
		kind = "QIND"
	case strings.HasPrefix(line, "AT+CFUN="):
		kind = "echo"
	}
	if len(t.kinds) < 16 {
		t.kinds = append(t.kinds, kind)
	}
}
func (t *cfunTrace) report(deviceID, command string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	logger.Info("CFUN 串口回包统计", "device", deviceID, "cmd", command, "elapsed_ms", time.Since(t.started).Milliseconds(), "rx_bytes", t.bytes, "raw_ok_seen", t.rawOK, "parsed_ok_seen", t.parsedOK, "cpin_ready_ms", t.readyMS, "line_kinds", t.kinds)
}
