package device

import (
	"errors"
	"testing"
	"time"

	"github.com/1239t/vohive/internal/config"
	"github.com/1239t/vohive/internal/ecm"
	"github.com/1239t/vohive/internal/esim"
)

func khiveECMRecoveryFixture(t *testing.T) (*Pool, *Worker, *fakeController) {
	t.Helper()
	p := NewPool(nil)
	t.Cleanup(p.cancel)
	nc := &fakeController{}
	w := &Worker{ID: "recover", Config: config.DeviceConfig{NetworkEnabled: true}, ECMCore: &ecm.Manager{}, EsimMgr: &esim.Manager{}, netOverride: nc, stop: make(chan struct{})}
	w.state.Identity.ICCID = "current"
	w.state.Identity.IMSI = "001010000000001"
	w.state.Identity.Ready = true
	p.workers[w.ID] = w
	return p, w, nc
}

func TestKHiveECMStartupRetryRecoversAndIsBounded(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		t.Run(map[bool]string{true: "eventual success", false: "bounded failure"}[succeeds], func(t *testing.T) {
			p, w, nc := khiveECMRecoveryFixture(t)
			calls := 0
			nc.connectHook = func() {
				calls++
				nc.connErr = errors.New("modem still initializing")
				if succeeds && calls == 3 {
					nc.connErr = nil
				}
			}
			p.recoverECMNetwork(w, "current", []time.Duration{0, 0, 0, 0, 0})
			want := 5
			if succeeds {
				want = 3
			}
			if calls != want || nc.connected != succeeds {
				t.Fatalf("calls=%d connected=%v", calls, nc.connected)
			}
		})
	}
}

func TestKHiveECMStartupRetryRejectsStalePolicyAndWorker(t *testing.T) {
	for _, scenario := range []string{"offline", "replaced", "network off", "flight", "vowifi", "identity unconfirmed", "different card", "switching"} {
		t.Run(scenario, func(t *testing.T) {
			p, w, nc := khiveECMRecoveryFixture(t)
			calls := 0
			nc.connectHook = func() { calls++ }
			switch scenario {
			case "offline":
				w.EsimMgr.StopSwitchRecovery()
			case "replaced":
				p.workers[w.ID] = &Worker{ID: w.ID}
			case "network off":
				w.Config.NetworkEnabled = false
			case "flight":
				w.Config.AirplaneEnabled = true
			case "vowifi":
				w.Config.VoWiFiEnabled = true
			case "identity unconfirmed":
				w.state.Identity.Ready = false
			case "different card":
				w.state.Identity.ICCID = "replacement"
			case "switching":
				p.switchingDevices[w.ID] = true
			}
			p.recoverECMNetwork(w, "current", []time.Duration{0})
			if calls != 0 {
				t.Fatal("stale or disabled recovery invoked network")
			}
		})
	}
}

func TestKHiveECMStartupRetryCancelsPendingWait(t *testing.T) {
	p, w, nc := khiveECMRecoveryFixture(t)
	done := make(chan struct{})
	go func() { defer close(done); p.recoverECMNetwork(w, "current", []time.Duration{time.Hour}) }()
	w.EsimMgr.StopSwitchRecovery()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old worker recovery did not cancel")
	}
	if nc.connected {
		t.Fatal("canceled recovery connected")
	}
}
