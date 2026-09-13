package api

import (
	"github.com/1239t/vohive/internal/esim"
	"testing"
)

func TestKHiveOverviewPublishesSwitchPhaseAndGuardChanges(t *testing.T) {
	makeVersion := func(token uint64, phase esim.SwitchPhase, busy bool) overviewStreamEmitVersion {
		return newOverviewStreamEmitVersion(deviceMgmtOverviewLiteItem{SwitchProgress: &esim.SwitchProgress{Token: token, Phase: phase, Busy: busy}})
	}
	base := makeVersion(7, esim.SwitchPhaseSIMInitializing, true)
	for _, current := range []overviewStreamEmitVersion{makeVersion(7, esim.SwitchPhaseConnectivityCheck, true), makeVersion(7, esim.SwitchPhaseSIMInitializing, false), makeVersion(8, esim.SwitchPhaseSIMInitializing, true)} {
		if shouldSkipOverviewStatePush(&base, current) {
			t.Fatal("switch progress change suppressed")
		}
	}
}

func TestKHiveOverviewPublishesFailureAndSessionChanges(t *testing.T) {
	item := deviceMgmtOverviewLiteItem{ESIMSession: "old", SwitchProgress: &esim.SwitchProgress{Token: 1, Phase: esim.SwitchPhaseDegraded, FailurePhase: esim.SwitchPhaseSIMInitializing, FailureCode: "sim_init_failed"}}
	original := newOverviewStreamEmitVersion(item)
	item.SwitchProgress.FailureCode = "device_disconnected"
	if shouldSkipOverviewStatePush(&original, newOverviewStreamEmitVersion(item)) {
		t.Fatal("failure change suppressed")
	}
	item.SwitchProgress = nil
	item.ESIMSession = "new"
	if shouldSkipOverviewStatePush(&original, newOverviewStreamEmitVersion(item)) {
		t.Fatal("new session suppressed")
	}
}
