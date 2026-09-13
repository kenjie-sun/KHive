package device

import (
	"context"
	"fmt"
	"time"

	"github.com/1239t/vohive/internal/esim"
)

// Command acceptance is not connectivity readiness. This check runs while
// the Manager still holds the switch guard, after applying the target policy.
func (p *Pool) awaitSwitchConnectivityReady(ctx context.Context, worker *Worker) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if p.GetWorker(worker.ID) != worker {
			return fmt.Errorf("设备已重新初始化")
		}
		connected := worker.NetworkConnected()
		if worker.Config.VoWiFiEnabled {
			state, ok := p.GetVoWiFiRuntimeState(worker.ID)
			if ok && state.IMSReady && state.SMSReady && !connected {
				return nil
			}
		} else if connected == (worker.Config.NetworkEnabled && !worker.Config.AirplaneEnabled) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("当前卡策略要求的网络尚未就绪: %w", ctx.Err())
		case <-p.ctx.Done():
			return p.ctx.Err()
		case <-ticker.C:
		}
	}
}

// Optional observers keep transport-only Manager construction usable without
// a running pool while production workers report their actual readiness.
type esimSwitchObservers struct {
	awaitReady func(context.Context) error
	changed    func()
}

func (p *Pool) esimSwitchObservers(w *Worker) esimSwitchObservers {
	return esimSwitchObservers{
		awaitReady: func(ctx context.Context) error { return p.awaitSwitchConnectivityReady(ctx, w) },
		changed:    func() { p.broadcastVoWiFiStateChange(w.ID) },
	}
}

func (p *Pool) switchWorkerContext(worker *Worker) context.Context {
	if worker != nil && worker.EsimMgr != nil {
		return worker.EsimMgr.SwitchContext()
	}
	if p.ctx != nil {
		return p.ctx
	}
	return context.Background()
}
func (p *Pool) switchWorkerStillCurrent(worker *Worker) bool {
	return worker != nil && p.GetWorker(worker.ID) == worker && p.switchWorkerContext(worker).Err() == nil
}
func (p *Pool) reportESIMSwitchFailure(worker *Worker, token uint64, phase esim.SwitchPhase, code string) {
	if !p.switchWorkerStillCurrent(worker) {
		return
	}
	if worker.EsimMgr != nil {
		worker.EsimMgr.ReportSwitchFailure(token, phase, code)
	}
	p.markESIMSwitchPhaseIfToken(worker.ID, token, esim.SwitchPhaseDegraded)
}
