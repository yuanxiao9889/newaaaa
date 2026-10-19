package controller

import (
	"context"
	"fmt"
	"io"
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
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGenericTaskTest(t *testing.T) *model.Task {
	t.Helper()
	originalDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	// The dialect harness lets the same fixture run against MySQL and
	// PostgreSQL when TEST_TASK_DB_DIALECT selects them; SQLite stays the default.
	database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.Channel{}, &model.User{})
	model.DB = database
	t.Cleanup(func() {
		model.DB = originalDB
		common.RedisEnabled = previousRedisEnabled
	})

	require.NoError(t, database.Create(&model.User{
		Id: 7, Username: "artifact-owner", Status: common.UserStatusEnabled,
		Role: common.RoleCommonUser, Group: "default",
	}).Error)
	baseURL := "https://example.com"
	require.NoError(t, database.Create(&model.Channel{
		Id: 1, Name: "artifact", Key: "key", BaseURL: &baseURL, Status: common.ChannelStatusEnabled,
	}).Error)
	task := &model.Task{
		TaskID: "task_generic", Platform: "document", UserId: 7, ChannelId: 1,
		Status: model.TaskStatusSuccess, Progress: "100%", SubmitTime: 10, FinishTime: 20,
	}
	require.NoError(t, database.Create(task).Error)
	return task
}

func allowPrivateTaskMediaTest(t *testing.T) {
	t.Helper()
	originalFetchSetting := *system_setting.GetFetchSetting()
	system_setting.GetFetchSetting().EnableSSRFProtection = true
	system_setting.GetFetchSetting().AllowPrivateIp = true
	system_setting.GetFetchSetting().AllowedPorts = []string{"1-65535"}
	t.Cleanup(func() { *system_setting.GetFetchSetting() = originalFetchSetting })
	service.InitHttpClient()
}

func TestGetTaskDoesNotProjectArtifacts(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.FailReason = "https://stale-upstream.invalid/video.mp4"
	task.PrivateData = model.TaskPrivateData{
		ResultURL: "https://private-upstream.invalid/video.mp4",
		Execution: &model.TaskExecutionSnapshot{
			TaskPlugin: &model.TaskPluginSnapshot{Key: "missing-plugin", Name: "Missing"},
		},
	}
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 7)
	c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID, nil)

	GetTask(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, task.TaskID, response["task_id"])
	assert.NotContains(t, response, "artifacts")
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
}

func TestUserTaskEndpointsEnforceOwnershipAndPrivacy(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Platform = "private-platform"
	task.Status = model.TaskStatusFailure
	task.FailReason = "private-platform private-model upstream.invalid retry:141"
	task.Properties = model.Properties{OriginModelName: "public-model", UpstreamModelName: "private-model", Input: "private-input"}
	task.Data = []byte(`{"upstream":"upstream.invalid","model":"private-model"}`)
	task.PrivateData.UpstreamTaskID = "private-upstream-task"
	require.NoError(t, model.DB.Save(task).Error)
	fetchVideo := func(c *gin.Context) {
		if taskErr := relay.RelayTaskFetch(c, relayconstant.RelayModeVideoFetchByID); taskErr != nil {
			respondTaskError(c, taskErr)
		}
	}
	for _, endpoint := range []struct {
		name    string
		path    string
		param   string
		handler gin.HandlerFunc
	}{
		{"generic", "/v1/tasks/", "key", GetTask},
		{"detail", "/api/task/self/", "id", GetUserTaskDetail},
		{"list", "/api/task/self?task_id=", "unused", GetUserTask},
		{"video", "/v1/video/generations/", "task_id", fetchVideo},
		{"openai-video", "/v1/videos/", "task_id", fetchVideo},
	} {
		for _, userID := range []int{task.UserId, task.UserId + 1} {
			t.Run(fmt.Sprintf("%s/user-%d", endpoint.name, userID), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", userID)
				c.Set("role", common.RoleCommonUser)
				c.Params = gin.Params{{Key: endpoint.param, Value: task.TaskID}}
				c.Request = httptest.NewRequest(http.MethodGet, endpoint.path+task.TaskID, nil)
				endpoint.handler(c)
				body := recorder.Body.String()
				for _, secret := range []string{"private-platform", "private-model", "private-input", "private-upstream-task", "upstream.invalid", "retry:141"} {
					assert.NotContains(t, body, secret)
				}
				if userID == task.UserId {
					assert.Contains(t, body, task.TaskID)
					assert.Contains(t, body, "public-model")
					assert.Contains(t, body, "[generation_failed]")
					if endpoint.name == "generic" {
						assert.Contains(t, body, `"error_code":"generation_failed"`)
					}
				} else {
					assert.NotContains(t, body, task.TaskID)
					assert.NotContains(t, body, "public-model")
				}
			})
		}
	}
}

func TestGetTaskArtifactsReturnsEmptyForLegacyTask(t *testing.T) {
	task := setupGenericTaskTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", nil)

	GetTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	var response struct {
		TaskID    string                 `json:"task_id"`
		Artifacts []taskArtifactResponse `json:"artifacts"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, task.TaskID, response.TaskID)
	assert.Empty(t, response.Artifacts)
}

func TestUserLegacyDrawingLogsHideInternalMetadata(t *testing.T) {
	task := setupGenericTaskTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Midjourney{}))
	for _, userID := range []int{task.UserId, task.UserId + 1} {
		require.NoError(t, model.DB.Create(&model.Midjourney{
			UserId: userID, MjId: fmt.Sprintf("drawing-%d", userID), ChannelId: 141,
			Status: "FAILURE", Quota: 123, FailReason: "prompt too long https://upstream.invalid/error?token=private-token mj-api-secret=private-secret",
			Properties: `{"model":"private-model"}`, ImageUrl: "https://upstream.invalid/image.png",
		}).Error)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/mj/self", nil)
	GetUserMidjourney(c)
	for _, secret := range []string{"private-", "upstream.invalid", fmt.Sprintf("drawing-%d", task.UserId+1)} {
		assert.NotContains(t, recorder.Body.String(), secret)
	}
	assert.Contains(t, recorder.Body.String(), "[input_too_large]")
	assert.Contains(t, recorder.Body.String(), "prompt too long")
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf("drawing-%d", task.UserId))
}

func TestInternalImageTaskArtifacts(t *testing.T) {
	task := setupGenericTaskTest(t)
	previousSecret := common.CryptoSecret
	previousPublicAddress := system_setting.TaskPublicAddress
	common.CryptoSecret = "internal-image-artifact-test-secret"
	system_setting.TaskPublicAddress = "https://gateway.example"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.TaskPublicAddress = previousPublicAddress
	})
	imagePath := filepath.Join(t.TempDir(), "result.png")
	imageBytes := []byte("stored-image-bytes")
	require.NoError(t, os.WriteFile(imagePath, imageBytes, 0600))
	task.Platform = constant.TaskPlatformInternalImage
	task.Action = constant.TaskActionImageGenerate
	task.PrivateData = model.TaskPrivateData{
		InternalAsync: true,
		AssetType:     service.AsyncImageAssetType,
		LocalPath:     imagePath,
		MimeType:      "image/png",
		ExpiresAt:     time.Now().Add(time.Hour).Unix(),
		SourceURL:     "https://private-upstream.invalid/result.png",
	}
	require.NoError(t, model.DB.Save(task).Error)

	var contentURL string
	for _, tc := range []struct {
		name      string
		dashboard bool
		userID    int
		role      int
		tokenID   int
		status    int
	}{
		{name: "owner API", userID: 7, status: http.StatusOK},
		{name: "owner dashboard", dashboard: true, userID: 7, role: common.RoleCommonUser, status: http.StatusOK},
		{name: "admin dashboard", dashboard: true, userID: 8, role: common.RoleAdminUser, status: http.StatusOK},
		{name: "other user", dashboard: true, userID: 8, role: common.RoleCommonUser, status: http.StatusNotFound},
		{name: "admin token cannot read foreign task", dashboard: true, userID: 8, role: common.RoleRootUser, tokenID: 99, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", tc.userID)
			c.Set("role", tc.role)
			c.Set("token_id", tc.tokenID)
			if tc.dashboard {
				c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
				c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)
				GetDashboardTaskArtifacts(c)
			} else {
				c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", nil)
				GetTaskArtifacts(c)
			}
			require.Equal(t, tc.status, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "private-upstream.invalid")
			assert.NotContains(t, recorder.Body.String(), "local_path")
			assert.NotContains(t, recorder.Body.String(), "internal-image-artifact-test-secret")
			if tc.status != http.StatusOK {
				return
			}
			var response struct {
				Artifacts []taskArtifactResponse `json:"artifacts"`
				Data      struct {
					Artifacts []taskArtifactResponse `json:"artifacts"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			artifacts := response.Artifacts
			if tc.dashboard {
				artifacts = response.Data.Artifacts
			}
			require.Len(t, artifacts, 1)
			assert.Equal(t, "image", artifacts[0].Key)
			assert.Equal(t, "image", artifacts[0].Type)
			assert.Equal(t, "image/png", artifacts[0].MimeType)
			contentURL = artifacts[0].ContentURL
		})
	}

	parsed, err := url.Parse(contentURL)
	require.NoError(t, err)
	require.Equal(t, "/v1/tasks/"+task.TaskID+"/artifacts/image/content", parsed.Path)
	wrongArtifactURL, err := service.BuildTaskArtifactContentURL(task.TaskID, "video")
	require.NoError(t, err)
	router := gin.New()
	router.Handle(http.MethodGet, "/v1/tasks/:key/artifacts/:artifact_key/content", middleware.TokenOrTaskArtifactAccessAuth("key", "artifact_key"), TaskArtifactContent)
	router.Handle(http.MethodHead, "/v1/tasks/:key/artifacts/:artifact_key/content", middleware.TokenOrTaskArtifactAccessAuth("key", "artifact_key"), TaskArtifactContent)
	for _, tc := range []struct {
		name        string
		method      string
		url         string
		rangeHeader string
		status      int
		body        string
	}{
		{name: "preview and download", method: http.MethodGet, url: parsed.RequestURI(), status: http.StatusOK, body: string(imageBytes)},
		{name: "HEAD", method: http.MethodHead, url: parsed.RequestURI(), status: http.StatusOK},
		{name: "range", method: http.MethodGet, url: parsed.RequestURI(), rangeHeader: "bytes=0-5", status: http.StatusPartialContent, body: "stored"},
		{name: "tampered signature", method: http.MethodGet, url: parsed.Path + "?access=" + strings.Repeat("A", 43), status: http.StatusNotFound},
		{name: "different task", method: http.MethodGet, url: strings.Replace(parsed.RequestURI(), task.TaskID, "another_task", 1), status: http.StatusNotFound},
		{name: "different artifact", method: http.MethodGet, url: strings.Replace(parsed.RequestURI(), "/image/", "/video/", 1), status: http.StatusNotFound},
		{name: "unknown artifact with valid signature", method: http.MethodGet, url: wrongArtifactURL, status: http.StatusNotFound},
		{name: "empty signature", method: http.MethodGet, url: parsed.Path + "?access=", status: http.StatusNotFound},
		{name: "unauthenticated", method: http.MethodGet, url: parsed.Path, status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.url, nil)
			req.Header.Set("Range", tc.rangeHeader)
			router.ServeHTTP(recorder, req)
			assert.Equal(t, tc.status, recorder.Code)
			assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			if tc.status < 400 {
				assert.Equal(t, tc.body, recorder.Body.String())
				assert.Equal(t, "image/png", recorder.Header().Get("Content-Type"))
				assert.Contains(t, recorder.Header().Get("Content-Disposition"), task.TaskID+".png")
			} else {
				assert.NotContains(t, recorder.Body.String(), string(imageBytes))
			}
		})
	}

	t.Run("four retained images have separate artifact links and downloads", func(t *testing.T) {
		task.PrivateData.ImageAssets = []model.AsyncImageAsset{{LocalPath: imagePath, MimeType: "image/png"}}
		for i := 1; i < 4; i++ {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("result-%d.png", i))
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("stored-image-%d", i)), 0600))
			task.PrivateData.ImageAssets = append(task.PrivateData.ImageAssets, model.AsyncImageAsset{LocalPath: path, MimeType: "image/png"})
		}
		require.NoError(t, model.DB.Save(task).Error)
		t.Cleanup(func() {
			task.PrivateData.ImageAssets = nil
			require.NoError(t, model.DB.Save(task).Error)
		})
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", task.UserId)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)
		GetDashboardTaskArtifacts(c)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Data struct {
				Artifacts []taskArtifactResponse `json:"artifacts"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Len(t, response.Data.Artifacts, 4)
		for i, artifact := range response.Data.Artifacts {
			expectedKey := "image"
			if i > 0 {
				expectedKey = fmt.Sprintf("image-%d", i+1)
			}
			assert.Equal(t, expectedKey, artifact.Key)
			want := string(imageBytes)
			if i > 0 {
				want = fmt.Sprintf("stored-image-%d", i)
			}
			// Artifact-key capabilities must ignore the unrelated image API's index query.
			for _, suffix := range []string{"", "&index=3"} {
				result := httptest.NewRecorder()
				router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, artifact.ContentURL+suffix, nil))
				require.Equal(t, http.StatusOK, result.Code)
				assert.Equal(t, want, result.Body.String())
			}
		}
		tampered := strings.Replace(response.Data.Artifacts[0].ContentURL, "/image/", "/image-2/", 1)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, tampered, nil))
		assert.Equal(t, http.StatusNotFound, result.Code)
	})

	for _, tc := range []struct {
		name      string
		status    model.TaskStatus
		path      string
		expiresAt int64
	}{
		{name: "unfinished", status: model.TaskStatusInProgress, path: imagePath, expiresAt: task.PrivateData.ExpiresAt},
		{name: "failed", status: model.TaskStatusFailure, path: imagePath, expiresAt: task.PrivateData.ExpiresAt},
		{name: "expired", status: model.TaskStatusSuccess, path: imagePath, expiresAt: time.Now().Add(-time.Hour).Unix()},
		{name: "missing file", status: model.TaskStatusSuccess, path: imagePath + ".missing", expiresAt: task.PrivateData.ExpiresAt},
		{name: "cleaned file", status: model.TaskStatusSuccess, expiresAt: task.PrivateData.ExpiresAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task.Status = tc.status
			task.PrivateData.LocalPath = tc.path
			task.PrivateData.ExpiresAt = tc.expiresAt
			require.NoError(t, model.DB.Save(task).Error)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
			assert.Equal(t, http.StatusNotFound, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), string(imageBytes))
			if tc.status != model.TaskStatusSuccess {
				recorder = httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", task.UserId)
				c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", nil)
				GetTaskArtifacts(c)
				require.Equal(t, http.StatusOK, recorder.Code)
				var response struct {
					Artifacts []taskArtifactResponse `json:"artifacts"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Empty(t, response.Artifacts)
			}
		})
	}

	t.Run("disabled owner revokes existing URL", func(t *testing.T) {
		task.Status = model.TaskStatusSuccess
		task.PrivateData.LocalPath = imagePath
		task.PrivateData.ExpiresAt = time.Now().Add(time.Hour).Unix()
		require.NoError(t, model.DB.Save(task).Error)
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", task.UserId).
			Update("status", common.UserStatusDisabled).Error)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
		assert.Equal(t, http.StatusNotFound, recorder.Code)
		assert.NotContains(t, recorder.Body.String(), string(imageBytes))
	})
}

func TestTaskArtifactAuthorizationKeepsForeignTasksHidden(t *testing.T) {
	task := setupGenericTaskTest(t)

	commonUser, _ := gin.CreateTestContext(httptest.NewRecorder())
	commonUser.Set("id", 8)
	commonUser.Set("role", common.RoleCommonUser)
	_, exists, err := getTaskForArtifactRequest(commonUser, task.TaskID)
	require.NoError(t, err)
	assert.False(t, exists)

	admin, _ := gin.CreateTestContext(httptest.NewRecorder())
	admin.Set("id", 8)
	admin.Set("role", common.RoleAdminUser)
	found, exists, err := getTaskForArtifactRequest(admin, task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, task.TaskID, found.TaskID)

	apiToken, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiToken.Set("id", 8)
	apiToken.Set("role", common.RoleRootUser)
	apiToken.Set("token_id", 99)
	_, exists, err = getTaskForArtifactRequest(apiToken, task.TaskID)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDashboardTaskArtifactsReturnsLegacyCapabilityWithoutUpstreamURL(t *testing.T) {
	task := setupGenericTaskTest(t)
	previousSecret := common.CryptoSecret
	previousPublicAddress := system_setting.TaskPublicAddress
	common.CryptoSecret = "controller-task-artifact-access-secret"
	system_setting.TaskPublicAddress = "https://gateway.example/prefix"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.TaskPublicAddress = previousPublicAddress
	})
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://upstream.invalid/private-video.mp4?signature=secret"
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Set("role", common.RoleCommonUser)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)

	GetDashboardTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Artifacts        []taskArtifactResponse `json:"artifacts"`
			LegacyContentURL string                 `json:"legacy_content_url"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Empty(t, response.Data.Artifacts)
	contentURL, err := url.Parse(response.Data.LegacyContentURL)
	require.NoError(t, err)
	assert.Equal(t, "/prefix/v1/tasks/"+task.TaskID+"/artifacts/video/content", contentURL.Path)
	assert.True(t, service.VerifyTaskArtifactAccess(
		contentURL.Query().Get(service.TaskArtifactAccessQueryParameter),
		task.TaskID,
		"video",
	))
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
	assert.NotContains(t, recorder.Body.String(), "signature=secret")
}

// Task lists no longer carry the persisted snapshot, so the dashboard reads a
// legacy Suno task's playable clips from the artifacts endpoint instead.
func TestDashboardTaskArtifactsProjectsLegacySunoAudioClips(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Platform = constant.TaskPlatformSuno
	task.Action = "MUSIC"
	task.Data = []byte(`[{"id":"clip-1","title":"Song","audio_url":"https://cdn.example/a.mp3","metadata":{"tags":"pop","duration":30},"lyric":"private"},{"id":"clip-2","title":"No audio"}]`)
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Set("role", common.RoleCommonUser)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)

	GetDashboardTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Artifacts        []taskArtifactResponse `json:"artifacts"`
			LegacyAudioClips []map[string]any       `json:"legacy_audio_clips"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Empty(t, response.Data.Artifacts)
	require.Len(t, response.Data.LegacyAudioClips, 1)
	contentURL, err := url.Parse(response.Data.LegacyAudioClips[0]["audio_url"].(string))
	require.NoError(t, err)
	assert.Contains(t, contentURL.Path, "/artifacts/audio_url-0/content")
	assert.True(t, service.VerifyTaskArtifactAccess(contentURL.Query().Get(service.TaskArtifactAccessQueryParameter), task.TaskID, "audio_url-0"))
	assert.NotContains(t, recorder.Body.String(), "cdn.example")
	assert.NotContains(t, recorder.Body.String(), "clip-1")
	assert.Equal(t, "Song", response.Data.LegacyAudioClips[0]["title"])
	assert.NotContains(t, recorder.Body.String(), "private")
}

// Task lists omit the data column; the API DTO keeps the key as null so shape
// checks survive while the payload no longer travels with every row.
func TestTaskListsOmitPersistedSnapshot(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Data = []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)
	require.NoError(t, model.DB.Save(task).Error)

	userTasks := model.TaskGetAllUserTask(task.UserId, 0, 10, model.SyncTaskQueryParams{})
	require.Len(t, userTasks, 1)
	assert.Empty(t, userTasks[0].Data)
	adminTasks := model.TaskGetAllTasks(0, 10, model.SyncTaskQueryParams{})
	require.Len(t, adminTasks, 1)
	assert.Empty(t, adminTasks[0].Data)
	assert.Equal(t, task.TaskID, adminTasks[0].TaskID)

	encoded, err := common.Marshal(tasksToDto(adminTasks, false, common.RoleAdminUser)[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"data":null`)
	assert.NotContains(t, string(encoded), `"result_discarded"`)

	adminTasks[0].PrivateData.ResultDiscarded = true
	encoded, err = common.Marshal(tasksToDto(adminTasks, false, common.RoleAdminUser)[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"result_discarded":true`, "task lists tell the UI that an inline result was not retained")

	stored, exists, err := model.GetByTaskId(task.UserId, task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.JSONEq(t, string(task.Data), string(stored.Data), "single-task lookups keep the snapshot")
}

func TestTaskArtifactAccessRequiresActiveOwner(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://upstream.invalid/private-video.mp4"
	require.NoError(t, model.DB.Save(task).Error)
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", task.UserId).
		Update("status", common.UserStatusDisabled).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
}

func TestTaskArtifactAccessRejectsAmbiguousHistoricalTaskID(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://first-upstream.invalid/video.mp4"
	require.NoError(t, model.DB.Save(task).Error)
	require.NoError(t, model.DB.Create(&model.User{
		Id: 8, Username: "other-artifact-owner", Status: common.UserStatusEnabled,
		Role: common.RoleCommonUser, Group: "default", AffCode: "artifact-owner-8",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Task{
		TaskID: task.TaskID, Platform: task.Platform, UserId: 8, ChannelId: task.ChannelId,
		Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess,
		FailReason: "https://second-upstream.invalid/video.mp4",
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
}

func TestLegacyVideoArtifactContentUsesGetResultURL(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bytes=0-3", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()
	allowPrivateTaskMediaTest(t)

	task.Action = constant.TaskActionTextToVideo
	task.PrivateData.ResultURL = upstream.URL
	task.FailReason = "https://stale.invalid/legacy-fallback.mp4"
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)
	c.Request.Header.Set("Range", "bytes=0-3")

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "data", recorder.Body.String())
	assert.Equal(t, "bytes 0-3/4", recorder.Header().Get("Content-Range"))

	// The same gateway media path must also serve historical audio without
	// giving the browser an upstream URL.
	task.Platform = constant.TaskPlatformSuno
	encodedAudio, err := common.Marshal([]map[string]any{{"audio_url": upstream.URL}})
	require.NoError(t, err)
	task.Data = encodedAudio
	require.NoError(t, model.DB.Save(task).Error)
	audioRecorder := httptest.NewRecorder()
	audioContext, _ := gin.CreateTestContext(audioRecorder)
	audioContext.Set("id", task.UserId)
	audioContext.Params = gin.Params{{Key: "key", Value: task.TaskID}, {Key: "artifact_key", Value: "audio_url-0"}}
	audioContext.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts/audio_url-0/content", nil)
	audioContext.Request.Header.Set("Range", "bytes=0-3")
	TaskArtifactContent(audioContext)
	assert.Equal(t, http.StatusPartialContent, audioRecorder.Code)
	assert.Equal(t, "data", audioRecorder.Body.String())
}

func TestDisabledArtifactStorePreservesPluginUpstreamContent(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "provider-key", r.Header.Get("x-goog-api-key"))
		assert.Equal(t, "bytes=0-13", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-13/14")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("artifact-bytes"))
	}))
	defer upstream.Close()
	allowPrivateTaskMediaTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })

	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Updates(map[string]any{
		"type":     constant.ChannelTypeGemini,
		"key":      "provider-key",
		"base_url": upstream.URL,
	}).Error)
	task.Platform = constant.TaskPlatform("google")
	task.PrivateData.Execution = &model.TaskExecutionSnapshot{TaskPlugin: &model.TaskPluginSnapshot{
		Key: "google", Name: "Google Veo (Gemini API)", Version: "1.0.0", APIVersion: 1,
	}}
	task.SetData(map[string]any{"response": map[string]any{
		"generateVideoResponse": map[string]any{
			"generatedVideos": []any{map[string]any{"video": map[string]any{"uri": upstream.URL}}},
		},
	}})
	require.NoError(t, model.DB.Save(task).Error)
	require.False(t, service.GetTaskArtifactStore().Enabled())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts/video/content", nil)
	c.Request.Header.Set("Range", "bytes=0-13")

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "artifact-bytes", recorder.Body.String())
	assert.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "bytes 0-13/14", recorder.Header().Get("Content-Range"))
}

func TestProjectedTaskArtifactValidationRejectsAmbiguousIdentity(t *testing.T) {
	validated, err := validateProjectedTaskArtifacts([]relaychannel.TaskArtifact{
		{Key: "video-main", Type: "video", MimeType: "video/mp4"},
		{Key: "cover.main", Type: "image", MimeType: "image/png"},
	})
	require.NoError(t, err)
	require.Len(t, validated, 2)
	assert.Equal(t, "video-main", validated[0].Key)

	for _, artifacts := range [][]relaychannel.TaskArtifact{
		{{Key: "../video", Type: "video"}},
		{{Key: "video/0", Type: "video"}},
		{{Key: "video-main", Type: "video"}, {Key: "video-main", Type: "image"}},
		{{Key: "video-main", Type: "unknown"}},
		{{Key: "video-main", Type: "video", MimeType: "video/mp4\r\nX-Test: injected"}},
	} {
		_, err := validateProjectedTaskArtifacts(artifacts)
		assert.ErrorIs(t, err, errTaskArtifactPlugin)
	}
}

func TestProxyTaskMediaForwardsRangeAndFiltersResponseHeaders(t *testing.T) {
	task := setupGenericTaskTest(t)
	var receivedRange, receivedAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange = r.Header.Get("Range")
		receivedAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Set-Cookie", "provider=secret")
		w.Header().Set("WWW-Authenticate", "Bearer provider")
		w.Header().Set("X-Provider-Secret", "hidden")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()

	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/task_generic/artifacts/video-main/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")

	err := proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL:     upstream.URL,
		Method:  http.MethodGet,
		Headers: map[string]string{"Authorization": "Bearer provider-secret"},
	})

	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "data", recorder.Body.String())
	assert.Equal(t, "bytes=0-3", receivedRange)
	assert.Equal(t, "Bearer provider-secret", receivedAuthorization)
	assert.Equal(t, "bytes 0-3/10", recorder.Header().Get("Content-Range"))
	assert.Equal(t, "bytes", recorder.Header().Get("Accept-Ranges"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "sandbox; default-src 'none'", recorder.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
	assert.Empty(t, recorder.Header().Get("WWW-Authenticate"))
	assert.Empty(t, recorder.Header().Get("X-Provider-Secret"))
}

func TestProxyTaskMediaPassesThroughUnsatisfiedRange(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes */10")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer upstream.Close()

	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	require.NoError(t, proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL: upstream.URL, Method: http.MethodGet,
	}))
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, recorder.Code)
	assert.Equal(t, "bytes */10", recorder.Header().Get("Content-Range"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
}

func TestTaskMediaResponseHeaderTimeoutDoesNotTruncateBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(75 * time.Millisecond)
		_, _ = w.Write([]byte("complete-body"))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	response, err := doTaskMediaRequest(upstream.Client(), request, 20*time.Millisecond)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "complete-body", string(body))
}

func TestTaskMediaResponseHeaderTimeoutCancelsBeforeHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(75 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	_, err = doTaskMediaRequest(upstream.Client(), request, 10*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWriteVideoDataURLStreamsAndSupportsHead(t *testing.T) {
	const dataURL = "data:video/mp4;base64,Y29tcGxldGUtYm9keQ=="

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	require.NoError(t, writeVideoDataURL(getContext, dataURL))
	assert.Equal(t, http.StatusOK, getRecorder.Code)
	assert.Equal(t, "complete-body", getRecorder.Body.String())
	assert.Equal(t, "13", getRecorder.Header().Get("Content-Length"))

	headRecorder := httptest.NewRecorder()
	headContext, _ := gin.CreateTestContext(headRecorder)
	headContext.Request = httptest.NewRequest(http.MethodHead, "/content", nil)
	require.NoError(t, writeVideoDataURL(headContext, dataURL))
	assert.Equal(t, http.StatusOK, headRecorder.Code)
	assert.Empty(t, headRecorder.Body.String())
	assert.Equal(t, "13", headRecorder.Header().Get("Content-Length"))
}

func TestWriteVideoDataURLRejectsOversizedPayloadBeforeDecode(t *testing.T) {
	previousLimit := taskMediaDataURLMaxEncodedBytes
	taskMediaDataURLMaxEncodedBytes = 32
	t.Cleanup(func() { taskMediaDataURLMaxEncodedBytes = previousLimit })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	err := writeVideoDataURL(c, "data:video/mp4;base64,"+strings.Repeat("A", 64))

	assert.ErrorIs(t, err, errTaskMediaRequestRejected)
	assert.Empty(t, recorder.Header().Get("Content-Type"))
}

func TestProxyTaskMediaAllowsOnlyCredentiallessCrossOriginRedirect(t *testing.T) {
	task := setupGenericTaskTest(t)
	var destinationAuthorization, destinationRange string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization = r.Header.Get("Authorization")
		destinationRange = r.Header.Get("Range")
		_, _ = w.Write([]byte("redirected"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")

	err := proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL: source.URL, Method: http.MethodGet, Credentialless: true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "redirected", recorder.Body.String())
	assert.Empty(t, destinationAuthorization)
	assert.Equal(t, "bytes=0-3", destinationRange)

	destinationRange = ""
	rejectedRecorder := httptest.NewRecorder()
	rejectedContext, _ := gin.CreateTestContext(rejectedRecorder)
	rejectedContext.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	err = proxyTaskMedia(rejectedContext, task, &relaychannel.TaskContentRequest{
		URL: source.URL, Method: http.MethodGet,
		Headers: map[string]string{"Authorization": "Bearer provider-secret"},
	})
	var proxyErr *taskMediaProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, "artifact_request_rejected", proxyErr.code)
	assert.Empty(t, destinationRange)
}

func TestTaskMediaRequestHeaderPolicy(t *testing.T) {
	header := http.Header{}
	require.NoError(t, applyTaskMediaRequestHeaders(header, map[string]string{
		"Authorization": "Bearer provider-secret",
		"X-Signature":   "signed",
	}))
	assert.Equal(t, "Bearer provider-secret", header.Get("Authorization"))
	assert.Equal(t, "signed", header.Get("X-Signature"))

	for _, name := range []string{"Host", "Content-Length", "Accept-Encoding", "Connection", "Proxy-Authorization", "Transfer-Encoding"} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, applyTaskMediaRequestHeaders(http.Header{}, map[string]string{name: "bad"}), errTaskMediaRequestRejected)
		})
	}
	assert.ErrorIs(t, applyTaskMediaRequestHeaders(http.Header{}, map[string]string{"X-Test": "bad\r\ninjected"}), errTaskMediaRequestRejected)
}

func TestCredentiallessTaskMediaDescriptorRejectsCredentialsAndBody(t *testing.T) {
	task := setupGenericTaskTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	for _, descriptor := range []*relaychannel.TaskContentRequest{
		{URL: "https://example.com/video", Method: http.MethodPost, Credentialless: true},
		{URL: "https://example.com/video", Method: http.MethodGet, Body: []byte("secret"), Credentialless: true},
		{URL: "https://example.com/video", Method: http.MethodGet, Headers: map[string]string{"X-Key": "secret"}, Credentialless: true},
	} {
		err := proxyTaskMedia(c, task, descriptor)
		var proxyErr *taskMediaProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, "artifact_request_rejected", proxyErr.code)
	}
}

func TestSelfTaskMediaURLGuard(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/videos/task-1/content", nil)
	c.Request.Host = "gateway.example"

	selfURL, err := url.Parse("https://gateway.example/v1/videos/task-1/content")
	require.NoError(t, err)
	assert.True(t, isSelfTaskMediaURL(c, selfURL))

	remoteURL, err := url.Parse("https://cdn.example/v1/videos/task-1/content")
	require.NoError(t, err)
	assert.False(t, isSelfTaskMediaURL(c, remoteURL))
	assert.True(t, isTaskMediaFallbackLoop(remoteURL.String(), "task-1"))
	assert.False(t, isTaskMediaFallbackLoop(remoteURL.String(), "task-2"))
}
