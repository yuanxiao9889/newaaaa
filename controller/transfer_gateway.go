package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type bridgeRuntimeSnapshot struct {
	CheckedAt int64                        `json:"checked_at"`
	Available bool                         `json:"available"`
	Status    *service.BridgeGatewayStatus `json:"status,omitempty"`
	Error     string                       `json:"error,omitempty"`
}

var bridgeRuntimeCache struct {
	sync.Mutex
	expires time.Time
	value   bridgeRuntimeSnapshot
}

func readBridgeRuntime(ctx context.Context, force bool) bridgeRuntimeSnapshot {
	bridgeRuntimeCache.Lock()
	defer bridgeRuntimeCache.Unlock()
	if !force && time.Now().Before(bridgeRuntimeCache.expires) {
		return bridgeRuntimeCache.value
	}
	status, err := service.ReadBridgeGatewayStatus(ctx)
	value := bridgeRuntimeSnapshot{CheckedAt: time.Now().Unix(), Available: err == nil, Status: status}
	if err != nil {
		value.Error = "Bridge runtime status is unavailable; cache and transfer counters are unknown"
	}
	bridgeRuntimeCache.expires = time.Now().Add(5 * time.Second)
	bridgeRuntimeCache.value = value
	return value
}

func UpdateBridgeRuntime(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Config service.BridgeGatewayConfig `json:"config"`
		Reason string                      `json:"reason"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil || len(strings.TrimSpace(input.Reason)) < 1 || utf8.RuneCountInString(input.Reason) > 500 {
		c.JSON(400, gin.H{"success": false, "message": "A configuration and change reason are required"})
		return
	}
	if err := service.ValidateBridgeGatewayConfig(input.Config); err != nil || input.Config.Revision < 1 {
		c.JSON(400, gin.H{"success": false, "message": "Invalid bridge cache, recovery limits or update revision"})
		return
	}
	if err := service.ApplyBridgeGatewayConfig(c.Request.Context(), input.Config); err != nil {
		status := 503
		if errors.Is(err, service.ErrBridgeGatewayConflict) {
			status = 409
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	// Runtime secrets and the download capability are never part of the audit.
	model.RecordLog(c.GetInt("id"), model.LogTypeManage,
		"Applied bridge cache/recovery configuration revision "+strconv.FormatInt(input.Config.Revision, 10)+
			"; reason="+strconv.Quote(strings.TrimSpace(input.Reason)))
	readBridgeRuntime(c.Request.Context(), true)
	GetBridgeManagement(c)
}
