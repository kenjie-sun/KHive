package esim

import (
	"fmt"
	"strings"

	"github.com/1239t/vohive/pkg/logger"
)

// Only protocol framing is retained. Never log APDU data, identifiers, AKA
// material or the original transport error (which may include response bytes).
func apduTraceFields(command []byte) []any {
	fields := []any{"command_bytes", len(command)}
	if len(command) < 4 {
		return fields
	}
	fields = append(fields, "cla", fmt.Sprintf("%02X", command[0]), "ins", fmt.Sprintf("%02X", command[1]), "p1", fmt.Sprintf("%02X", command[2]), "p2", fmt.Sprintf("%02X", command[3]))
	if len(command) == 5 {
		return append(fields, "le", int(command[4]))
	}
	if len(command) > 5 && command[4] != 0 {
		fields = append(fields, "lc", int(command[4]))
		if len(command) == 6+int(command[4]) {
			fields = append(fields, "le", int(command[len(command)-1]))
		}
	}
	return fields
}

func apduErrorKind(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "设备返回错误: ERROR"):
		return "at_error"
	case strings.Contains(text, "超时"), strings.Contains(text, "deadline"), strings.Contains(text, "timeout"):
		return "timeout"
	case strings.Contains(text, "解析"):
		return "parse_error"
	default:
		return "transport_error"
	}
}

func traceAPDUResult(transport string, channel byte, command, response []byte, elapsedMS int64, err error) {
	fields := append([]any{"transport", transport, "channel", int(channel), "elapsed_ms", elapsedMS, "response_bytes", len(response)}, apduTraceFields(command)...)
	if len(response) >= 2 {
		fields = append(fields, "sw", fmt.Sprintf("%02X%02X", response[len(response)-2], response[len(response)-1]))
	}
	if err != nil {
		fields = append(fields, "error_kind", apduErrorKind(err))
		logger.Warn("eSIM APDU 传输诊断", fields...)
		return
	}
	logger.Info("eSIM APDU 传输诊断", fields...)
}
