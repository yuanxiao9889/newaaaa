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
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupImageTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalSQLite := common.UsingSQLite
	originalMySQL := common.UsingMySQL
	originalPostgreSQL := common.UsingPostgreSQL

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

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
		common.UsingSQLite = originalSQLite
		common.UsingMySQL = originalMySQL
		common.UsingPostgreSQL = originalPostgreSQL
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
