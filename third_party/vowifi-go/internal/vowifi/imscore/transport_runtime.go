package imscore

import (
	"context"
	"fmt"
	"github.com/1239t/vowifi-go/internal/vowifi/ipsec3gpp"
	"github.com/1239t/vowifi-go/runtimehost/voiceclient"
	"net"
	"sync"
)

// The SIP transport is the sole reader and writer of both TCP connections.
// ESP protection is handled below TCP by the shared SWu netstack.
type transportRuntime struct {
	portSListener net.Listener
	portCConn     net.Conn
	stop          func() bool
	once          sync.Once
}

func startTransportRuntime(parent context.Context, cfg Config, swu voiceclient.SWUTCPDialer, policy ipsec3gpp.Policy, transport *ipsec3gpp.Transport, portCConn net.Conn, listener net.Listener) (*transportRuntime, error) {
	if swu == nil || portCConn == nil || transport == nil {
		return nil, fmt.Errorf("imscore: missing secure transport")
	}
	if listener == nil {
		return nil, fmt.Errorf("IMS port-s listener not established during registration")
	}
	rt := &transportRuntime{portSListener: listener, portCConn: portCConn}
	rt.stop = context.AfterFunc(parent, rt.Close)
	return rt, nil
}
func (rt *transportRuntime) Close() {
	if rt == nil {
		return
	}
	rt.once.Do(func() {
		if rt.portSListener != nil {
			_ = rt.portSListener.Close()
		}
		if rt.portCConn != nil {
			_ = rt.portCConn.Close()
		}
	})
}
