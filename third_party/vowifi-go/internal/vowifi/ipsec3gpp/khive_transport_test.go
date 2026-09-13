package ipsec3gpp

import (
	"bytes"
	"net"
	"testing"
)

func TestKHiveIPv6ESPAuthenticationBeforeReplay(t *testing.T) {
	flow := Flow{OutboundSPI: 84, InboundSPI: 42, LocalPort: 5064, RemotePort: 6200, AuthAlg: "hmac-sha-1-96", EncAlg: "aes-cbc", CK: bytes.Repeat([]byte{1}, 16), IK: bytes.Repeat([]byte{2}, 16)}
	other := flow
	other.InboundSPI, other.OutboundSPI = 43, 85
	policy := Policy{LocalIP: net.ParseIP("2001:db8::2").To16(), RemoteIP: net.ParseIP("2001:db8::1").To16(), FlowC: flow, FlowS: other}
	tr, err := NewTransport(policy)
	if err != nil {
		t.Fatal(err)
	}
	sa := cloneSAForInbound(tr.outbound[0].sa, 42)
	payload := buildMinimalTCPSegment(6200, 5064, []byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"))
	esp, err := encapsulateTransport(payload, sa, ipProtoTCP)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), esp...)
	corrupt[7] = 127 // unauthenticated sequence number must not poison replay state
	if _, err = tr.TransformInbound(buildIPv6Packet(policy.RemoteIP, policy.LocalIP, ipProtoESP, corrupt)); err == nil {
		t.Fatal("corrupt ESP accepted")
	}
	packet := buildIPv6Packet(policy.RemoteIP, policy.LocalIP, ipProtoESP, esp)
	got, err := tr.TransformInbound(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got[6] != ipProtoTCP || !bytes.Equal(got[40:], payload) {
		t.Fatal("IPv6 transport payload damaged")
	}
	if _, err = tr.TransformInbound(packet); err == nil {
		t.Fatal("replay accepted")
	}
}

func TestKHiveReplayRejectsInitialZero(t *testing.T) {
	w := NewReplayWindow(64)
	if w.Accept(0) {
		t.Fatal("ESP sequence zero accepted")
	}
	if !w.Accept(1) {
		t.Fatal("valid sequence rejected")
	}
}
