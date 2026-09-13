package imscore

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
)

const (
	registerTransportReadTimeout  = 12 * time.Second
	registerTransportCandidateGap = 150 * time.Millisecond
)

type stableSIPConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr

	mu     sync.Mutex
	closed bool
}

func wrapStableSIPConn(conn net.Conn) net.Conn {
	if conn == nil {
		return nil
	}
	return &stableSIPConn{
		Conn:   &sipFramingConn{Conn: conn},
		local:  conn.LocalAddr(),
		remote: conn.RemoteAddr(),
	}
}

func (c *stableSIPConn) LocalAddr() net.Addr {
	if c == nil || c.local == nil {
		return nil
	}
	return c.local
}

func (c *stableSIPConn) RemoteAddr() net.Addr {
	if c == nil || c.remote == nil {
		return nil
	}
	return c.remote
}

func (c *stableSIPConn) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

type sipFramingConn struct {
	net.Conn

	mu       sync.Mutex
	lastByte byte
	hasLast  bool
}

func (c *sipFramingConn) Read(p []byte) (int, error) {
	if c == nil || c.Conn == nil {
		return 0, net.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasLast && len(p) > 0 {
		p[0] = c.lastByte
		c.hasLast = false
		n, err := c.Conn.Read(p[1:])
		if err != nil {
			return n + 1, err
		}
		return n + 1, nil
	}

	return c.Conn.Read(p)
}

func (c *sipFramingConn) shouldCoalesceShortCRLF(buf []byte) bool {
	return len(bytesTrim(buf, "\x00\r\n")) == 0 && len(buf) <= 4
}

func (c *sipFramingConn) rememberLastByte(b byte) {
	c.lastByte = b
	c.hasLast = true
}

func bytesTrim(buf []byte, cutset string) []byte {
	start, end := 0, len(buf)
	for start < end && strings.ContainsRune(cutset, rune(buf[start])) {
		start++
	}
	for end > start && strings.ContainsRune(cutset, rune(buf[end-1])) {
		end--
	}
	return buf[start:end]
}

type connRegisterTransport struct {
	conn     net.Conn
	rawConn  net.Conn
	traceID  string
	deviceID string
	reader   *bufio.Reader

	mu       sync.Mutex
	released bool
	closed   bool
}

func newConnRegisterTransport(conn net.Conn, traceID, deviceID string) *connRegisterTransport {
	stable := wrapStableSIPConn(conn)
	return &connRegisterTransport{
		conn:     stable,
		rawConn:  conn,
		traceID:  strings.TrimSpace(traceID),
		deviceID: strings.TrimSpace(deviceID),
		reader:   bufio.NewReaderSize(stable, 8192),
	}
}

func registerTransportDeadline() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VOHIVE_REGISTER_TRANSPORT_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return registerTransportReadTimeout
}

func (t *connRegisterTransport) Send(ctx context.Context, req *sip.Request) error {
	if t == nil || req == nil {
		return fmt.Errorf("imscore: register transport unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.released {
		return fmt.Errorf("imscore: register transport closed")
	}
	payload := []byte(req.String())
	stop := armRegisterDeadline(ctx, t.conn, false)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	for rest := payload; len(rest) > 0; {
		n, err := t.conn.Write(rest)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		rest = rest[n:]
	}
	if strings.TrimSpace(os.Getenv("VOHIVE_SIP_TRACE")) != "" {
		loggerSIPWrite(t.traceID, t.deviceID, "tcp", connLocalAddrString(t.conn), connRemoteAddrString(t.conn), payload)
	}
	return nil
}

// armRegisterDeadline also wakes a blocked read/write on cancellation. Stopping
// waits for the callback before clearing deadlines, so handoff cannot inherit it.
func armRegisterDeadline(ctx context.Context, conn net.Conn, read bool) func() {
	deadline := time.Now().Add(registerTransportDeadline())
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	set := conn.SetWriteDeadline
	if read {
		set = conn.SetReadDeadline
	}
	_ = set(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		_ = set(time.Time{})
	}
}

func (t *connRegisterTransport) ReadResponse(ctx context.Context) (*sip.Response, error) {
	if t == nil {
		return nil, fmt.Errorf("imscore: register transport unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.released {
		return nil, net.ErrClosed
	}
	stop := armRegisterDeadline(ctx, t.conn, true)
	defer stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame, err := readRegisterFrame(t.reader)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		msg, err := sip.ParseMessage(frame)
		if err != nil {
			return nil, err
		}
		res, ok := msg.(*sip.Response)
		if !ok {
			return nil, fmt.Errorf("imscore: unexpected request before REGISTER completed")
		}
		if res.StatusCode < 200 {
			continue
		}
		return res, nil
	}
}

// Read exactly one bounded SIP message; buffered bytes after the final response
// belong to the subsequent SIP reader and must survive ReleaseConn.
func readRegisterFrame(r *bufio.Reader) ([]byte, error) {
	var frame bytes.Buffer
	contentLength := -1
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		if frame.Len()+len(line) > 64*1024 {
			return nil, fmt.Errorf("SIP headers too large")
		}
		if frame.Len() == 0 && bytes.Equal(line, []byte("\r\n")) {
			continue
		}
		frame.Write(line)
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
		key, val, found := strings.Cut(string(line), ":")
		if found && (strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "l")) {
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || n < 0 || n > 1024*1024 || contentLength >= 0 {
				return nil, fmt.Errorf("invalid SIP Content-Length")
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing SIP Content-Length on TCP")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	frame.Write(body)
	return frame.Bytes(), nil
}

type bufferedRegisterConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedRegisterConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (t *connRegisterTransport) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true

	var err error
	if t.conn != nil {
		err = t.conn.Close()
		t.conn = nil
	}
	if t.rawConn != nil {
		if closeErr := t.rawConn.Close(); err == nil {
			err = closeErr
		}
		t.rawConn = nil
	}
	return err
}

func (t *connRegisterTransport) ReleaseConn() net.Conn {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.released = true

	if t.closed || t.conn == nil {
		return nil
	}
	_ = t.conn.SetDeadline(time.Time{})
	conn := &bufferedRegisterConn{Conn: t.conn, reader: t.reader}
	t.conn = nil
	t.rawConn = nil
	return conn
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func connLocalAddrString(conn net.Conn) string {
	if conn == nil || conn.LocalAddr() == nil {
		return ""
	}
	return conn.LocalAddr().String()
}

func connRemoteAddrString(conn net.Conn) string {
	if conn == nil || conn.RemoteAddr() == nil {
		return ""
	}
	return conn.RemoteAddr().String()
}

func loggerSIPWrite(traceID, deviceID, transport, local, remote string, payload []byte) {
	sipTraceLogger{traceID: traceID, deviceID: deviceID}.SIPTraceWrite(transport, local, remote, payload)
}

func loggerSIPRead(traceID, deviceID, transport, local, remote string, payload []byte) {
	sipTraceLogger{traceID: traceID, deviceID: deviceID}.SIPTraceRead(transport, local, remote, payload)
}
