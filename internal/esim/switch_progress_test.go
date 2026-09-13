package esim

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKHiveSwitchProgressKeepsBusyUntilConnectivityReady(t *testing.T) {
	mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, new(atomic.Int32))
	mgr.postSwitchMinDelay = time.Millisecond
	mgr.onBeforeSwitch = func(SwitchOperation, string) uint64 { return 7 }
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	mgr.awaitSwitchReady = func(context.Context) error { close(entered); <-release; return nil }
	mgr.onSwitchProgress = func() {
		s := mgr.SwitchProgress()
		if s != nil && !s.Busy {
			once.Do(func() { close(finished) })
		}
	}
	_, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ready check did not start")
	}
	snapshot := mgr.SwitchProgress()
	if !snapshot.Busy || snapshot.Phase != SwitchPhaseConnectivityCheck || snapshot.Token != 7 {
		t.Fatalf("progress=%+v", snapshot)
	}
	snapshot.Phase = SwitchPhaseDone
	mgr.UpdateSwitchProgress(6, SwitchPhaseFailed)
	if mgr.SwitchProgress().Phase != SwitchPhaseConnectivityCheck {
		t.Fatal("stale update or copied snapshot overwrote active progress")
	}
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000002", "A0000005591010"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("overlapping switch=%v", err)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("completion was not published")
	}
	result := mgr.SwitchProgress()
	if result.Busy || result.Phase != SwitchPhaseDone || result.TargetICCID == "" {
		t.Fatalf("completed progress=%+v", result)
	}
}

func TestKHiveSwitchProgressRetainsConnectivityFailure(t *testing.T) {
	mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, new(atomic.Int32))
	mgr.postSwitchMinDelay = time.Millisecond
	mgr.awaitSwitchReady = func(context.Context) error { return errors.New("not ready") }
	done := make(chan struct{})
	var once sync.Once
	mgr.onSwitchProgress = func() {
		s := mgr.SwitchProgress()
		if s != nil && !s.Busy {
			once.Do(func() { close(done) })
		}
	}
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion not published")
	}
	if s := mgr.SwitchProgress(); s.Busy || s.Phase != SwitchPhaseDegraded {
		t.Fatalf("failed progress=%+v", s)
	}
}

func TestKHiveSwitchFailureStageSurvivesCleanupAndUnlocks(t *testing.T) {
	for _, tc := range []struct {
		phase SwitchPhase
		code  string
	}{
		{SwitchPhaseSIMInitializing, "sim_init_failed"},
		{SwitchPhaseIdentityRefresh, "identity_unconfirmed"},
		{SwitchPhaseConnectivityCheck, "network_ready_timeout"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, new(atomic.Int32))
			mgr.postSwitchMinDelay = time.Millisecond
			mgr.onAfterSwitch = func(_ SwitchOperation, token uint64) {
				mgr.UpdateSwitchProgress(token, tc.phase)
				mgr.ReportSwitchFailure(token, tc.phase, tc.code)
				mgr.UpdateSwitchProgress(token, SwitchPhaseRuntimeRestore)
				mgr.UpdateSwitchProgress(token, SwitchPhaseDone)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010"); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(time.Second)
				for mgr.SwitchProgress().Busy && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				s := mgr.SwitchProgress()
				if s.Busy || s.Phase != SwitchPhaseDegraded || s.FailureCode != tc.code || s.FailurePhase != tc.phase {
					t.Fatalf("failure=%+v", s)
				}
			}
		})
	}
}
func TestKHiveSwitchDisconnectCancelsOldSession(t *testing.T) {
	mgr := newTestATManagerForSIMReload(t, &fakeSIMPowerBackend{}, new(atomic.Int32))
	mgr.postSwitchMinDelay = time.Millisecond
	oldSession := mgr.SessionID()
	entered, exited := make(chan struct{}), make(chan struct{})
	mgr.awaitSwitchReady = func(ctx context.Context) error { close(entered); <-ctx.Done(); close(exited); return ctx.Err() }
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("readiness not entered")
	}
	mgr.StopSwitchRecovery()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("old readiness not canceled")
	}
	mgr.UpdateSwitchProgress(0, SwitchPhaseDone)
	s := mgr.SwitchProgress()
	if s.Busy || s.Phase != SwitchPhaseFailed || s.FailureCode != "device_disconnected" {
		t.Fatalf("disconnect=%+v", s)
	}
	if _, err := mgr.SwitchProfileWithResult(context.Background(), "8986000000000000001", "A0000005591010"); err == nil {
		t.Fatal("removed Manager accepted a new switch")
	}
	fresh := &Manager{}
	if fresh.SessionID() == oldSession || fresh.SwitchProgress() != nil || fresh.SwitchContext().Err() != nil {
		t.Fatal("new Manager inherited old session")
	}
	if mgr.SessionID() != oldSession {
		t.Fatal("old session changed in place")
	}
}
func TestKHiveSwitchFailureRejectsRawDiagnosticText(t *testing.T) {
	mgr := &Manager{}
	mgr.beginSwitchProgress(SwitchOperationEnableProfile, "target")
	mgr.UpdateSwitchProgress(1, SwitchPhaseIdentityRefresh)
	mgr.ReportSwitchFailure(1, SwitchPhaseIdentityRefresh, "arbitrary subscriber payload")
	if s := mgr.SwitchProgress(); s.FailureCode != "identity_unconfirmed" {
		t.Fatalf("unexpected public failure code %q", s.FailureCode)
	}
}
