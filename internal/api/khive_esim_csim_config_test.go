package api

import (
	"github.com/1239t/vohive/internal/config"
	"testing"
)

func TestKHiveDeviceSettingsPreserveCSIMRuntimeChoice(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		base := config.DeviceConfig{ID: "fixture", DeviceBackend: "at", ESIMATCSIM: enabled}
		got := deviceConfigFromDTOWithBase(deviceConfigDTO{ID: "fixture", Name: "renamed", DeviceBackend: "at"}, &base)
		if got.ESIMATCSIM != enabled {
			t.Fatal("ordinary settings save changed CSIM transport")
		}
	}
	if deviceConfigFromDTO(deviceConfigDTO{ID: "new"}).ESIMATCSIM {
		t.Fatal("new device should default to CGLA")
	}
}
