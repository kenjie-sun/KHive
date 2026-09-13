package device_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1239t/vohive/internal/config"
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vohive/internal/device"
	"github.com/1239t/vohive/internal/notify"
	"github.com/1239t/vohive/pkg/logger"
)

// Runs the real RP-DATA -> TPDU -> transactional assembly -> notifier -> HTTP
// pipeline. No modem, SIP connection, carrier send function or host network is
// supplied. Channel failure must leave the received message and RP-ACK intact.
func TestKHiveCompleteSMSNotificationPipeline(t *testing.T) {
	for _, tc := range []struct {
		channel string
		status  int
	}{{"webhook", 200}, {"webhook", 503}, {"telegram", 200}, {"telegram", 503}} {
		status := tc.status
		t.Run(tc.channel+"/"+http.StatusText(status), func(t *testing.T) {
			dir := t.TempDir()
			if err := db.Init(filepath.Join(dir, "sms.db")); err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(dir, "notify.log")})
			logs := logger.GlobalBroadcaster.Subscribe()
			defer logger.GlobalBroadcaster.Unsubscribe(logs)
			var hits atomic.Int32
			bodies := make(chan string, 4)
			pollStop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.channel == "telegram" {
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "/getMe") {
						_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"fixture","username":"fixture_bot"}}`)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/getUpdates") {
						select {
						case <-pollStop:
						case <-r.Context().Done():
						}
						_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
						return
					}
					_ = r.ParseForm()
					hits.Add(1)
					bodies <- r.Form.Get("text")
					w.WriteHeader(status)
					if status == 200 {
						_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":123,"type":"private"}}}`)
					} else {
						_, _ = io.WriteString(w, `{"ok":false,"error_code":503,"description":"simulated channel unavailable"}`)
					}
					return
				}
				b, _ := io.ReadAll(r.Body)
				hits.Add(1)
				bodies <- string(b)
				w.WriteHeader(status)
			}))
			defer server.Close()
			pool := device.NewPool(nil)
			cfg := &config.Config{Webhook: config.WebhookConfig{Enabled: true, URLs: []string{server.URL}, TimeoutMs: 1000, RetryMax: 1}}
			if tc.channel == "telegram" {
				cfg.Webhook.Enabled = false
				cfg.Telegram = config.TelegramConfig{Enabled: true, BotToken: "test-only", ChatID: 123, BaseURL: server.URL}
			}
			manager, err := notify.NewManager(cfg, pool)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { manager.Close(); close(pollStop) }()
			pool.SetNotifier(manager)
			store := device.KHiveIncomingStoreForTest(pool)
			// Two UCS2 SMS-DELIVER fragments, synthetic sender +1234, text 中文短信.
			receive := func(seq byte, content string, mr byte) {
				t.Helper()
				ud, err := hex.DecodeString(content)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := hex.DecodeString("4004912143000862902121436500")
				raw = append(raw, byte(6+len(ud)), 5, 0, 3, 42, 2, seq)
				raw = append(raw, ud...)
				rp := append([]byte{1, mr, 0, 0, byte(len(raw))}, raw...)
				ack, err := store.ReceiveSMS(context.Background(), "notify-fixture", "SIM-test", rp)
				if err != nil || len(ack) < 2 || ack[0] != 2 || ack[1] != mr {
					t.Fatalf("RP-ACK missing: %x %v", ack, err)
				}
			}
			receive(2, "77ED4FE1", 1)
			receive(2, "77ED4FE1", 2)
			if hits.Load() != 0 {
				t.Fatal("partial SMS notified")
			}
			receive(1, "4E2D6587", 3)
			// Duplicate fragments after completion must be acknowledged without another
			// broadcast, including when the first notification is failing/retrying.
			receive(1, "4E2D6587", 4)
			receive(2, "77ED4FE1", 5)
			terminal := "通知渠道发送完成"
			wantHits := int32(1)
			if status == 503 {
				terminal = "通知渠道发送失败"
				if tc.channel == "webhook" {
					wantHits = 2
				}
			}
			startedID := ""
			finishedID := ""
			deadline := time.After(4 * time.Second)
			for finishedID == "" {
				select {
				case entry := <-logs:
					var fields map[string]any
					_ = json.Unmarshal([]byte(entry.Fields), &fields)
					if entry.Message == "开始发送短信通知" {
						startedID, _ = fields["notification_id"].(string)
					}
					if entry.Message == terminal {
						finishedID, _ = fields["notification_id"].(string)
					}
				case <-deadline:
					t.Fatal("notification terminal trace missing")
				}
			}
			if startedID == "" || startedID != finishedID {
				t.Fatal("notification not correlated")
			}
			if hits.Load() != wantHits {
				t.Fatalf("HTTP attempts=%d want %d", hits.Load(), wantHits)
			}
			for i := int32(0); i < wantHits; i++ {
				if body := <-bodies; !strings.Contains(body, "中文短信") {
					t.Fatal("notified incomplete or corrupt Chinese SMS")
				}
			}
			var messages []db.SMS
			if err := db.DB.Find(&messages).Error; err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Type != 1 || messages[0].Content != "中文短信" {
				t.Fatalf("received message lost or outgoing SMS created: %+v", messages)
			}
			if !strings.HasPrefix(finishedID, "sms-") {
				t.Fatal("trace identifier absent")
			}
		})
	}
}
