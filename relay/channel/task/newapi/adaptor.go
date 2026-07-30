package newapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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
)

var modelList = []string{
	"hj-2.0-fast-480p",
	"hj-2.0-fast-720p",
	"hj-2.0-480p",
	"hj-2.0-720p",
	"hj-2.0-1080p",
}

var supportedModels = map[string]struct{}{
	"hj-2.0-fast-480p": {},
	"hj-2.0-fast-720p": {},
	"hj-2.0-480p":      {},
	"hj-2.0-720p":      {},
	"hj-2.0-1080p":     {},
}

var supportedRatios = map[string]struct{}{
	"16:9": {},
	"9:16": {},
	"1:1":  {},
	"4:3":  {},
	"3:4":  {},
	"21:9": {},
}

type generationRequest struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	Duration        *int     `json:"duration,omitempty"`
	Ratio           string   `json:"ratio,omitempty"`
	Images          []string `json:"images,omitempty"`
	StartFrame      []string `json:"start_frame,omitempty"`
	EndFrame        []string `json:"end_frame,omitempty"`
	ReferenceVideos []string `json:"reference_videos,omitempty"`
	ReferenceAudios []string `json:"reference_audios,omitempty"`
	GenerateAudio   *bool    `json:"generate_audio,omitempty"`
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

type upstreamUsageData struct {
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
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

	if request.Duration == nil {
		duration := 5
		request.Duration = &duration
	}
	if *request.Duration < 4 || *request.Duration > 15 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("duration must be between 4 and 15 seconds"), "invalid_duration", http.StatusBadRequest)
	}
	if request.Ratio == "" {
		request.Ratio = "16:9"
	}
	if _, ok := supportedRatios[request.Ratio]; !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("unsupported ratio %q", request.Ratio), "invalid_ratio", http.StatusBadRequest)
	}
	if request.GenerateAudio == nil {
		generateAudio := true
		request.GenerateAudio = &generateAudio
	}

	if len(request.Images) > 9 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("images supports at most 9 URLs"), "invalid_images", http.StatusBadRequest)
	}
	if len(request.StartFrame) > 1 || len(request.EndFrame) > 1 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("start_frame and end_frame support at most one URL each"), "invalid_frames", http.StatusBadRequest)
	}
	if len(request.ReferenceVideos) > 3 || len(request.ReferenceAudios) > 3 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("reference_videos and reference_audios support at most 3 URLs each"), "invalid_references", http.StatusBadRequest)
	}
	if len(request.Images) > 0 && (len(request.StartFrame) > 0 || len(request.EndFrame) > 0) {
		return service.TaskErrorWrapperLocal(fmt.Errorf("images cannot be combined with start_frame or end_frame"), "invalid_images", http.StatusBadRequest)
	}
	if len(request.ReferenceAudios) > 0 && len(request.Images) == 0 && len(request.StartFrame) == 0 && len(request.EndFrame) == 0 && len(request.ReferenceVideos) == 0 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("reference_audios requires at least one image or reference video"), "invalid_references", http.StatusBadRequest)
	}

	mediaFields := []struct {
		name string
		urls []string
	}{
		{name: "images", urls: request.Images},
		{name: "start_frame", urls: request.StartFrame},
		{name: "end_frame", urls: request.EndFrame},
		{name: "reference_videos", urls: request.ReferenceVideos},
		{name: "reference_audios", urls: request.ReferenceAudios},
	}
	for _, field := range mediaFields {
		if err := validatePublicMediaURLs(field.name, field.urls); err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_media_url", http.StatusBadRequest)
		}
	}

	info.Action = constant.TaskActionGenerate
	c.Set(requestContextKey, request)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    request.Model,
		Prompt:   request.Prompt,
		Images:   request.Images,
		Duration: *request.Duration,
	})
	return nil
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

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
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
	if strings.TrimSpace(info.UpstreamModelName) != "" {
		request.Model = info.UpstreamModelName
	}
	body, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
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

	var upstream submitResponse
	if err := common.Unmarshal(body, &upstream); err != nil {
		return "", nil, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	upstreamTaskID := strings.TrimSpace(upstream.TaskID)
	if upstreamTaskID == "" {
		upstreamTaskID = strings.TrimSpace(upstream.ID)
	}
	if upstreamTaskID == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusBadGateway)
	}

	createdAt := upstream.CreatedAt
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
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/video/generations/"+url.PathEscape(taskID), nil)
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
	var response queryResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, errors.Wrap(err, "unmarshal New API video task result failed")
	}
	result := &relaycommon.TaskInfo{Progress: response.Data.Progress}
	status := response.Data.Status
	if status == "" {
		status = response.NewAPIStatus
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "NOT_START", "SUBMITTED", "PENDING", "QUEUED":
		result.Status = model.TaskStatusQueued
	case "IN_PROGRESS", "PROCESSING", "RUNNING":
		result.Status = model.TaskStatusInProgress
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		result.Status = model.TaskStatusSuccess
		result.Url = response.Data.ResultURL
	case "FAILURE", "FAILED", "CANCELLED":
		result.Status = model.TaskStatusFailure
		result.Reason = response.Data.FailReason
		if result.Reason == "" {
			result.Reason = response.Message
		}
	default:
		return result, nil
	}

	if len(response.Data.Data) > 0 {
		var usage upstreamUsageData
		if err := common.Unmarshal(response.Data.Data, &usage); err == nil {
			result.TotalTokens = usage.Usage.TotalTokens
		}
	}
	return result, nil
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

func (a *TaskAdaptor) GetModelList() []string {
	return append([]string(nil), modelList...)
}

func (a *TaskAdaptor) GetChannelName() string {
	return "newapi-video"
}
