package modem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errSIMFunctionRestarted = errors.New("SIM function restarted; readiness requires readback")

// ReloadSIMAfterSwitch refreshes the modem's SIM application and identity cache
// after EnableProfile. The AT firmware may acknowledge refresh=true before
// actually replacing its SIM context. CFUN=4 only disables RF; it does not
// reinitialize the SIM. Keep the complete 0 -> 1 sequence exclusive with AT/APDU.
func (m *Manager) ReloadSIMAfterSwitch(ctx context.Context) error {
	m.atSequenceMu.Lock()
	defer m.atSequenceMu.Unlock()
	return reloadSIMSequence(ctx, func(cmd string, timeout time.Duration) (string, error) {
		return m.executeATInSequence(cmd, timeout, true, false, cmd == "AT+CFUN=1")
	}, 500*time.Millisecond)
}

// Quectel documents up to 15 seconds for CFUN; reserve a margin for the
// USB/AT transport instead of timing out at the normal 5-second command limit.
func reloadSIMSequence(ctx context.Context, execute func(string, time.Duration) (string, error), settle time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := execute("AT+CFUN=0", 20*time.Second); err != nil {
		return fmt.Errorf("SIM deactivate: %w", err)
	}
	timer := time.NewTimer(settle)
	select {
	case <-ctx.Done():
		timer.Stop()
	case <-timer.C:
	}
	// Once CFUN=0 succeeded, always attempt CFUN=1, even after cancellation.
	_, reactivateErr := execute("AT+CFUN=1", 20*time.Second)
	online := reactivateErr == nil
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("SIM readiness not confirmed: %w (CFUN: %v)", err, reactivateErr)
		}
		// A firmware restart can lose the terminal OK. Read back actual
		// functionality and PIN state before classifying that as failure.
		if !online {
			mode, err := execute("AT+CFUN?", 2*time.Second)
			online = err == nil && strings.TrimSpace(mode) == "+CFUN: 1"
		}
		resp, err := execute("AT+CPIN?", 2*time.Second)
		if online && err == nil && strings.TrimSpace(resp) == "+CPIN: READY" {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
