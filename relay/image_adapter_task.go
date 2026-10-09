package relay

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

func configuredImageTaskSnapshot(body []byte, config *dto.ImageAdapterTaskConfig) (*NativeAsyncImageSnapshot, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid image task response")
	}
	var id string
	for _, path := range config.IDPaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			id = strings.TrimSpace(value.String())
			break
		}
	}
	if id == "" || len(id) > 512 || id == "." || id == ".." || strings.ContainsAny(id, "/\\\r\n\x00") {
		return nil, fmt.Errorf("image task response missing a valid configured task ID")
	}
	poll := expandImageTaskURL(config.PollURLTemplate, id)
	content := expandImageTaskURL(config.ContentURLTemplate, id)
	return &NativeAsyncImageSnapshot{ID: id, PollURL: poll, ContentURL: content, Task: config}, nil
}

func expandImageTaskURL(template, id string) string {
	path, query, hasQuery := strings.Cut(template, "?")
	path = strings.ReplaceAll(path, "{task_id}", url.PathEscape(id))
	if hasQuery {
		return path + "?" + strings.ReplaceAll(query, "{task_id}", url.QueryEscape(id))
	}
	return path
}

// normalizeConfiguredImageTaskResponse translates configured upstream envelopes
// to the existing worker contract. It never changes a completed task into a new
// generation request.
func normalizeConfiguredImageTaskResponse(body []byte, config *dto.ImageAdapterTaskConfig) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid image task status response")
	}
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, config.StatusPath).String()))
	canonical := ""
	for value, states := range map[string][]string{"running": config.PendingStates, "completed": config.SuccessStates, "failed": config.FailureStates} {
		for _, candidate := range states {
			if status == strings.ToLower(strings.TrimSpace(candidate)) {
				canonical = value
				break
			}
		}
	}
	if canonical == "" {
		return nil, fmt.Errorf("unrecognized configured image task status")
	}
	envelope := map[string]any{"status": canonical}
	if canonical == "failed" && (status == "expired" || status == "cancelled" || status == "canceled") {
		envelope["status"] = status
	}
	if canonical == "failed" {
		message := "upstream image task failed"
		if config.ErrorPath != "" {
			value := gjson.GetBytes(body, config.ErrorPath)
			if value.Type == gjson.String && value.String() != "" {
				message = value.String()
			} else if nested := value.Get("message"); nested.Type == gjson.String && nested.String() != "" {
				message = nested.String()
			}
		}
		errorInfo := map[string]any{"message": message}
		if config.ErrorPath != "" {
			value := gjson.GetBytes(body, config.ErrorPath)
			for _, field := range []string{"code", "type", "status", "status_code"} {
				if nested := value.Get(field); nested.Type == gjson.String || nested.Type == gjson.Number {
					errorInfo[field] = nested.Value()
				}
			}
			if retryable := value.Get("retryable"); retryable.Type == gjson.False {
				errorInfo["retryable"] = false
			}
		}
		envelope["error"] = errorInfo
	}
	if canonical == "completed" {
		// Keep the provider's usage alongside the translated image envelope.
		// Missing usage must remain missing, rather than becoming zero tokens.
		if usage := gjson.GetBytes(body, "usage"); usage.Exists() {
			envelope["usage"] = json.RawMessage(usage.Raw)
		}
		image := dto.ImageData{}
		if config.ResultURLPath != "" {
			if value := gjson.GetBytes(body, config.ResultURLPath); value.Type == gjson.String {
				image.Url = strings.TrimSpace(value.String())
			}
		}
		if config.ResultB64Path != "" {
			if value := gjson.GetBytes(body, config.ResultB64Path); value.Type == gjson.String {
				image.B64Json = strings.TrimSpace(value.String())
			}
		}
		if image.Url != "" || image.B64Json != "" {
			envelope["data"] = []dto.ImageData{image}
		}
	}
	return common.Marshal(envelope)
}
