package esim

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestKHiveAPDUTraceExcludesPayload(t *testing.T) {
	secret := []byte("private-activation-or-identity")
	command := append([]byte{0x81, 0xE2, 0x91, 0, byte(len(secret))}, secret...)
	got := fmt.Sprint(apduTraceFields(command))
	if strings.Contains(got, string(secret)) || strings.Contains(got, fmt.Sprintf("%X", secret)) {
		t.Fatal("payload leaked")
	}
	if !strings.Contains(got, "ins E2") || !strings.Contains(got, fmt.Sprintf("lc %d", len(secret))) {
		t.Fatal(got)
	}
	got = fmt.Sprint(apduTraceFields([]byte{0x81, 0xC0, 0, 0, 6}))
	if !strings.Contains(got, "le 6") {
		t.Fatal(got)
	}
	if got := apduErrorKind(errors.New("APDU 透传失败: 设备返回错误: ERROR")); got != "at_error" {
		t.Fatal(got)
	}
	if got := apduErrorKind(errors.New("命令执行超时")); got != "timeout" {
		t.Fatal(got)
	}
}
