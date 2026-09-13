package updater

import (
	"errors"
	"github.com/1239t/vohive/internal/global"
	"os"
)

var ErrUpdatesDisabled = errors.New("KHive 尚未配置自己的发布源，请使用经过验证的 KHive 构建包手动更新")

type UpdateInfo struct {
	HasUpdate      bool   `json:"has_update"`
	CurrentVer     string `json:"current_version"`
	LatestVer      string `json:"latest_version"`
	ReleaseNote    string `json:"release_note"`
	IsDocker       bool   `json:"is_docker"`
	Enabled        bool   `json:"enabled"`
	DisabledReason string `json:"disabled_reason"`
}

// KHive must never replace itself with an upstream VoHive release.
// Until a KHive release channel is configured, checking is read-only and local.
func CheckUpdate() (*UpdateInfo, error) {
	_, err := os.Stat("/.dockerenv")
	return &UpdateInfo{
		CurrentVer:     global.Version,
		LatestVer:      global.Version,
		IsDocker:       err == nil,
		DisabledReason: ErrUpdatesDisabled.Error(),
	}, nil
}

func ApplyUpdate() error { return ErrUpdatesDisabled }
