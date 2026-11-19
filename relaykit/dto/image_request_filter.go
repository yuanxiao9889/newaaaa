package dto

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ImageRequestFilterConfig limits which image requests may use a channel.
// Rules match exact model names and require both an allowed quality and size.
type ImageRequestFilterConfig struct {
	Enabled bool               `json:"enabled"`
	Rules   []ImageRequestRule `json:"rules"`
}

type ImageRequestRule struct {
	Models     []string                `json:"models"`
	Qualities  []string                `json:"qualities"`
	Sizes      []string                `json:"sizes"`
	CustomSize *ImageRequestCustomSize `json:"custom_size,omitempty"`
}

// ImageRequestCustomSize bounds explicit custom dimensions. Dimensions must be
// multiples of 16 with an aspect ratio between 1:3 and 3:1.
type ImageRequestCustomSize struct {
	MinPixels int `json:"min_pixels"`
	MaxPixels int `json:"max_pixels"`
	MaxEdge   int `json:"max_edge"`
}

// AllowsSize accepts an exact preset or a custom size within the configured bounds.
func (r ImageRequestRule) AllowsSize(size string) bool {
	if slices.Contains(r.Sizes, size) {
		return true
	}
	c := r.CustomSize
	if c == nil || !imageAdapterSizePattern.MatchString(size) {
		return false
	}
	w, h, _ := strings.Cut(size, "x")
	width, err := strconv.ParseUint(w, 10, 32)
	if err != nil {
		return false
	}
	height, err := strconv.ParseUint(h, 10, 32)
	if err != nil || width == 0 || height == 0 || width%16 != 0 || height%16 != 0 {
		return false
	}
	// Bound dimensions before multiplication, including when called without Validate.
	if c.MaxEdge <= 0 || c.MaxEdge > 3840 || max(width, height) > uint64(c.MaxEdge) || max(width, height) > 3*min(width, height) {
		return false
	}
	pixels := width * height
	return c.MinPixels >= 655360 && c.MaxPixels <= 8294400 && c.MaxPixels >= c.MinPixels && pixels >= uint64(c.MinPixels) && pixels <= uint64(c.MaxPixels)
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
		if (len(rule.Sizes) == 0 && rule.CustomSize == nil) || len(rule.Sizes) > 256 {
			return fmt.Errorf("image_request_filter.rules[%d] must contain up to 256 sizes or a custom_size range", i)
		}
		if c := rule.CustomSize; c != nil {
			if c.MinPixels < 655360 || c.MaxPixels > 8294400 || c.MinPixels > c.MaxPixels || c.MaxEdge < 16 || c.MaxEdge > 3840 || c.MaxEdge%16 != 0 || c.MaxEdge*c.MaxEdge < c.MinPixels {
				return fmt.Errorf("image_request_filter.rules[%d].custom_size has invalid pixel or edge bounds", i)
			}
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
