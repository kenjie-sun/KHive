package device

import (
	"strings"

	"github.com/1239t/vohive/internal/backend"
	"github.com/1239t/vohive/internal/ecm"
	mbimcore "github.com/1239t/vohive/internal/mbim"
	qmicore "github.com/1239t/vohive/internal/qmi"
)

type NetworkController interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	RotateIP() error
	GetPrivateIP() string
	GetPrivateIPv6() string
	GetPublicIPv4AndV6NoCache() (publicV4 string, publicV6 string)
}

var (
	_ NetworkController = (*qmicore.Manager)(nil)
	_ NetworkController = (*mbimcore.Manager)(nil)
	_ NetworkController = (*ecm.Manager)(nil)
)

func (w *Worker) NetworkController() NetworkController {
	if w == nil {
		return nil
	}
	if w.netOverride != nil {
		return w.netOverride
	}
	if w.QMICore != nil {
		return w.QMICore
	}
	if w.MBIMCore != nil {
		return w.MBIMCore
	}
	if w.ECMCore != nil {
		return w.ECMCore
	}
	return nil
}

func (w *Worker) initECMNetwork() {
	if w.Backend != nil && w.Backend.Mode() == backend.BackendAT && w.QMICore == nil && w.MBIMCore == nil && strings.TrimSpace(w.Config.Interface) != "" {
		w.ECMCore = ecm.New(ecm.Config{Interface: w.Config.Interface, APN: w.Config.APN, IPVersion: w.Config.IPVersion}, w.Modem)
	}
}
