package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/1239t/vohive/internal/config"
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vohive/pkg/logger"
)

func ConfigureSMSOutbox(cfg *config.Config) {
	names := []string{}
	for _, c := range []struct {
		name    string
		enabled bool
	}{
		{"telegram", cfg.Telegram.Enabled}, {"feishu", cfg.Feishu.Enabled}, {"qq", cfg.QQ.Enabled},
		{"webhook", cfg.Webhook.Enabled}, {"bark", cfg.Bark.Enabled}, {"email", cfg.Email.Enabled}, {"pushplus", cfg.Pushplus.Enabled},
	} {
		if c.enabled {
			names = append(names, c.name)
		}
	}
	db.SetSMSNotificationChannels(names)
}

func (m *Manager) startOutbox() {
	if db.DB == nil {
		return
	}
	m.durable.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	m.outboxCancel = cancel
	database := db.SMSNotificationStore{DB: db.DB}
	for _, channel := range m.channelSnapshot() {
		channel := channel
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				row, err := database.Claim(ctx, channel.Name(), time.Now())
				if err == nil && row != nil {
					sendNotificationDelivery(database, channel, *row, m.resolveDeviceName(row.DeviceID))
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
}

func sendNotificationDelivery(store db.SMSNotificationStore, channel Channel, row db.SMSNotification, deviceName string) {
	// Renew while a provider is still executing. A slow send must not become
	// eligible for a second worker merely because its original lease expired.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = store.DB.WithContext(ctx).Model(&db.SMSNotification{}).Where("id = ? AND state = 'sending' AND lease_token = ?", row.ID, row.LeaseToken).Update("lease_until", time.Now().Add(5*time.Minute)).Error
				cancel()
			}
		}
	}()
	logger.Info("开始发送短信通知", "notification_id", row.ID, "channel", row.Channel, "event", "sms_received", "sms_device", row.DeviceID, "attempt", row.Attempts)
	text := fmt.Sprintf("收到新短信 / %s\n设备  %s\n号码  %s\n时间  %s\n内容  %s", row.Source, row.DeviceID, row.Sender, row.Timestamp.Format("2006-01-02 15:04:05"), row.Content)
	event := NotificationContext{NotificationID: row.ID, Event: "sms_received", Text: text, DeviceID: row.DeviceID, DeviceName: deviceName, Timestamp: row.Timestamp}
	var err error
	if ch, ok := channel.(contextualChannel); ok {
		err = ch.SendWithContext(event)
	} else {
		err = channel.Send(text)
	}
	failure := notificationFailureReason(err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if finishErr := store.Finish(ctx, row, failure, time.Now()); finishErr != nil {
		logger.Warn("通知投递结果未能持久化，将按相同通知 ID 重试", "notification_id", row.ID, "channel", row.Channel)
		return
	}
	if err != nil {
		logger.Warn("通知渠道发送失败", "notification_id", row.ID, "channel", row.Channel, "event", "sms_received", "reason", failure)
		return
	}
	logger.Info("通知渠道发送完成", "notification_id", row.ID, "channel", row.Channel, "event", "sms_received")
}

// Provider errors can contain URLs with credentials or message text. Persist
// only classified reasons; never put the original error into the delivery API.
func notificationFailureReason(err error) string {
	if err == nil {
		return ""
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "渠道连接或应答超时"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "429") {
		return "渠道限流，等待退避重试"
	}
	if strings.Contains(message, "401") || strings.Contains(message, "403") {
		return "渠道拒绝认证或访问，请检查配置"
	}
	if strings.Contains(message, "500") || strings.Contains(message, "502") || strings.Contains(message, "503") || strings.Contains(message, "504") {
		return "渠道服务暂时不可用"
	}
	return "渠道投递失败，请检查连接和渠道配置"
}
