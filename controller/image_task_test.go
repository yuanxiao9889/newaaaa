package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupImageTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	model.DB = db

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		model.DB = originalDB
	})

	return db
}

func TestBuildImageTaskResponseIncludesSignedURLOnlyForSucceededTasks(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	imagePath := filepath.Join(t.TempDir(), "result.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png-bytes"), 0600))
	now := time.Now().Unix()
	successTask := &model.Task{
		TaskID: "task_success",
		UserId: 1,
		Action: constant.TaskActionImageGenerate,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: service.AsyncImageAssetType,
			LocalPath: imagePath,
			StoredAt:  now - 10,
			ExpiresAt: now + 7200,
		},
	}

	successResp := buildImageTaskResponse(successTask)
	require.Equal(t, dto.ImageAsyncStatusSucceeded, successResp.Status)
	require.NotEmpty(t, successResp.URL)
	require.NotZero(t, successResp.URLExpiresAt)
	require.Contains(t, successResp.URL, "/v1/images/tasks/task_success/signed-content?token=")

	queuedTask := *successTask
	queuedTask.TaskID = "task_queued"
	queuedTask.Status = model.TaskStatusQueued
	queuedResp := buildImageTaskResponse(&queuedTask)
	require.Equal(t, dto.ImageAsyncStatusQueued, queuedResp.Status)
	require.Empty(t, queuedResp.URL)
	require.Zero(t, queuedResp.URLExpiresAt)
	failedTask := *successTask
	failedTask.Status = model.TaskStatusFailure
	failedTask.FailReason = "provider-private model-private channel=141"
	failedResp := buildImageTaskResponse(&failedTask)
	require.Equal(t, "generation_failed", failedResp.ErrorCode)
	require.Contains(t, failedResp.Error, "[generation_failed]")
	require.NotContains(t, failedResp.Error, "provider-private")
	require.Empty(t, failedResp.URL)

	failedTask.PrivateData.ImageRetry = &model.AsyncImageRetryState{Details: []model.AsyncImageRetryDetail{{
		ChannelID: 141, Error: `{"error":{"code":"InvalidImage","message":"Task failed"}}`,
	}}}
	failedTask.FailReason = "Task failed\nAsync Channel Retry Path: 141"
	require.Equal(t, "invalid_image_input", buildImageTaskResponse(&failedTask).ErrorCode, "host retry annotations must not prevent matching structured diagnostics")
	failedTask.FailReason = "context deadline exceeded"
	require.Equal(t, "request_timeout", buildImageTaskResponse(&failedTask).ErrorCode, "the final failure takes precedence over an earlier attempt")
	failedTask.FailReason = "Task failed"
	failedTask.ChannelId = 222
	require.Equal(t, "generation_failed", buildImageTaskResponse(&failedTask).ErrorCode, "an earlier channel cannot explain the current task failure")
	failedTask.PrivateData.ImageRetry = nil
	failedTask.FailReason = "The image request is invalid. Check the input and try again."
	require.Equal(t, "invalid_image_input", buildImageTaskResponse(&failedTask).ErrorCode, "historical tasks may only persist the failure text")
}

func TestFailedImageTaskReturnsSafeReasonAndCode(t *testing.T) {
	db := setupImageTaskTestDB(t)
	for _, tc := range []struct {
		name, reason, rawCode, wantCode, wantMessage string
		status                                       int
	}{
		{"invalid_image", "The image request is invalid. Check the input and try again.", "invalid_image_input", "invalid_image_input", "输入图片无效", 400},
		{"moderation", "图片审核失败，请重试", "generation_failed", "content_policy_violation", "安全审核", 502},
		{"unsafe_prompt", "The provided prompt is considered unsafe and it cannot be used to generate content.", "unknown_error", "content_policy_violation", "安全审核提示", 502},
		{"unsafe_output", `poll failed: 451 {"error_code":"image_unsafe","message":"The generated images appear to be unsafe."}`, "unknown_error", "content_policy_violation", "安全审核提示", 400},
		{"uncertain_safety", "safety check failed", "unknown_error", "content_safety_unknown", "尚不能确认内容被拦截", 502},
		{"structured_safety", "safety check failed", "image_unsafe", "content_policy_violation", "安全审核提示", 502},
		{"review_passed", "safety check passed; Task failed", "unknown_error", "generation_failed", "未提供可公开确认的具体原因", 502},
		{"authentication", "provider-private authentication failed", "invalid_api_key", "service_auth_error", "无需更换您的令牌", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{
				TaskID: "public_error_" + tc.name, UserId: 901, Status: model.TaskStatusFailure,
				Action: constant.TaskActionImageGenerate, FailReason: tc.reason,
				PrivateData: model.TaskPrivateData{
					InternalAsync: true,
					ImageRetry: &model.AsyncImageRetryState{Details: []model.AsyncImageRetryDetail{{
						ChannelID: 141, ChannelName: "provider-private", ErrorCode: tc.rawCode,
						StatusCode: tc.status, Error: tc.reason + " model-private https://upstream.invalid sk-secret",
					}}},
				},
			}
			require.NoError(t, db.Create(task).Error)
			for _, owner := range []int{901, 902} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+task.TaskID, nil)
				c.Set("id", owner)
				c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
				GetImageTask(c)
				if owner != task.UserId {
					require.Equal(t, http.StatusNotFound, recorder.Code)
					require.Contains(t, recorder.Body.String(), "task_not_found")
					continue
				}
				require.Equal(t, http.StatusOK, recorder.Code)
				var response dto.ImageAsyncTaskResponse
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.Equal(t, "failed", response.Status)
				require.Equal(t, tc.wantCode, response.ErrorCode)
				require.Contains(t, response.Error, "["+tc.wantCode+"]")
				require.Contains(t, response.Error, tc.wantMessage)
				for _, secret := range []string{"provider-private", "model-private", "upstream.invalid", "sk-secret", "141"} {
					require.NotContains(t, recorder.Body.String(), secret)
				}
			}
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.Equal(t, tc.reason, stored.FailReason, "administrator diagnostics must remain intact")
		})
	}
}

func TestImageTaskPreservesUpstreamFailureStatus(t *testing.T) {
	const reason = "分组 default 下模型 all-image-2.5-sunburst-1k 的可用渠道不存在（retry）; retry: 124"
	for _, tc := range []struct {
		name, reason, wantCode, wantMessage string
		detail                              *model.AsyncImageRetryDetail
	}{
		{"stored status", "status_code=500, " + reason, "model_unavailable", "当前资源紧张，暂时无可用资源", nil},
		{"unknown upstream failure", "status_code=500, Task failed", "generation_failed", "生成失败", nil},
		{"retry status", "Task failed", "generation_failed", "生成失败", &model.AsyncImageRetryDetail{ChannelID: 141, StatusCode: 500, Error: "Task failed"}},
		{"known reason retry status", "context deadline exceeded", "request_timeout", "请求处理超时", &model.AsyncImageRetryDetail{ChannelID: 141, StatusCode: 500, Error: "context deadline exceeded"}},
		{"retry without status", "status_code=500, Task failed", "generation_failed", "生成失败", &model.AsyncImageRetryDetail{ChannelID: 141, Error: "Task failed"}},
		{"retry diagnostic", "Task failed", "model_unavailable", "当前资源紧张，暂时无可用资源", &model.AsyncImageRetryDetail{ChannelID: 141, StatusCode: 500, Error: "Task failed: " + reason}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{TaskID: "task_status", ChannelId: 141, Status: model.TaskStatusFailure, FailReason: tc.reason}
			if tc.detail != nil {
				task.PrivateData.ImageRetry = &model.AsyncImageRetryState{Details: []model.AsyncImageRetryDetail{*tc.detail}}
			}
			response := buildImageTaskResponse(task)
			assert.Equal(t, tc.wantCode, response.ErrorCode)
			assert.Contains(t, response.Error, "status_code=500")
			assert.Contains(t, response.Error, tc.wantMessage)
			assert.Equal(t, tc.reason, task.FailReason)
			encoded, err := common.Marshal(response)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), "status_code=500")
		})
	}
}

func TestGetSignedImageTaskContentServesImageWithoutAuthorization(t *testing.T) {
	db := setupImageTaskTestDB(t)
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	imagePath := filepath.Join(t.TempDir(), "result.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png-bytes"), 0600))
	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_download",
		UserId: 71,
		Action: constant.TaskActionImageGenerate,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: service.AsyncImageAssetType,
			LocalPath: imagePath,
			MimeType:  "image/png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}
	require.NoError(t, db.Create(task).Error)

	signedURL, _ := service.BuildSignedAsyncImageContentURL(task)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	c.Request = req
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}

	GetSignedImageTaskContent(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "image/png", recorder.Header().Get("Content-Type"))
	require.Equal(t, "png-bytes", recorder.Body.String())
	etag := recorder.Header().Get("ETag")
	require.Len(t, etag, 66)
	for _, test := range []struct {
		ifRange    string
		wantStatus int
		wantBody   string
	}{
		{etag, 206, "ng-"},
		{`"different-version"`, 200, "png-bytes"},
	} {
		recorder = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
		c.Request.Header.Set("Range", "bytes=1-3")
		c.Request.Header.Set("If-Range", test.ifRange)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		GetSignedImageTaskContent(c)
		assert.Equal(t, test.wantStatus, recorder.Code)
		assert.Equal(t, test.wantBody, recorder.Body.String())
	}
}

func TestGetSignedImageTaskContentRejectsTamperedTokenAndOtherTask(t *testing.T) {
	db := setupImageTaskTestDB(t)
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	imagePath := filepath.Join(t.TempDir(), "result.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png-bytes"), 0600))
	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_owner",
		UserId: 71,
		Action: constant.TaskActionImageGenerate,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: service.AsyncImageAssetType,
			LocalPath: imagePath,
			MimeType:  "image/png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}
	otherTask := *task
	otherTask.TaskID = "task_other"
	otherTask.UserId = 72
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, db.Create(&otherTask).Error)

	signedURL, _ := service.BuildSignedAsyncImageContentURL(task)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)
	token := parsed.Query().Get("token")
	require.NotEmpty(t, token)

	tamperedToken := tamperImageTaskToken(token)
	requireSignedContentStatus(t, task.TaskID, tamperedToken, http.StatusUnauthorized)
	requireSignedContentStatus(t, otherTask.TaskID, token, http.StatusUnauthorized)
}

func TestGetSignedImageTaskContentReturnsGoneWhenFileIsMissing(t *testing.T) {
	db := setupImageTaskTestDB(t)
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_missing",
		UserId: 71,
		Action: constant.TaskActionImageGenerate,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: service.AsyncImageAssetType,
			LocalPath: filepath.Join(t.TempDir(), "missing.png"),
			MimeType:  "image/png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}
	require.NoError(t, db.Create(task).Error)
	signedURL, _ := service.BuildSignedAsyncImageContentURL(task)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)

	requireSignedContentStatus(t, task.TaskID, parsed.Query().Get("token"), http.StatusGone)
}

func requireSignedContentStatus(t *testing.T, taskID string, token string, expected int) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+taskID+"/signed-content?token="+url.QueryEscape(token), nil)
	c.Request = req
	c.Params = gin.Params{{Key: "task_id", Value: taskID}}

	GetSignedImageTaskContent(c)

	require.Equal(t, expected, recorder.Code)
}

func tamperImageTaskToken(token string) string {
	if strings.HasSuffix(token, "x") {
		return token[:len(token)-1] + "y"
	}
	return token[:len(token)-1] + "x"
}
