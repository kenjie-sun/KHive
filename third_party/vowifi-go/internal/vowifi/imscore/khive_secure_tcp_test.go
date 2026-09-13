package imscore

import (
	"bytes"
	"context"
	"fmt"
	"github.com/1239t/vowifi-go/internal/vowifi/ipsec3gpp"
	"github.com/1239t/vowifi-go/runtimehost/voiceclient"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type khivePacketPipe struct {
	rx, tx     chan []byte
	esp, plain atomic.Int64
}

func (p *khivePacketPipe) InnerPackets() <-chan []byte { return p.rx }
func (p *khivePacketPipe) SendInnerPacket(packet []byte) error {
	protocol := packet[9]
	if packet[0]>>4 == 6 {
		protocol = packet[6]
	}
	if protocol == 50 {
		p.esp.Add(1)
	} else {
		p.plain.Add(1)
	}
	select {
	case p.tx <- append([]byte(nil), packet...):
		return nil
	default:
		return fmt.Errorf("test packet queue full")
	}
}
func TestKHiveTCPHandshakeAndPayloadProtectedBelowTCP(t *testing.T) {
	for _, family := range []string{"IPv4", "IPv6"} {
		t.Run(family, func(t *testing.T) {
			local, remote := net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.1")
			if family == "IPv6" {
				local = net.ParseIP("2001:db8::2")
				remote = net.ParseIP("2001:db8::1")
			}
			a, b := make(chan []byte, 128), make(chan []byte, 128)
			ueDP := &khivePacketPipe{rx: a, tx: b}
			peerDP := &khivePacketPipe{rx: b, tx: a}
			ue, err := voiceclient.NewSWUTCPDialer(local, ueDP)
			if err != nil {
				t.Fatal(err)
			}
			defer ue.Close()
			peer, err := voiceclient.NewSWUTCPDialer(remote, peerDP)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			policy, err := ipsec3gpp.NewPolicy(ipsec3gpp.PolicyInput{LocalIP: local, RemoteIP: remote, LocalSPIc: 111, LocalSPIs: 112, LocalPortC: 5064, LocalPortS: 5063, CK: bytes.Repeat([]byte{1}, 16), IK: bytes.Repeat([]byte{2}, 16), Mech: ipsec3gpp.SecurityMechanism{SPIc: 221, SPIs: 222, PortC: 6201, PortS: 6200, Alg: "hmac-sha-1-96", EAlg: "aes-cbc"}})
			if err != nil {
				t.Fatal(err)
			}
			flip := func(flow ipsec3gpp.Flow) ipsec3gpp.Flow {
				flow.InboundSPI, flow.OutboundSPI = flow.OutboundSPI, flow.InboundSPI
				flow.LocalPort, flow.RemotePort = flow.RemotePort, flow.LocalPort
				return flow
			}
			peerPolicy := ipsec3gpp.Policy{LocalIP: policy.RemoteIP, RemoteIP: policy.LocalIP, FlowC: flip(policy.FlowS), FlowS: flip(policy.FlowC)}
			protect, err := ipsec3gpp.NewTransport(policy)
			if err != nil {
				t.Fatal(err)
			}
			unprotect, err := ipsec3gpp.NewTransport(peerPolicy)
			if err != nil {
				t.Fatal(err)
			}
			ue.(voiceclient.PacketTransformInstaller).SetPacketTransform(protect)
			peer.(voiceclient.PacketTransformInstaller).SetPacketTransform(unprotect)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			listener, err := peer.ListenContextTCP(ctx, remote, 6200)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverErr := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverErr <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				body := make([]byte, 5)
				_, err = io.ReadFull(conn, body)
				if err == nil && !bytes.Equal(body, []byte("hello")) {
					err = fmt.Errorf("payload mismatch")
				}
				if err == nil {
					_, err = conn.Write([]byte("world"))
				}
				serverErr <- err
			}()
			conn, err := ue.DialContextTCP(ctx, local, 5064, remote, 6200)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := conn.Write([]byte("hello")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 5)
			if _, err := io.ReadFull(conn, got); err != nil || string(got) != "world" {
				t.Fatalf("echo=%s err=%v", got, err)
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
			if ueDP.esp.Load() < 2 || peerDP.esp.Load() < 2 || ueDP.plain.Load() != 0 || peerDP.plain.Load() != 0 {
				t.Fatalf("UE esp=%d plain=%d peer esp=%d plain=%d", ueDP.esp.Load(), ueDP.plain.Load(), peerDP.esp.Load(), peerDP.plain.Load())
			}
		})
	}
}
