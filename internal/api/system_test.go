package api

import (
	"github.com/1239t/vohive/internal/config"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKHiveHasNoUninstallRoute(t *testing.T) {
	cfg := &config.Config{}
	s := New(cfg, nil, nil, nil, nil, nil, "test-config.yaml")
	for _, route := range s.newRouter().Routes() {
		if route.Path == "/api/system/uninstall" {
			t.Fatal("KHive must not expose a destructive uninstall route")
		}
	}
}

func TestKHiveApplyUpdateReportsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	(&Server{}).handleApplyUpdate(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured update returned %d instead of 503", w.Code)
	}
}
