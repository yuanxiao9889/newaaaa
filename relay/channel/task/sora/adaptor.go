package sora

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/tidwall/sjson"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // for text type
	ImageURL *ImageURL `json:"image_url,omitempty"` // for image_url type
}

type ImageURL struct {
	URL string `json:"url"`
}

type responseTask struct {
	ID                 string                `json:"id"`
	TaskID             string                `json:"task_id,omitempty"` //兼容旧接口
	Created            int64                 `json:"created,omitempty"`
	Object             string                `json:"object"`
	Model              string                `json:"model"`
	Status             string                `json:"status"`
	Progress           int                   `json:"progress"`
	CreatedAt          int64                 `json:"created_at"`
	CompletedAt        int64                 `json:"completed_at,omitempty"`
	ExpiresAt          int64                 `json:"expires_at,omitempty"`
	Seconds            string                `json:"seconds,omitempty"`
	Size               string                `json:"size,omitempty"`
	RemixedFromVideoID string                `json:"remixed_from_video_id,omitempty"`
	Error              *dto.OpenAIVideoError `json:"error,omitempty"`
	URL                string                `json:"url,omitempty"`
	VideoURL           string                `json:"video_url,omitempty"`
	FinalURL           string                `json:"final_url,omitempty"`
	DownloadURL        string                `json:"download_url,omitempty"`
	DetailURL          string                `json:"detail_url,omitempty"`
	Result             *responseTaskResult   `json:"result,omitempty"`
	Data               *responseTaskData     `json:"data,omitempty"`
}

type responseTaskResult struct {
	URL         string               `json:"url,omitempty"`
	VideoURL    string               `json:"video_url,omitempty"`
	VideoURLs   []string             `json:"video_urls,omitempty"`
	FinalURL    string               `json:"final_url,omitempty"`
	DownloadURL string               `json:"download_url,omitempty"`
	ResultURL   string               `json:"result_url,omitempty"`
	Data        []responseTaskResult `json:"data,omitempty"`
}

type responseTaskData struct {
	ID        string   `json:"id,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Status    string   `json:"status,omitempty"`
	Model     string   `json:"model,omitempty"`
	VideoURL  string   `json:"video_url,omitempty"`
	VideoURLs []string `json:"video_urls,omitempty"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func validateRemixRequest(c *gin.Context) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("field prompt is required"), "invalid_request", http.StatusBadRequest)
	}
	// 存储原始请求到 context，与 ValidateMultipartDirect 路径保持一致
	c.Set("task_request", req)
	return nil
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if info.Action == constant.TaskActionRemix {
		return validateRemixRequest(c)
	}
	return relaycommon.ValidateMultipartDirect(c, info)
}

// EstimateBilling 根据用户请求的 seconds 和 size 计算 OtherRatios。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	// remix 路径的 OtherRatios 已在 ResolveOriginTask 中设置
	if info.Action == constant.TaskActionRemix {
		return nil
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	seconds, _ := strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if seconds <= 0 {
		seconds = 4
	}

	size := req.Size
	if size == "" {
		size = "720x1280"
	}

	ratios := map[string]float64{
		"seconds": float64(seconds),
		"size":    1,
	}
	if size == "1792x1024" || size == "1024x1792" {
		ratios["size"] = 1.666667
	}
	return ratios
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == constant.TaskActionRemix {
		return fmt.Sprintf("%s/v1/videos/%s/remix", a.baseURL, info.OriginTaskID), nil
	}
	if isGrokVideoGenerationsChannel(info) {
		return fmt.Sprintf("%s/v1/videos/generations", a.baseURL), nil
	}
	return fmt.Sprintf("%s/v1/videos", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_request_body_failed")
	}
	cachedBody, err := storage.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "read_body_bytes_failed")
	}
	contentType := c.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "application/json") {
		if isGrokVideoGenerationsChannel(info) {
			convertedBody, err := buildGrokVideoGenerationsRequest(
				cachedBody,
				info.UpstreamModelName,
				info.OriginModelName,
			)
			if err != nil {
				return nil, err
			}
			return bytes.NewReader(convertedBody), nil
		}

		var bodyMap map[string]interface{}
		if err := common.Unmarshal(cachedBody, &bodyMap); err == nil {
			bodyMap["model"] = info.UpstreamModelName
			if newBody, err := common.Marshal(bodyMap); err == nil {
				return bytes.NewReader(newBody), nil
			}
		}
		return bytes.NewReader(cachedBody), nil
	}

	if strings.Contains(contentType, "multipart/form-data") {
		formData, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return bytes.NewReader(cachedBody), nil
		}
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writer.WriteField("model", info.UpstreamModelName)
		for key, values := range formData.Value {
			if key == "model" {
				continue
			}
			for _, v := range values {
				writer.WriteField(key, v)
			}
		}
		for fieldName, fileHeaders := range formData.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				ct := fh.Header.Get("Content-Type")
				if ct == "" || ct == "application/octet-stream" {
					buf512 := make([]byte, 512)
					n, _ := io.ReadFull(f, buf512)
					ct = http.DetectContentType(buf512[:n])
					// Re-open after sniffing so the full content is copied below
					f.Close()
					f, err = fh.Open()
					if err != nil {
						continue
					}
				}
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fh.Filename))
				h.Set("Content-Type", ct)
				part, err := writer.CreatePart(h)
				if err != nil {
					f.Close()
					continue
				}
				io.Copy(part, f)
				f.Close()
			}
		}
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &buf, nil
	}

	return common.NewReplayableBodyReader(storage), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) ParseResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (result *channel.TaskSubmitResponse, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Sora response
	var dResp responseTask
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamID := firstNonEmpty(dResp.ID, dResp.TaskID)
	if upstreamID == "" && dResp.Data != nil {
		upstreamID = firstNonEmpty(dResp.Data.ID, dResp.Data.TaskID)
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 使用公开 task_xxxx ID 返回给客户端
	if dResp.Status == "" && dResp.Data != nil {
		dResp.Status = dResp.Data.Status
	}
	if dResp.Model == "" && dResp.Data != nil {
		dResp.Model = dResp.Data.Model
	}
	if dResp.Error != nil && dResp.Error.Message == "" && dResp.Error.Code == "" {
		dResp.Error = nil
	}
	dResp.ID = info.PublicTaskID
	dResp.TaskID = info.PublicTaskID
	if dResp.Data != nil {
		if dResp.Data.ID != "" {
			dResp.Data.ID = info.PublicTaskID
		}
		if dResp.Data.TaskID != "" {
			dResp.Data.TaskID = info.PublicTaskID
		}
	}
	return &channel.TaskSubmitResponse{UpstreamTaskID: upstreamID, TaskData: responseBody, ClientResponse: dResp}, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, task *model.Task, proxy string) (*http.Response, error) {
	taskID := task.GetUpstreamTaskID()
	if taskID == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r responseTaskResult) videoURL() string {
	if url := firstNonEmpty(r.VideoURL, r.URL, r.FinalURL, r.DownloadURL, r.ResultURL); url != "" {
		return url
	}
	for _, url := range r.VideoURLs {
		if strings.TrimSpace(url) != "" {
			return url
		}
	}
	for _, item := range r.Data {
		if url := item.videoURL(); url != "" {
			return url
		}
	}
	return ""
}

func (r responseTask) videoURL() string {
	if url := firstNonEmpty(r.VideoURL, r.URL, r.FinalURL, r.DownloadURL, r.DetailURL); url != "" {
		return url
	}
	if r.Data != nil {
		if url := firstNonEmpty(r.Data.VideoURL); url != "" {
			return url
		}
		for _, url := range r.Data.VideoURLs {
			if strings.TrimSpace(url) != "" {
				return url
			}
		}
	}
	if r.Result != nil {
		return r.Result.videoURL()
	}
	return ""
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) ParseTaskResult(_ *model.Task, _ *http.Response, respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}
	if resTask.Error != nil && resTask.Error.Message == "" && resTask.Error.Code == "" {
		resTask.Error = nil
	}

	switch resTask.Status {
	case "queued", "pending":
		taskResult.Status = model.TaskStatusQueued
	case "processing", "in_progress", "running":
		taskResult.Status = model.TaskStatusInProgress
	case "completed", "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Url = resTask.videoURL()
		// Url intentionally left empty — the caller constructs the proxy URL using the public task ID
	case "failed", "cancelled":
		taskResult.Status = model.TaskStatusFailure
		if resTask.Error != nil {
			taskResult.Reason = resTask.Error.Message
		} else {
			taskResult.Reason = "task failed"
		}
	default:
	}
	if resTask.Progress > 0 && resTask.Progress < 100 {
		taskResult.Progress = fmt.Sprintf("%d%%", resTask.Progress)
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	data := task.Data
	var err error
	if data, err = sjson.SetBytes(data, "id", task.TaskID); err != nil {
		return nil, errors.Wrap(err, "set id failed")
	}
	if !isGrokVideoGenerationsPlatform(task.Platform) {
		return data, nil
	}

	if data, err = sjson.SetBytes(data, "task_id", task.TaskID); err != nil {
		return nil, errors.Wrap(err, "set task_id failed")
	}
	if data, err = sjson.SetBytes(data, "status", task.Status.ToVideoStatus()); err != nil {
		return nil, errors.Wrap(err, "set status failed")
	}
	if task.Status == model.TaskStatusSuccess {
		if resultURL := strings.TrimSpace(task.GetResultURL()); resultURL != "" {
			if data, err = sjson.SetBytes(data, "video_url", resultURL); err != nil {
				return nil, errors.Wrap(err, "set video_url failed")
			}
		}
		if data, err = sjson.SetBytes(data, "url", taskcommon.BuildProxyURL(task.TaskID)); err != nil {
			return nil, errors.Wrap(err, "set proxy url failed")
		}
	}
	return data, nil
}
