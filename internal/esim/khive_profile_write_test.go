package esim

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
)

func TestKHiveProfileWritesRejectOverlapAndStaleManager(t *testing.T) {
	for _, mode := range []string{"write", "switch", "stopped", "read_lock"} {
		t.Run(mode, func(t *testing.T) {
			var opens atomic.Int32
			m := NewManagerWithChannelFactory("test", func([]byte) (*lpa.Client, error) { opens.Add(1); return nil, errors.New("must not open") }, nil, nil, nil)
			switch mode {
			case "write":
				release, err := m.tryProfileWrite()
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				if m.beginSwitchProgress(SwitchOperationEnableProfile, "target") {
					t.Fatal("switch accepted during write")
				}
			case "switch":
				m.beginSwitchProgress(SwitchOperationEnableProfile, "target")
			case "stopped":
				m.StopSwitchRecovery()
			case "read_lock":
				m.opMu.Lock()
				defer m.opMu.Unlock()
			}
			if _, err := m.DownloadProfile(context.Background(), "A0000005591010", "example.com", "code", "", "", nil); err == nil {
				t.Fatal("download accepted")
			}
			if _, err := m.DeleteProfile("8986000000000000001", "A0000005591010"); err == nil {
				t.Fatal("delete accepted")
			}
			if err := m.RenameProfile("8986000000000000001", "name", "A0000005591010"); err == nil {
				t.Fatal("rename accepted")
			}
			if opens.Load() != 0 {
				t.Fatalf("opened %d channels", opens.Load())
			}
		})
	}
	m := newTestManagerWithOverviewLoader(nil)
	release, err := m.tryProfileWrite()
	if err != nil {
		t.Fatal(err)
	}
	release()
	release, err = m.tryProfileWrite()
	if err != nil {
		t.Fatal("gate not released", err)
	}
	release()
}

func TestKHiveDeleteRequiresFreshDisabledTarget(t *testing.T) {
	target, _ := sgp22.NewICCID("8986000000000000001")
	for _, tc := range []struct {
		name     string
		profiles []*sgp22.ProfileInfo
		want     DeleteProfileErrorCode
	}{
		{"enabled", []*sgp22.ProfileInfo{{ICCID: target, ProfileState: sgp22.ProfileEnabled}}, DeleteProfileErrorActive},
		{"absent", nil, DeleteProfileErrorProfileNotFound},
		{"disabled", []*sgp22.ProfileInfo{{ICCID: target, ProfileState: sgp22.ProfileDisabled}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes, reads atomic.Int32
			client := &lpa.Client{APDU: fakeProfileOperationTransmitter{calls: &writes, listCalls: &reads, profiles: tc.profiles}}
			err := validateDeleteTarget(client, target)
			if ClassifyDeleteProfileError(err) != tc.want {
				t.Fatalf("err=%v code=%s", err, ClassifyDeleteProfileError(err))
			}
			if writes.Load() != 0 || reads.Load() != 1 {
				t.Fatalf("writes=%d reads=%d", writes.Load(), reads.Load())
			}
		})
	}
	if err := validateDeleteTarget(&lpa.Client{}, target); err == nil {
		t.Fatal("unknown state accepted")
	}
}

func TestKHiveDownloadServerAndSecretRedaction(t *testing.T) {
	for _, address := range []string{"", "https://user:password@example.com", "https://example.com/path", "example.com?code=secret", "ftp://example.com", "LPA:1$example.com$secret"} {
		if _, err := parseDownloadServer(address); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	for _, address := range []string{"example.com", "https://example.com/", "http://example.com"} {
		u, err := parseDownloadServer(address)
		if err != nil || u.String() != "https://example.com" {
			t.Fatalf("address=%s url=%v err=%v", address, u, err)
		}
	}
	got := redactDownloadSecrets("error secret+code secret%2Bcode confirm", "secret+code", "confirm")
	if strings.Contains(got, "secret") || strings.Contains(got, "confirm") {
		t.Fatal(got)
	}
}
