package service

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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

func tamperToken(token string) string {
	if strings.HasSuffix(token, "x") {
		return token[:len(token)-1] + "y"
	}
	return token[:len(token)-1] + "x"
}
