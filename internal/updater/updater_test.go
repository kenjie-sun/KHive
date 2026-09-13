package updater

import (
	"errors"
	"testing"
)

func TestKHiveUpdatesRemainUnavailableWithoutReleaseChannel(t *testing.T) {
	info, err := CheckUpdate()
	if err != nil || info.Enabled || info.HasUpdate || info.DisabledReason == "" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if !errors.Is(ApplyUpdate(), ErrUpdatesDisabled) {
		t.Fatal("update should be unavailable")
	}
}
