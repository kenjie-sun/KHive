package esim

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
)

func TestKHiveCloneProfilesKeepsEmptyJSONArray(t *testing.T) {
	data, err := json.Marshal(cloneProfiles([]EUICCProfiles{{AIDHex: "empty"}}))
	if err != nil || !strings.Contains(string(data), `"profiles":[]`) {
		t.Fatalf("empty partition response=%s err=%v", data, err)
	}
}

func TestKHiveSwitchRejectsOverlapThroughRecovery(t *testing.T) {
	var calls atomic.Int32
	mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, &calls)
	mgr.postSwitchMinDelay = time.Millisecond
	started, release := make(chan struct{}), make(chan struct{})
	mgr.onAfterSwitch = func(SwitchOperation, uint64) { close(started); <-release }
	defer close(release)
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	for _, target := range []string{"8986000000000000001", "8986000000000000002"} {
		if _, err := mgr.SwitchProfileWithResult(context.Background(), target, "A0000005591010"); !errors.Is(err, ErrOperationInProgress) {
			t.Fatalf("overlap error=%v", err)
		}
	}
	if err := mgr.DisableProfile(context.Background(), "8986000000000000001", "A0000005591010"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("disable during recovery=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("APDU operations=%d want 1", calls.Load())
	}
}

func TestKHiveSwitchGuardReleasedAfterFailure(t *testing.T) {
	mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, new(atomic.Int32))
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "invalid", ""); err == nil {
		t.Fatal("invalid ICCID accepted")
	}
	if mgr.switchInProgress.Load() {
		t.Fatal("failure left guard locked")
	}
}

func TestKHiveAlreadyEnabledRequiresFreshTargetState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active bool
		wantOK bool
	}{{"confirmed", true, true}, {"still_disabled", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, reads atomic.Int32
			mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, &calls)
			target, _ := sgp22.NewICCID("8986000000000000001")
			state := sgp22.ProfileDisabled
			if tc.active {
				state = sgp22.ProfileEnabled
			}
			mgr.channelFactory = func([]byte) (*lpa.Client, error) {
				return &lpa.Client{APDU: fakeProfileOperationTransmitter{
					calls: &calls, listCalls: &reads,
					err:      sgp22.ProfileOperationError{Operation: sgp22.EnableProfile, Result: sgp22.ProfileOperationResultProfileNotInDisabledState},
					profiles: []*sgp22.ProfileInfo{{ICCID: target, ProfileState: state}},
				}}, nil
			}
			mgr.postSwitchMinDelay = time.Millisecond
			done := make(chan struct{}, 1)
			mgr.onAfterSwitch = func(SwitchOperation, uint64) { done <- struct{}{} }
			result, err := mgr.SwitchProfileWithResult(context.Background(), target.String(), "A0000005591010")
			if (err == nil) != tc.wantOK {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if reads.Load() != 1 || calls.Load() != 1 {
				t.Fatalf("writes=%d fresh reads=%d", calls.Load(), reads.Load())
			}
			if tc.wantOK {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("recovery not invoked")
				}
			}
		})
	}
}
