package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UpdateUserVIP(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	userID, err := strconv.Atoi(c.Param("id"))
	var req struct {
		IsVIP *bool `json:"is_vip"`
	}
	if err != nil || userID <= 0 || common.DecodeJson(c.Request.Body, &req) != nil || req.IsVIP == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	previous, err := model.SetUserVIP(userID, c.GetInt("role"), *req.IsVIP)
	if errors.Is(err, model.ErrUserVIPPermission) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, i18n.MsgUserNotExists)
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, userID, "user.vip_update", map[string]any{
		"target_user_id":  userID,
		"previous_is_vip": previous,
		"is_vip":          *req.IsVIP,
	})
	common.ApiSuccess(c, gin.H{"id": userID, "is_vip": *req.IsVIP})
}
