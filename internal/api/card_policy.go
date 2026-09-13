package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/1239t/vohive/internal/db"
	"github.com/gin-gonic/gin"
)

// patchCardPolicyForDevice 解析设备当前 ICCID，对 card_policies 行执行原地修改并落库。
// mutate 在 resolve 后的副本上改字段（source 会被强制为 "user"）。
// applied=false 且 err=nil 表示设备当前无 ICCID（离线/未识别），跳过落库。
func (s *Server) patchCardPolicyForDevice(deviceID string, mutate func(*db.CardPolicy)) (iccid string, applied bool, err error) {
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		return "", false, fmt.Errorf("设备未找到")
	}
	iccid = worker.CurrentICCID()
	if iccid == "" {
		return "", false, nil
	}
	p, err := db.ResolveCardPolicy(iccid)
	if err != nil {
		return iccid, false, fmt.Errorf("获取卡策略失败: %w", err)
	}
	mutate(&p)
	p.Source = "user"
	db.NormalizeCardPolicy(&p)
	if err := db.UpsertCardPolicy(p); err != nil {
		return iccid, false, fmt.Errorf("保存卡策略失败: %w", err)
	}
	return iccid, true, nil
}

func (s *Server) handleGetCardPolicy(c *gin.Context) {
	iccid := c.Param("iccid")
	pol, err := db.GetCardPolicy(iccid)
	if errors.Is(err, db.ErrCardPolicyNotFound) {
		// 未建档则返回默认模板（不落库，读端点保持只读语义）
		c.JSON(http.StatusOK, db.DefaultCardPolicy(iccid))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pol)
}

func (s *Server) handleListCardPolicies(c *gin.Context) {
	var out []db.CardPolicy
	if db.DB != nil {
		if err := db.DB.Order("updated_at desc").Find(&out).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"policies": out})
}

func (s *Server) handlePutCardPolicy(c *gin.Context) {
	iccid := c.Param("iccid")
	var req struct {
		NetworkEnabled  *bool   `json:"network_enabled"`
		VoWiFiEnabled   *bool   `json:"vowifi_enabled"`
		AirplaneEnabled *bool   `json:"airplane_enabled"`
		IPVersion       *string `json:"ip_version"`
		APN             *string `json:"apn"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 查出当前策略（查不到则用默认值）
	pol, err := db.GetCardPolicy(iccid)
	if errors.Is(err, db.ErrCardPolicyNotFound) {
		pol = db.DefaultCardPolicy(iccid)
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取卡策略失败"})
		return
	}

	if req.NetworkEnabled != nil {
		pol.NetworkEnabled = *req.NetworkEnabled
	}
	if req.VoWiFiEnabled != nil {
		pol.VoWiFiEnabled = *req.VoWiFiEnabled
	}
	if req.AirplaneEnabled != nil {
		pol.AirplaneEnabled = *req.AirplaneEnabled
	}
	if req.IPVersion != nil {
		pol.IPVersion = strings.TrimSpace(*req.IPVersion)
		switch pol.IPVersion {
		case "v4", "v6", "v4v6":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "IP 版本必须为 v4、v6 或 v4v6"})
			return
		}
	}
	if req.APN != nil {
		pol.APN = strings.TrimSpace(*req.APN)
	}
	if pol.NetworkEnabled && (pol.VoWiFiEnabled || pol.AirplaneEnabled) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "网络不能与 VoWiFi 或飞行模式同时开启"})
		return
	}
	pol.Source = "user"

	if err := db.UpsertCardPolicy(pol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, pol)
}
