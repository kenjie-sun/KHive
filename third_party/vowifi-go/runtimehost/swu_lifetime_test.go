package runtimehost

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKHiveSWuLossImmediatelyInvalidatesIMS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	i := &Instance{pipelineCancel: cancel, state: State{SIMReady: true, AccessReady: true, TunnelReady: true, IMSReady: true, SMSReady: true}}
	observed := make(chan State, 2)
	i.AddObserver(ObserverFunc(func(ctx context.Context, ev Event) { observed <- ev.State }))
	svc := &lifetimeStub{done: make(chan struct{})}
	exited := make(chan struct{})
	go func() { i.watchIMSService(ctx, svc); close(exited) }()
	i.failSWuSession(ctx, errors.New("rekey rejected"))
	select {
	case <-ctx.Done():
	default:
		t.Fatal("IMS pipeline was not canceled")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("waited for TCP failure after tunnel loss")
	}
	select {
	case s := <-observed:
		if s.TunnelReady || s.IMSReady || s.SMSReady || s.Phase != "failed" || s.LastErrorClass != "tunnel" {
			t.Fatalf("stale observer state: %+v", s)
		}
	default:
		t.Fatal("tunnel failure not published")
	}
	// A concurrent register-success callback must not resurrect the failed instance.
	i.updateState(func(s *State) { s.TunnelReady = true; s.IMSReady = true; s.SMSReady = true; s.LastErrorClass = "" })
	st := i.State()
	if st.IMSReady || st.SMSReady || st.TunnelReady || st.LastError != "rekey rejected" {
		t.Fatalf("late readiness overwrote tunnel failure: %+v", st)
	}
	i.failSWuSession(context.Background(), errors.New("later Connect result"))
	select {
	case <-observed:
		t.Fatal("duplicate failure notification")
	default:
	}
}

func TestKHiveSWuExplicitStopIsNotFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	i := &Instance{}
	i.failSWuSession(ctx, context.Canceled)
	if i.State().LastErrorClass != "" {
		t.Fatal("explicit cancellation reported as tunnel failure")
	}
	i.stopped = true
	i.failSWuSession(context.Background(), errors.New("late result"))
	if i.State().LastErrorClass != "" {
		t.Fatal("stopped instance reported a new failure")
	}
}
