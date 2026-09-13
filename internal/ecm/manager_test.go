package ecm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeAT struct{ calls []string }

func (a *fakeAT) ExecuteATSilent(s string, _ time.Duration) (string, error) {
	a.calls = append(a.calls, s)
	return "+QCFG: \"usbnet\",1", nil
}

type fakeHost struct {
	up      bool
	applied lease
	cleared int
	fail    bool
}

func (h *fakeHost) Up() error { h.up = true; return nil }
func (h *fakeHost) Apply(l lease) error {
	if h.fail {
		return errors.New("route failure")
	}
	h.applied = l
	return nil
}
func (h *fakeHost) Clear() error         { h.up = false; h.applied = lease{}; h.cleared++; return nil }
func (h *fakeHost) Ready(ip string) bool { return h.up && h.applied.IP == ip }
func TestECMDHCPLeaseValidation(t *testing.T) {
	good := "noise\nKHIVE_ECM_LEASE\n192.0.2.10\n255.255.255.0\n192.0.2.1 192.0.2.2\n3600\n"
	l, e := parseLease(good)
	if e != nil || l.Gateway != "192.0.2.1" {
		t.Fatalf("%+v %v", l, e)
	}
	for _, bad := range []string{strings.Replace(good, "3600", "0", 1), strings.Replace(good, "255.255.255.0", "255.0.255.0", 1), strings.Replace(good, "192.0.2.1 192.0.2.2", "198.51.100.1", 1), "no lease"} {
		if _, err := parseLease(bad); err == nil {
			t.Fatal("invalid lease accepted")
		}
	}
}
func TestECMHookDoesNotApplyHostNetwork(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(p, []byte(leaseHook), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", p, "bound")
	cmd.Env = []string{"PATH=/nonexistent", "ip=192.0.2.3", "subnet=255.255.255.0", "router=192.0.2.1", "lease=600"}
	b, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseLease(string(b)); err != nil {
		t.Fatal(err)
	}
}
func TestECMConnectDisconnectAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "rollback", false: "connected"}[fail], func(t *testing.T) {
			h := &fakeHost{fail: fail}
			a := &fakeAT{}
			m := New(Config{Interface: "fake", IPVersion: "v4"}, a)
			m.host = h
			m.acquire = func(context.Context, string) (lease, error) {
				return lease{IP: "192.0.2.3", Mask: "255.255.255.0", Gateway: "192.0.2.1", Seconds: 600}, nil
			}
			err := m.Connect()
			if fail {
				if err == nil || h.up || m.IsConnected() {
					t.Fatal("failed connection left state active")
				}
				return
			}
			if err != nil || !m.IsConnected() {
				t.Fatalf("%v", err)
			}
			if len(a.calls) != 1 {
				t.Fatal("blank APN must not modify PDP")
			}
			if err = m.Disconnect(); err != nil || m.IsConnected() || h.up {
				t.Fatal("disconnect did not clean up")
			}
		})
	}
}
func TestECMCancelRenewalBeforeCleanup(t *testing.T) {
	h := &fakeHost{}
	m := New(Config{Interface: "fake"}, &fakeAT{})
	m.host = h
	started := make(chan struct{})
	calls := 0
	m.acquire = func(ctx context.Context, _ string) (lease, error) {
		calls++
		if calls == 1 {
			return lease{IP: "192.0.2.3", Seconds: 4}, nil
		}
		close(started)
		<-ctx.Done()
		return lease{}, ctx.Err()
	}
	if err := m.Connect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("renewal did not start")
	}
	if err := m.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if h.up || m.GetPrivateIP() != "" {
		t.Fatal("late renewal restored disconnected state")
	}
}

func TestECMRenewsAtHalfLease(t *testing.T) {
	m := New(Config{Interface: "fake"}, &fakeAT{})
	m.host = &fakeHost{}
	m.checkInterval = 10 * time.Millisecond
	renewed := make(chan struct{}, 1)
	calls := 0
	m.acquire = func(context.Context, string) (lease, error) {
		calls++
		if calls > 1 {
			renewed <- struct{}{}
		}
		return lease{IP: "192.0.2.3", Seconds: 4}, nil
	}
	started := time.Now()
	if err := m.Connect(); err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	select {
	case <-renewed:
		if time.Since(started) < 1900*time.Millisecond {
			t.Fatal("renewed before T1")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no T1 renewal")
	}
	if !m.IsConnected() {
		t.Fatal("renewal lost connection")
	}
}

func TestECMRecoversLostLongLease(t *testing.T) {
	m := New(Config{Interface: "fake"}, &fakeAT{})
	h := &fakeHost{}
	m.host = h
	m.checkInterval = 10 * time.Millisecond
	m.acquire = func(context.Context, string) (lease, error) { return lease{IP: "192.0.2.3", Seconds: 43200}, nil }
	if err := m.Connect(); err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	m.mu.Lock()
	h.up = false
	h.applied = lease{}
	m.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for !m.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !m.IsConnected() {
		t.Fatal("link loss waited for long DHCP T1")
	}
}

func TestECMFailedRenewalKeepsValidLeaseThenClearsAtExpiry(t *testing.T) {
	m := New(Config{Interface: "fake"}, &fakeAT{})
	h := &fakeHost{}
	m.host = h
	m.checkInterval = 10 * time.Millisecond
	renewing := make(chan struct{}, 1)
	calls := 0
	m.acquire = func(ctx context.Context, _ string) (lease, error) {
		calls++
		if calls == 1 {
			return lease{IP: "192.0.2.3", Seconds: 4}, nil
		}
		select {
		case renewing <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return lease{}, ctx.Err()
	}
	if err := m.Connect(); err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	// Accelerate this scenario without a production test hook.
	m.mu.Lock()
	m.renewAt = time.Now()
	m.expires = time.Now().Add(100 * time.Millisecond)
	m.mu.Unlock()
	select {
	case <-renewing:
	case <-time.After(time.Second):
		t.Fatal("no renewal")
	}
	if !m.IsConnected() {
		t.Fatal("valid lease discarded during renewal")
	}
	deadline := time.Now().Add(time.Second)
	cleared := false
	for time.Now().Before(deadline) {
		m.mu.Lock()
		cleared = h.cleared > 0 && h.applied.IP == ""
		m.mu.Unlock()
		if cleared {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !cleared || m.IsConnected() {
		t.Fatal("expired lease was not cleared")
	}
}

type modeQueryAT struct {
	response string
	err      error
}

func (a modeQueryAT) ExecuteATSilent(string, time.Duration) (string, error) { return a.response, a.err }
func TestECMUSBModeQueryDistinguishesTransientAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		err            error
		want           string
	}{
		{"query timeout", "", errors.New("timeout"), "query failed"},
		{"empty success", "", nil, "no valid mode"},
		{"other mode", "+QCFG: \"usbnet\",0", nil, "requires usbnet=1"},
		{"prefix is not mode one", "+QCFG: \"usbnet\",10", nil, "requires usbnet=1"},
		{"invalid mode", "+QCFG: \"usbnet\",?", nil, "no valid mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Config{Interface: "fixture"}, modeQueryAT{tc.response, tc.err})
			h := &fakeHost{}
			m.host = h
			err := m.Connect()
			if err == nil || !strings.Contains(err.Error(), tc.want) || h.up {
				t.Fatalf("err=%v host_up=%v", err, h.up)
			}
		})
	}
	for _, response := range []string{"+QCFG: \"usbnet\",1", "\r\n+QCFG: \"usbnet\", 1\r\nOK\r\n"} {
		mode, ok := parseUSBNetMode(response)
		if !ok || mode != 1 {
			t.Fatalf("mode=%d ok=%v", mode, ok)
		}
	}
}
