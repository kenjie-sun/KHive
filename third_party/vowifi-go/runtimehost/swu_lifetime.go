package runtimehost

import (
	"context"
	"errors"
	"time"
)

func applySWuFailure(s *State, err error) {
	s.TunnelReady = false
	s.IMSReady = false
	s.SMSReady = false
	s.LastErrorClass = "tunnel"
	s.LastError = err.Error()
	s.LastReason = "tunnel_connection_lost"
	s.UpdatedAt = time.Now()
}

// Publish loss before waiting for IMS TCP timeouts. Both OnSessionDown and the
// final Connect result may arrive; only the first unexpected failure is acted on.
func (i *Instance) failSWuSession(ctx context.Context, err error) {
	if err == nil {
		err = errors.New("SWu session ended")
	}
	i.mu.Lock()
	if i.stopped || ctx.Err() != nil || i.swuFailure != nil {
		i.mu.Unlock()
		return
	}
	i.swuFailure = err
	applySWuFailure(&i.state, err)
	i.state.Phase = runtimePhase(i.state)
	cancel, svc := i.pipelineCancel, i.svc
	i.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	i.notifyObservers(context.WithoutCancel(ctx))
	if closer, ok := svc.(interface{ Close(context.Context) error }); ok && closer != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = closer.Close(closeCtx)
	}
}
