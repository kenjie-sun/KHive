package config

import "testing"

func TestKHiveCSIMConfigSurvivesDeviceSettingsSave(t *testing.T) {
	path := writeTempConfig(t, "devices:\n- id: fixture\n  device_backend: at\n  esim_at_csim: true\n- id: default\n  device_backend: at\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Devices[0].ESIMATCSIM || cfg.Devices[1].ESIMATCSIM {
		t.Fatal("explicit transport/default not loaded")
	}
	// The UI's ordinary settings update does not own this advanced YAML option.
	if err := UpdateDeviceInFile(path, "fixture", DeviceConfig{ID: "fixture", DeviceBackend: "at"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Devices[0].ESIMATCSIM {
		t.Fatal("settings save removed explicit CSIM transport")
	}
	if err := AddDeviceInFile(path, DeviceConfig{ID: "added", DeviceBackend: "at", ESIMATCSIM: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Devices[2].ESIMATCSIM {
		t.Fatal("new device lost CSIM setting")
	}
}
