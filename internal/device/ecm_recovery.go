package device

import (
	"time"

	"github.com/1239t/vohive/pkg/logger"
)

// A newly enumerated modem can answer identity queries before its data
// commands are ready. Initial Connect failures have no DHCP renewal loop yet.
// Retry a bounded number of times, only for the same confirmed SIM and Worker.
func (p *Pool) scheduleECMNetworkRecovery(w *Worker) {
	if w == nil || w.ECMCore == nil || !w.ecmRecoveryActive.CompareAndSwap(false, true) {
		return
	}
	iccid := w.CurrentICCID()
	go func() {
		defer w.ecmRecoveryActive.Store(false)
		p.recoverECMNetwork(w, iccid, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 12 * time.Second, 20 * time.Second})
	}()
}

func (p *Pool) recoverECMNetwork(w *Worker, iccid string, delays []time.Duration) {
	ctx := p.switchWorkerContext(w)
	for attempt, delay := range delays {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-p.ctx.Done():
			timer.Stop()
			return
		case <-w.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		if !p.switchWorkerStillCurrent(w) || p.IsESIMSwitching(w.ID) || !w.Config.NetworkEnabled || w.Config.AirplaneEnabled || w.Config.VoWiFiEnabled {
			return
		}
		if progress := w.EsimMgr.SwitchProgress(); progress != nil && progress.Busy {
			return
		}
		w.cacheMu.RLock()
		identityReady := w.state.Identity.Ready && normalizeSIMIdentityForCompare(w.state.Identity.ICCID) == normalizeSIMIdentityForCompare(iccid) && iccid != ""
		w.cacheMu.RUnlock()
		if !identityReady {
			return
		}
		if nc := w.NetworkController(); nc == nil || nc.IsConnected() {
			return
		}
		if err := w.StartNetwork(); err != nil {
			logger.Warn("ECM 初始化后网络恢复尚未完成", "device", w.ID, "attempt", attempt+1, "err", err)
			continue
		}
		if !p.switchWorkerStillCurrent(w) {
			return
		}
		p.refreshIPs(w, true)
		p.broadcastVoWiFiStateChange(w.ID)
		logger.Info("ECM 初始化后网络已自动恢复", "device", w.ID, "attempt", attempt+1)
		return
	}
}
