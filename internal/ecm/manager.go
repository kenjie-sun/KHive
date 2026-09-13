// Package ecm manages the host side of a Quectel ECM data connection.
package ecm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/1239t/vohive/internal/netprobe"
	"github.com/1239t/vohive/pkg/logger"
)

type Config struct{ Interface, APN, IPVersion string }
type AT interface {
	ExecuteATSilent(string, time.Duration) (string, error)
}
type lease struct {
	IP, Mask, Gateway string
	Seconds           int
}
type host interface {
	Up() error
	Apply(lease) error
	Clear() error
	Ready(string) bool
}
type Manager struct {
	opMu          sync.Mutex
	mu            sync.Mutex
	cfg           Config
	at            AT
	host          host
	acquire       func(context.Context, string) (lease, error)
	current       lease
	expires       time.Time
	renewAt       time.Time
	checkInterval time.Duration
	cancel        context.CancelFunc
	done          chan struct{}
}

func New(cfg Config, at AT) *Manager {
	return &Manager{cfg: cfg, at: at, host: &linuxHost{iface: cfg.Interface}, acquire: acquireLease}
}
func (m *Manager) SetConfig(apn, version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.APN = apn
	m.cfg.IPVersion = version
}
func (m *Manager) Connect() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		if m.readyLocked() {
			return nil
		}
		return fmt.Errorf("ECM connection recovery in progress")
	}
	if m.cfg.IPVersion != "" && m.cfg.IPVersion != "v4" {
		return fmt.Errorf("ECM currently supports IPv4 only; select v4")
	}
	if m.at == nil {
		return fmt.Errorf("ECM AT manager unavailable")
	}
	response, err := m.at.ExecuteATSilent("AT+QCFG=\"usbnet\"", 3*time.Second)
	if err != nil {
		return fmt.Errorf("ECM USB mode query failed: %w", err)
	}
	mode, confirmed := parseUSBNetMode(response)
	if !confirmed {
		return fmt.Errorf("ECM USB mode query returned no valid mode")
	}
	if mode != 1 {
		return fmt.Errorf("ECM requires usbnet=1")
	}
	if err := m.prepareAPN(); err != nil {
		return err
	}
	if err := m.host.Up(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := m.acquire(ctx, m.cfg.Interface)
	if err == nil {
		err = m.host.Apply(l)
	}
	if err != nil {
		cancel()
		_ = m.host.Clear()
		return fmt.Errorf("ECM DHCP: %w", err)
	}
	m.current = l
	m.expires = time.Now().Add(time.Duration(l.Seconds) * time.Second)
	m.renewAt = time.Now().Add(time.Duration(l.Seconds) * time.Second / 2)
	m.cancel = cancel
	m.done = make(chan struct{})
	go m.renew(ctx, m.done)
	logger.Info("ECM 数据连接已建立", "interface", m.cfg.Interface, "private_ip", l.IP, "lease_seconds", l.Seconds)
	return nil
}

func parseUSBNetMode(response string) (int, bool) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+QCFG:") {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+QCFG:")), ",")
		if len(fields) != 2 || strings.TrimSpace(fields[0]) != "\"usbnet\"" {
			continue
		}
		mode, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		return mode, err == nil
	}
	return 0, false
}
func (m *Manager) prepareAPN() error {
	apn := strings.TrimSpace(m.cfg.APN)
	if strings.ContainsAny(apn, "\"\r\n\\") || len(apn) > 100 {
		return fmt.Errorf("invalid ECM APN")
	}
	// Empty means retain the modem/carrier default, not erase an active PDP.
	if apn == "" {
		return nil
	}
	r, err := m.at.ExecuteATSilent("AT+CGDCONT?", 3*time.Second)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(r, "\n") {
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+CGDCONT:")), ",")
		if len(fields) >= 3 && strings.TrimSpace(fields[0]) == "1" && strings.Trim(strings.TrimSpace(fields[2]), "\"") == apn {
			return nil
		}
	}
	_, err = m.at.ExecuteATSilent(fmt.Sprintf("AT+CGDCONT=1,\"IP\",\"%s\"", apn), 5*time.Second)
	return err
}

// Poll local link/address/route health independently of a potentially long
// DHCP lease. DHCP itself runs only at T1 or when the owned connection is lost.
func (m *Manager) renew(ctx context.Context, done chan struct{}) {
	defer close(done)
	interval := m.checkInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		m.mu.Lock()
		delay := interval
		if until := time.Until(m.renewAt); until < delay {
			delay = until
		}
		if m.current.IP != "" {
			if until := time.Until(m.expires); until < delay {
				delay = until
			}
		}
		m.mu.Unlock()
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		healthy := m.readyLocked()
		if healthy && time.Now().Before(m.renewAt) {
			m.mu.Unlock()
			continue
		}
		if m.current.IP != "" && !time.Now().Before(m.expires) {
			if err := m.host.Clear(); err != nil {
				logger.Warn("ECM 过期租约清理失败", "interface", m.cfg.Interface, "err", err)
			}
			m.current = lease{}
		}
		err := m.host.Up()
		iface := m.cfg.Interface
		oldExpires := m.expires
		m.mu.Unlock()
		var l lease
		if err == nil {
			acquireCtx := ctx
			cancel := func() {}
			if time.Now().Before(oldExpires) {
				acquireCtx, cancel = context.WithDeadline(ctx, oldExpires)
			}
			l, err = m.acquire(acquireCtx, iface)
			cancel()
		}
		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		if err == nil {
			err = m.host.Apply(l)
		}
		if err == nil {
			m.current = l
			now := time.Now()
			m.expires = now.Add(time.Duration(l.Seconds) * time.Second)
			m.renewAt = now.Add(time.Duration(l.Seconds) * time.Second / 2)
			logger.Info("ECM 租约已续期或连接已恢复", "interface", iface, "lease_seconds", l.Seconds, "recovering", !healthy)
		} else {
			m.renewAt = time.Now().Add(interval)
			logger.Warn("ECM DHCP 续租或恢复失败", "interface", iface, "err", err)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) Disconnect() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	if cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Connect cannot start another session while cancel remains non-nil.
	m.cancel = nil
	m.done = nil
	m.current = lease{}
	m.expires = time.Time{}
	m.renewAt = time.Time{}
	return m.host.Clear()
}
func (m *Manager) readyLocked() bool {
	return m.current.IP != "" && time.Now().Before(m.expires) && m.host.Ready(m.current.IP)
}
func (m *Manager) IsConnected() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.readyLocked() }
func (m *Manager) GetPrivateIP() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readyLocked() {
		return m.current.IP
	}
	return ""
}
func (m *Manager) GetPrivateIPv6() string { return "" }
func (m *Manager) RotateIP() error {
	return fmt.Errorf("ECM IP rotation is not supported; a DHCP renewal does not rotate the carrier IP")
}
func (m *Manager) GetPublicIPv4AndV6NoCache() (string, string) {
	if !m.IsConnected() {
		return "", ""
	}
	p := netprobe.New(netprobe.Config{Interface: m.cfg.Interface, URLs: []string{"https://api.ipify.org"}, Timeout: 8 * time.Second})
	return p.Probe(context.Background(), netprobe.FamilyV4), ""
}

// The hook only returns lease fields. It never runs the system udhcpc hook,
// replaces the host default route, or changes resolv.conf.
const leaseHook = `#!/bin/sh
case "$1" in
 bound|renew) printf 'KHIVE_ECM_LEASE\n%s\n%s\n%s\n%s\n' "$ip" "$subnet" "$router" "$lease" ;;
esac
`

func acquireLease(parent context.Context, iface string) (lease, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "khive-ecm-")
	if err != nil {
		return lease{}, err
	}
	defer os.RemoveAll(dir)
	hook := filepath.Join(dir, "lease.sh")
	if err = os.WriteFile(hook, []byte(leaseHook), 0700); err != nil {
		return lease{}, err
	}
	cmd := exec.CommandContext(ctx, "udhcpc", "-f", "-n", "-q", "-i", iface, "-t", "4", "-T", "3", "-s", hook)
	output, err := cmd.Output()
	if err != nil {
		return lease{}, fmt.Errorf("lease acquisition failed: %w", err)
	}
	return parseLease(string(output))
}
func parseLease(output string) (lease, error) {
	_, tail, ok := strings.Cut(output, "KHIVE_ECM_LEASE\n")
	if !ok {
		return lease{}, fmt.Errorf("DHCP supplied no lease")
	}
	f := strings.Split(strings.TrimSpace(tail), "\n")
	if len(f) < 4 {
		return lease{}, fmt.Errorf("incomplete DHCP lease")
	}
	l := lease{IP: strings.TrimSpace(f[0]), Mask: strings.TrimSpace(f[1])}
	gateways := strings.Fields(f[2])
	if len(gateways) > 0 {
		l.Gateway = gateways[0]
	}
	l.Seconds, _ = strconv.Atoi(strings.TrimSpace(f[3]))
	ip, mask, gw := net.ParseIP(l.IP).To4(), net.ParseIP(l.Mask).To4(), net.ParseIP(l.Gateway).To4()
	if ip == nil || mask == nil || gw == nil || ip.IsUnspecified() || !ip.IsGlobalUnicast() || !gw.IsGlobalUnicast() || l.Seconds < 4 {
		return lease{}, fmt.Errorf("invalid DHCP lease")
	}
	prefix, bits := net.IPMask(mask).Size()
	if bits != 32 || prefix < 1 || !(&net.IPNet{IP: ip.Mask(net.IPMask(mask)), Mask: net.IPMask(mask)}).Contains(gw) {
		return lease{}, fmt.Errorf("invalid DHCP subnet/gateway")
	}
	return l, nil
}
