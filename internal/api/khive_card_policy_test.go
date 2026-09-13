package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1239t/vohive/internal/db"
	"github.com/gin-gonic/gin"
)

func TestKHiveCardPolicyValidationAndClearing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	openTestDB(t)
	s := &Server{}
	r := gin.New()
	r.PUT("/api/cards/:iccid/policy", s.handlePutCardPolicy)
	r.GET("/api/cards/:iccid/policy", s.handleGetCardPolicy)
	put := func(body string, code int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/cards/8986000000000000001/policy", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != code {
			t.Fatalf("code=%d want=%d body=%s", w.Code, code, w.Body.String())
		}
	}
	put(`{"network_enabled":false,"vowifi_enabled":false,"airplane_enabled":true,"apn":"ims","ip_version":"v4v6"}`, 200)
	put(`{"vowifi_enabled":true,"apn":""}`, 200)
	got, err := db.GetCardPolicy("8986000000000000001")
	if err != nil || !got.AirplaneEnabled || !got.VoWiFiEnabled || got.APN != "" || got.IPVersion != "v4v6" {
		t.Fatalf("policy=%+v error=%v", got, err)
	}
	put(`{"network_enabled":true}`, 400)
	put(`{"ip_version":"garbage"}`, 400)
	after, _ := db.GetCardPolicy(got.ICCID)
	if after.NetworkEnabled || after.IPVersion != "v4v6" {
		t.Fatal("invalid request changed policy")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/cards/8986000000000000002/policy", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err := db.GetCardPolicy("8986000000000000002"); err != db.ErrCardPolicyNotFound {
		t.Fatalf("GET wrote a row: %v", err)
	}
	sqlDB, _ := db.DB.DB()
	_ = sqlDB.Close()
	put(`{"network_enabled":false}`, 500)
}
