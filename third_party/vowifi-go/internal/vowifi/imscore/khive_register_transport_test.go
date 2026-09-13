package imscore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/1239t/vowifi-go/engine/sim"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestKHiveRegisterHandoffPreservesCoalescedMessage(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	transport := newConnRegisterTransport(client, "", "")
	response := "SIP/2.0 100 Trying\r\nContent-Length: 0\r\n\r\nSIP/2.0 200 OK\r\nContent-Length: 3\r\n\r\nabc"
	message := "MESSAGE sip:test@example.invalid SIP/2.0\r\nContent-Length: 4\r\n\r\nbody"
	go func() { _, _ = server.Write([]byte(response + message)) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := transport.ReadResponse(ctx)
	if err != nil || res.StatusCode != 200 || string(res.Body()) != "abc" {
		t.Fatalf("response: %v %v", res, err)
	}
	conn := transport.ReleaseConn()
	defer conn.Close()
	_ = transport.Close()
	cancel()
	got := make([]byte, len(message))
	_, err = io.ReadFull(conn, got)
	if err != nil || string(got) != message {
		t.Fatalf("handoff lost data: %q %v", got, err)
	}
}
func TestKHiveRegisterReadCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	transport := newConnRegisterTransport(client, "", "")
	defer transport.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := transport.ReadResponse(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked read ignored cancellation")
	}
}
func TestKHiveRegisterFramingRejectsInvalidLength(t *testing.T) {
	for _, headers := range []string{"Content-Length: -1", "Content-Length: 1048577", "Content-Length: 0\r\nl: 1", "X: no-length"} {
		_, err := readRegisterFrame(bufio.NewReader(strings.NewReader("SIP/2.0 200 OK\r\n" + headers + "\r\n\r\n")))
		if err == nil {
			t.Fatalf("accepted %s", headers)
		}
	}
}

type khiveAKA struct{ calls int }

func (a *khiveAKA) CalculateAKA(r, n []byte) (sim.AKAResult, error) {
	a.calls++
	return sim.AKAResult{RES: []byte{0, 1, 128, 255}, CK: bytes.Repeat([]byte{1}, 16), IK: bytes.Repeat([]byte{2}, 16)}, nil
}
func TestKHiveAKAOneSIMOperationAndFullURI(t *testing.T) {
	provider := &khiveAKA{}
	req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", Host: "ims.example.invalid"})
	aka, auth, err := computeAKAAuth(Config{AKA: provider, PrivateID: "test@ims.example.invalid"}, &digest.Challenge{Realm: "ims.example.invalid", Algorithm: "AKAv1-MD5", Nonce: base64.StdEncoding.EncodeToString(make([]byte, 32))}, req)
	if err != nil || provider.calls != 1 || len(aka.CK) != 16 || !strings.Contains(auth, `uri="sip:ims.example.invalid"`) {
		t.Fatalf("calls=%d keys=%d auth=%s err=%v", provider.calls, len(aka.CK), auth, err)
	}
}
