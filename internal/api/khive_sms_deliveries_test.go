package api

import (
	"encoding/json"
	"github.com/1239t/vohive/internal/db"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKHiveSMSDeliveryHistoryWithoutActiveIMS(t *testing.T) {
	openTestDB(t)
	now := time.Now()
	if err := db.CreateSMSDelivery("stored", "SIM-A", "device", "+1234", "fixture", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSMSDeliveryPart("stored", 1, "call", 3, "acked", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReserveSMSTerminalReference("stored", 1); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	r := gin.New()
	r.GET("/sms/delivery/:message_id", s.handleSMSDelivery)
	r.GET("/sms/deliveries", s.handleSMSTerminalDeliveries)
	for _, url := range []string{"/sms/delivery/stored", "/sms/deliveries?imsi=SIM-A&peer=%2B1234"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", url, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sms/deliveries?imsi=SIM-B&peer=%2B1234", nil))
	var data struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || len(data.Items) != 0 {
		t.Fatal("history crossed SIM")
	}
}
func TestKHiveNotificationDeliveryMetadataAndRetry(t *testing.T) {
	openTestDB(t)
	row := db.SMSNotification{ID: "sms-api", SMSID: 1, Channel: "webhook", Content: "private-message-body", State: "failed", Attempts: 10}
	if err := db.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	r := gin.New()
	r.GET("/notifications/deliveries", s.handleNotificationDeliveries)
	r.POST("/notifications/deliveries/:id/retry", s.handleRetryNotificationDelivery)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/notifications/deliveries", nil))
	var data struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || len(data.Items) != 1 {
		t.Fatalf("list %s", w.Body.String())
	}
	if _, ok := data.Items[0]["Content"]; ok {
		t.Fatal("content exposed")
	}
	if _, ok := data.Items[0]["content"]; ok {
		t.Fatal("content exposed")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/notifications/deliveries/sms-api/retry", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	db.DB.First(&row, "id = ?", "sms-api")
	if row.State != "pending" || row.Attempts != 0 {
		t.Fatalf("retry %+v", row)
	}
	db.DB.Model(&row).Update("state", "sent")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/notifications/deliveries/sms-api/retry", nil))
	if w.Code != 409 {
		t.Fatal("sent notification requeued")
	}
}
