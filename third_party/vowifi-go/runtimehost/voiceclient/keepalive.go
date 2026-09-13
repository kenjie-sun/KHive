package voiceclient

import (
	"io"
	"math/rand/v2"
	"time"
)

// CRLF pings keep the established IMS TCP flow active (RFC 5626 section 4.4).
// No outbound negotiation is assumed, so lack of a pong alone is not failure.
// Writes are serialized with complete SIP frames, without retrying any SMS.
func (c *Client) signalingKeepaliveLoop() {
	for {
		timer := time.NewTimer(time.Duration(24+rand.IntN(7)) * time.Second)
		select {
		case <-c.lifecycleCtx.Done():
			timer.Stop()
			return
		case <-c.failure:
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := c.signaling.keepalive(); err != nil {
			c.failSignaling(err)
			return
		}
		c.mu.Lock()
		c.keepalives++
		c.mu.Unlock()
	}
}

func (c *observedSignalingConn) keepalive() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer c.Conn.SetWriteDeadline(time.Time{})
	n, err := c.Conn.Write([]byte("\r\n\r\n"))
	if err == nil && n != 4 {
		err = io.ErrShortWrite
	}
	return err
}

// SignalingStatus exposes successful writes and reads separately: writing a
// keepalive is not proof that the carrier received or answered it.
func (c *Client) SignalingStatus() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]interface{}{
		"ims_connected_at":       c.connectedAt,
		"signaling_last_read_at": c.lastReadAt,
		"signaling_keepalives":   c.keepalives,
		"ims_refreshes":          c.refreshes,
		"ims_last_refresh_at":    c.lastRefreshAt,
	}
}
