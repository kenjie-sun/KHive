package e911

import (
	"github.com/1239t/vowifi-go/runtimehost/carrier"
	"os"
	"path/filepath"
	"testing"

	"github.com/1239t/vohive/internal/modem"
)

func TestSetupAvailableUsesNativePLMN(t *testing.T) {
	status := modem.DeviceStatus{
		IMSI:      "999990000000001",
		NativeMCC: "310",
		NativeMNC: "280",
	}
	if !SetupAvailable(status) {
		t.Fatal("SetupAvailable=false, want true for native 310/280")
	}
}

func TestSetupAvailableRejectsUnsupportedCarrier(t *testing.T) {
	status := modem.DeviceStatus{
		IMSI:      "460001234567890",
		NativeMCC: "460",
		NativeMNC: "00",
	}
	if SetupAvailable(status) {
		t.Fatal("SetupAvailable=true, want false for unsupported carrier")
	}
}

func TestKHiveE911UsesSIMRule(t *testing.T) {
	carrier.ClearCarrierOverrides()
	t.Cleanup(carrier.ClearCarrierOverrides)
	path := filepath.Join(t.TempDir(), "carrier.json")
	if err := os.WriteFile(path, []byte(`[{"id":"mvno-no-e911","mcc":"310","mnc":"280","match":{"spn":["Example MVNO"]},"e911_enabled":false}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := carrier.LoadCarrierOverrides(path); err != nil {
		t.Fatal(err)
	}
	status := modem.DeviceStatus{IMSI: "310280000000001", NativeMCC: "310", NativeMNC: "280", NativeSPN: "Example MVNO"}
	if SetupAvailable(status) {
		t.Fatal("MVNO inherited host-network E911")
	}
	status.NativeSPN = "Host"
	if !SetupAvailable(status) {
		t.Fatal("legacy host E911 fallback lost")
	}
}
