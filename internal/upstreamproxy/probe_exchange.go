package upstreamproxy

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"

	"golang.org/x/net/dns/dnsmessage"
)

// Keep the control connection open until the authenticated UDP exchange ends.
// A random transaction ID, matching question and source prevent unrelated UDP
// packets (or a relay merely echoing the request) from passing the probe.
func probeDNSRoundTrip(ctx context.Context, control net.Conn, relay *net.UDPAddr, target string) (int, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return 0, fmt.Errorf("invalid DNS probe target: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return 0, fmt.Errorf("DNS probe target must use an IP address")
	}
	destination, err := net.ResolveUDPAddr("udp", net.JoinHostPort(ip.String(), port))
	if err != nil || destination.Port == 0 {
		return 0, fmt.Errorf("invalid DNS probe port")
	}
	endpoint := *relay
	if endpoint.IP.IsUnspecified() {
		peer, ok := control.RemoteAddr().(*net.TCPAddr)
		if !ok {
			return 0, fmt.Errorf("cannot resolve unspecified UDP relay")
		}
		endpoint.IP = peer.IP
	}
	if endpoint.IP == nil || endpoint.Port == 0 {
		return 0, fmt.Errorf("invalid UDP relay address")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", endpoint.String())
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClose()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	var nonce [2]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, err
	}
	question := dnsmessage.Question{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: binary.BigEndian.Uint16(nonce[:]), RecursionDesired: true}, Questions: []dnsmessage.Question{question}}
	payload, err := query.Pack()
	if err != nil {
		return 0, err
	}
	packet := []byte{0, 0, 0, socks5AtypIPv6}
	if v4 := ip.To4(); v4 != nil {
		packet[3] = socks5AtypIPv4
		packet = append(packet, v4...)
	} else {
		packet = append(packet, ip.To16()...)
	}
	packet = binary.BigEndian.AppendUint16(packet, uint16(destination.Port))
	packet = append(packet, payload...)
	if _, err := connection.Write(packet); err != nil {
		return 0, err
	}
	buffer := make([]byte, 4096)
	for {
		n, err := connection.Read(buffer)
		if err != nil {
			return 0, fmt.Errorf("UDP DNS probe %s: %w", target, err)
		}
		response, err := parseProbeDNSReply(buffer[:n], destination, query.Header.ID, question)
		if err == nil {
			return int(response.Header.RCode), nil
		}
		// Ignore malformed, fragmented, wrong-source or unrelated replies until
		// the shared deadline; a subsequent valid answer may still arrive.
	}
}

func parseProbeDNSReply(packet []byte, target *net.UDPAddr, id uint16, question dnsmessage.Question) (dnsmessage.Message, error) {
	var response dnsmessage.Message
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return response, fmt.Errorf("invalid SOCKS5 UDP header")
	}
	length := 0
	switch packet[3] {
	case socks5AtypIPv4:
		length = 4
	case socks5AtypIPv6:
		length = 16
	default:
		return response, fmt.Errorf("unexpected UDP source type")
	}
	if len(packet) < 4+length+2 {
		return response, fmt.Errorf("short UDP source")
	}
	if !net.IP(packet[4:4+length]).Equal(target.IP) || int(binary.BigEndian.Uint16(packet[4+length:])) != target.Port {
		return response, fmt.Errorf("unexpected UDP source")
	}
	if err := response.Unpack(packet[6+length:]); err != nil {
		return response, err
	}
	if !response.Response || response.ID != id || response.OpCode != 0 || len(response.Questions) != 1 || response.Questions[0] != question {
		return response, fmt.Errorf("unrelated DNS answer")
	}
	// A valid DNS error is still evidence of bidirectional UDP; expose RCode
	// separately instead of mistaking resolver policy for a transport failure.
	return response, nil
}
