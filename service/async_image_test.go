package service

import (
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestBuildSignedAsyncImageContentURLIncludesShortLivedToken(t *testing.T) {
	originalServerAddress := system_setting.ServerAddress
	originalCryptoSecret := common.CryptoSecret
	system_setting.ServerAddress = "https://example.test"
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		system_setting.ServerAddress = originalServerAddress
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_signed",
		UserId: 23,
		Action: constant.TaskActionImageGenerate,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: "image.png",
			StoredAt:  now - 10,
			ExpiresAt: now + 7200,
		},
	}

	signedURL, urlExpiresAt := BuildSignedAsyncImageContentURL(task)

	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)
	require.Equal(t, "https://example.test/v1/images/tasks/task_signed/signed-content", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	require.NotEmpty(t, parsed.Query().Get("token"))
	require.InDelta(t, now+3600, urlExpiresAt, 2)
	require.True(t, VerifyAsyncImageToken(parsed.Query().Get("token"), task))
}

func TestVerifyAsyncImageTokenRejectsTamperingExpiryAndDifferentTask(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_owner",
		UserId: 88,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: "image.png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}
	signedURL, _ := BuildSignedAsyncImageContentURL(task)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)
	token := parsed.Query().Get("token")
	require.NotEmpty(t, token)

	tampered := tamperToken(token)
	require.False(t, VerifyAsyncImageToken(tampered, task))

	otherTask := *task
	otherTask.TaskID = "task_other"
	require.False(t, VerifyAsyncImageToken(token, &otherTask))

	expiredTask := *task
	expiredTask.PrivateData.ExpiresAt = now - 1
	require.False(t, VerifyAsyncImageToken(token, &expiredTask))
}

func TestBuildSignedAsyncImageContentURLReturnsEmptyForInvalidTask(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	url, expiresAt := BuildSignedAsyncImageContentURL(&model.Task{
		TaskID: "task_missing_file",
		UserId: 1,
		PrivateData: model.TaskPrivateData{
			StoredAt:  time.Now().Unix(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	})

	require.Empty(t, url)
	require.Zero(t, expiresAt)
}

func TestSignedURLTTLIsCappedByContentExpiry(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_short_content_expiry",
		UserId: 42,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: "image.png",
			StoredAt:  now - 10,
			ExpiresAt: now + 60,
		},
	}

	_, urlExpiresAt := BuildSignedAsyncImageContentURL(task)

	require.InDelta(t, now+60, urlExpiresAt, 2)
}

func TestVerifyAsyncImageTokenRejectsExpiredTokenBeforeContentExpiry(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "async-image-test-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "1")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_short_token",
		UserId: 42,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: "image.png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}

	signedURL, _ := BuildSignedAsyncImageContentURL(task)
	token := signedURL[strings.LastIndex(signedURL, "token=")+len("token="):]
	time.Sleep(2 * time.Second)

	require.False(t, VerifyAsyncImageToken(token, task))
}

func TestAsyncImageSigningSecretEnvOverridesCryptoSecret(t *testing.T) {
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "fallback-secret"
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_TTL_SECONDS", "3600")
	t.Setenv("ASYNC_IMAGE_SIGNING_SECRET", "env-secret")
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID: "task_env_secret",
		UserId: 42,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: "image.png",
			StoredAt:  now - 10,
			ExpiresAt: now + 3600,
		},
	}

	signedURL, _ := BuildSignedAsyncImageContentURL(task)
	token := signedURL[strings.LastIndex(signedURL, "token=")+len("token="):]
	common.CryptoSecret = "different-fallback-secret"

	require.True(t, VerifyAsyncImageToken(token, task))
}

func TestAsyncImageTaskDataSummariesDoNotPersistInlinePayloads(t *testing.T) {
	largeInlineImage := "data:image/png;base64," + strings.Repeat("A", 4096)
	task := &model.Task{
		TaskID: "task_slim_submit",
		Action: constant.TaskActionGeminiImage,
		Properties: model.Properties{
			OriginModelName: "gemini-image-test",
			Input:           largeInlineImage,
		},
		PrivateData: model.TaskPrivateData{
			ResultURL: BuildAsyncImageContentURL("task_slim_submit"),
		},
	}

	for name, tc := range map[string]struct {
		body   []byte
		status string
	}{
		"submitted": {body: BuildPendingAsyncImageTaskData(task), status: dto.ImageAsyncStatusSubmitted},
		"failed":    {body: BuildFailedAsyncImageTaskData(task), status: dto.ImageAsyncStatusFailed},
	} {
		t.Run(name, func(t *testing.T) {
			body := tc.body
			require.NotEmpty(t, body)
			require.Less(t, len(body), 512)
			require.NotContains(t, string(body), "data:image")
			require.NotContains(t, string(body), "inlineData")
			require.NotContains(t, string(body), "b64_json")

			var payload map[string]string
			require.NoError(t, common.Unmarshal(body, &payload))
			require.Equal(t, "async_image.task", payload["object"])
			require.Equal(t, tc.status, payload["status"])
			require.Equal(t, constant.TaskActionGeminiImage, payload["action"])
			require.Equal(t, "gemini-image-test", payload["model"])
			require.Equal(t, BuildAsyncImageContentURL("task_slim_submit"), payload["result_url"])
		})
	}
}

func TestBuildStoredAsyncImageTaskDataKeepsOnlyResultURL(t *testing.T) {
	resultURL := BuildAsyncImageContentURL("task_slim_success")
	body := BuildStoredAsyncImageTaskData(dto.ImageResponse{
		Created: 123,
		Data: []dto.ImageData{{
			B64Json:       "data:image/png;base64," + strings.Repeat("B", 4096),
			RevisedPrompt: "clean prompt",
		}},
	}, resultURL)

	require.NotEmpty(t, body)
	require.Less(t, len(body), 512)
	require.NotContains(t, string(body), "data:image")
	require.NotContains(t, string(body), "b64_json")

	var payload dto.ImageResponse
	require.NoError(t, common.Unmarshal(body, &payload))
	require.Equal(t, int64(123), payload.Created)
	require.Len(t, payload.Data, 1)
	require.Equal(t, resultURL, payload.Data[0].Url)
	require.Equal(t, "clean prompt", payload.Data[0].RevisedPrompt)
	require.Empty(t, payload.Data[0].B64Json)
}

func TestCleanupExpiredAsyncImagesCompactsExpiredTerminalTaskData(t *testing.T) {
	truncate(t)
	setAsyncImageRetentionHoursForTest(t, "2")

	now := time.Now().Unix()
	expiredFile := writeAsyncImageTestFile(t, "expired.png")
	freshFile := writeAsyncImageTestFile(t, "fresh.png")
	largeData := []byte(`{"inlineData":{"mimeType":"image/png","data":"data:image/png;base64,` + strings.Repeat("A", 4096) + `"},"b64_json":"data:image/png;base64,` + strings.Repeat("B", 4096) + `"}`)

	expiredSuccess := &model.Task{
		TaskID:     "task_expired_success",
		UserId:     1,
		Platform:   constant.TaskPlatformInternalImage,
		Action:     constant.TaskActionGeminiImage,
		Status:     model.TaskStatusSuccess,
		FinishTime: now - 3600,
		Properties: model.Properties{
			OriginModelName: "gemini-image-test",
		},
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: expiredFile,
			StoredAt:  now - 7200,
			ExpiresAt: now - 1,
			ResultURL: BuildAsyncImageContentURL("task_expired_success"),
		},
		Data: largeData,
	}
	expiredFailure := &model.Task{
		TaskID:     "task_expired_failure",
		UserId:     1,
		Platform:   constant.TaskPlatformInternalImage,
		Action:     constant.TaskActionGeminiImage,
		Status:     model.TaskStatusFailure,
		FinishTime: now - 3*3600,
		Properties: model.Properties{
			OriginModelName: "gemini-image-test",
		},
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			ResultURL: BuildAsyncImageContentURL("task_expired_failure"),
		},
		Data: largeData,
	}
	freshSuccess := &model.Task{
		TaskID:     "task_fresh_success",
		UserId:     1,
		Platform:   constant.TaskPlatformInternalImage,
		Action:     constant.TaskActionGeminiImage,
		Status:     model.TaskStatusSuccess,
		FinishTime: now - 60,
		PrivateData: model.TaskPrivateData{
			AssetType: AsyncImageAssetType,
			LocalPath: freshFile,
			StoredAt:  now - 120,
			ExpiresAt: now + 3600,
			ResultURL: BuildAsyncImageContentURL("task_fresh_success"),
		},
		Data: largeData,
	}
	require.NoError(t, model.DB.Create(expiredSuccess).Error)
	require.NoError(t, model.DB.Create(expiredFailure).Error)
	require.NoError(t, model.DB.Create(freshSuccess).Error)

	require.NoError(t, CleanupExpiredAsyncImages())

	require.NoFileExists(t, expiredFile)
	require.FileExists(t, freshFile)
	assertTaskDataCompacted(t, "task_expired_success")
	assertTaskDataCompacted(t, "task_expired_failure")

	var fresh model.Task
	require.NoError(t, model.DB.Where("task_id = ?", "task_fresh_success").First(&fresh).Error)
	require.Contains(t, string(fresh.Data), "data:image")
}

func setAsyncImageRetentionHoursForTest(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	original := common.OptionMap[common.AsyncImageRetentionHoursOptionKey]
	common.OptionMap[common.AsyncImageRetentionHoursOptionKey] = value
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if original == "" {
			delete(common.OptionMap, common.AsyncImageRetentionHoursOptionKey)
		} else {
			common.OptionMap[common.AsyncImageRetentionHoursOptionKey] = original
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func writeAsyncImageTestFile(t *testing.T, name string) string {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + name
	require.NoError(t, os.WriteFile(path, []byte("image"), 0600))
	return path
}

func assertTaskDataCompacted(t *testing.T, taskID string) {
	t.Helper()
	var task model.Task
	require.NoError(t, model.DB.Where("task_id = ?", taskID).First(&task).Error)
	require.Less(t, len(task.Data), 512)
	require.NotContains(t, string(task.Data), "data:image")
	require.NotContains(t, string(task.Data), "inlineData")
	require.NotContains(t, string(task.Data), "b64_json")
}

func tamperToken(token string) string {
	if strings.HasSuffix(token, "x") {
		return token[:len(token)-1] + "y"
	}
	return token[:len(token)-1] + "x"
}
