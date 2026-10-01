package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

var filterEvalOrder = []dto.ChannelFilterKind{
	dto.FilterChannelIdentity,
	dto.FilterRequestPath,
	dto.FilterImageRequest,
	dto.FilterTaskPluginIdentity,
	dto.FilterResponsesWebSocket,
}

// ChannelSatisfiesFilters reports whether ch passes every filter.
// On false, it returns the kind of the first violated filter (request_path
// then task_plugin_identity) for error attribution.
func ChannelSatisfiesFilters(ch *Channel, modelName string, filters []dto.ChannelFilter) (bool, dto.ChannelFilterKind) {
	if ch == nil {
		return false, ""
	}
	for _, kind := range filterEvalOrder {
		for _, filter := range filters {
			if filter.Kind != kind {
				continue
			}
			if !channelMatchesFilter(ch, modelName, filter) {
				return false, kind
			}
		}
	}
	return true, ""
}

// filterCandidateIDs applies filters to a cached candidate id list.
// Caller must hold channelSyncLock (read lock). The input slice is never mutated.
// A missing id in channelsIDM is kept for request_path (downstream consistency
// error) and dropped for task_plugin_identity, matching the previous filters.
func filterCandidateIDs(ids []int, modelName string, filters []dto.ChannelFilter) (kept []int, emptiedBy dto.ChannelFilterKind) {
	if len(ids) == 0 {
		return ids, ""
	}
	kept = ids
	for _, kind := range filterEvalOrder {
		kindFilters := filtersByKind(filters, kind)
		if len(kindFilters) == 0 {
			continue
		}
		next := make([]int, 0, len(kept))
		for _, id := range kept {
			channel, exists := channelsIDM[id]
			if candidatePassesKindFilters(channel, exists, modelName, kind, kindFilters) {
				next = append(next, id)
			}
		}
		if len(kept) > 0 && len(next) == 0 {
			return next, kind
		}
		kept = next
	}
	return kept, ""
}

func filtersByKind(filters []dto.ChannelFilter, kind dto.ChannelFilterKind) []dto.ChannelFilter {
	var matched []dto.ChannelFilter
	for _, filter := range filters {
		if filter.Kind == kind {
			matched = append(matched, filter)
		}
	}
	return matched
}

func candidatePassesKindFilters(ch *Channel, exists bool, modelName string, kind dto.ChannelFilterKind, filters []dto.ChannelFilter) bool {
	if kind == dto.FilterRequestPath && !exists {
		return true
	}
	if !exists || ch == nil {
		return false
	}
	for _, filter := range filters {
		if !channelMatchesFilter(ch, modelName, filter) {
			return false
		}
	}
	return true
}

func channelMatchesFilter(ch *Channel, modelName string, filter dto.ChannelFilter) bool {
	switch filter.Kind {
	case dto.FilterChannelIdentity:
		return ch.Status == common.ChannelStatusEnabled && (filter.ChannelID == 0 || ch.Id == filter.ChannelID) && !slices.Contains(filter.ExcludedChannelIDs, ch.Id)
	case dto.FilterRequestPath:
		if filter.RequestPath == "" {
			return true
		}
		if !constant.IsAdvancedCustomChannel(ch.Type) {
			return true
		}
		config := ch.GetOtherSettings().AdvancedCustom
		return config != nil && config.SupportsPathForModel(filter.RequestPath, modelName)
	case dto.FilterTaskPluginIdentity:
		if ch.Type == constant.ChannelTypeTaskPlugin {
			key := ch.GetSetting().TaskPluginKey
			return filter.TaskPluginKey != "" && (key == filter.TaskPluginKey || slices.Contains(filter.TaskPluginKeys, key))
		}
		return filter.TaskPluginKey == "" || slices.Contains(filter.TaskPluginChannelTypes, ch.Type)
	case dto.FilterResponsesWebSocket:
		return (ch.Type == constant.ChannelTypeOpenAI || ch.Type == constant.ChannelTypeCodex) && ch.GetSetting().ResponsesWebSocketEnabled
	case dto.FilterImageRequest:
		return channelMatchesImageRequestFilter(ch, modelName, filter)
	default:
		return true
	}
}

func channelMatchesImageRequestFilter(ch *Channel, modelName string, filter dto.ChannelFilter) bool {
	if ch.Setting == nil || *ch.Setting == "" {
		return true
	}
	// Do not use GetSetting here: its malformed-JSON recovery clears and saves
	// the setting, which would silently remove an admission restriction.
	var settings struct {
		ImageRequestFilter *kitdto.ImageRequestFilterConfig `json:"image_request_filter"`
	}
	if err := common.Unmarshal([]byte(*ch.Setting), &settings); err != nil {
		return false
	}
	config := settings.ImageRequestFilter
	if config == nil || !config.Enabled {
		return true
	}
	if filter.ImageModelAmbiguous {
		return false
	}
	var completeSettings kitdto.ChannelSettings
	if err := common.Unmarshal([]byte(*ch.Setting), &completeSettings); err != nil {
		return false
	}
	if err := config.Validate(); err != nil {
		return false
	}
	models, err := ResolveChannelModelMapping(ch.GetModelMapping(), modelName)
	if err != nil {
		return false
	}
	for _, candidate := range models {
		base := hostreasoning.BaseModelName(candidate)
		for _, rule := range config.Rules {
			if !slices.Contains(rule.Models, candidate) && !slices.Contains(rule.Models, base) {
				continue
			}
			if !filter.ImageRequestValid || !slices.Contains(rule.Qualities, filter.ImageQuality) || !slices.Contains(rule.Sizes, filter.ImageSize) {
				return false
			}
		}
	}
	return true
}
