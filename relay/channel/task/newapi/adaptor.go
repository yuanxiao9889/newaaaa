package newapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	requestContextKey               = "newapi_video_generation_request"
	referenceVideoBillingMultiplier = 2.0
	miniMaxH3PromptMaxCharacters    = 2000
)

var modelList = []string{
	"hj-2.0-fast-480p",
	"hj-2.0-fast-720p",
	"hj-2.0-480p",
	"hj-2.0-720p",
	"hj-2.0-1080p",
	"MiniMax-H3",
	"MiniMax-H3-C",
	"MiniMax-H3-933-1440P-GF",
	"OK-video",
	"ok-video-1.5-preview",
	"ok-imagine-video",
}

var supportedModels = map[string]struct{}{
	"hj-2.0-fast-480p":        {},
	"hj-2.0-fast-720p":        {},
	"hj-2.0-480p":             {},
	"hj-2.0-720p":             {},
	"hj-2.0-1080p":            {},
	"MiniMax-H3":              {},
	"MiniMax-H3-C":            {},
	"MiniMax-H3-933-1440P-GF": {},
	"OK-video":                {},
	"ok-video-1.5-preview":    {},
	"ok-imagine-video":        {},
}

var supportedRatios = map[string]struct{}{
	"16:9": {},
	"9:16": {},
	"1:1":  {},
	"4:3":  {},
	"3:4":  {},
	"21:9": {},
}

var miniMaxH3Sizes = map[string]string{
	"3360x1440": "21:9",
	"2560x1440": "16:9",
	"1920x1440": "4:3",
	"1440x1440": "1:1",
	"1440x1920": "3:4",
	"1440x2560": "9:16",
}

var miniMaxH3SizesByRatio = map[string]string{
	"21:9": "3360x1440",
	"16:9": "2560x1440",
	"4:3":  "1920x1440",
	"1:1":  "1440x1440",
	"3:4":  "1440x1920",
	"9:16": "1440x2560",
}

type flexibleInt int

func (i *flexibleInt) UnmarshalJSON(data []byte) error {
	var value int
	if err := common.Unmarshal(data, &value); err != nil {
		var text string
		if stringErr := common.Unmarshal(data, &text); stringErr != nil {
			return fmt.Errorf("duration must be an integer")
		}
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(text))
		if parseErr != nil {
			return fmt.Errorf("duration must be an integer")
		}
		value = parsed
	}
	if value < 0 || value > relaycommon.MaxTaskDurationSeconds {
		return fmt.Errorf("duration must be between 0 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	*i = flexibleInt(value)
	return nil
}

type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	var values []string
	if err := common.Unmarshal(data, &values); err == nil {
		*s = values
		return nil
	}
	var value string
	if err := common.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("media reference must be a URL string or URL array")
	}
	if strings.TrimSpace(value) == "" {
		*s = nil
	} else {
		*s = []string{value}
	}
	return nil
}

type generationRequest struct {
	Model           string          `json:"model"`
	Prompt          string          `json:"prompt"`
	Duration        *flexibleInt    `json:"duration,omitempty"`
	Seconds         *flexibleInt    `json:"seconds,omitempty"`
	Ratio           string          `json:"ratio,omitempty"`
	AspectRatio     string          `json:"aspect_ratio,omitempty"`
	Size            string          `json:"size,omitempty"`
	Resolution      string          `json:"resolution,omitempty"`
	ResolutionName  string          `json:"resolution_name,omitempty"`
	InputMode       string          `json:"input_mode,omitempty"`
	Images          stringList      `json:"images,omitempty"`
	Image           string          `json:"image,omitempty"`
	ImageURL        string          `json:"image_url,omitempty"`
	InputReference  stringList      `json:"input_reference,omitempty"`
	ReferenceImages stringList      `json:"reference_images,omitempty"`
	StartFrame      stringList      `json:"start_frame,omitempty"`
	EndFrame        stringList      `json:"end_frame,omitempty"`
	ReferenceVideos stringList      `json:"reference_videos,omitempty"`
	ReferenceAudios stringList      `json:"reference_audios,omitempty"`
	AudioReference  stringList      `json:"audio_reference,omitempty"`
	GenerateAudio   *bool           `json:"generate_audio,omitempty"`
	Audio           *bool           `json:"audio,omitempty"`
	Stream          *bool           `json:"stream,omitempty"`
	Messages        json.RawMessage `json:"messages,omitempty"`
}

type submitResponse struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Object    string `json:"object"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
}

type queryResponse struct {
	Code         string        `json:"code"`
	Message      string        `json:"message,omitempty"`
	Data         queryTaskData `json:"data"`
	NewAPITaskID string        `json:"new_api_task_id,omitempty"`
	NewAPIStatus string        `json:"new_api_status,omitempty"`
}

type queryTaskData struct {
	TaskID     string          `json:"task_id"`
	Status     string          `json:"status"`
	Progress   string          `json:"progress,omitempty"`
	ResultURL  string          `json:"result_url,omitempty"`
	FailReason string          `json:"fail_reason,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	var request generationRequest
	if err := common.UnmarshalBodyReusable(c, &request); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}

	request.Model = strings.TrimSpace(request.Model)
	request.Prompt = strings.TrimSpace(request.Prompt)
	if _, ok := supportedModels[request.Model]; !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("unsupported model %q", request.Model), "invalid_model", http.StatusBadRequest)
	}
	if request.Prompt == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest)
	}
	if isMiniMaxH3Model(request.Model) && utf8.RuneCountInString(request.Prompt) > miniMaxH3PromptMaxCharacters {
		return service.TaskErrorWrapperLocal(fmt.Errorf("MiniMax H3 prompt exceeds %d characters", miniMaxH3PromptMaxCharacters), "invalid_prompt", http.StatusBadRequest)
	}

	if request.Duration != nil && request.Seconds != nil && *request.Duration != *request.Seconds {
		return service.TaskErrorWrapperLocal(fmt.Errorf("duration and seconds must match when both are provided"), "invalid_duration", http.StatusBadRequest)
	}
	duration := request.Duration
	if duration == nil {
		duration = request.Seconds
	}
	if duration == nil {
		defaultDuration := flexibleInt(defaultDurationForModel(request.Model))
		duration = &defaultDuration
	}
	request.Duration = duration
	request.Seconds = duration
	if err := validateModelDuration(request.Model, int(*duration)); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_duration", http.StatusBadRequest)
	}

	request.Ratio = strings.TrimSpace(request.Ratio)
	request.AspectRatio = strings.TrimSpace(request.AspectRatio)
	if request.Ratio == "" {
		request.Ratio = request.AspectRatio
	}
	if request.Ratio == "" && request.Size != "" {
		request.Ratio = ratioFromSize(request.Size)
	}
	if request.Ratio == "" {
		request.Ratio = "16:9"
	}
	if request.AspectRatio != "" && request.AspectRatio != request.Ratio {
		return service.TaskErrorWrapperLocal(fmt.Errorf("ratio and aspect_ratio must match"), "invalid_ratio", http.StatusBadRequest)
	}
	if _, ok := supportedRatios[request.Ratio]; !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("unsupported ratio %q", request.Ratio), "invalid_ratio", http.StatusBadRequest)
	}
	if request.Model == "ok-video-1.5-preview" && request.Ratio != "16:9" && request.Ratio != "9:16" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("ok-video-1.5-preview supports only 16:9 and 9:16"), "invalid_ratio", http.StatusBadRequest)
	}

	request.Resolution = strings.TrimSpace(request.Resolution)
	request.ResolutionName = strings.TrimSpace(request.ResolutionName)
	if request.Resolution == "" {
		request.Resolution = request.ResolutionName
	}
	if request.ResolutionName != "" && !strings.EqualFold(request.ResolutionName, request.Resolution) {
		return service.TaskErrorWrapperLocal(fmt.Errorf("resolution and resolution_name must match"), "invalid_resolution", http.StatusBadRequest)
	}
	if err := validateResolution(request.Model, request.Resolution); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_resolution", http.StatusBadRequest)
	}

	if request.GenerateAudio == nil {
		request.GenerateAudio = request.Audio
	}
	if request.GenerateAudio == nil {
		generateAudio := true
		request.GenerateAudio = &generateAudio
	}
	request.InputMode = normalizeInputMode(request)
	// MiniMax H3-C is the first New API task model that requires this channel capability.
	if request.Model == "MiniMax-H3-C" &&
		request.InputMode == "reference" &&
		!info.ChannelSetting.MultiImageReferenceEnabled {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("Multi-image references are disabled for this channel"),
			"multi_reference_disabled",
			http.StatusBadRequest,
		)
	}

	if err := validateReferences(request); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_references", http.StatusBadRequest)
	}
	media := request.allMediaURLs()
	if err := validatePublicMediaURLs("media", media); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_media_url", http.StatusBadRequest)
	}

	info.Action = constant.TaskActionGenerate
	c.Set(requestContextKey, request)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    request.Model,
		Prompt:   request.Prompt,
		Images:   request.referenceImageURLs(),
		Duration: int(*request.Duration),
		Size:     request.Size,
	})
	return nil
}

func defaultDurationForModel(modelName string) int {
	switch modelName {
	case "MiniMax-H3", "MiniMax-H3-933-1440P-GF":
		return 5
	case "MiniMax-H3-C", "ok-video-1.5-preview", "ok-imagine-video":
		return 10
	case "OK-video":
		return 6
	default:
		return 5
	}
}

func validateModelDuration(modelName string, duration int) error {
	switch modelName {
	case "MiniMax-H3", "MiniMax-H3-933-1440P-GF":
		if duration < 5 || duration > 15 {
			return fmt.Errorf("MiniMax H3 duration must be between 5 and 15 seconds")
		}
	case "MiniMax-H3-C":
		if duration != 10 && duration != 15 {
			return fmt.Errorf("MiniMax H3-C duration must be 10 or 15 seconds")
		}
	case "OK-video":
		if duration != 6 && duration != 10 {
			return fmt.Errorf("OK-video duration must be 6 or 10 seconds")
		}
	case "ok-video-1.5-preview":
		if duration != 10 && duration != 15 {
			return fmt.Errorf("ok-video-1.5-preview duration must be 10 or 15 seconds")
		}
	case "ok-imagine-video":
		if duration != 10 && duration != 16 && duration != 20 {
			return fmt.Errorf("ok-imagine-video duration must be 10, 16, or 20 seconds")
		}
	default:
		if duration < 4 || duration > 15 {
			return fmt.Errorf("duration must be between 4 and 15 seconds")
		}
	}
	return nil
}

func validateResolution(modelName, resolution string) error {
	if resolution == "" {
		return nil
	}
	switch modelName {
	case "MiniMax-H3", "MiniMax-H3-933-1440P-GF":
		if resolution != "1440P" {
			return fmt.Errorf("MiniMax H3 resolution must be 1440P")
		}
	case "MiniMax-H3-C":
		if !strings.EqualFold(resolution, "2k") {
			return fmt.Errorf("MiniMax H3-C resolution must be 2k")
		}
	case "ok-video-1.5-preview":
		if !strings.EqualFold(resolution, "720p") {
			return fmt.Errorf("ok-video-1.5-preview resolution must be 720p")
		}
	case "OK-video", "ok-imagine-video":
		switch strings.ToLower(resolution) {
		case "480p", "720p", "1080p":
		default:
			return fmt.Errorf("resolution must be 480p, 720p, or 1080p")
		}
	}
	return nil
}

func validateReferences(request generationRequest) error {
	images := request.referenceImageURLs()
	messageImages := extractMessageImageURLs(request.Messages)
	imageCount := len(uniqueNonEmpty(append(append([]string{}, images...), messageImages...)))
	videoCount := len(request.ReferenceVideos)
	audioCount := len(request.ReferenceAudios) + len(request.AudioReference)

	if len(request.StartFrame) > 1 || len(request.EndFrame) > 1 {
		return fmt.Errorf("start_frame and end_frame support at most one URL each")
	}
	if videoCount > 3 || audioCount > 3 {
		return fmt.Errorf("reference_videos and reference audio support at most 3 URLs each")
	}
	if isMiniMaxH3Model(request.Model) || request.Model == "MiniMax-H3-C" {
		if imageCount+len(request.StartFrame)+len(request.EndFrame) > 5 {
			return fmt.Errorf("MiniMax H3 supports at most 5 images")
		}
		if videoCount > 0 {
			return fmt.Errorf("MiniMax H3 reference videos are not supported")
		}
	}
	if request.Model == "MiniMax-H3-C" {
		if audioCount > 0 || len(request.EndFrame) > 0 {
			return fmt.Errorf("MiniMax H3-C does not support reference audio or end frames")
		}
		switch request.InputMode {
		case "firstFrame":
			if imageCount+len(request.StartFrame) != 1 {
				return fmt.Errorf("MiniMax H3-C firstFrame mode requires exactly 1 image")
			}
		case "reference":
			if imageCount < 2 || imageCount > 5 || len(request.StartFrame) > 0 {
				return fmt.Errorf("MiniMax H3-C reference mode requires 2 to 5 images")
			}
		default:
			return fmt.Errorf("MiniMax H3-C requires firstFrame or reference mode")
		}
	}
	if request.Model == "ok-video-1.5-preview" {
		if imageCount+len(request.StartFrame) == 0 {
			return fmt.Errorf("ok-video-1.5-preview requires a reference image")
		}
		if videoCount > 0 || audioCount > 0 {
			return fmt.Errorf("ok-video-1.5-preview supports image references only")
		}
	}
	if request.Model == "ok-imagine-video" {
		if videoCount > 0 || audioCount > 0 || len(request.EndFrame) > 0 {
			return fmt.Errorf("ok-imagine-video supports image references only")
		}
		switch request.InputMode {
		case "textToVideo":
			if imageCount+len(request.StartFrame) > 0 {
				return fmt.Errorf("textToVideo mode does not accept reference images")
			}
		case "firstFrame":
			if imageCount+len(request.StartFrame) != 1 {
				return fmt.Errorf("firstFrame mode requires exactly 1 image")
			}
		case "reference":
			if imageCount == 0 {
				return fmt.Errorf("reference mode requires at least 1 image")
			}
		}
	}
	if !isOopiiVideosModel(request.Model) {
		if len(request.Images) > 9 {
			return fmt.Errorf("images supports at most 9 URLs")
		}
		if len(request.Images) > 0 && (len(request.StartFrame) > 0 || len(request.EndFrame) > 0) {
			return fmt.Errorf("images cannot be combined with start_frame or end_frame")
		}
		if audioCount > 0 && imageCount == 0 && len(request.StartFrame) == 0 && len(request.EndFrame) == 0 && videoCount == 0 {
			return fmt.Errorf("reference audio requires at least one image or reference video")
		}
	}
	return nil
}

func normalizeInputMode(request generationRequest) string {
	mode := strings.TrimSpace(request.InputMode)
	switch mode {
	case "textToVideo", "firstFrame", "firstLastFrame", "reference":
		return mode
	}
	if len(request.StartFrame) > 0 {
		return "firstFrame"
	}
	if len(request.referenceImageURLs()) > 0 || len(extractMessageImageURLs(request.Messages)) > 0 {
		if request.Model == "ok-imagine-video" && len(request.referenceImageURLs()) == 1 {
			return "firstFrame"
		}
		return "reference"
	}
	return "textToVideo"
}

func (request generationRequest) referenceImageURLs() []string {
	values := make([]string, 0, len(request.Images)+len(request.ReferenceImages)+len(request.InputReference)+2)
	values = append(values, request.Images...)
	values = append(values, request.ReferenceImages...)
	values = append(values, request.InputReference...)
	if request.Image != "" {
		values = append(values, request.Image)
	}
	if request.ImageURL != "" {
		values = append(values, request.ImageURL)
	}
	return uniqueNonEmpty(values)
}

func (request generationRequest) allMediaURLs() []string {
	values := request.referenceImageURLs()
	values = append(values, request.StartFrame...)
	values = append(values, request.EndFrame...)
	values = append(values, request.ReferenceVideos...)
	values = append(values, request.ReferenceAudios...)
	values = append(values, request.AudioReference...)
	values = append(values, extractMessageImageURLs(request.Messages)...)
	return uniqueNonEmpty(values)
}

func extractMessageImageURLs(messages json.RawMessage) []string {
	if len(messages) == 0 {
		return nil
	}
	var value any
	if err := common.Unmarshal(messages, &value); err != nil {
		return nil
	}
	var urls []string
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if imageURL, ok := typed["image_url"]; ok {
				switch image := imageURL.(type) {
				case string:
					urls = append(urls, image)
				case map[string]any:
					if value, ok := image["url"].(string); ok {
						urls = append(urls, value)
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return uniqueNonEmpty(urls)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func validatePublicMediaURLs(field string, values []string) error {
	for _, value := range values {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%s contains an invalid public HTTP/HTTPS URL", field)
		}
		if ip := net.ParseIP(parsed.Hostname()); ip != nil && common.IsPrivateIP(ip) {
			return fmt.Errorf("%s contains a private network URL", field)
		}
	}
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	value, ok := c.Get(requestContextKey)
	if !ok {
		return nil
	}
	request, ok := value.(generationRequest)
	if !ok || request.Duration == nil {
		return nil
	}
	ratios := map[string]float64{"seconds": float64(*request.Duration)}
	if len(request.ReferenceVideos) > 0 {
		ratios["reference_video"] = referenceVideoBillingMultiplier
	}
	return ratios
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	modelName := requestModelName(info)
	if isOopiiVideosModel(modelName) {
		return a.baseURL + "/v1/videos", nil
	}
	return a.baseURL + "/v1/video/generations", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, request *http.Request, _ *relaycommon.RelayInfo) error {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	value, ok := c.Get(requestContextKey)
	if !ok {
		return nil, fmt.Errorf("validated video request not found")
	}
	request, ok := value.(generationRequest)
	if !ok {
		return nil, fmt.Errorf("invalid validated video request")
	}
	upstreamModel := strings.TrimSpace(info.UpstreamModelName)
	if upstreamModel == "" {
		upstreamModel = request.Model
	}

	var payload map[string]any
	switch {
	case isMiniMaxH3Model(request.Model):
		payload = buildMiniMaxH3Payload(request)
	case request.Model == "MiniMax-H3-C":
		payload = buildMiniMaxH3CPayload(request)
	case request.Model == "ok-video-1.5-preview":
		payload = buildPreviewPayload(request, upstreamModel)
	case request.Model == "ok-imagine-video":
		payload = buildImaginePayload(request, upstreamModel)
	case request.Model == "OK-video":
		payload = buildLegacyOopiiVideoPayload(request, upstreamModel)
	default:
		payload = buildColomiPayload(request, upstreamModel)
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

func buildColomiPayload(request generationRequest, modelName string) map[string]any {
	payload := map[string]any{
		"model":            modelName,
		"prompt":           request.Prompt,
		"duration":         int(*request.Duration),
		"ratio":            request.Ratio,
		"images":           []string(request.Images),
		"start_frame":      []string(request.StartFrame),
		"end_frame":        []string(request.EndFrame),
		"reference_videos": []string(request.ReferenceVideos),
		"reference_audios": []string(request.ReferenceAudios),
		"generate_audio":   *request.GenerateAudio,
	}
	return payload
}

func buildMiniMaxH3Payload(request generationRequest) map[string]any {
	size := strings.TrimSpace(request.Size)
	if _, ok := miniMaxH3Sizes[size]; !ok {
		size = miniMaxH3SizesByRatio[request.Ratio]
	}
	if size == "" {
		size = "2560x1440"
	}
	payload := map[string]any{
		"model":      "MiniMax-H3",
		"prompt":     request.Prompt,
		"seconds":    int(*request.Duration),
		"size":       size,
		"resolution": "1440P",
		"audio":      *request.GenerateAudio,
	}
	if ratio := miniMaxH3Sizes[size]; ratio == "16:9" || ratio == "9:16" {
		payload["aspect_ratio"] = ratio
	}
	if images := request.referenceImageURLs(); len(images) > 0 {
		payload["reference_images"] = images
	}
	if len(request.StartFrame) > 0 {
		payload["start_frame"] = request.StartFrame[0]
	}
	if len(request.EndFrame) > 0 {
		payload["end_frame"] = request.EndFrame[0]
	}
	if audio := uniqueNonEmpty(append([]string(request.ReferenceAudios), request.AudioReference...)); len(audio) > 0 {
		payload["audio_reference"] = audio
	}
	return payload
}

func buildMiniMaxH3CPayload(request generationRequest) map[string]any {
	var inputReference any
	if request.InputMode == "firstFrame" {
		images := append([]string{}, request.StartFrame...)
		images = append(images, request.referenceImageURLs()...)
		inputReference = images[0]
	} else {
		inputReference = request.referenceImageURLs()
	}
	return map[string]any{
		"model":           "MiniMax H3",
		"prompt":          request.Prompt,
		"duration":        int(*request.Duration),
		"aspect_ratio":    request.Ratio,
		"resolution":      "2k",
		"input_reference": inputReference,
	}
}

func buildPreviewPayload(request generationRequest, modelName string) map[string]any {
	image := firstReferenceImage(request)
	return map[string]any{
		"model":           modelName,
		"prompt":          request.Prompt,
		"seconds":         strconv.Itoa(int(*request.Duration)),
		"size":            sizeForOopiiRatio(request.Ratio),
		"resolution":      "720p",
		"input_reference": image,
		"image_url":       image,
	}
}

func buildImaginePayload(request generationRequest, modelName string) map[string]any {
	resolution := request.Resolution
	if resolution == "" {
		resolution = "720p"
	}
	payload := map[string]any{
		"model":           modelName,
		"prompt":          request.Prompt,
		"stream":          false,
		"duration":        strconv.Itoa(int(*request.Duration)),
		"seconds":         strconv.Itoa(int(*request.Duration)),
		"aspect_ratio":    request.Ratio,
		"ratio":           request.Ratio,
		"size":            sizeForOopiiRatio(request.Ratio),
		"resolution_name": resolution,
		"resolution":      resolution,
	}
	if request.Stream != nil {
		payload["stream"] = *request.Stream
	}
	if len(request.Messages) > 0 {
		payload["messages"] = request.Messages
		if request.InputMode == "firstFrame" {
			payload["image_url"] = firstReferenceImage(request)
		}
		return payload
	}
	if request.InputMode == "textToVideo" {
		return payload
	}
	images := request.referenceImageURLs()
	if len(request.StartFrame) > 0 {
		images = append([]string{request.StartFrame[0]}, images...)
	}
	content := []any{map[string]any{"type": "text", "text": request.Prompt}}
	if request.InputMode == "firstFrame" {
		image := images[0]
		payload["image_url"] = image
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image}})
	} else {
		for _, image := range images {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image}})
		}
	}
	payload["messages"] = []any{map[string]any{"role": "user", "content": content}}
	return payload
}

func buildLegacyOopiiVideoPayload(request generationRequest, modelName string) map[string]any {
	resolution := request.Resolution
	if resolution == "" {
		resolution = "720p"
	}
	size := strings.TrimSpace(request.Size)
	if size == "" {
		size = sizeForOopiiRatio(request.Ratio)
	}
	payload := map[string]any{
		"model":      modelName,
		"prompt":     request.Prompt,
		"seconds":    strconv.Itoa(int(*request.Duration)),
		"size":       size,
		"resolution": resolution,
	}
	images := request.referenceImageURLs()
	if len(request.StartFrame) > 0 {
		payload["image"] = request.StartFrame[0]
	} else if len(images) > 0 {
		payload["image"] = images[0]
	}
	if len(images) > 0 {
		payload["reference_images"] = images
	}
	return payload
}

func firstReferenceImage(request generationRequest) string {
	if len(request.InputReference) > 0 {
		return request.InputReference[0]
	}
	if request.ImageURL != "" {
		return request.ImageURL
	}
	if request.Image != "" {
		return request.Image
	}
	if len(request.StartFrame) > 0 {
		return request.StartFrame[0]
	}
	images := request.referenceImageURLs()
	if len(images) > 0 {
		return images[0]
	}
	return ""
}

func sizeForOopiiRatio(ratio string) string {
	switch ratio {
	case "9:16":
		return "720x1280"
	case "1:1":
		return "1024x1024"
	default:
		return "1280x720"
	}
}

func ratioFromSize(size string) string {
	switch strings.TrimSpace(size) {
	case "720x1280", "1024x1792", "1440x2560":
		return "9:16"
	case "1024x1024", "1440x1440":
		return "1:1"
	case "3360x1440":
		return "21:9"
	case "1920x1440":
		return "4:3"
	case "1440x1920":
		return "3:4"
	default:
		return "16:9"
	}
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, response *http.Response, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = response.Body.Close()

	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", nil, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	upstreamTaskID := extractTaskID(payload)
	if upstreamTaskID == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusBadGateway)
	}
	createdAt := extractInt64(payload, []string{"created_at"}, []string{"task", "created_at"}, []string{"data", "created_at"})
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	c.JSON(http.StatusOK, submitResponse{
		ID:        info.PublicTaskID,
		TaskID:    info.PublicTaskID,
		Object:    "video",
		Model:     info.OriginModelName,
		Status:    "queued",
		CreatedAt: createdAt,
	})
	return upstreamTaskID, body, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	modelName, _ := body["model"].(string)
	path := "/v1/video/generations/"
	if isOopiiVideosModel(modelName) {
		path = "/v1/videos/"
	}
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+path+url.PathEscape(taskID), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(request)
}

func (a *TaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrap(err, "unmarshal New API video task result failed")
	}
	status := strings.ToUpper(strings.TrimSpace(extractStatus(payload)))
	result := &relaycommon.TaskInfo{
		TaskID:      extractTaskID(payload),
		Progress:    extractProgress(payload),
		Url:         extractVideoURL(payload),
		Reason:      extractFailureReason(payload),
		TotalTokens: extractTotalTokens(payload),
	}
	switch status {
	case "NOT_START", "CREATED", "SUBMITTED", "PENDING", "QUEUED":
		result.Status = string(model.TaskStatusQueued)
	case "IN_PROGRESS", "PROCESSING", "RUNNING":
		result.Status = string(model.TaskStatusInProgress)
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		result.Status = string(model.TaskStatusSuccess)
	case "FAILURE", "FAILED", "CANCELLED", "CANCELED":
		result.Status = string(model.TaskStatusFailure)
	default:
		if result.Url != "" {
			result.Status = string(model.TaskStatusSuccess)
		}
	}
	if result.Status == string(model.TaskStatusFailure) && result.Reason == "" {
		result.Reason = "task failed"
	}
	return result, nil
}

func candidateMaps(payload map[string]any) []map[string]any {
	candidates := []map[string]any{payload}
	for _, path := range [][]string{{"task"}, {"data"}, {"data", "data"}, {"data", "task"}} {
		if value, ok := valueAtPath(payload, path...).(map[string]any); ok {
			candidates = append(candidates, value)
		}
	}
	return candidates
}

func valueAtPath(payload map[string]any, path ...string) any {
	var current any = payload
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func extractTaskID(payload map[string]any) string {
	for _, object := range candidateMaps(payload) {
		for _, key := range []string{"task_id", "taskId", "request_id", "id"} {
			if value := stringValue(object[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func extractStatus(payload map[string]any) string {
	for _, object := range candidateMaps(payload) {
		for _, key := range []string{"status", "state", "new_api_status"} {
			if value := stringValue(object[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func extractProgress(payload map[string]any) string {
	for _, object := range candidateMaps(payload) {
		if value, ok := object["progress"]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case float64:
				return fmt.Sprintf("%g%%", typed)
			case json.Number:
				return typed.String() + "%"
			}
		}
	}
	return ""
}

func extractVideoURL(payload map[string]any) string {
	for _, object := range candidateMaps(payload) {
		for _, key := range []string{"result_url", "output_url", "video_url", "url"} {
			if value := stringValue(object[key]); value != "" {
				return value
			}
		}
		if content, ok := object["content"].(map[string]any); ok {
			if value := stringValue(content["url"]); value != "" {
				return value
			}
		}
		if value := extractURLFromOutput(object["output"]); value != "" {
			return value
		}
	}
	return ""
}

func extractURLFromOutput(output any) string {
	switch typed := output.(type) {
	case map[string]any:
		for _, key := range []string{"video_url", "url", "output_url", "result_url"} {
			if value := stringValue(typed[key]); value != "" {
				return value
			}
		}
	case []any:
		for _, item := range typed {
			if value := extractURLFromOutput(item); value != "" {
				return value
			}
		}
	}
	return ""
}

func extractFailureReason(payload map[string]any) string {
	for _, object := range candidateMaps(payload) {
		for _, key := range []string{"fail_reason", "error_message", "reason", "message"} {
			if value := stringValue(object[key]); value != "" {
				return value
			}
		}
		if errorObject, ok := object["error"].(map[string]any); ok {
			if value := stringValue(errorObject["message"]); value != "" {
				return value
			}
		}
		if value := stringValue(object["error"]); value != "" {
			return value
		}
	}
	return ""
}

func extractTotalTokens(payload map[string]any) int {
	for _, object := range candidateMaps(payload) {
		if usage, ok := object["usage"].(map[string]any); ok {
			if value := intValue(usage["total_tokens"]); value > 0 {
				return value
			}
		}
		if value := intValue(object["total_tokens"]); value > 0 {
			return value
		}
	}
	return 0
}

func extractInt64(payload map[string]any, paths ...[]string) int64 {
	for _, path := range paths {
		value := valueAtPath(payload, path...)
		switch typed := value.(type) {
		case float64:
			return int64(typed)
		case json.Number:
			parsed, _ := typed.Int64()
			return parsed
		case string:
			parsed, _ := strconv.ParseInt(typed, 10, 64)
			return parsed
		}
	}
	return 0
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func (a *TaskAdaptor) ConvertToVideoGeneration(task *model.Task) ([]byte, error) {
	var upstream queryResponse
	_ = common.Unmarshal(task.Data, &upstream)
	response := queryResponse{
		Code: "success",
		Data: queryTaskData{
			TaskID:     task.TaskID,
			Status:     string(task.Status),
			Progress:   task.Progress,
			ResultURL:  task.GetResultURL(),
			FailReason: task.FailReason,
			Data:       upstream.Data.Data,
		},
		NewAPITaskID: task.TaskID,
		NewAPIStatus: string(task.Status),
	}
	return common.Marshal(response)
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	response := task.ToOpenAIVideo()
	response.ID = task.TaskID
	response.TaskID = task.TaskID
	if response.Model == "" {
		response.Model = task.Properties.OriginModelName
	}
	if task.Status == model.TaskStatusFailure {
		response.Error = &taskdto.OpenAIVideoError{
			Message: task.FailReason,
			Code:    "video_generation_failed",
		}
	}
	return common.Marshal(response)
}

func requestModelName(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if modelName := strings.TrimSpace(info.OriginModelName); modelName != "" {
		return modelName
	}
	return strings.TrimSpace(info.UpstreamModelName)
}

func isMiniMaxH3Model(modelName string) bool {
	return modelName == "MiniMax-H3" || modelName == "MiniMax-H3-933-1440P-GF"
}

func isOopiiVideosModel(modelName string) bool {
	return isMiniMaxH3Model(modelName) || modelName == "MiniMax-H3-C" || modelName == "OK-video" || modelName == "ok-video-1.5-preview" || modelName == "ok-imagine-video"
}

func (a *TaskAdaptor) GetModelList() []string {
	return append([]string(nil), modelList...)
}

func (a *TaskAdaptor) GetChannelName() string {
	return "newapi-video"
}
