package upstreamproxy

import (
	"context"
	"encoding/binary"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"testing"
	"time"
)

func TestKHiveProbeRejectsEchoWrongIDSourceAndFragment(t *testing.T) {
	q := dnsmessage.Question{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	target := &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 53}
	for _, name := range []string{"valid", "echo", "wrong-id", "wrong-source", "fragment", "reserved", "wrong-question", "dns-refused"} {
		t.Run(name, func(t *testing.T) {
			msg := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, Response: true}, Questions: []dnsmessage.Question{q}}
			packet := []byte{0, 0, 0, 1, 1, 1, 1, 1, 0, 53}
			switch name {
			case "echo":
				msg.Response = false
			case "wrong-id":
				msg.ID = 43
			case "wrong-source":
				packet[7] = 2
			case "fragment":
				packet[2] = 1
			case "reserved":
				packet[0] = 1
			case "wrong-question":
				msg.Questions[0].Type = dnsmessage.TypeAAAA
			case "dns-refused":
				msg.RCode = dnsmessage.RCodeRefused
			}
			raw, err := msg.Pack()
			if err != nil {
				t.Fatal(err)
			}
			packet = append(packet, raw...)
			_, err = parseProbeDNSReply(packet, target, 42, q)
			wantOK := name == "valid" || name == "dns-refused"
			if (err == nil) != wantOK {
				t.Fatalf("validation %v", err)
			}
		})
	}
}
func TestKHiveProbeCancellationClosesControl(t *testing.T) {
	relay := startDNSProbeRelay(t, "udp4", "127.0.0.1:0", true)
	closed := make(chan struct{})
	addr := startProbeServer(t, func(c net.Conn) {
		defer c.Close()
		readBytes(t, c, 3)
		writeBytes(t, c, []byte{5, 0})
		readBytes(t, c, 10)
		reply := []byte{5, 0, 0, 1, 127, 0, 0, 1}
		reply = binary.BigEndian.AppendUint16(reply, uint16(relay.Port))
		writeBytes(t, c, reply)
		_, _ = io.Copy(io.Discard, c)
		close(closed)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err := ProbeSOCKS5(ctx, ProbeConfig{ProxyAddr: addr, Timeout: 5 * time.Second})
	if err == nil || !r.UDPAssociateOK || r.UDPExchangeOK || time.Since(start) > time.Second {
		t.Fatalf("cancel result %+v %v", r, err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("control connection leaked")
	}
}
