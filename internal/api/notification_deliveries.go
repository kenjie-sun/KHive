package api

import (
	"github.com/1239t/vohive/internal/db"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) handleNotificationDeliveries(c *gin.Context) {
	state := c.Query("state")
	switch state {
	case "", "pending", "sending", "retry", "failed", "sent":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"message": "无效的投递状态"})
		return
	}
	rows, err := (db.SMSNotificationStore{DB: db.DB}).List(c.Request.Context(), state, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "读取通知投递记录失败"})
		return
	}
	enabled := []string{}
	if s.notifyMgr != nil {
		enabled = s.notifyMgr.GetChannelNames()
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "enabled_channels": enabled})
}

func (s *Server) handleRetryNotificationDelivery(c *gin.Context) {
	if err := (db.SMSNotificationStore{DB: db.DB}).Retry(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "通知已重新排队；渠道启用后继续投递"})
}
