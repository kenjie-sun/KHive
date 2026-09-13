package esim

import (
	"bytes"
	"github.com/1239t/vohive/internal/backend"
	"github.com/1239t/vohive/internal/modem"
	"testing"
)

func TestKHiveCSIMChannelRequiresExplicitDeviceOption(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m := &modem.Manager{}
		manager, err := NewManager(ManagerOptions{DeviceID: "fixture", Transport: "at", Modem: m, Backend: backend.NewATBackend(m), ATUseCSIM: enabled})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := manager.smartCardChannelFactory()
		if err != nil {
			t.Fatal(err)
		}
		ch, ok := raw.(*ModemChannel)
		if !ok || ch.useCSIM != enabled {
			t.Fatal("wrong APDU transport")
		}
		if !enabled && ch.apduTransportName() != "cgla" {
			t.Fatal("default changed")
		}
	}
}

func TestKHiveCSIMEncodesRawAndLPAChannelsWithoutChangingPayload(t *testing.T) {
	for _, tc := range []struct {
		original byte
		channel  byte
		want     byte
	}{
		{0x00, 2, 0x02}, {0x82, 2, 0x82}, {0x80, 19, 0xCF},
	} {
		original := []byte{tc.original, 0, 3, 0, 0}
		got, err := logicalCSIMCommand(original, tc.channel)
		if err != nil {
			t.Fatal(err)
		}
		if original[0] != tc.original || got[0] != tc.want || !bytes.Equal(original[1:], got[1:]) {
			t.Fatal("channel or payload changed incorrectly")
		}
	}
	if _, err := logicalCSIMCommand([]byte{0, 0, 3, 0, 0}, 0); err == nil {
		t.Fatal("accepted unopened channel")
	}
}
