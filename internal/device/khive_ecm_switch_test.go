package device

import (
	"context"
	"testing"

	"github.com/1239t/vohive/internal/backend"
	"github.com/1239t/vohive/internal/cardpolicy"
	"github.com/1239t/vohive/internal/config"
)

// Hide QMI readiness: this is the capability surface of the actual AT backend.
type khiveATSwitchBackend struct {
	backend.DeviceBackend
	iccid, imsi string
	reloads     int
}

func (b *khiveATSwitchBackend) ReloadSIMAfterSwitch(context.Context) error { b.reloads++; return nil }

func (b *khiveATSwitchBackend) GetICCIDLive(context.Context) (string, error) { return b.iccid, nil }
func (b *khiveATSwitchBackend) GetIMSILive(context.Context) (string, error)  { return b.imsi, nil }
func TestKHiveATSwitchUsesNewCardPolicy(t *testing.T) {
	withFastPostSwitchIdentityPolling(t)
	p := NewPool(nil)
	defer p.cancel()
	old := &esimSwitchRestoreBackendStub{}
	b := &khiveATSwitchBackend{DeviceBackend: old, iccid: "8900000000000000001", imsi: "248020000000001"}
	nc := &fakeController{}
	w := &Worker{ID: "test", Backend: b, Config: config.DeviceConfig{VoWiFiEnabled: true, AirplaneEnabled: true, ESIMSwitch: config.ESIMSwitchConfig{UseRefreshTrue: true}}, netOverride: nc}
	p.workers[w.ID] = w
	snap := esimSwitchContext{TargetICCID: b.iccid, FlightModeBefore: true, VoWiFiActiveBefore: true}
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{ICCID: b.iccid, NetworkEnabled: true, IPVersion: "v4"}})
	result := p.runPostSwitchConvergence(w.ID, 0, w, snap)
	if !result.Ready || w.CurrentICCID() != b.iccid || b.reloads != 1 {
		t.Fatalf("AT convergence: %+v", result)
	}
	if !p.resolveAndApplyPolicy(w, "esim_switched").Applied {
		t.Fatal("policy not applied")
	}
	snap = postSwitchSnapshotForPolicy(snap, w)
	p.restorePostSwitchConnectivity(w.ID, w, snap, nil, true)
	if !nc.connected || w.Config.VoWiFiEnabled || snap.FlightModeBefore {
		t.Fatal("old flight/VoWiFi state overrode new data card")
	}
}
func TestKHiveATSwitchRejectsWrongOrIncompleteIdentity(t *testing.T) {
	withFastPostSwitchIdentityPolling(t)
	for _, tc := range []struct {
		iccid, imsi string
	}{{"old", "248020000000001"}, {"target", ""}} {
		p := NewPool(nil)
		b := &khiveATSwitchBackend{DeviceBackend: &esimSwitchRestoreBackendStub{}, iccid: tc.iccid, imsi: tc.imsi}
		w := &Worker{ID: "test", Backend: b}
		r := p.runPostSwitchConvergence(w.ID, 0, w, esimSwitchContext{TargetICCID: "target"})
		p.cancel()
		if r.Ready || !r.Degraded {
			t.Fatalf("unsafe identity accepted: %+v", r)
		}
	}
}
