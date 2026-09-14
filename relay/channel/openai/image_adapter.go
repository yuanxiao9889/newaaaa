package openai

import (
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ConvertConfiguredImageRequest converts only opted-in channels. The original
// request and relay mode remain authoritative for billing and task provenance.
func ConvertConfiguredImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (map[string]any, error) {
	config := info.ChannelSetting.ImageAdapter
	if err := config.Validate(); err != nil {
		return nil, err
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var input map[string]any
	if err = common.Unmarshal(encoded, &input); err != nil {
		return nil, err
	}
	// The generic DTO intentionally omits unknown fields on marshal. Preserve
	// them only inside this explicitly configured conversion boundary.
	for field, raw := range request.Extra {
		if _, exists := input[field]; !exists {
			var value any
			if err = common.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("invalid image field %s", field)
			}
			input[field] = value
		}
	}
	if c.Request.MultipartForm != nil {
		for field, values := range c.Request.MultipartForm.Value {
			if _, exists := input[field]; !exists && len(values) == 1 {
				input[field] = values[0]
			}
		}
	}
	for _, field := range config.Request.RejectFields {
		value := input[field]
		present := value != nil
		switch typed := value.(type) {
		case string:
			present = typed != "" && !(field == "background" && (typed == "opaque" || typed == "auto"))
		case bool:
			present = typed
		case []any:
			present = len(typed) > 0
		}
		if c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File[field]) > 0 {
			present = true
		}
		if present {
			return nil, fmt.Errorf("image adapter does not support %s", field)
		}
	}
	output := maps.Clone(input)
	for _, field := range config.Request.DropFields {
		delete(output, field)
	}
	for source, target := range config.Request.FieldMap {
		if source != target {
			delete(output, source)
		}
	}
	// Evaluate every mapping from input before writing targets. This permits
	// quality -> detail_level followed by size -> quality without collisions.
	for source, values := range config.Request.ValueMap {
		value, exists := input[source]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("image adapter field %s must be a string", source)
		}
		mapped, ok := values[text]
		if !ok {
			return nil, fmt.Errorf("image adapter has no mapping for %s=%s", source, text)
		}
		target := config.Request.FieldMap[source]
		if target == "" {
			target = source
		}
		output[target] = mapped
	}
	for source, target := range config.Request.FieldMap {
		if _, mapped := config.Request.ValueMap[source]; mapped {
			continue
		}
		if value, exists := input[source]; exists {
			output[target] = value
		}
	}
	if len(config.Request.SizeMap) > 0 && request.Size != "" {
		fields, ok := config.Request.SizeMap[request.Size]
		if !ok {
			return nil, fmt.Errorf("image adapter does not support size %s", request.Size)
		}
		delete(output, "size")
		for field, value := range fields {
			output[field] = value
		}
	}
	if config.References != nil {
		urls, err := service.PrepareImageAdapterReferences(c, &request, config.References)
		if err != nil {
			return nil, err
		}
		for _, field := range []string{"image", "images", "image_url", "image_urls", "reference_images", "input_reference", "content"} {
			delete(output, field)
		}
		if len(urls) > 0 {
			if config.References.Encoding == "image_url_content" {
				items := make([]any, 0, len(urls))
				for _, imageURL := range urls {
					items = append(items, map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]string{"url": imageURL}})
				}
				output[config.References.Field] = items
			} else {
				output[config.References.Field] = urls
			}
		}
	} else if strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "multipart/") {
		return nil, fmt.Errorf("image adapter reference settings are required for file uploads")
	}
	c.Request.Header.Set("Content-Type", "application/json")
	return output, nil
}

// ValidateConfiguredImageCount prevents parameter overrides from changing the
// billable quantity after request validation and pre-consumption.
func ValidateConfiguredImageCount(body []byte, request *dto.ImageRequest) error {
	var outgoing struct {
		N           *uint `json:"n"`
		CreateCount *uint `json:"create_count"`
	}
	if err := common.Unmarshal(body, &outgoing); err != nil {
		return fmt.Errorf("invalid converted image count")
	}
	want := uint(1)
	if request.N != nil {
		want = *request.N
	}
	if want == 0 || want > dto.MaxImageN || (outgoing.N != nil && *outgoing.N != want) || (outgoing.CreateCount != nil && *outgoing.CreateCount != want) || (outgoing.N == nil && want != 1) {
		return fmt.Errorf("image adapter cannot change the requested image count")
	}
	return nil
}

// configuredImageEndpoint keeps the configured path on the channel origin;
// templates cannot redirect authenticated generation requests to another host.
func configuredImageEndpoint(base, path string) (string, error) {
	origin, err := url.Parse(base)
	if err != nil || origin.Host == "" || (origin.Scheme != "https" && origin.Scheme != "http") || origin.User != nil {
		return "", fmt.Errorf("invalid image adapter endpoint")
	}
	relative, err := url.Parse(path)
	if err != nil || relative.IsAbs() || relative.Host != "" {
		return "", fmt.Errorf("invalid image adapter path")
	}
	return origin.ResolveReference(relative).String(), nil
}
