package esim

import (
	"context"
	"crypto/rand"
	"time"
)

// SwitchProgress survives browser reloads for the lifetime of this Manager.
// Failure fields are stable codes, never arbitrary modem errors or identifiers.
type SwitchProgress struct {
	Token        uint64          `json:"token"`
	Operation    SwitchOperation `json:"operation"`
	TargetICCID  string          `json:"target_iccid,omitempty"`
	Phase        SwitchPhase     `json:"phase"`
	Busy         bool            `json:"busy"`
	FailurePhase SwitchPhase     `json:"failure_phase,omitempty"`
	FailureCode  string          `json:"failure_code,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (m *Manager) initSwitchLifecycle() {
	m.switchLifecycleOnce.Do(func() {
		m.switchContext, m.cancelSwitchLifecycle = context.WithCancel(context.Background())
		m.switchSessionID = rand.Text()
	})
}

// SessionID changes on Worker replacement and service restart. Tokens alone
// cannot distinguish a previous Manager's request from a new session.
func (m *Manager) SessionID() string {
	if m == nil {
		return ""
	}
	m.initSwitchLifecycle()
	return m.switchSessionID
}
func (m *Manager) SwitchContext() context.Context {
	m.initSwitchLifecycle()
	return m.switchContext
}

// StopSwitchRecovery invalidates this Manager permanently before closing its
// transport. Late callbacks cannot publish success or accept another switch.
func (m *Manager) StopSwitchRecovery() {
	if m == nil {
		return
	}
	m.initSwitchLifecycle()
	m.cancelSwitchLifecycle()
	m.switchProgressMu.Lock()
	if m.switchProgress != nil && m.switchInProgress.Load() {
		m.setSwitchFailureLocked("device_disconnected", "")
		m.switchProgress.Phase = SwitchPhaseFailed
		m.switchProgress.UpdatedAt = time.Now()
	}
	m.switchInProgress.Store(false)
	m.switchProgressMu.Unlock()
	m.notifySwitchProgress()
}
func (m *Manager) notifySwitchProgress() {
	if m.onSwitchProgress != nil {
		m.onSwitchProgress()
	}
}

func (m *Manager) beginSwitchProgress(operation SwitchOperation, target string) bool {
	m.initSwitchLifecycle()
	m.switchProgressMu.Lock()
	defer func() { m.switchProgressMu.Unlock(); m.notifySwitchProgress() }()
	if m.switchContext.Err() != nil || m.profileWriteInProgress || !m.switchInProgress.CompareAndSwap(false, true) {
		return false
	}
	now := time.Now()
	m.switchProgress = &SwitchProgress{Operation: operation, TargetICCID: target, Phase: SwitchPhasePrepare, StartedAt: now, UpdatedAt: now}
	return true
}
func (m *Manager) SwitchProgress() *SwitchProgress {
	if m == nil {
		return nil
	}
	m.switchProgressMu.RLock()
	defer m.switchProgressMu.RUnlock()
	if m.switchProgress == nil {
		return nil
	}
	snapshot := *m.switchProgress
	snapshot.Busy = m.switchInProgress.Load()
	return &snapshot
}
func terminalSwitchPhase(phase SwitchPhase) bool {
	return phase == SwitchPhaseFailed || phase == SwitchPhaseDegraded
}
func defaultSwitchFailureCode(phase SwitchPhase) string {
	switch phase {
	case SwitchPhasePrepare:
		return "prepare_failed"
	case SwitchPhaseAPDUSwitching, SwitchPhaseCardResetSettling:
		return "profile_command_failed"
	case SwitchPhaseSIMInitializing, SwitchPhaseReloadWarning:
		return "sim_init_failed"
	case SwitchPhaseIdentityRefresh:
		return "identity_unconfirmed"
	case SwitchPhasePolicyApplying:
		return "policy_apply_failed"
	case SwitchPhaseConnectivityCheck:
		return "network_ready_timeout"
	case SwitchPhaseNetworkRestore, SwitchPhaseVoWiFiRestore:
		return "network_restore_failed"
	default:
		return "recovery_failed"
	}
}
func (m *Manager) setSwitchFailureLocked(code string, phase SwitchPhase) {
	p := m.switchProgress
	if p == nil || p.FailureCode != "" {
		return
	}
	if phase == "" {
		phase = p.Phase
	}
	switch code {
	case "prepare_failed", "profile_command_failed", "sim_init_failed", "identity_unconfirmed", "policy_apply_failed", "network_ready_timeout", "network_restore_failed", "device_disconnected", "recovery_timeout", "recovery_failed":
	default:
		code = defaultSwitchFailureCode(phase)
	}
	p.FailureCode = code
	p.FailurePhase = phase
}

// ReportSwitchFailure keeps the first failure stage even if fallback cleanup
// subsequently changes phase. It deliberately accepts no raw error text.
func (m *Manager) ReportSwitchFailure(token uint64, phase SwitchPhase, code string) {
	if m == nil {
		return
	}
	m.switchProgressMu.Lock()
	defer func() { m.switchProgressMu.Unlock(); m.notifySwitchProgress() }()
	p := m.switchProgress
	if p == nil || !m.switchInProgress.Load() || (p.Token != 0 && p.Token != token) {
		return
	}
	m.setSwitchFailureLocked(code, phase)
	p.Phase = SwitchPhaseDegraded
	p.UpdatedAt = time.Now()
}

// Pool callbacks may publish without recursively invoking phase callbacks.
func (m *Manager) UpdateSwitchProgress(token uint64, phase SwitchPhase) {
	if m == nil || phase == "" {
		return
	}
	m.switchProgressMu.Lock()
	defer func() { m.switchProgressMu.Unlock(); m.notifySwitchProgress() }()
	p := m.switchProgress
	if p == nil || !m.switchInProgress.Load() || (p.Token != 0 && p.Token != token) {
		return
	}
	if terminalSwitchPhase(p.Phase) && !terminalSwitchPhase(phase) {
		return
	}
	if token != 0 {
		p.Token = token
	}
	if terminalSwitchPhase(phase) {
		m.setSwitchFailureLocked("", "")
	}
	p.Phase = phase
	p.UpdatedAt = time.Now()
}
func (m *Manager) finishSwitchProgress() {
	m.switchProgressMu.Lock()
	defer func() { m.switchProgressMu.Unlock(); m.notifySwitchProgress() }()
	if m.switchProgress != nil {
		m.switchProgress.UpdatedAt = time.Now()
	}
	m.switchInProgress.Store(false)
}
