package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestKHiveSMSFragmentsSurviveRestartAndIsolateSIM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receive.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	fragment := IncomingSMSFragment{IMSI: "SIM-A", Sender: "+100", Content: "world", TPDU: []byte{1, 2}, Timestamp: time.Now(), Ref: 7, RefBits: 8, Total: 2, Seq: 2}
	result, err := StoreIncomingSMS(context.Background(), fragment)
	if err != nil || !result.Pending {
		t.Fatalf("first: %+v %v", result, err)
	}
	sqlDB, _ := DB.DB()
	_ = sqlDB.Close()
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	other := fragment
	other.IMSI = "SIM-B"
	other.Seq = 1
	other.Content = "wrong"
	other.TPDU = []byte{1, 3}
	if _, err := StoreIncomingSMS(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	fragment.Seq = 1
	fragment.Content = "hello "
	fragment.TPDU = []byte{1, 4}
	result, err = StoreIncomingSMS(context.Background(), fragment)
	if err != nil || !result.Stored || result.SMS.Content != "hello world" || result.SMS.IMSI != "SIM-A" {
		t.Fatalf("complete: %+v %v", result, err)
	}
	dup, err := StoreIncomingSMS(context.Background(), fragment)
	if err != nil || !dup.Duplicate {
		t.Fatalf("duplicate: %+v %v", dup, err)
	}
	var count int64
	DB.Model(&SMS{}).Count(&count)
	if count != 1 {
		t.Fatalf("history rows %d", count)
	}
	var contact SMSContact
	if err := DB.First(&contact).Error; err != nil || contact.UnreadCount != 1 {
		t.Fatalf("contact %+v %v", contact, err)
	}
}
func TestKHiveSMSReceiveTransactionRollsBack(t *testing.T) {
	openTestDB(t)
	if err := DB.Exec("CREATE TRIGGER khive_fail_history BEFORE INSERT ON sms BEGIN SELECT RAISE(ABORT,'test storage failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	in := IncomingSMSFragment{IMSI: "SIM-C", Sender: "+100", Content: "test", TPDU: []byte{1, 2, 3}, Timestamp: time.Now()}
	if _, err := StoreIncomingSMS(context.Background(), in); err == nil {
		t.Fatal("acknowledged failed history insert")
	}
	var count int64
	DB.Model(&SMSIncomingPart{}).Count(&count)
	if count != 0 {
		t.Fatalf("failed transaction retained %d fragments", count)
	}
	if err := DB.Exec("DROP TRIGGER khive_fail_history").Error; err != nil {
		t.Fatal(err)
	}
	result, err := StoreIncomingSMS(context.Background(), in)
	if err != nil || !result.Stored {
		t.Fatalf("retry: %+v %v", result, err)
	}
}
func TestKHiveSMSReferenceWidthAndValidation(t *testing.T) {
	openTestDB(t)
	in := IncomingSMSFragment{IMSI: "SIM-D", Sender: "+100", Content: "eight", TPDU: []byte{1}, Timestamp: time.Now(), Ref: 1, RefBits: 8, Total: 2, Seq: 1}
	if _, err := StoreIncomingSMS(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.RefBits = 16
	in.Seq = 2
	in.TPDU = []byte{2}
	result, err := StoreIncomingSMS(context.Background(), in)
	if err != nil || !result.Pending {
		t.Fatalf("mixed reference widths: %+v %v", result, err)
	}
	in.Seq = 3
	if _, err := StoreIncomingSMS(context.Background(), in); err == nil {
		t.Fatal("invalid sequence accepted")
	}
}
