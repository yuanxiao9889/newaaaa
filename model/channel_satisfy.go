package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// GetSatisfiedChannels returns the same eligible set used by ordinary routing,
// before priority selection. Automatic failover must never widen that set.
func GetSatisfiedChannels(group, modelName string, filters []dto.ChannelFilter) ([]*Channel, error) {
	if !common.MemoryCacheEnabled {
		var abilities []Ability
		err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, modelName, true).Find(&abilities).Error
		if err != nil {
			return nil, err
		}
		abilities = filterAbilitiesByConstraints(abilities, modelName, filters)
		if len(abilities) == 0 {
			normalized := ratio_setting.RoutingMatchModelName(modelName)
			if normalized != modelName {
				if err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, normalized, true).Find(&abilities).Error; err != nil {
					return nil, err
				}
				abilities = filterAbilitiesByConstraints(abilities, modelName, filters)
			}
		}
		ids := make([]int, 0, len(abilities))
		for _, ability := range abilities {
			ids = append(ids, ability.ChannelId)
		}
		if len(ids) == 0 {
			return nil, nil
		}
		var channels []*Channel
		err = DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error
		return channels, err
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	ids, _ := filterCandidateIDs(group2model2channels[group][modelName], modelName, filters)
	if len(ids) == 0 {
		ids, _ = filterCandidateIDs(group2model2channels[group][ratio_setting.RoutingMatchModelName(modelName)], modelName, filters)
	}
	channels := make([]*Channel, 0, len(ids))
	for _, id := range ids {
		if ch := channelsIDM[id]; ch != nil && ch.Status == common.ChannelStatusEnabled {
			channels = append(channels, ch)
		}
	}
	return channels, nil
}

// GetConfiguredChannelsForModel reports compatible configuration membership,
// including disabled channels. Callers must not use this set for routing: disabling
// a participant must not silently dissolve its peers' existing health protection.
func GetConfiguredChannelsForModel(group, modelName string, filters []dto.ChannelFilter) ([]*Channel, error) {
	modelNames := []string{modelName}
	if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != modelName && normalized != "" {
		modelNames = append(modelNames, normalized)
	}
	for _, configuredModel := range modelNames {
		var ids []int
		if err := DB.Model(&Ability{}).Where(map[string]any{"group": group, "model": configuredModel}).Distinct("channel_id").Pluck("channel_id", &ids).Error; err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		var channels []*Channel
		if err := DB.Where("id IN ?", ids).Find(&channels).Error; err != nil {
			return nil, err
		}
		compatible := make([]*Channel, 0, len(channels))
		for _, channel := range channels {
			// Existing getters repair malformed JSON by saving the channel. Check
			// first so this inventory stays read-only and fails closed instead.
			var settings kitdto.ChannelSettings
			if channel.Setting != nil && *channel.Setting != "" {
				if err := common.UnmarshalJsonStr(*channel.Setting, &settings); err != nil {
					continue
				}
			}
			var otherSettings kitdto.ChannelOtherSettings
			if channel.OtherSettings != "" {
				if err := common.UnmarshalJsonStr(channel.OtherSettings, &otherSettings); err != nil {
					continue
				}
			}
			configured := *channel
			configured.Status = common.ChannelStatusEnabled
			if ok, _ := ChannelSatisfiesFilters(&configured, modelName, filters); ok {
				compatible = append(compatible, channel)
			}
		}
		if len(compatible) > 0 {
			return compatible, nil
		}
	}
	return nil, nil
}

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func isChannelIDInList(list []int, channelID int) bool {
	return slices.Contains(list, channelID)
}
