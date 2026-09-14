package dto

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ImageAdapterConfig describes an opt-in OpenAI image protocol conversion.
// It contains no provider credentials and can be persisted with a task snapshot.
type ImageAdapterConfig struct {
	Enabled    bool                         `json:"enabled"`
	Models     []string                     `json:"models,omitempty"`
	Request    ImageAdapterRequestConfig    `json:"request"`
	References *ImageAdapterReferenceConfig `json:"references,omitempty"`
	Task       *ImageAdapterTaskConfig      `json:"task,omitempty"`
}

type ImageAdapterRequestConfig struct {
	GenerationsPath string                       `json:"generations_path,omitempty"`
	EditsPath       string                       `json:"edits_path,omitempty"`
	FieldMap        map[string]string            `json:"field_map,omitempty"`
	ValueMap        map[string]map[string]string `json:"value_map,omitempty"`
	SizeMap         map[string]map[string]string `json:"size_map,omitempty"`
	DropFields      []string                     `json:"drop_fields,omitempty"`
	RejectFields    []string                     `json:"reject_fields,omitempty"`
}

type ImageAdapterReferenceConfig struct {
	Field       string `json:"field"`
	Encoding    string `json:"encoding"`
	MaxImages   int    `json:"max_images"`
	UploadLocal bool   `json:"upload_local"`
}

type ImageAdapterTaskConfig struct {
	IDPaths             []string `json:"id_paths"`
	StatusPath          string   `json:"status_path"`
	ResultURLPath       string   `json:"result_url_path,omitempty"`
	ResultB64Path       string   `json:"result_b64_path,omitempty"`
	ErrorPath           string   `json:"error_path,omitempty"`
	PollURLTemplate     string   `json:"poll_url_template"`
	ContentURLTemplate  string   `json:"content_url_template,omitempty"`
	PendingStates       []string `json:"pending_states,omitempty"`
	SuccessStates       []string `json:"success_states"`
	FailureStates       []string `json:"failure_states"`
	PollIntervalSeconds int      `json:"poll_interval_seconds,omitempty"`
}

var imageAdapterFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var imageAdapterJSONPathPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.(?:[A-Za-z_][A-Za-z0-9_]*|0|[1-9][0-9]*))*$`)
var imageAdapterSizePattern = regexp.MustCompile(`^[1-9][0-9]{0,5}x[1-9][0-9]{0,5}$`)

// Matches uses exact upstream model identifiers, after channel model mapping.
func (c *ImageAdapterConfig) Matches(model string) bool {
	if c == nil || !c.Enabled {
		return false
	}
	if len(c.Models) == 0 {
		return true
	}
	model = strings.TrimSpace(model)
	for _, candidate := range c.Models {
		if strings.TrimSpace(candidate) == model {
			return true
		}
	}
	return false
}

func (s *ChannelSettings) ValidateImageAdapter() error {
	if s == nil || s.ImageAdapter == nil || !s.ImageAdapter.Enabled {
		return nil
	}
	if s.PassThroughBodyEnabled {
		return fmt.Errorf("image_adapter cannot be enabled with pass_through_body_enabled")
	}
	return s.ImageAdapter.Validate()
}

func (c *ImageAdapterConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if len(c.Models) > 256 {
		return fmt.Errorf("image_adapter.models must contain at most 256 models")
	}
	seenModels := make(map[string]bool, len(c.Models))
	for _, model := range c.Models {
		if model == "" || model != strings.TrimSpace(model) || len(model) > 256 || strings.ContainsAny(model, "\r\n\x00") || seenModels[model] {
			return fmt.Errorf("image_adapter.models contains an invalid or duplicate model")
		}
		seenModels[model] = true
	}
	r := c.Request
	if r.GenerationsPath == "" && r.EditsPath == "" && len(r.FieldMap)+len(r.ValueMap)+len(r.SizeMap)+len(r.DropFields)+len(r.RejectFields) == 0 && c.References == nil && c.Task == nil {
		return fmt.Errorf("image_adapter requires a request conversion or task configuration")
	}
	for field, path := range map[string]string{"generations_path": r.GenerationsPath, "edits_path": r.EditsPath} {
		if path != "" {
			if err := validateImageAdapterURL(path, false); err != nil {
				return fmt.Errorf("image_adapter.request.%s: %w", field, err)
			}
		}
	}
	if len(r.FieldMap) > 64 || len(r.ValueMap) > 64 || len(r.SizeMap) > 256 || len(r.DropFields) > 64 || len(r.RejectFields) > 64 {
		return fmt.Errorf("image_adapter.request exceeds the mapping limit")
	}
	targets := make(map[string]bool, len(r.FieldMap))
	for source, target := range r.FieldMap {
		if !validImageAdapterField(source, true) || !validImageAdapterField(target, true) || targets[target] {
			return fmt.Errorf("image_adapter.request.field_map contains an invalid, protected or duplicate target field")
		}
		targets[target] = true
	}
	for source, values := range r.ValueMap {
		if !validImageAdapterField(source, true) || len(values) == 0 || len(values) > 256 {
			return fmt.Errorf("image_adapter.request.value_map contains an invalid field or value map")
		}
		for from, to := range values {
			if from == "" || to == "" || len(from) > 256 || len(to) > 256 {
				return fmt.Errorf("image_adapter.request.value_map values must contain 1 to 256 characters")
			}
		}
	}
	for size, values := range r.SizeMap {
		if !imageAdapterSizePattern.MatchString(size) || len(values) == 0 || len(values) > 16 {
			return fmt.Errorf("image_adapter.request.size_map requires pixel dimensions and 1 to 16 output fields")
		}
		width, height, _ := strings.Cut(size, "x")
		w, _ := strconv.Atoi(width)
		h, _ := strconv.Atoi(height)
		if w > 65536 || h > 65536 {
			return fmt.Errorf("image_adapter.request.size_map dimensions must not exceed 65536")
		}
		for field, value := range values {
			if !validImageAdapterField(field, true) || value == "" || len(value) > 256 {
				return fmt.Errorf("image_adapter.request.size_map contains an invalid or protected output field")
			}
		}
	}
	for _, fields := range [][]string{r.DropFields, r.RejectFields} {
		seen := make(map[string]bool, len(fields))
		for _, field := range fields {
			if !validImageAdapterField(field, true) || seen[field] {
				return fmt.Errorf("image_adapter request field lists contain an invalid, protected or duplicate field")
			}
			seen[field] = true
		}
	}
	if ref := c.References; ref != nil {
		if !validImageAdapterField(ref.Field, true) {
			return fmt.Errorf("image_adapter.references.field must be an unprotected top-level field")
		}
		if ref.Encoding != "url_array" && ref.Encoding != "image_url_content" {
			return fmt.Errorf("image_adapter.references.encoding must be url_array or image_url_content")
		}
		if ref.MaxImages < 1 || ref.MaxImages > 16 {
			return fmt.Errorf("image_adapter.references.max_images must be between 1 and 16")
		}
	}
	if c.Task != nil {
		return c.Task.Validate()
	}
	return nil
}

func (t *ImageAdapterTaskConfig) Validate() error {
	if t == nil {
		return nil
	}
	if len(t.IDPaths) == 0 || len(t.IDPaths) > 16 || t.StatusPath == "" {
		return fmt.Errorf("image_adapter.task requires 1 to 16 id_paths and a status_path")
	}
	paths := append([]string{t.StatusPath}, t.IDPaths...)
	for _, path := range []string{t.ResultURLPath, t.ResultB64Path, t.ErrorPath} {
		if path != "" {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		if len(path) > 256 || !imageAdapterJSONPathPattern.MatchString(path) {
			return fmt.Errorf("image_adapter.task paths must use simple dot-separated fields or array indexes")
		}
	}
	if err := validateImageAdapterURL(t.PollURLTemplate, true); err != nil {
		return fmt.Errorf("image_adapter.task.poll_url_template: %w", err)
	}
	if t.ContentURLTemplate != "" {
		if err := validateImageAdapterURL(t.ContentURLTemplate, true); err != nil {
			return fmt.Errorf("image_adapter.task.content_url_template: %w", err)
		}
	}
	if t.ResultURLPath == "" && t.ResultB64Path == "" && t.ContentURLTemplate == "" {
		return fmt.Errorf("image_adapter.task requires a result path or content_url_template")
	}
	if t.PollIntervalSeconds != 0 && (t.PollIntervalSeconds < 3 || t.PollIntervalSeconds > 60) {
		return fmt.Errorf("image_adapter.task.poll_interval_seconds must be 0 or between 3 and 60")
	}
	if len(t.SuccessStates) == 0 || len(t.FailureStates) == 0 {
		return fmt.Errorf("image_adapter.task requires success_states and failure_states")
	}
	seen := make(map[string]bool)
	for _, states := range [][]string{t.PendingStates, t.SuccessStates, t.FailureStates} {
		if len(states) > 32 {
			return fmt.Errorf("image_adapter.task state lists must contain at most 32 entries")
		}
		for _, state := range states {
			key := strings.ToLower(state)
			if state == "" || state != strings.TrimSpace(state) || len(state) > 64 || strings.ContainsAny(state, "\r\n\x00") || seen[key] {
				return fmt.Errorf("image_adapter.task states must be nonempty and disjoint")
			}
			seen[key] = true
		}
	}
	return nil
}

func validImageAdapterField(field string, protectBilling bool) bool {
	if !imageAdapterFieldPattern.MatchString(field) {
		return false
	}
	switch strings.ToLower(field) {
	case "__proto__", "constructor", "prototype":
		return false
	case "model", "n", "create_count", "batch_size", "num_images", "image_count", "cost", "quota", "price", "total_tokens", "input_tokens", "output_tokens", "max_tokens", "max_output_tokens":
		return !protectBilling
	}
	return true
}

func validateImageAdapterURL(raw string, taskTemplate bool) error {
	if len(raw) == 0 || len(raw) > 2048 || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\#\r\n\x00") || raw != strings.TrimSpace(raw) {
		return fmt.Errorf("must be a channel-relative path starting with one slash")
	}
	if taskTemplate && !strings.Contains(raw, "{task_id}") {
		return fmt.Errorf("must contain {task_id}")
	}
	probe := raw
	if taskTemplate {
		probe = strings.ReplaceAll(probe, "{task_id}", "task-placeholder")
	}
	if strings.ContainsAny(probe, "{}") {
		return fmt.Errorf("only {task_id} is supported in task URL templates")
	}
	parsed, err := url.Parse(probe)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("must stay on the channel origin")
	}
	path := parsed.EscapedPath()
	for range 3 {
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return fmt.Errorf("contains invalid path escaping")
		}
		if strings.HasPrefix(decoded, "//") || strings.ContainsAny(decoded, "\\\r\n\x00") {
			return fmt.Errorf("contains an unsafe path")
		}
		for segment := range strings.SplitSeq(decoded, "/") {
			if segment == "." || segment == ".." {
				return fmt.Errorf("must not contain dot traversal")
			}
		}
		if decoded == path {
			return nil
		}
		path = decoded
	}
	return fmt.Errorf("contains excessive path escaping")
}
