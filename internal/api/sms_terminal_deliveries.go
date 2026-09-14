package api

import (
	"github.com/1239t/vohive/internal/db"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

func (s *Server) handleSMSTerminalDeliveries(c *gin.Context) {
	imsi, peer := strings.TrimSpace(c.Query("imsi")), strings.TrimSpace(c.Query("peer"))
	if imsi == "" || peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "imsi 和 peer 不能为空"})
		return
	}
	if db.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "数据库不可用"})
		return
	}
	var messages []db.SMSDelivery
	if err := db.DB.WithContext(c.Request.Context()).Where("imsi = ? AND peer = ?", imsi, peer).Order("created_at desc").Limit(20).Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "读取发送状态失败"})
		return
	}
	items := make([]*db.SMSDeliveryStatus, 0, len(messages))
	for _, msg := range messages {
		status, err := db.GetSMSDeliveryStatus(msg.MessageID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "读取发送状态失败"})
			return
		}
		items = append(items, status)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
