package device

import (
	"context"
	"errors"
	"github.com/1239t/vohive/internal/backend"
	"github.com/1239t/vohive/internal/esim"
	"testing"
	"time"
)

func TestKHiveRemovedWorkerCannotRewriteReplacementSwitch(t *testing.T) {
	p := NewPool(nil)
	defer p.cancel()
	old := &Worker{ID: "lifecycle", EsimMgr: &esim.Manager{}, stop: make(chan struct{})}
	if err := p.registerWorkerStarting(old); err != nil {
		t.Fatal(err)
	}
	oldSession := old.EsimMgr.SessionID()
	_, after, failed, degraded, phase := p.newESIMSwitchCallbacks(old.ID, old)
	snapshot := p.beginESIMSwitch(old.ID, "old-target")
	if err := p.RemoveWorker(old.ID); err != nil {
		t.Fatal(err)
	}
	if old.EsimMgr.SwitchContext().Err() == nil || p.isLatestSwitchToken(old.ID, snapshot.SwitchToken) {
		t.Fatal("old switch lifecycle retained")
	}
	replacement := &Worker{ID: old.ID, EsimMgr: &esim.Manager{}, stop: make(chan struct{})}
	if err := p.registerWorkerStarting(replacement); err != nil {
		t.Fatal(err)
	}
	defer p.RemoveWorker(replacement.ID)
	current := p.beginESIMSwitch(replacement.ID, "new-target")
	after(snapshot.SwitchToken)
	failed(0, errors.New("old failure"))
	degraded(current.SwitchToken, esim.SwitchPhaseDegraded, errors.New("late"))
	phase(current.SwitchToken, esim.SwitchPhaseDone)
	got, ok := p.resolvePostSwitchSnapshotIfToken(replacement.ID, current.SwitchToken)
	if !ok || got.Phase != esim.SwitchPhasePrepare || got.TargetICCID != "new-target" {
		t.Fatalf("old callback rewrote new switch: %+v", got)
	}
	if replacement.EsimMgr.SessionID() == oldSession || replacement.generation == old.generation {
		t.Fatal("replacement session/generation not advanced")
	}
}

type khiveLateIdentityBackend struct {
	backend.DeviceBackend
	entered, release chan struct{}
}

func (b *khiveLateIdentityBackend) GetICCIDLive(context.Context) (string, error) {
	close(b.entered)
	<-b.release
	return "target", nil
}
func (b *khiveLateIdentityBackend) GetIMSILive(context.Context) (string, error) {
	return "001010000000001", nil
}
func TestKHiveRemovedWorkerDiscardsLateIdentity(t *testing.T) {
	p := NewPool(nil)
	defer p.cancel()
	b := &khiveLateIdentityBackend{DeviceBackend: &esimSwitchRestoreBackendStub{}, entered: make(chan struct{}), release: make(chan struct{})}
	w := &Worker{ID: "late", Backend: b, EsimMgr: &esim.Manager{}}
	p.workers[w.ID] = w
	done := make(chan error, 1)
	go func() {
		_, err := p.refreshPostSwitchIdentity(w.ID, w, esimSwitchContext{TargetICCID: "target"})
		done <- err
	}()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("identity read not started")
	}
	w.EsimMgr.StopSwitchRecovery()
	close(b.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late read=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late identity did not exit")
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	if w.state.Identity.ICCID != "" || w.state.Identity.IMSI != "" || w.state.Identity.Ready {
		t.Fatal("late identity was committed")
	}
}

type khiveSIMReloadFailure struct{ khiveATSwitchBackend }

func (*khiveSIMReloadFailure) ReloadSIMAfterSwitch(context.Context) error {
	return errors.New("sim initialization failed")
}
func TestKHiveSIMInitializationFailureDoesNotConfirmIdentity(t *testing.T) {
	p := NewPool(nil)
	defer p.cancel()
	b := &khiveSIMReloadFailure{khiveATSwitchBackend{DeviceBackend: &esimSwitchRestoreBackendStub{}, iccid: "target", imsi: "001010000000001"}}
	w := &Worker{ID: "init-failure", Backend: b}
	result := p.runPostSwitchConvergence(w.ID, 0, w, esimSwitchContext{TargetICCID: "target"})
	if !result.Degraded || result.Ready || result.Reason != "at_sim_reload_failed" || w.CurrentICCID() != "" {
		t.Fatalf("initialization failure=%+v", result)
	}
}
