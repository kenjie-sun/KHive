package modem

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/1239t/vohive/pkg/logger"
)

type smsReportMode int

const (
	smsReportsDisabled smsReportMode = iota
	smsReportsDirect
	smsReportsStored
)

// ConfigureSMSReports restores incoming SMS and terminal-report indications.
// Capability negotiation is modem-wide, independent of the SIM's carrier.
func (m *Manager) ConfigureSMSReports() {
	if _, err := configureSMSReports(m.ExecuteATSilent); err != nil {
		logger.Warn("短信上报配置恢复失败", "device", m.DeviceID(), "err", err)
	}
}

func configureSMSReports(execute func(string, time.Duration) (string, error)) (smsReportMode, error) {
	var lastErr error
	for _, mode := range []smsReportMode{smsReportsDirect, smsReportsStored, smsReportsDisabled} {
		_, err := execute(fmt.Sprintf("AT+CNMI=2,1,0,%d,0", mode), 2*time.Second)
		if err == nil {
			return mode, nil
		}
		lastErr = err
	}
	return smsReportsDisabled, fmt.Errorf("SMS indication setup and fallback failed: %w", lastErr)
}

// SetSMSReportCallbackFactory binds report processing to the SIM identity at
// reception, before asynchronous persistence can overlap a profile switch.
// The factory must only snapshot local state, never issue AT commands.
func (m *Manager) SetSMSReportCallbackFactory(factory func() func(string) error) {
	m.infoMu.Lock()
	m.smsReportCallbackFactory = factory
	m.infoMu.Unlock()
}

type directSMSReportFrame struct {
	length int
	since  time.Time
}

// Called only by the serial command loop, for both idle and solicited input.
// A +CDS header and its PDU may be separated by other AT response/URC lines.
func (m *Manager) consumeDirectSMSReportLine(line string) bool {
	line = strings.TrimSpace(line)
	if m.directReportFrame.length != 0 && time.Since(m.directReportFrame.since) > 5*time.Second {
		m.directReportFrame = directSMSReportFrame{}
		logger.Warn("短信状态报告等待 PDU 超时", "device", m.DeviceID())
	}
	if strings.HasPrefix(line, "+CDS:") {
		m.directReportFrame = directSMSReportFrame{}
		length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "+CDS:")))
		if err != nil || length < 1 || length > 255 {
			logger.Warn("短信状态报告头格式无效", "device", m.DeviceID())
			return true
		}
		m.directReportFrame = directSMSReportFrame{length: length, since: time.Now()}
		return true
	}
	if line == "RDY" {
		m.directReportFrame = directSMSReportFrame{}
	}
	if m.directReportFrame.length == 0 || line == "" {
		return false
	}
	raw, err := hex.DecodeString(line)
	if err != nil {
		return false // Preserve unrelated command responses and indications.
	}
	length := m.directReportFrame.length
	if len(raw) < 3 || 1+int(raw[0]) >= len(raw) {
		return false
	}
	offset := 1 + int(raw[0])
	if len(raw)-offset != length || length < 2 || raw[offset]&3 != 2 {
		return false
	}
	m.directReportFrame = directSMSReportFrame{}
	m.infoMu.RLock()
	factory, callback := m.smsReportCallbackFactory, m.pduCallback
	m.infoMu.RUnlock()
	if factory != nil {
		callback = factory()
	}
	if callback == nil {
		logger.Warn("短信状态报告接收器尚未就绪", "device", m.DeviceID())
		return true
	}
	// Never wait for another AT command inside the serial command loop: the
	// report can arrive before the CMGS transaction has completed.
	go func() {
		if err := persistAndAcknowledgeSMSReport(line, callback, m.ExecuteATHigh); err != nil {
			logger.Warn("短信状态报告处理失败", "device", m.DeviceID(), "err", err)
			return
		}
		logger.Info("直接短信状态报告已处理", "device", m.DeviceID(), "tp_mr", int(raw[offset+1]))
	}()
	return true
}

func persistAndAcknowledgeSMSReport(raw string, persist func(string) error, execute func(string, time.Duration) (string, error)) error {
	if err := persist(raw); err != nil {
		return fmt.Errorf("persist report: %w", err)
	}
	response, err := execute("AT+CSMS?", 2*time.Second)
	if err != nil {
		return fmt.Errorf("report saved but acknowledgement mode unknown: %w", err)
	}
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+CSMS:") {
			continue
		}
		service := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "+CSMS:"), ",", 2)[0])
		switch service {
		case "0": // The modem handles network acknowledgement.
			return nil
		case "1":
			if _, err := execute("AT+CNMA=1", 2*time.Second); err != nil {
				return fmt.Errorf("report saved but CNMA failed: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("report saved but CSMS response invalid")
}
