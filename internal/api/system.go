package api

import (
	"github.com/1239t/vohive/internal/updater"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) handleCheckUpdate(c *gin.Context) {
	info, err := updater.CheckUpdate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

func (s *Server) handleApplyUpdate(c *gin.Context) {
	// No background success response for an update that cannot be applied.
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": updater.ErrUpdatesDisabled.Error()})
}
