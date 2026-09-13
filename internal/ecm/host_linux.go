package ecm

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/iniwex5/netlink"
	"golang.org/x/sys/unix"
)

// Every route lives in a dedicated table selected by source address or bound
// output interface. No default route is installed in the host's main table.
type linuxHost struct {
	iface  string
	link   netlink.Link
	wasUp  bool
	addr   *netlink.Addr
	routes []netlink.Route
	rules  []netlink.Rule
}

func (h *linuxHost) Up() error {
	if h.link != nil {
		current, lookupErr := netlink.LinkByName(h.iface)
		if lookupErr == nil && current.Attrs().Index == h.link.Attrs().Index {
			return netlink.LinkSetUp(current)
		}
		// USB re-enumeration creates a different ifindex. Remove only objects
		// owned by the old link before adopting the newly discovered interface.
		if err := h.clearLease(); err != nil {
			return err
		}
		h.link = nil
		if lookupErr != nil {
			return lookupErr
		}
	}
	if h.iface == "" || strings.ContainsAny(h.iface, "/\x00") {
		return fmt.Errorf("invalid ECM interface")
	}
	driver, err := os.Readlink(filepath.Join("/sys/class/net", h.iface, "device/driver"))
	if err != nil || filepath.Base(driver) != "cdc_ether" {
		return fmt.Errorf("%s is not an ECM cdc_ether interface", h.iface)
	}
	l, err := netlink.LinkByName(h.iface)
	if err != nil {
		return err
	}
	if l.Attrs().MasterIndex != 0 {
		return fmt.Errorf("ECM interface belongs to another network manager/bridge")
	}
	addresses, err := netlink.AddrList(l, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	if len(addresses) > 0 {
		return fmt.Errorf("ECM interface already has an unmanaged IPv4 address")
	}
	table := 20000 + l.Attrs().Index
	existing, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return fmt.Errorf("ECM routing table %d is already in use", table)
	}
	h.link = l
	h.wasUp = l.Attrs().Flags&net.FlagUp != 0
	if err = netlink.LinkSetUp(l); err != nil {
		h.link = nil
		return err
	}
	return nil
}
func (h *linuxHost) Apply(l lease) (err error) {
	if h.link == nil {
		return fmt.Errorf("ECM link is not prepared")
	}
	prefix, _ := net.IPMask(net.ParseIP(l.Mask).To4()).Size()
	ip := net.ParseIP(l.IP).To4()
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: net.CIDRMask(prefix, 32)}, Flags: unix.IFA_F_NOPREFIXROUTE}
	if h.addr != nil && h.addr.IPNet.String() == addr.IPNet.String() && len(h.routes) == 2 && h.routes[1].Gw.Equal(net.ParseIP(l.Gateway)) && h.Ready(l.IP) {
		return nil
	}
	if err = h.clearLease(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = h.clearLease()
		}
	}()
	if err = netlink.AddrAdd(h.link, addr); err != nil {
		return err
	}
	h.addr = addr
	idx := h.link.Attrs().Index
	table := 20000 + idx
	for _, r := range []netlink.Route{
		{LinkIndex: idx, Table: table, Dst: &net.IPNet{IP: ip.Mask(addr.Mask), Mask: addr.Mask}, Scope: netlink.SCOPE_LINK, Src: ip, Protocol: unix.RTPROT_STATIC},
		{LinkIndex: idx, Table: table, Gw: net.ParseIP(l.Gateway).To4(), Protocol: unix.RTPROT_STATIC},
	} {
		if err = netlink.RouteAdd(&r); err != nil {
			return err
		}
		h.routes = append(h.routes, r)
	}
	source := netlink.NewRule()
	source.Family = netlink.FAMILY_V4
	source.Table = table
	source.Priority = 20000 + idx*2
	source.Src = &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
	output := netlink.NewRule()
	output.Family = netlink.FAMILY_V4
	output.Table = table
	output.Priority = source.Priority + 1
	output.OifName = h.iface
	for _, r := range []*netlink.Rule{source, output} {
		if err = netlink.RuleAdd(r); err != nil {
			return err
		}
		h.rules = append(h.rules, *r)
	}
	return nil
}

// Kernel removal of an address/link may already have removed these objects.
func missingNetworkObject(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EADDRNOTAVAIL) || errors.Is(err, unix.ENODEV)
}

func (h *linuxHost) clearLease() error {
	var first error
	for i := len(h.rules) - 1; i >= 0; i-- {
		if e := netlink.RuleDel(&h.rules[i]); e != nil && !missingNetworkObject(e) && first == nil {
			first = e
		}
	}
	h.rules = nil
	for i := len(h.routes) - 1; i >= 0; i-- {
		if e := netlink.RouteDel(&h.routes[i]); e != nil && !missingNetworkObject(e) && first == nil {
			first = e
		}
	}
	h.routes = nil
	if h.addr != nil {
		if e := netlink.AddrDel(h.link, h.addr); e != nil && !missingNetworkObject(e) && first == nil {
			first = e
		}
		h.addr = nil
	}
	return first
}
func (h *linuxHost) Clear() error {
	err := h.clearLease()
	if h.link != nil && !h.wasUp {
		if e := netlink.LinkSetDown(h.link); err == nil && !missingNetworkObject(e) {
			err = e
		}
	}
	h.link = nil
	return err
}
func (h *linuxHost) Ready(ip string) bool {
	l, err := netlink.LinkByName(h.iface)
	if err != nil || h.link == nil || l.Attrs().Index != h.link.Attrs().Index || l.Attrs().Flags&net.FlagUp == 0 || l.Attrs().Flags&net.FlagRunning == 0 {
		return false
	}
	addrs, err := netlink.AddrList(l, netlink.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a.IP.String() == ip {
			return h.leaseRoutesReady()
		}
	}
	return false
}

func (h *linuxHost) leaseRoutesReady() bool {
	if h.link == nil || len(h.routes) != 2 || len(h.rules) != 2 {
		return false
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: 20000 + h.link.Attrs().Index}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return false
	}
	for _, want := range h.routes {
		found := false
		for _, got := range routes {
			sameDst := routeDestination(want.Dst) == routeDestination(got.Dst)
			if sameDst && want.LinkIndex == got.LinkIndex && want.Gw.Equal(got.Gw) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, want := range h.rules {
		found := false
		for _, got := range rules {
			sameSrc := (want.Src == nil && got.Src == nil) || (want.Src != nil && got.Src != nil && want.Src.String() == got.Src.String())
			if sameSrc && want.Table == got.Table && want.Priority == got.Priority && want.OifName == got.OifName {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func routeDestination(dst *net.IPNet) string {
	if dst == nil {
		return "0.0.0.0/0"
	}
	return dst.String()
}
