package transport

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestKHiveSOCKSControlCloseIsTerminal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closeControl := make(chan struct{})
	serverResult := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err = io.ReadFull(c, make([]byte, 3)); err == nil {
			_, err = c.Write([]byte{5, 0})
		}
		if err == nil {
			_, err = io.ReadFull(c, make([]byte, 10))
		}
		if err == nil {
			_, err = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 9})
		}
		serverResult <- err
		if err == nil {
			<-closeControl
		}
	}()
	p, err := NewSocks5UDPTransport(listener.Addr().String(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	received := make(chan error, 1)
	go func() { _, _, err := p.RecvFrom(make([]byte, 1024)); received <- err }()
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	close(closeControl)
	select {
	case err := <-received:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("receive: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UDP reader survived SOCKS control close")
	}
	if _, err := p.SendTo([]byte{1}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed sender reopened: %v", err)
	}
	if _, _, err := p.RecvFrom(make([]byte, 1024)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed receiver reopened: %v", err)
	}
}
