package ecm

import (
	"net"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"

	"github.com/iniwex5/netlink"
	"golang.org/x/sys/unix"
)

// This test creates its own network namespace before any netlink mutation.
// Normal unprivileged unit runs skip it; the explicit root invocation only
// launches the isolated child and never changes the host namespace.
func TestECMIsolatedRouteOwnership(t *testing.T) {
	if os.Getenv("KHIVE_ECM_NETNS") != "1" {
		if os.Geteuid() != 0 {
			t.Skip("requires root to create an isolated network namespace")
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestECMIsolatedRouteOwnership$", "-test.v")
		cmd.Env = []string{"KHIVE_ECM_NETNS=1"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated child: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	}
	actual, err := netlink.LinkByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.LinkSetUp(actual); err != nil {
		t.Fatal(err)
	}
	sentinel := netlink.Route{Dst: &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_BLACKHOLE, Priority: 999}
	if err = netlink.RouteAdd(&sentinel); err != nil {
		t.Fatal(err)
	}
	before, _ := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	h := &linuxHost{iface: "lo", link: actual, wasUp: true}
	for _, gw := range []string{"192.0.2.1", "192.0.2.2"} {
		if err = h.Apply(lease{IP: "192.0.2.3", Mask: "255.255.255.0", Gateway: gw, Seconds: 600}); err != nil {
			t.Fatal(err)
		}
		routes, e := netlink.RouteGetWithOptions(net.ParseIP("198.51.100.1"), &netlink.RouteGetOptions{Oif: "lo"})
		if e != nil || len(routes) == 0 || routes[0].LinkIndex != actual.Attrs().Index || routes[0].Gw.String() != gw {
			t.Fatalf("bound route: %+v %v", routes, e)
		}
		after, _ := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("ECM changed main routing table")
		}
	}

	for _, loss := range []string{"address", "route", "rule"} {
		l := lease{IP: "192.0.2.3", Mask: "255.255.255.0", Gateway: "192.0.2.2", Seconds: 600}
		if !h.Ready(l.IP) {
			t.Fatalf("not ready before %s loss", loss)
		}
		switch loss {
		case "address":
			err = netlink.AddrDel(actual, h.addr)
		case "route":
			err = netlink.RouteDel(&h.routes[1])
		case "rule":
			err = netlink.RuleDel(&h.rules[0])
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Ready(l.IP) {
			t.Fatalf("%s loss not detected", loss)
		}
		if err = h.Apply(l); err != nil {
			t.Fatalf("repair %s: %v", loss, err)
		}
		if !h.Ready(l.IP) {
			t.Fatalf("%s not repaired", loss)
		}
	}
	if err = h.Clear(); err != nil {
		t.Fatal(err)
	}
	addresses, _ := netlink.AddrList(actual, netlink.FAMILY_V4)
	for _, address := range addresses {
		if address.IP.String() == "192.0.2.3" {
			t.Fatal("owned address leaked")
		}
	}
	routes, _ := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: 20000 + actual.Attrs().Index}, netlink.RT_FILTER_TABLE)
	if len(routes) != 0 {
		t.Fatal("owned route leaked")
	}
	rules, _ := netlink.RuleList(netlink.FAMILY_V4)
	for _, r := range rules {
		if r.Table == 20000+actual.Attrs().Index {
			t.Fatal("owned rule leaked")
		}
	}
	after, _ := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cleanup changed host routes")
	}
	testECMReenumeratedInterface(t)

}

// Runs only inside the namespace established by the parent test. A TUN link
// supplies removable kernel interfaces; production still requires cdc_ether.
func testECMReenumeratedInterface(t *testing.T) {
	makeLink := func() *netlink.Tuntap {
		link := &netlink.Tuntap{LinkAttrs: netlink.LinkAttrs{Name: "khive-ecm"}, Mode: netlink.TUNTAP_MODE_TUN, Queues: 1, Flags: netlink.TUNTAP_DEFAULTS}
		if err := netlink.LinkAdd(link); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, fd := range link.Fds {
				fd.Close()
			}
		})
		if err := netlink.LinkSetUp(link); err != nil {
			t.Fatal(err)
		}
		return link
	}
	old := makeLink()
	oldIndex := old.Attrs().Index
	h := &linuxHost{iface: "khive-ecm", link: old, wasUp: true}
	l := lease{IP: "192.0.2.3", Mask: "255.255.255.0", Gateway: "192.0.2.1", Seconds: 600}
	if err := h.Apply(l); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkDel(old); err != nil {
		t.Fatal(err)
	}
	current := makeLink()
	defer netlink.LinkDel(current)
	if current.Attrs().Index == oldIndex {
		t.Fatal("kernel did not replace interface index")
	}
	if h.Ready(l.IP) {
		t.Fatal("removed interface reported ready")
	}
	// Up must clear old ownership, then reject this non-ECM fixture by driver.
	if err := h.Up(); err == nil {
		t.Fatal("TUN accepted as a production ECM interface")
	}
	if h.link != nil || h.addr != nil || len(h.routes) != 0 || len(h.rules) != 0 {
		t.Fatal("old interface ownership retained")
	}
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Table == 20000+oldIndex {
			t.Fatal("old interface rule leaked")
		}
	}
	// Validate lease installation/cleanup on the new kernel interface separately.
	h.link = current
	h.wasUp = true
	if err := h.Apply(l); err != nil {
		t.Fatal(err)
	}
	if !h.Ready(l.IP) {
		t.Fatal("new interface lease not ready")
	}
	if err := h.Clear(); err != nil {
		t.Fatal(err)
	}
}
