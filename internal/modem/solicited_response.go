package modem

import "strings"

// Only explicitly ambiguous read commands belong here. An unrelated URC
// interleaved with a command must still reach the event dispatcher.
func isSolicitedATResponse(command, line string) bool {
	command = strings.ToUpper(strings.TrimSpace(command))
	key := urcKey(line)
	switch command {
	case "AT+CPIN?", "AT+QSIMSTAT?":
		return key == strings.TrimSuffix(strings.TrimPrefix(command, "AT"), "?")
	case "AT+CREG?", "AT+CGREG?", "AT+CEREG?":
		if key != strings.TrimSuffix(strings.TrimPrefix(command, "AT"), "?") {
			return false
		}
		// Read response: <n>,<stat>[,...]. URC: <stat>[,"lac",...].
		fields := strings.Split(parseURCAfterColon(line), ",")
		if len(fields) < 2 {
			return false
		}
		second := strings.TrimSpace(fields[1])
		return len(second) == 1 && second[0] >= '0' && second[0] <= '9'
	}
	return false
}
