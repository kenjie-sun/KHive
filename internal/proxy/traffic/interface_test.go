package traffic

import (
	"context"
	"testing"
	"time"
)

func TestECMInterfaceCountersDoNotRequireQMI(t *testing.T) {
	if _, err := readInterfaceTrafficCounters(context.Background(), "lo"); err != nil {
		t.Fatal(err)
	}
	if _, err := readInterfaceTrafficCounters(context.Background(), "missing-khive"); err == nil {
		t.Fatal("missing interface reported valid counters")
	}
}
func TestECMRealtimeReportsInterfaceSource(t *testing.T) {
	m := NewRealtimeManager(RealtimeOptions{Interval: 10 * time.Millisecond, ReaderForDevice: func(string) (realtimeCounterReader, error) {
		return realtimeCounterReader{DeviceID: "test", Interface: "lo", Source: trafficCounterSourceInterface, ReadCounters: func(context.Context) (trafficCounters, error) { return trafficCounters{RXBytes: 100, TXBytes: 50}, nil }}, nil
	}})
	ch, stop := m.Subscribe(context.Background(), "test")
	defer stop()
	s := receiveRealtimeSnapshot(t, ch)
	if s.Source != trafficCounterSourceInterface || s.Status == RealtimeStatusError {
		t.Fatalf("wrong ECM source: %+v", s)
	}
}
