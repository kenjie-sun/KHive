package modem

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKHiveSIMReloadReinitializesSIMBeforeReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := []string{}
	probes := 0
	err := reloadSIMSequence(ctx, func(cmd string, timeout time.Duration) (string, error) {
		if (cmd == "AT+CFUN=0" || cmd == "AT+CFUN=1") && timeout < 15*time.Second {
			t.Fatal("CFUN timeout shorter than documented response window")
		}
		calls = append(calls, cmd)
		if cmd == "AT+CPIN?" {
			probes++
			if probes == 1 {
				return "", errors.New("initializing")
			}
			return "+CPIN: READY", nil
		}
		return "OK", nil
	}, 0)
	if err != nil || !reflect.DeepEqual(calls, []string{"AT+CFUN=0", "AT+CFUN=1", "AT+CPIN?", "AT+CPIN?", "AT+CNMI=2,1,0,1,0"}) {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}
func TestKHiveSIMReloadRestoresReportsAfterFirmwareReset(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		t.Run(fmt.Sprint(unsupported), func(t *testing.T) {
			cnmi := "AT+CNMI=2,1,0,1,0"
			err := reloadSIMSequence(context.Background(), func(cmd string, _ time.Duration) (string, error) {
				switch cmd {
				case "AT+CFUN=1":
					cnmi = "AT+CNMI=2,1,0,0,0"
				case "AT+CPIN?":
					return "+CPIN: READY", nil
				case "AT+CNMI=2,1,0,1,0", "AT+CNMI=2,1,0,2,0":
					if unsupported {
						return "", errors.New("unsupported")
					}
					cnmi = cmd
				case "AT+CNMI=2,1,0,0,0":
					cnmi = cmd
				}
				return "OK", nil
			}, 0)
			want := "AT+CNMI=2,1,0,1,0"
			if unsupported {
				want = "AT+CNMI=2,1,0,0,0"
			}
			if err != nil || cnmi != want {
				t.Fatalf("cnmi=%s err=%v", cnmi, err)
			}
		})
	}
}

func TestKHiveSIMReloadDoesNotHideSMSRestoreFailure(t *testing.T) {
	err := reloadSIMSequence(context.Background(), func(cmd string, _ time.Duration) (string, error) {
		if cmd == "AT+CPIN?" {
			return "+CPIN: READY", nil
		}
		if strings.HasPrefix(cmd, "AT+CNMI=") {
			return "", errors.New("transport failed")
		}
		return "OK", nil
	}, 0)
	if err == nil {
		t.Fatal("SMS indication restore failure accepted as ready")
	}
}

func TestKHiveSIMReloadCancellationRestoresFunctionality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := []string{}
	err := reloadSIMSequence(ctx, func(cmd string, _ time.Duration) (string, error) {
		calls = append(calls, cmd)
		if cmd == "AT+CFUN=0" {
			cancel()
		}
		return "OK", nil
	}, 0)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(calls, []string{"AT+CFUN=0", "AT+CFUN=1"}) {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}
func TestKHiveSIMReloadStopsOnDeactivateFailure(t *testing.T) {
	calls := 0
	err := reloadSIMSequence(context.Background(), func(string, time.Duration) (string, error) { calls++; return "", errors.New("unsupported") }, 0)
	if err == nil || calls != 1 {
		t.Fatal("continued after failure")
	}
}

func TestKHiveSIMReloadVerifiesStateAfterLostOK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := reloadSIMSequence(ctx, func(cmd string, _ time.Duration) (string, error) {
		switch cmd {
		case "AT+CFUN=1":
			return "", errors.New("timeout")
		case "AT+CFUN?":
			return "+CFUN: 1", nil
		case "AT+CPIN?":
			return "+CPIN: READY", nil
		default:
			return "OK", nil
		}
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
}
func TestKHiveSIMReloadDoesNotAcceptPINReadyWhileOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := reloadSIMSequence(ctx, func(cmd string, _ time.Duration) (string, error) {
		switch cmd {
		case "AT+CFUN=1":
			return "", errors.New("failed")
		case "AT+CFUN?":
			return "+CFUN: 0", nil
		case "AT+CPIN?":
			return "+CPIN: READY", nil
		default:
			return "OK", nil
		}
	}, 0)
	if err == nil {
		t.Fatal("offline modem accepted as reinitialized")
	}
}
