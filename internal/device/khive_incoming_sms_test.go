package device

import (
	"context"
	"encoding/hex"
	"github.com/1239t/vohive/internal/db"
	"testing"
)

func TestKHiveIncomingSMSDecodePersistAcknowledge(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	// Synthetic SMS-DELIVER from +1234, UCS2 payload "Hi".
	raw, err := hex.DecodeString("00049121430008629021214365000400480069")
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{1, 42, 0, 0, byte(len(raw))}, raw...)
	store := vowifiDeliveryStore{}
	reply, err := store.ReceiveSMS(context.Background(), "test-device", "SIM-A", body)
	if err != nil || len(reply) < 2 || reply[0] != 2 || reply[1] != 42 {
		t.Fatalf("reply=%x err=%v", reply, err)
	}
	var sms db.SMS
	if err := db.DB.First(&sms).Error; err != nil || sms.Content != "Hi" || sms.IMSI != "SIM-A" {
		t.Fatalf("sms=%+v err=%v", sms, err)
	}
	body[1] = 43
	reply, err = store.ReceiveSMS(context.Background(), "test-device", "SIM-A", body)
	if err != nil || reply[1] != 43 {
		t.Fatalf("retransmit reply=%x err=%v", reply, err)
	}
	var count int64
	db.DB.Model(&db.SMS{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate history=%d", count)
	}
}
