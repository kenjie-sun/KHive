package vowifihost

import (
	"context"
	"strings"
	"time"

	"github.com/1239t/vohive/pkg/logger"
	"github.com/1239t/vowifi-go/runtimehost"
)

const defaultDesiredRecoverReason = "desired_reconcile"

type DesiredRecoverRequest struct {
	DeviceID     string
	Reason       string
	OverrideEPDG string
	Generation   uint64
	Now          time.Time
	OnResult     func(deviceID, reason string, err error)
}

func (m *Manager) DesiredRecoverable(deviceID string) bool {
	if m == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	store := m.RuntimeStore()
	if store.Starting(deviceID) {
		return false
	}
	inst := store.Instance(deviceID)
	return inst == nil || failedRuntimeRecoverable(inst.State())
}

func (m *Manager) ScheduleDesiredRecover(ctx context.Context, req DesiredRecoverRequest) bool {
	if m == nil {
		return false
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return false
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = defaultDesiredRecoverReason
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !m.DesiredRecoverable(deviceID) {
		return false
	}
	if !m.BeginDesiredRecover(deviceID, now) {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	logger.Warn("VoWiFi 目标态恢复开始", "event", "VOWIFI_DESIRED_RECOVER", "device", deviceID, "reason", reason)
	go func() {
		err := m.Recover(ctx, LifecycleRecoverRequest{
			DeviceID:     deviceID,
			Reason:       reason,
			OverrideEPDG: req.OverrideEPDG,
			Generation:   req.Generation,
		})
		if req.OnResult != nil {
			req.OnResult(deviceID, reason, err)
		}
	}()
	return true
}

// A retained failed instance must not block the existing backoff/restart path.
func failedRuntimeRecoverable(st runtimehost.State) bool {
	return st.LastErrorClass != "" && !st.IMSReady && !st.SMSReady
}
