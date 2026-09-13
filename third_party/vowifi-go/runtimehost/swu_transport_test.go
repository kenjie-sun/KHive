//go:build linux

package runtimehost

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

type lostDatagram struct {
	done chan struct{}
	once sync.Once
}

func (d *lostDatagram) SendTo(b []byte, _ *net.UDPAddr) (int, error) { return len(b), nil }
func (d *lostDatagram) RecvFrom([]byte) (int, *net.UDPAddr, error) {
	<-d.done
	return 0, nil, net.ErrClosed
}
func (d *lostDatagram) Close() error        { d.once.Do(func() { close(d.done) }); return nil }
func (d *lostDatagram) LocalAddr() net.Addr { return &net.UDPAddr{} }

func TestKHiveProxyLossPublishesFailureAndStopsWithoutDeadlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	i := &Instance{pipelineCancel: cancel, state: State{TunnelReady: true, IMSReady: true, SMSReady: true}}
	d := &lostDatagram{done: make(chan struct{})}
	finished := make(chan struct{})
	var tr *swuDatagramTransport
	tr = newSWuDatagramTransport(d, &net.UDPAddr{}, func(err error) {
		i.failSWuSession(ctx, err)
		tr.Stop()
		close(finished)
	})
	tr.Start()
	_ = d.Close() // Unexpected receive failure, not an explicit transport Stop.
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("failure callback deadlocked")
	}
	s := i.State()
	if s.TunnelReady || s.IMSReady || s.SMSReady || s.LastErrorClass != "tunnel" {
		t.Fatalf("stale state: %+v", s)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("pipeline remained live")
	}
}

func TestKHiveProxyExplicitStopDoesNotPublishFailure(t *testing.T) {
	d := &lostDatagram{done: make(chan struct{})}
	called := make(chan struct{}, 1)
	tr := newSWuDatagramTransport(d, &net.UDPAddr{}, func(error) { called <- struct{}{} })
	tr.Start()
	tr.Stop()
	tr.Stop()
	select {
	case <-called:
		t.Fatal("intentional Stop reported as failure")
	default:
	}
}
