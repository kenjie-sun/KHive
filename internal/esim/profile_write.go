package esim

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
)

// Reserve the same lifecycle gate as switching, without publishing switch progress.
// Reject queued writes so an old confirmation cannot run against a later card state.
func (m *Manager) tryProfileWrite() (func(), error) {
	m.initSwitchLifecycle()
	m.switchProgressMu.Lock()
	defer m.switchProgressMu.Unlock()
	if m.switchContext.Err() != nil {
		return nil, fmt.Errorf("设备连接已失效，请等待重新上线")
	}
	if m.profileWriteInProgress || m.switchInProgress.Load() || !m.opMu.TryLock() {
		return nil, ErrOperationInProgress
	}
	m.profileWriteInProgress = true
	return func() {
		m.switchProgressMu.Lock()
		m.profileWriteInProgress = false
		m.opMu.Unlock()
		m.switchProgressMu.Unlock()
		m.notifyWriteDone()
	}, nil
}

func validateDeleteTarget(client *lpa.Client, target sgp22.ICCID) error {
	profiles, err := listBasicProfiles(client)
	if err != nil {
		return fmt.Errorf("无法确认待删除 Profile 的当前状态，已停止删除: %w", err)
	}
	for _, profile := range profiles {
		if profile == nil || !bytes.Equal(profile.ICCID, target) {
			continue
		}
		if profile.ProfileState != sgp22.ProfileDisabled {
			return NewDeleteProfileError(DeleteProfileErrorActive, "该 Profile 未处于停用状态，请先切换到其他卡后再删除", nil)
		}
		return nil
	}
	return NewDeleteProfileError(DeleteProfileErrorProfileNotFound, "目标 eUICC 中未找到待删除 Profile，已停止删除", nil)
}

func parseDownloadServer(address string) (*url.URL, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("无效的 SM-DP+ 地址，请填写服务器域名，激活码和确认码使用独立输入框")
	}
	return &url.URL{Scheme: "https", Host: u.Host}, nil
}

func redactDownloadSecrets(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
			message = strings.ReplaceAll(message, url.QueryEscape(secret), "[redacted]")
		}
	}
	return message
}
