package esim

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
)

// Keep delivery and card cleanup separate: a cleanup failure must not resend
// a notification that was already delivered in this operation.
type notificationCleanupError struct{ err error }

func (e *notificationCleanupError) Error() string {
	return "通知已发送，但卡内记录清理失败: " + e.err.Error()
}
func (e *notificationCleanupError) Unwrap() error { return e.err }

func sendAndRemoveNotification(client *lpa.Client, seq sgp22.SequenceNumber, pending []*sgp22.PendingNotification, delay time.Duration) error {
	if len(pending) == 0 {
		return fmt.Errorf("卡片未返回待发送通知，保留卡内记录")
	}
	// Validate the entire retrieval before sending or deleting anything.
	for _, n := range pending {
		if n == nil || n.Notification == nil || n.PendingNotification == nil || n.Notification.SequenceNumber != seq || n.Notification.Address == "" {
			return fmt.Errorf("待发送通知不完整或序号不匹配，保留卡内记录")
		}
	}
	for _, n := range pending {
		if err := retryWithBackoff(3, delay, nil, func() error { return client.HandleNotification(n) }); err != nil {
			return err
		}
	}
	if err := retryWithBackoff(3, delay, nil, func() error {
		err := client.RemoveNotificationFromList(seq)
		if errors.Is(err, sgp22.ErrNothingToDelete) {
			return nil
		}
		return err
	}); err != nil {
		return &notificationCleanupError{err: err}
	}
	return nil
}

// A transport error is not proof of deletion failure. Recovery requires both
// absence from a fresh profile list and a new, matching deletion notification.
func verifyDeletedProfile(client *lpa.Client, target sgp22.ICCID, baseline sgp22.SequenceNumber) error {
	profiles, err := listBasicProfiles(client)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if p != nil && bytes.Equal(p.ICCID, target) {
			return fmt.Errorf("目标 Profile 仍在卡内")
		}
	}
	notifications, err := safeListNotification(client, sgp22.NotificationEventDelete)
	if err != nil {
		return err
	}
	for _, n := range notifications {
		if n != nil && n.ProfileManagementOperation == sgp22.NotificationEventDelete && n.SequenceNumber > baseline && bytes.Equal(n.ICCID, target) {
			return nil
		}
	}
	return fmt.Errorf("未观察到目标 Profile 的新增删除通知")
}

func (m *Manager) recoverDeleteProfileClient(aid []byte, target sgp22.ICCID, baseline sgp22.SequenceNumber) (*lpa.Client, error) {
	if err := m.SwitchContext().Err(); err != nil {
		return nil, err
	}
	client, err := m.createLPAWithAID(aid)
	if err != nil {
		return nil, err
	}
	if err := verifyDeletedProfile(client, target, baseline); err != nil {
		_ = m.closeLPAClientForOperation("delete_profile_recovery", client)
		return nil, err
	}
	return client, nil
}
