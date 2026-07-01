package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupVideoProxyTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalSQLite := common.UsingSQLite
	originalMySQL := common.UsingMySQL
	originalPostgreSQL := common.UsingPostgreSQL
	originalMemoryCacheEnabled := common.MemoryCacheEnabled

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false
	service.InitHttpClient()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}))
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
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	return db
}

func allowLocalVideoProxyFetches(t *testing.T) {
	t.Helper()

	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = true
	fetchSetting.AllowedPorts = nil

	t.Cleanup(func() {
		*fetchSetting = original
	})
}

func TestVideoProxyOpenAIUsesStoredResultURLBeforeUpstreamContent(t *testing.T) {
	db := setupVideoProxyTestDB(t)
	allowLocalVideoProxyFetches(t)

	var resultHits atomic.Int32
	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resultHits.Add(1)
		require.Equal(t, "/saved-video.mp4", r.URL.Path)
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("saved-video-bytes"))
	}))
	defer resultServer.Close()

	var upstreamHits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		http.Error(w, "upstream content should not be used", http.StatusForbidden)
	}))
	defer upstreamServer.Close()

	baseURL := upstreamServer.URL
	channel := model.Channel{
		Id:      351,
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-test-secret",
		BaseURL: &baseURL,
		Name:    "grok-compatible",
	}
	require.NoError(t, db.Create(&channel).Error)

	task := model.Task{
		TaskID:    "task_saved_result",
		UserId:    61,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "upstream_task_saved_result",
			ResultURL:      resultServer.URL + "/saved-video.mp4",
		},
	}
	require.NoError(t, db.Create(&task).Error)

	recorder := performVideoProxyRequest(task.UserId, task.TaskID)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	require.Equal(t, "saved-video-bytes", recorder.Body.String())
	require.Equal(t, int32(1), resultHits.Load())
	require.Equal(t, int32(0), upstreamHits.Load())
}

func TestVideoProxyOpenAIFallsBackToUpstreamContentWhenResultURLMissing(t *testing.T) {
	db := setupVideoProxyTestDB(t)
	allowLocalVideoProxyFetches(t)

	var upstreamHits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		require.Equal(t, "/v1/videos/upstream_task_missing_result/content", r.URL.Path)
		require.Equal(t, "Bearer sk-test-secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("upstream-video-bytes"))
	}))
	defer upstreamServer.Close()

	baseURL := upstreamServer.URL
	channel := model.Channel{
		Id:      352,
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-test-secret",
		BaseURL: &baseURL,
		Name:    "openai-compatible",
	}
	require.NoError(t, db.Create(&channel).Error)

	task := model.Task{
		TaskID:    "task_missing_result",
		UserId:    61,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "upstream_task_missing_result",
		},
	}
	require.NoError(t, db.Create(&task).Error)

	recorder := performVideoProxyRequest(task.UserId, task.TaskID)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	require.Equal(t, "upstream-video-bytes", recorder.Body.String())
	require.Equal(t, int32(1), upstreamHits.Load())
}

func performVideoProxyRequest(userID int, taskID string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", userID)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+taskID+"/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: taskID}}

	VideoProxy(c)

	return recorder
}
