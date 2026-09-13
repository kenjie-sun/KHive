package runtimehost

import (
	"context"
	"errors"
	"testing"
	"time"
)

type lifetimeStub struct {
	done   chan struct{}
	closed bool
}

func (s *lifetimeStub) Done() <-chan struct{}       { return s.done }
func (s *lifetimeStub) Err() error                  { return errors.New("peer EOF") }
func (s *lifetimeStub) Close(context.Context) error { s.closed = true; return nil }

func TestKHiveIMSOwnerWaitsForConnectionLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &lifetimeStub{done: make(chan struct{})}
	i := &Instance{state: State{SIMReady: true, AccessReady: true, TunnelReady: true, IMSReady: true, SMSReady: true}}
	exited := make(chan struct{})
	go func() { defer close(exited); i.watchIMSService(ctx, svc) }()
	select {
	case <-exited:
		t.Fatal("IMS owner returned before service ended")
	case <-time.After(20 * time.Millisecond):
	}
	close(svc.done)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("IMS failure did not stop owner")
	}
	st := i.State()
	if st.IMSReady || st.SMSReady || st.TunnelReady || st.Phase != "failed" || !svc.closed {
		t.Fatalf("stale IMS readiness after EOF: %+v", st)
	}
}

func TestKHiveRuntimePhaseMatchesReadiness(t *testing.T) {
	if got := runtimePhase(State{SIMReady: true, AccessReady: true, TunnelReady: true, IMSReady: true, SMSReady: true}); got != "sms_ready" {
		t.Fatal(got)
	}
}
