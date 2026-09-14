package upstreamproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestProbeSOCKS5Success(t *testing.T) {
	relay := startDNSProbeRelay(t, "udp4", "127.0.0.1:0", false)
	addr := startProbeServer(t, func(conn net.Conn) {
		defer conn.Close()
		readBytes(t, conn, 3)
		writeBytes(t, conn, []byte{0x05, 0x00})
		readBytes(t, conn, 10)
		writeBytes(t, conn, []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, byte(relay.Port >> 8), byte(relay.Port)})
		_, _ = io.Copy(io.Discard, conn)
	})

	result, err := ProbeSOCKS5(context.Background(), ProbeConfig{
		ProxyAddr: addr,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !result.OK() {
		t.Fatalf("expected success result, got %+v", result)
	}
	if result.Stage != ProbeStageOK {
		t.Fatalf("stage mismatch: got=%q want=%q", result.Stage, ProbeStageOK)
	}
}

func TestProbeSOCKS5HandshakeEOF(t *testing.T) {
	addr := startProbeServer(t, func(conn net.Conn) {
		_ = conn.Close()
	})

	result, err := ProbeSOCKS5(context.Background(), ProbeConfig{
		ProxyAddr: addr,
		Timeout:   2 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected handshake error, got success: %+v", result)
	}
	if result.Stage != ProbeStageHandshake {
		t.Fatalf("stage mismatch: got=%q want=%q", result.Stage, ProbeStageHandshake)
	}
	if result.HandshakeOK {
		t.Fatalf("handshake should not be marked ok: %+v", result)
	}
}

func TestProbeSOCKS5UDPAssociateEOF(t *testing.T) {
	addr := startProbeServer(t, func(conn net.Conn) {
		defer conn.Close()
		readBytes(t, conn, 3)
		writeBytes(t, conn, []byte{0x05, 0x00})
		readBytes(t, conn, 10)
	})

	result, err := ProbeSOCKS5(context.Background(), ProbeConfig{
		ProxyAddr: addr,
		Timeout:   2 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected udp associate error, got success: %+v", result)
	}
	if result.Stage != ProbeStageUDPAssociate {
		t.Fatalf("stage mismatch: got=%q want=%q", result.Stage, ProbeStageUDPAssociate)
	}
	if !result.HandshakeOK {
		t.Fatalf("handshake should be marked ok: %+v", result)
	}
	if result.UDPAssociateOK {
		t.Fatalf("udp associate should not be marked ok: %+v", result)
	}
}

func TestProbeSOCKS5UsesIPv6UDPAssociateForIPv6Proxy(t *testing.T) {
	relay := startDNSProbeRelay(t, "udp6", "[::1]:0", false)
	atypCh := make(chan byte, 1)
	addr := startProbeServerOn(t, "tcp6", "[::1]:0", func(conn net.Conn) {
		defer conn.Close()
		readBytes(t, conn, 3)
		writeBytes(t, conn, []byte{0x05, 0x00})

		header := readBytes(t, conn, 4)
		atypCh <- header[3]
		switch header[3] {
		case socks5AtypIPv4:
			readBytes(t, conn, 6)
		case socks5AtypIPv6:
			readBytes(t, conn, 18)
		default:
			t.Fatalf("unexpected UDP ASSOCIATE ATYP: 0x%02x", header[3])
		}

		resp := make([]byte, 4+16+2)
		resp[0] = socks5Version
		resp[1] = socks5ReplySuccess
		resp[3] = socks5AtypIPv6
		copy(resp[4:20], net.IPv6loopback.To16())
		binary.BigEndian.PutUint16(resp[20:], uint16(relay.Port))
		writeBytes(t, conn, resp)
		_, _ = io.Copy(io.Discard, conn)
	})

	result, err := ProbeSOCKS5(context.Background(), ProbeConfig{
		ProxyAddr: addr,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !result.OK() {
		t.Fatalf("expected success result, got %+v", result)
	}

	select {
	case atyp := <-atypCh:
		if atyp != socks5AtypIPv6 {
			t.Fatalf("UDP ASSOCIATE ATYP = 0x%02x, want IPv6 0x%02x", atyp, socks5AtypIPv6)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for UDP ASSOCIATE request")
	}
}

func startProbeServer(t *testing.T, handler func(net.Conn)) string {
	return startProbeServerOn(t, "tcp", "127.0.0.1:0", handler)
}

func startProbeServerOn(t *testing.T, network, addr string, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handler(conn)
	}()

	return ln.Addr().String()
}

func readBytes(t *testing.T, r io.Reader, size int) []byte {
	t.Helper()
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	return buf
}

func writeBytes(t *testing.T, w io.Writer, payload []byte) {
	t.Helper()
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

func startDNSProbeRelay(t *testing.T, network, address string, drop bool) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenPacket(network, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, source, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			if drop || n < 22 {
				continue
			}
			packet := append([]byte(nil), buffer[:n]...)
			packet[12] |= 0x80 // DNS QR, after the IPv4 SOCKS5 UDP header.
			_, _ = conn.WriteTo(packet, source)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

func TestProbeRejectsUDPAssociateWithoutData(t *testing.T) {
	relay := startDNSProbeRelay(t, "udp4", "127.0.0.1:0", true)
	address := startProbeServer(t, func(conn net.Conn) {
		defer conn.Close()
		readBytes(t, conn, 3)
		writeBytes(t, conn, []byte{5, 0})
		readBytes(t, conn, 10)
		writeBytes(t, conn, []byte{5, 0, 0, 1, 0, 0, 0, 0, byte(relay.Port >> 8), byte(relay.Port)})
		_, _ = io.Copy(io.Discard, conn)
	})
	result, err := ProbeSOCKS5(context.Background(), ProbeConfig{ProxyAddr: address, Timeout: 100 * time.Millisecond})
	if err == nil || result.OK() || !result.UDPAssociateOK || result.UDPExchangeOK || result.Stage != ProbeStageUDPExchange {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}
