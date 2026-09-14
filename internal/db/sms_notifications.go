package db

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SMSNotification is a per-channel durable delivery, committed with the SMS.
// External delivery is at-least-once: a crash after the provider accepts it but
// before Finish commits can repeat the same ID. Webhooks receive this stable ID.
type SMSNotification struct {
	ID            string    `gorm:"primaryKey" json:"id"`
	SMSID         uint      `gorm:"uniqueIndex:idx_sms_notification_target,priority:1" json:"sms_id"`
	Channel       string    `gorm:"uniqueIndex:idx_sms_notification_target,priority:2;index:idx_notification_due,priority:1" json:"channel"`
	DeviceID      string    `json:"device_id"`
	Source        string    `json:"source"`
	Sender        string    `json:"sender"`
	Content       string    `json:"-"`
	Timestamp     time.Time `json:"timestamp"`
	State         string    `gorm:"index:idx_notification_due,priority:2" json:"state"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `gorm:"index:idx_notification_due,priority:3" json:"next_attempt_at"`
	LeaseUntil    time.Time `json:"-"`
	LeaseToken    string    `json:"-"`
	LastError     string    `json:"last_error"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

var smsNotificationChannels struct {
	sync.RWMutex
	names []string
}

// Configure before starting device workers. Pending deliveries for disabled
// channels stay in the database; enabling a new channel does not replay history.
func SetSMSNotificationChannels(names []string) {
	smsNotificationChannels.Lock()
	defer smsNotificationChannels.Unlock()
	smsNotificationChannels.names = nil
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			smsNotificationChannels.names = append(smsNotificationChannels.names, name)
			seen[name] = true
		}
	}
}

func enqueueSMSNotifications(tx *gorm.DB, sms SMS, deviceID, source string) error {
	if sms.Type != 1 {
		return nil
	}
	smsNotificationChannels.RLock()
	names := append([]string(nil), smsNotificationChannels.names...)
	smsNotificationChannels.RUnlock()
	if strings.TrimSpace(source) == "" {
		source = "蜂窝"
	}
	for _, channel := range names {
		row := SMSNotification{ID: "sms-" + uuid.NewString(), SMSID: sms.ID, Channel: channel, DeviceID: deviceID, Source: source, Sender: sms.Sender, Content: sms.Content, Timestamp: sms.Timestamp, State: "pending", NextAttemptAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

type SMSNotificationStore struct{ DB *gorm.DB }

func (s SMSNotificationStore) Claim(ctx context.Context, channel string, now time.Time) (*SMSNotification, error) {
	if s.DB == nil {
		return nil, nil
	}
	due := "channel = ? AND ((state IN ? AND next_attempt_at <= ?) OR (state = 'sending' AND lease_until <= ?))"
	args := []any{channel, []string{"pending", "retry"}, now, now}
	var row SMSNotification
	if err := s.DB.WithContext(ctx).Where(due, args...).Order("next_attempt_at ASC").First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	token := uuid.NewString()
	until := now.Add(5 * time.Minute)
	result := s.DB.WithContext(ctx).Model(&SMSNotification{}).Where("id = ?", row.ID).Where(due, args...).Updates(map[string]any{"state": "sending", "lease_token": token, "lease_until": until, "attempts": gorm.Expr("attempts + 1"), "updated_at": now})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	row.LeaseToken, row.LeaseUntil, row.State = token, until, "sending"
	row.Attempts++
	return &row, nil
}

func (s SMSNotificationStore) Finish(ctx context.Context, row SMSNotification, failure string, now time.Time) error {
	state := "sent"
	if failure != "" {
		state = "retry"
		if row.Attempts >= 10 {
			state = "failed"
		}
	}
	shift := row.Attempts - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 9 {
		shift = 9
	}
	delay := 5 * time.Second * time.Duration(1<<uint(shift))
	return s.DB.WithContext(ctx).Model(&SMSNotification{}).Where("id = ? AND state = 'sending' AND lease_token = ?", row.ID, row.LeaseToken).Updates(map[string]any{"state": state, "last_error": failure, "next_attempt_at": now.Add(delay), "lease_token": "", "lease_until": time.Time{}, "updated_at": now}).Error
}

func (s SMSNotificationStore) List(ctx context.Context, state string, limit int) ([]SMSNotification, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	if s.DB == nil {
		return nil, fmt.Errorf("SMS database unavailable")
	}
	q := s.DB.WithContext(ctx).Order("created_at DESC").Limit(limit)
	if state != "" {
		q = q.Where("state = ?", state)
	}
	var rows []SMSNotification
	err := q.Find(&rows).Error
	return rows, err
}

func (s SMSNotificationStore) Retry(ctx context.Context, id string) error {
	if s.DB == nil {
		return fmt.Errorf("SMS database unavailable")
	}
	result := s.DB.WithContext(ctx).Model(&SMSNotification{}).Where("id = ? AND state IN ?", id, []string{"failed", "retry"}).Updates(map[string]any{"state": "pending", "attempts": 0, "next_attempt_at": time.Now(), "last_error": "", "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("仅失败或等待重试的通知可重新排队")
	}
	return nil
}
