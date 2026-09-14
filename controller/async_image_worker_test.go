package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
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
	originalRedisEnabled := common.RedisEnabled
	originalErrorLogEnabled := constant.ErrorLogEnabled

	common.RedisEnabled = false
	constant.ErrorLogEnabled = true

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	var dialect gorm.Dialector = sqlite.Open(dsn)
	originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	if externalDSN := os.Getenv("NATIVE_BILLING_TEST_DSN"); externalDSN != "" {
		require.Contains(t, externalDSN, "native_billing_test", "only a dedicated test database is allowed")
		switch os.Getenv("NATIVE_BILLING_TEST_DIALECT") {
		case "mysql":
			dialect = mysql.Open(externalDSN)
			common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
		case "postgres":
			dialect = postgres.Open(externalDSN)
			common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		default:
			t.Fatal("unsupported test dialect")
		}
	}
	t.Cleanup(func() { common.SetDatabaseTypes(originalMainType, originalLogType) })
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.Task{}))
	model.DB = db
	model.LOG_DB = db
	if os.Getenv("NATIVE_BILLING_TEST_DSN") != "" {
		for _, table := range []string{"logs", "tasks", "tokens", "users", "channels", "quota_data"} {
			if db.Migrator().HasTable(table) {
				require.NoError(t, db.Exec("DELETE FROM "+table).Error)
			}
		}
	}

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		model.DB = originalDB
		model.LOG_DB = originalLogDB
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
	}, apiErr, []string{"28", "36", "36", "36"}, []internalAsyncChannelRetryDetail{
		{
			Attempt:     1,
			ChannelID:   28,
			ChannelName: "first-banana",
			ChannelType: constant.ChannelTypeGemini,
			Status:      "error",
			StatusCode:  http.StatusBadGateway,
			ErrorType:   "openai_error",
			ErrorCode:   "bad_gateway",
			Error:       "status_code=502, upstream unavailable",
			AttemptedAt: time.Now().Unix(),
		},
		{
			Attempt:     2,
			ChannelID:   36,
			ChannelName: "XGJ-banana",
			ChannelType: constant.ChannelTypeGemini,
			Status:      "error",
			StatusCode:  http.StatusUnavailableForLegalReasons,
			ErrorType:   "openai_error",
			ErrorCode:   "451",
			Error:       "status_code=451, unsafe image",
			AttemptedAt: time.Now().Unix(),
		},
	})

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
	require.Equal(t, []interface{}{"28", "36", "36", "36"}, other["admin_info"].(map[string]interface{})["async_channel_retry_path"])
	retryDetails, ok := other["admin_info"].(map[string]interface{})["async_channel_retry_details"].([]interface{})
	require.True(t, ok)
	require.Len(t, retryDetails, 2)
	firstDetail, ok := retryDetails[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(28), firstDetail["channel_id"])
	require.Equal(t, "first-banana", firstDetail["channel_name"])
	require.Contains(t, firstDetail["error"], "upstream unavailable")
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

func TestImageAdapterChannelUpdateValidatesEffectiveSettings(t *testing.T) {
	const enabled = `{"image_adapter":{"enabled":true,"request":{"generations_path":"/v1/images/generations"}}}`
	const passthrough = `{"pass_through_body_enabled":true,"image_adapter":{"enabled":true,"request":{"generations_path":"/v1/images/generations"}}}`
	for _, tc := range []struct {
		name          string
		originType    int
		originSetting string
		fields        map[string]any
		wantType      int
		wantError     string
	}{
		{name: "reject type-only move with retained adapter", originType: 1, originSetting: enabled, fields: map[string]any{"id": 136, "type": 24}, wantError: "supports only OpenAI"},
		{name: "settings-only patch uses stored type", originType: 1, originSetting: "{}", fields: map[string]any{"id": 136, "setting": enabled}, wantType: 1},
		{name: "settings-only patch cannot enable unsupported channel", originType: 24, originSetting: "{}", fields: map[string]any{"id": 136, "setting": enabled}, wantError: "supports only OpenAI"},
		{name: "explicitly clear adapter while moving type", originType: 1, originSetting: enabled, fields: map[string]any{"id": 136, "type": 24, "setting": "{}"}, wantType: 24},
		{name: "null settings retain adapter during type change", originType: 1, originSetting: enabled, fields: map[string]any{"id": 136, "type": 24, "setting": nil}, wantError: "supports only OpenAI"},
		{name: "settings-only patch still rejects passthrough conflict", originType: 1, originSetting: enabled, fields: map[string]any{"id": 136, "setting": passthrough}, wantError: "pass_through_body_enabled"},
		{name: "unrelated patch retains ordinary zero-value semantics", originType: 1, originSetting: enabled, fields: map[string]any{"id": 136, "name": "renamed"}, wantType: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := model.Channel{Id: 136, Type: tc.originType, Setting: common.GetPointer(tc.originSetting)}
			body, err := common.Marshal(tc.fields)
			require.NoError(t, err)
			var patch PatchChannel
			require.NoError(t, common.Unmarshal(body, &patch))
			err = validateImageAdapterChannelUpdate(&patch.Channel, &origin, tc.fields)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantType, patch.Type)
			require.NoError(t, validateChannel(&patch.Channel, false))
			require.Equal(t, tc.originType, origin.Type)
			require.Equal(t, tc.originSetting, *origin.Setting)
		})
	}
}

// Exercise the production adoption -> durable reload -> image store -> billing
// chain with an already reserved balance and a real HTTP upstream.
func TestNativeAsyncImageFinalBilling(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		invalid     bool
		wantSuccess bool
		configured  bool
	}{
		{name: "upstream_suspended", status: "failed"},
		{name: "valid_image", status: "succeeded", wantSuccess: true},
		{name: "invalid_image_bytes", status: "succeeded", invalid: true},
		{name: "configured_adapter_image", status: "done", configured: true, wantSuccess: true},
		{name: "configured_adapter_string_failure", status: "broken", configured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.QuotaData{}))
			oldBatch, oldLog := common.BatchUpdateEnabled, common.LogConsumeEnabled
			common.BatchUpdateEnabled = false
			common.LogConsumeEnabled = true
			t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch; common.LogConsumeEnabled = oldLog })
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "native_billing_test", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "native-user-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			var posts, gets int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					w.WriteHeader(500)
					return
				}
				gets++
				if r.Header.Get("Authorization") != "Bearer native-upstream-key" {
					w.WriteHeader(401)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "image/png")
					if tc.invalid {
						_, _ = w.Write([]byte("this is not an image"))
					} else {
						_, _ = w.Write(imageBytes.Bytes())
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.configured {
					if r.URL.Path != "/configured/upstream-test/status" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					_, _ = fmt.Fprintf(w, `{"job":{"state":%q,"error":"adapter rejected request native-upstream-key","output":{"image":%q}}}`, tc.status, base64.StdEncoding.EncodeToString(imageBytes.Bytes()))
					return
				}
				_, _ = fmt.Fprintf(w, `{"status":%q,"error":{"message":"invalid image_url: upload init failed: You are suspended for violating our user agreement."}}`, tc.status)
			}))
			defer upstream.Close()
			base := upstream.URL
			require.NoError(t, db.Create(&model.Channel{Id: 63, Name: "native-test", Type: 1, Key: "native-upstream-key", BaseURL: &base, Status: 1}).Error)
			snapshot := &relay.NativeAsyncImageSnapshot{ID: "upstream-test", PollURL: base + "/task", ContentURL: base + "/task/content", ChannelID: 63, KeyFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte("native-upstream-key")))}
			if tc.configured {
				snapshot.PollURL = base + "/configured/upstream-test/status"
				snapshot.ContentURL = ""
				snapshot.Task = &dto.ImageAdapterTaskConfig{
					IDPaths:         []string{"job.id"},
					StatusPath:      "job.state",
					ResultB64Path:   "job.output.image",
					ErrorPath:       "job.error",
					PollURLTemplate: "/configured/{task_id}/status",
					PendingStates:   []string{"working"},
					SuccessStates:   []string{"done"},
					FailureStates:   []string{"broken"},
				}
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			c.Set("token_name", "native-test")
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: "dou-5-0-pro", FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 63}}
			// Reservation has already happened in the request BillingSession. Adoption
			// must preserve it without posting a second reserve or a consumption log.
			require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "dou-5-0-pro"}, snapshot))
			require.Equal(t, http.StatusOK, recorder.Code)
			var task model.Task
			require.NoError(t, db.First(&task).Error)
			require.Equal(t, service.TaskBillingStatePending, task.PrivateData.BillingState)
			require.Equal(t, 150000, task.Quota)
			var consumes int64
			require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			require.Zero(t, consumes)
			saved, err := os.ReadFile(task.PrivateData.RequestBodyPath)
			require.NoError(t, err)
			require.NotContains(t, string(saved), "native-upstream-key")
			if tc.configured {
				var reloadedSnapshot relay.NativeAsyncImageSnapshot
				require.NoError(t, common.Unmarshal(saved, &reloadedSnapshot))
				require.Equal(t, snapshot.Task, reloadedSnapshot.Task)
				require.Equal(t, snapshot.PollURL, reloadedSnapshot.PollURL)
				require.NotEqual(t, snapshot.ID, task.TaskID)
			}
			// Reloaded state, with no process-local native task registration, is enough.
			require.True(t, claimInternalAsyncImageTask(&task))
			err = executeInternalAsyncImageTask(context.Background(), &task)
			if tc.wantSuccess {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, db.First(&task, task.ID).Error)
			var user model.User
			require.NoError(t, db.First(&user, 61).Error)
			var token model.Token
			require.NoError(t, db.First(&token, 62).Error)
			require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			if tc.wantSuccess {
				require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
				require.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
				require.Equal(t, 850000, user.Quota)
				require.Equal(t, 850000, token.RemainQuota)
				require.EqualValues(t, 1, consumes)
				stored, err := os.ReadFile(task.PrivateData.LocalPath)
				require.NoError(t, err)
				require.Equal(t, imageBytes.Bytes(), stored)
				require.NoError(t, service.ConfirmTaskBillingSettled(context.Background(), &task, 150000, "duplicate terminal notification"))
			} else {
				require.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				require.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
				require.Equal(t, 1000000, user.Quota)
				require.Equal(t, 1000000, token.RemainQuota)
				require.Zero(t, consumes)
				if tc.configured {
					require.Equal(t, "adapter rejected request [redacted]", task.FailReason)
					require.NotContains(t, task.FailReason, "native-upstream-key")
				}
				require.NoError(t, service.RefundTaskQuota(context.Background(), &task, "duplicate terminal notification"))
			}
			var reloadedUser model.User
			require.NoError(t, db.First(&reloadedUser, 61).Error)
			require.Equal(t, user.Quota, reloadedUser.Quota)
			var reloadedToken model.Token
			require.NoError(t, db.First(&reloadedToken, 62).Error)
			require.Equal(t, token.RemainQuota, reloadedToken.RemainQuota)
			require.Equal(t, token.UsedQuota, reloadedToken.UsedQuota)
			var finalConsumes int64
			require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&finalConsumes).Error)
			require.Equal(t, consumes, finalConsumes)
			require.Zero(t, posts)
			require.Positive(t, gets)
			if tc.configured {
				require.Equal(t, 1, gets)
			}
		})
	}
}
