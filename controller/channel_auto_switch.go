package controller

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// GetChannelAutoSwitch reports configuration candidates separately from observed
// health. A ready pool can still lack a peer for a particular request's inputs.
func GetChannelAutoSwitch(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	states, err := model.ListChannelAutoSwitchStates(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	scopes := make([]gin.H, 0)
	for _, group := range channel.GetGroups() {
		names := channel.GetModels()
		for _, state := range states {
			if state.Group == group && !slices.Contains(names, state.Model) && slices.Contains(names, ratio_setting.RoutingMatchModelName(state.Model)) {
				names = append(names, state.Model)
			}
		}
		for _, name := range names {
			candidates, err := model.GetConfiguredChannelsForModel(group, name, nil)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			participants := 0
			for _, candidate := range candidates {
				if candidate.GetOtherSettings().AutoSwitchEnabled && candidate.Type != constant.ChannelTypeMidjourney && candidate.Type != constant.ChannelTypeMidjourneyPlus {
					participants++
				}
			}
			found := false
			for _, state := range states {
				if state.Group != group || state.Model != name {
					continue
				}
				found = true
				status := state.Status
				if participants < 2 {
					status = "waiting"
				}
				scopes = append(scopes, gin.H{"group": group, "model": name, "request_path": state.Protocol,
					"participant_count": participants, "status": status, "cooldown_until": state.CooldownUntil,
					"probe_successes": state.ProbeSuccesses, "consecutive_failures": state.ConsecutiveFailures, "last_reason": state.LastReason})
			}
			if !found {
				status := "waiting"
				if participants >= 2 {
					status = "ready"
				}
				scopes = append(scopes, gin.H{"group": group, "model": name, "request_path": "", "participant_count": participants,
					"status": status, "cooldown_until": 0, "probe_successes": 0, "consecutive_failures": 0, "last_reason": ""})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"enabled": channel.GetOtherSettings().AutoSwitchEnabled,
		"policy": gin.H{"failure_threshold": 3, "recovery_successes": 3, "cooldown_seconds": 300, "window_seconds": 600}, "scopes": scopes}})
}
