package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationOutboxAtomicAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	SetSMSNotificationChannels([]string{"telegram", "webhook"})
	defer SetSMSNotificationChannels(nil)
	ctx := context.Background()
	in := IncomingSMSFragment{IMSI: "fixture", DeviceID: "device", Sender: "+100", Content: "hello", TPDU: []byte{1, 2, 3}, Timestamp: time.Now()}
	if err := DB.Exec("CREATE TRIGGER fail_outbox BEFORE INSERT ON sms_notifications BEGIN SELECT RAISE(ABORT,'fixture'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := StoreIncomingSMS(ctx, in); err == nil {
		t.Fatal("outbox failure committed SMS")
	}
	var count int64
	DB.Model(&SMS{}).Count(&count)
	if count != 0 {
		t.Fatal("SMS committed without notification")
	}
	DB.Model(&SMSIncomingPart{}).Count(&count)
	if count != 0 {
		t.Fatal("fragment committed without notification")
	}
	if err := DB.Exec("DROP TRIGGER fail_outbox").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := StoreIncomingSMS(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreIncomingSMS(ctx, in); err != nil {
		t.Fatal(err)
	}
	store := SMSNotificationStore{DB: DB}
	rows, err := store.List(ctx, "", 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	now := time.Now().Add(time.Second)
	first, err := store.Claim(ctx, "telegram", now)
	if err != nil || first == nil {
		t.Fatalf("claim: %v %v", first, err)
	}
	if next, err := store.Claim(ctx, "telegram", now); err != nil || next != nil {
		t.Fatal("live lease claimed twice")
	}
	sqlDB, _ := DB.DB()
	_ = sqlDB.Close()
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	defer func() { sqlDB, _ := DB.DB(); _ = sqlDB.Close() }()
	store = SMSNotificationStore{DB: DB}
	recovered, err := store.Claim(ctx, "telegram", now.Add(6*time.Minute))
	if err != nil || recovered == nil || recovered.ID != first.ID || recovered.LeaseToken == first.LeaseToken {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	if err := store.Finish(ctx, *first, "", now); err != nil {
		t.Fatal(err)
	}
	var stored SMSNotification
	DB.First(&stored, "id = ?", recovered.ID)
	if stored.State != "sending" {
		t.Fatal("stale worker overwrote current lease")
	}
	if err := store.Finish(ctx, *recovered, "503", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(ctx, recovered.ID); err != nil {
		t.Fatal(err)
	}
	resent, err := store.Claim(ctx, "telegram", time.Now().Add(time.Second))
	if err != nil || resent == nil || resent.ID != first.ID {
		t.Fatal("manual retry lost stable ID")
	}
	if err := store.Finish(ctx, *resent, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(ctx, resent.ID); err == nil {
		t.Fatal("sent notification was requeued")
	}
	// A stuck/failing channel never claims another channel's work.
	other, err := store.Claim(ctx, "webhook", time.Now().Add(time.Second))
	if err != nil || other == nil {
		t.Fatal("independent channel blocked")
	}
}

func TestNotificationOutboxWaitsForAllFragments(t *testing.T) {
	openTestDB(t)
	SetSMSNotificationChannels([]string{"webhook"})
	defer SetSMSNotificationChannels(nil)
	part := IncomingSMSFragment{IMSI: "A", Sender: "+100", Content: "one", TPDU: []byte{8}, Timestamp: time.Now(), Ref: 7, RefBits: 8, Total: 2, Seq: 1}
	if _, err := StoreIncomingSMS(context.Background(), part); err != nil {
		t.Fatal(err)
	}
	var count int64
	DB.Model(&SMSNotification{}).Count(&count)
	if count != 0 {
		t.Fatal("partial message notified")
	}
	part.Seq = 2
	part.Content = "two"
	part.TPDU = []byte{9}
	if _, err := StoreIncomingSMS(context.Background(), part); err != nil {
		t.Fatal(err)
	}
	var rows []SMSNotification
	if err := DB.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].Content != "onetwo" {
		t.Fatalf("%+v %v", rows, err)
	}
}
