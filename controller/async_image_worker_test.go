package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAsyncImageWorkerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalSQLite := common.UsingSQLite
	originalMySQL := common.UsingMySQL
	originalPostgreSQL := common.UsingPostgreSQL
	originalRedisEnabled := common.RedisEnabled
	originalErrorLogEnabled := constant.ErrorLogEnabled

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	constant.ErrorLogEnabled = true

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.Task{}))
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.UsingSQLite = originalSQLite
		common.UsingMySQL = originalMySQL
		common.UsingPostgreSQL = originalPostgreSQL
		common.RedisEnabled = originalRedisEnabled
		constant.ErrorLogEnabled = originalErrorLogEnabled
	})

	return db
}

func TestBuildInternalAsyncBaseContextSuppressesPerAttemptErrorLogs(t *testing.T) {
	setupAsyncImageWorkerTestDB(t)

	task := &model.Task{
		TaskID: "task_async_suppress",
		UserId: 123,
		Group:  "default",
		Properties: model.Properties{
			OriginModelName: "monkey-image-flash 2",
		},
		PrivateData: model.TaskPrivateData{
			RequestMethod:      http.MethodPost,
			RequestPath:        "/v1beta/models/monkey-image-flash 2:generateContent",
			RequestContentType: "application/json",
			TokenId:            456,
		},
	}

	c, _, err := buildInternalAsyncBaseContext(context.Background(), task, []byte(`{"contents":[]}`))

	require.NoError(t, err)
	require.True(t, c.GetBool(suppressRelayErrorLogContextKey))
	require.True(t, c.GetBool(asyncImageWorkerContextKey))
}

func TestRecordInternalAsyncImageFinalErrorLogStoresRetryPathAndIdentity(t *testing.T) {
	db := setupAsyncImageWorkerTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 397, Username: "lf", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 370, UserId: 397, Name: "lf", Status: common.TokenStatusEnabled}).Error)

	task := &model.Task{
		TaskID:    "task_synS8LUXsVjHDlf48DzIvDijhZZlUshG",
		UserId:    397,
		Group:     "default",
		StartTime: time.Now().Add(-45 * time.Second).Unix(),
		Properties: model.Properties{
			OriginModelName: "monkey-image-flash 2",
		},
		PrivateData: model.TaskPrivateData{
			TokenId:     370,
			RequestPath: "/v1beta/models/monkey-image-flash 2:generateContent",
		},
	}
	c, _, err := buildInternalAsyncBaseContext(context.Background(), task, []byte(`{"contents":[]}`))
	require.NoError(t, err)

	apiErr := types.NewErrorWithStatusCode(
		fmt.Errorf("The generated images appear to be unsafe. Try modifying the prompts or the seeds."),
		"451",
		http.StatusUnavailableForLegalReasons,
	)
	recordInternalAsyncImageFinalErrorLog(c, task, &model.Channel{
		Id:   36,
		Name: "XGJ-banana",
		Type: constant.ChannelTypeGemini,
	}, apiErr, []string{"28", "36", "36", "36"})

	var logs []model.Log
	require.NoError(t, db.Find(&logs).Error)
	require.Len(t, logs, 1)

	log := logs[0]
	require.Equal(t, model.LogTypeError, log.Type)
	require.Equal(t, 397, log.UserId)
	require.Equal(t, "lf", log.Username)
	require.Equal(t, 370, log.TokenId)
	require.Equal(t, "lf", log.TokenName)
	require.Equal(t, 36, log.ChannelId)
	require.Equal(t, "monkey-image-flash 2", log.ModelName)
	require.Contains(t, log.Content, "retry: 28 -> 36 -> 36 -> 36")
	require.Equal(t, task.TaskID, log.RequestId)

	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	require.Equal(t, true, other["async_task"])
	require.Equal(t, true, other["is_task"])
	require.Equal(t, task.TaskID, other["task_id"])
	require.Equal(t, []interface{}{"28", "36", "36", "36"}, other["async_channel_retry_path"])
	require.Equal(t, "/v1beta/models/monkey-image-flash 2:generateContent", other["request_path"])
}

func TestFailInternalAsyncImageTaskCompactsStoredData(t *testing.T) {
	db := setupAsyncImageWorkerTestDB(t)
	largeInlinePayload := "data:image/png;base64," + strings.Repeat("A", 4096)
	task := &model.Task{
		TaskID:     "task_async_compact_failure",
		UserId:     61,
		Group:      "default",
		Action:     constant.TaskActionGeminiImage,
		Status:     model.TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: time.Now().Add(-time.Minute).Unix(),
		StartTime:  time.Now().Add(-30 * time.Second).Unix(),
		Properties: model.Properties{
			OriginModelName: "gemini-image-test",
			Input:           largeInlinePayload,
		},
		PrivateData: model.TaskPrivateData{
			InternalAsync: true,
			AssetType:     service.AsyncImageAssetType,
			ResultURL:     service.BuildAsyncImageContentURL("task_async_compact_failure"),
		},
		Data: []byte(`{"inlineData":{"mimeType":"image/png","data":"` + largeInlinePayload + `"},"b64_json":"` + largeInlinePayload + `"}`),
	}
	require.NoError(t, db.Create(task).Error)

	failInternalAsyncImageTask(context.Background(), task, "upstream rejected image")

	var stored model.Task
	require.NoError(t, db.Where("task_id = ?", task.TaskID).First(&stored).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusFailure), stored.Status)
	require.Contains(t, stored.FailReason, "upstream rejected image")
	require.Less(t, len(stored.Data), 512)
	require.NotContains(t, string(stored.Data), "data:image")
	require.NotContains(t, string(stored.Data), "inlineData")
	require.NotContains(t, string(stored.Data), "b64_json")

	var payload map[string]string
	require.NoError(t, common.Unmarshal(stored.Data, &payload))
	require.Equal(t, "async_image.task", payload["object"])
	require.Equal(t, dto.ImageAsyncStatusFailed, payload["status"])
	require.Equal(t, constant.TaskActionGeminiImage, payload["action"])
	require.Equal(t, "gemini-image-test", payload["model"])
	require.Equal(t, service.BuildAsyncImageContentURL("task_async_compact_failure"), payload["result_url"])
}
