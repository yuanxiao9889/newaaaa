package dto

import (
	"fmt"
	"regexp"
	"strings"
)

// ImageRequestFilterConfig limits which image requests may use a channel.
// Rules match exact model names and require both an allowed quality and size.
type ImageRequestFilterConfig struct {
	Enabled bool               `json:"enabled"`
	Rules   []ImageRequestRule `json:"rules"`
}

type ImageRequestRule struct {
	Models    []string `json:"models"`
	Qualities []string `json:"qualities"`
	Sizes     []string `json:"sizes"`
}

var imageRequestQualityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func (s *ChannelSettings) ValidateImageRequestFilter() error {
	if s == nil {
		return nil
	}
	return s.ImageRequestFilter.Validate()
}

func (c *ImageRequestFilterConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if len(c.Rules) == 0 || len(c.Rules) > 64 {
		return fmt.Errorf("image_request_filter.rules must contain 1 to 64 rules")
	}
	models := make(map[string]struct{})
	for i, rule := range c.Rules {
		if len(rule.Models) == 0 || len(rule.Models) > 256 {
			return fmt.Errorf("image_request_filter.rules[%d].models must contain 1 to 256 models", i)
		}
		for _, name := range rule.Models {
			if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n\t") {
				return fmt.Errorf("image_request_filter.rules[%d].models contains an invalid model name", i)
			}
			if _, duplicate := models[name]; duplicate {
				return fmt.Errorf("image_request_filter.rules contains duplicate model %q", name)
			}
			models[name] = struct{}{}
		}
		if len(models) > 256 {
			return fmt.Errorf("image_request_filter.rules must contain at most 256 models in total")
		}
		if len(rule.Qualities) == 0 || len(rule.Qualities) > 32 {
			return fmt.Errorf("image_request_filter.rules[%d].qualities must contain 1 to 32 values", i)
		}
		qualities := make(map[string]struct{}, len(rule.Qualities))
		for _, quality := range rule.Qualities {
			if quality == "auto" || !imageRequestQualityPattern.MatchString(quality) {
				return fmt.Errorf("image_request_filter.rules[%d].qualities contains an invalid quality", i)
			}
			if _, duplicate := qualities[quality]; duplicate {
				return fmt.Errorf("image_request_filter.rules[%d].qualities contains a duplicate value", i)
			}
			qualities[quality] = struct{}{}
		}
		if len(rule.Sizes) == 0 || len(rule.Sizes) > 256 {
			return fmt.Errorf("image_request_filter.rules[%d].sizes must contain 1 to 256 values", i)
		}
		sizes := make(map[string]struct{}, len(rule.Sizes))
		for _, size := range rule.Sizes {
			if !imageAdapterSizePattern.MatchString(size) {
				return fmt.Errorf("image_request_filter.rules[%d].sizes must contain explicit WIDTHxHEIGHT dimensions", i)
			}
			if _, duplicate := sizes[size]; duplicate {
				return fmt.Errorf("image_request_filter.rules[%d].sizes contains a duplicate value", i)
			}
			sizes[size] = struct{}{}
		}
	}
	return nil
}
