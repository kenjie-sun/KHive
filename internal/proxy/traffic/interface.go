package traffic

import (
	"context"
	"fmt"

	"github.com/iniwex5/netlink"
)

// ECM exposes Ethernet counters, not QMI WDS statistics. These include host
// interface overhead and are not the carrier's billing counters.
func readInterfaceTrafficCounters(ctx context.Context, iface string) (trafficCounters, error) {
	if err := ctx.Err(); err != nil {
		return trafficCounters{}, err
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return trafficCounters{}, err
	}
	stats := link.Attrs().Statistics
	if stats == nil {
		return trafficCounters{}, fmt.Errorf("interface counters unavailable")
	}
	return trafficCounters{RXBytes: stats.RxBytes, TXBytes: stats.TxBytes}, nil
}
