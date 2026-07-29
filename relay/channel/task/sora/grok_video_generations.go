package sora

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type grokVideoGenerationsRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Seconds     *string  `json:"seconds,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	Quality     string   `json:"quality,omitempty"`
	Image       string   `json:"image,omitempty"`
	Images      []string `json:"images,omitempty"`
}

func isGrokVideoGenerationsModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "grok-api-video-1.5",
		"grok-api-video-1.5-10s",
		"grok-api-video-1.5-15s",
		"grok-video-3",
		"grok-video-3-10s",
		"grok-video-3-16s",
		"grok-video-3-20s",
		"grok-video-1.5-10s":
		return true
	default:
		return false
	}
}

func isGrokVideoGenerationsChannel(info *relaycommon.RelayInfo) bool {
	return info != nil &&
		info.ChannelMeta != nil &&
		info.ChannelType == constant.ChannelTypeGrokVideo
}

func isGrokVideoGenerationsPlatform(platform constant.TaskPlatform) bool {
	return platform == constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGrokVideo))
}

func isGrokVideo3Model(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-video-3-")
}

func isDynamicGrokVideo3Model(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "grok-video-3")
}

func isFlexibleGrokVideoModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "grok-api-video-1.5")
}

func firstGrokString(body map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := body[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func grokRequestSeconds(body map[string]any, maxSeconds int) (int, bool, error) {
	for _, key := range []string{"seconds", "duration"} {
		value, exists := body[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			seconds, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			if err != nil {
				return 0, true, fmt.Errorf("%s must be an integer", key)
			}
			return checkedGrokSeconds(seconds, key, maxSeconds)
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) {
				return 0, true, fmt.Errorf("%s must be an integer", key)
			}
			if typed < 1 || typed > float64(maxSeconds) {
				return 0, true, fmt.Errorf("%s must be between 1 and %d", key, maxSeconds)
			}
			return int(typed), true, nil
		case int:
			return checkedGrokSeconds(int64(typed), key, maxSeconds)
		case int64:
			return checkedGrokSeconds(typed, key, maxSeconds)
		case json.Number:
			seconds, err := typed.Int64()
			if err != nil {
				return 0, true, fmt.Errorf("%s must be an integer", key)
			}
			return checkedGrokSeconds(seconds, key, maxSeconds)
		default:
			return 0, true, fmt.Errorf("%s must be an integer", key)
		}
	}
	return 0, false, nil
}

func checkedGrokSeconds(seconds int64, field string, maxSeconds int) (int, bool, error) {
	if seconds < 1 || seconds > int64(maxSeconds) {
		return 0, true, fmt.Errorf("%s must be between 1 and %d", field, maxSeconds)
	}
	return int(seconds), true, nil
}

func fixedGrokAPIModelForSeconds(seconds int) (string, bool) {
	switch seconds {
	case 10, 15:
		return fmt.Sprintf("grok-api-video-1.5-%ds", seconds), true
	default:
		return "", false
	}
}

func fixedGrokVideo3ModelForSeconds(seconds int) (string, bool) {
	switch seconds {
	case 10, 16, 20:
		return fmt.Sprintf("grok-video-3-%ds", seconds), true
	default:
		return "", false
	}
}

func normalizeGrokAspectRatio(body map[string]any) string {
	ratio := firstGrokString(body, "aspect_ratio", "ratio")
	switch ratio {
	case "16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3":
		return ratio
	}

	switch strings.ToLower(firstGrokString(body, "size")) {
	case "720x1280", "1024x1792":
		return "9:16"
	case "1024x1024":
		return "1:1"
	case "1024x768":
		return "4:3"
	case "768x1024":
		return "3:4"
	case "1536x1024":
		return "3:2"
	case "1024x1536":
		return "2:3"
	default:
		return "16:9"
	}
}

func normalizeGrokResolution(body map[string]any) string {
	resolution := strings.ToUpper(firstGrokString(body, "resolution", "resolution_name"))
	switch resolution {
	case "480P", "720P", "1080P":
		return resolution
	default:
		return ""
	}
}

func appendGrokImage(images *[]string, seen map[string]struct{}, value any) {
	switch typed := value.(type) {
	case string:
		image := strings.TrimSpace(typed)
		if image == "" {
			return
		}
		if _, exists := seen[image]; exists {
			return
		}
		seen[image] = struct{}{}
		*images = append(*images, image)
	case []string:
		for _, item := range typed {
			appendGrokImage(images, seen, item)
		}
	case []any:
		for _, item := range typed {
			appendGrokImage(images, seen, item)
		}
	case map[string]any:
		for _, key := range []string{"url", "image", "image_url"} {
			if nested, exists := typed[key]; exists {
				appendGrokImage(images, seen, nested)
			}
		}
	}
}

func collectGrokRequestImages(body map[string]any) []string {
	images := make([]string, 0, 7)
	seen := make(map[string]struct{})
	for _, key := range []string{
		"images",
		"reference_images",
		"image",
		"input_reference",
		"image_url",
	} {
		if value, exists := body[key]; exists {
			appendGrokImage(&images, seen, value)
		}
	}

	messages, _ := body["messages"].([]any)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		content, _ := message["content"].([]any)
		for _, rawItem := range content {
			item, _ := rawItem.(map[string]any)
			if itemType, _ := item["type"].(string); itemType != "image_url" {
				continue
			}
			appendGrokImage(&images, seen, item["image_url"])
		}
	}

	if len(images) > 7 {
		return images[:7]
	}
	return images
}

func buildGrokVideoGenerationsRequest(
	clientBody []byte,
	upstreamModel string,
	originModel string,
) ([]byte, error) {
	if !isGrokVideoGenerationsModel(upstreamModel) {
		return nil, fmt.Errorf(
			"unsupported model %q for Grok Video (Generations) channel",
			upstreamModel,
		)
	}

	var body map[string]any
	if err := common.Unmarshal(clientBody, &body); err != nil {
		return nil, fmt.Errorf("unmarshal Grok video request failed: %w", err)
	}

	request := grokVideoGenerationsRequest{
		Model:       upstreamModel,
		Prompt:      firstGrokString(body, "prompt"),
		AspectRatio: normalizeGrokAspectRatio(body),
	}
	if request.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	resolution := normalizeGrokResolution(body)
	images := collectGrokRequestImages(body)
	if isDynamicGrokVideo3Model(upstreamModel) {
		seconds, exists, err := grokRequestSeconds(body, 20)
		if err != nil {
			return nil, fmt.Errorf("invalid seconds for %s: %w", originModel, err)
		}
		if !exists {
			seconds = 10
		}
		fixedModel, supported := fixedGrokVideo3ModelForSeconds(seconds)
		if !supported {
			return nil, fmt.Errorf(
				"%s only supports fixed durations 10, 16, or 20 seconds",
				originModel,
			)
		}
		request.Model = fixedModel
	}
	if isGrokVideo3Model(request.Model) {
		request.Quality = firstGrokString(body, "quality")
		if request.Quality == "" {
			request.Quality = resolution
		}
		request.Images = images
	} else {
		request.Resolution = resolution
		if len(images) > 0 {
			request.Image = images[0]
		}
		if isFlexibleGrokVideoModel(upstreamModel) {
			if strings.EqualFold(strings.TrimSpace(originModel), "ok-video-1.5-preview") {
				seconds, exists, err := grokRequestSeconds(body, 15)
				if err != nil {
					return nil, fmt.Errorf("invalid seconds for %s: %w", originModel, err)
				}
				if !exists {
					seconds = 10
				}
				fixedModel, supported := fixedGrokAPIModelForSeconds(seconds)
				if !supported {
					return nil, fmt.Errorf(
						"%s only supports fixed durations 10 or 15 seconds",
						originModel,
					)
				}
				request.Model = fixedModel
				return common.Marshal(request)
			}

			seconds, exists, err := grokRequestSeconds(body, 20)
			if err != nil {
				return nil, fmt.Errorf("invalid seconds for %s: %w", upstreamModel, err)
			}
			if exists {
				secondsString := strconv.Itoa(seconds)
				request.Seconds = &secondsString
			}
		}
	}

	return common.Marshal(request)
}
