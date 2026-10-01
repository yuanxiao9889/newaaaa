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
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
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
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
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
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.Task{}))
	model.DB = db
	model.LOG_DB = db
	t.Setenv("LOG_SQL_DSN", "")
	originalMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = originalMaster
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
		common.SetDatabaseTypes(originalMainType, originalLogType)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = originalMaster
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

func imageRequestFilterTestChannel(t *testing.T) *model.Channel {
	t.Helper()
	// These are the actual 4K sizes sent by Storyboard Copilot, including square
	// and ultrawide canvases whose longest edge need not be 3840 pixels.
	sizes := []string{
		"2880x2880", "3200x2560", "2160x3840", "3696x1584", "1584x3696",
		"1280x3840", "3840x1280", "1920x3840", "3840x1920", "3840x2160",
		"3264x2448", "3504x2336", "2560x3200", "2448x3264", "2336x3504",
	}
	settings := kitdto.ChannelSettings{ImageRequestFilter: &kitdto.ImageRequestFilterConfig{
		Enabled: true,
		Rules: []kitdto.ImageRequestRule{
			{Models: []string{"gpt-image-2"}, Qualities: []string{"medium", "high"}, Sizes: sizes},
			{Models: []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"}, Qualities: []string{"high", "xhigh", "max"}, Sizes: sizes},
		},
	}}
	encoded, err := common.Marshal(settings)
	require.NoError(t, err)
	return &model.Channel{
		Id: 136, Type: constant.ChannelTypeOpenAI, Name: "image-filter-test", Key: "test-only",
		Models: "gpt-image-2,gpt-image-2.5-sunburst,gpt-image-2.5-flare,image-alias,other-image",
		Group:  "image_filter_test", Status: common.ChannelStatusEnabled,
		Priority: common.GetPointer(int64(10)), Weight: common.GetPointer(uint(100)),
		Setting: common.GetPointer(string(encoded)),
	}
}

func TestImageRequestChannelFilterRequests(t *testing.T) {
	channel := imageRequestFilterTestChannel(t)
	for _, rule := range channel.GetSetting().ImageRequestFilter.Rules {
		for _, modelName := range rule.Models {
			for _, quality := range []string{"low", "medium", "high", "xhigh", "max", "auto"} {
				for _, size := range rule.Sizes {
					body, err := common.Marshal(map[string]any{"model": modelName, "quality": quality, "size": size})
					require.NoError(t, err)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", bytes.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")
					service.AppendImageRequestFilter(c)
					accepted, _ := model.ChannelSatisfiesFilters(channel, modelName, service.GetChannelConstraints(c).Filters)
					want := quality == "high" || modelName == "gpt-image-2" && quality == "medium" || modelName != "gpt-image-2" && (quality == "xhigh" || quality == "max")
					assert.Equal(t, want, accepted, "model=%s quality=%s size=%s", modelName, quality, size)
					common.CleanupBodyStorage(c)
				}
			}
		}
	}

	for _, tc := range []struct {
		name, body, modelName, mapping, setting string
		accepted                                bool
	}{
		{name: "1K excluded", body: `{"quality":"high","size":"1024x1024"}`},
		{name: "2K excluded", body: `{"quality":"high","size":"2048x2048"}`},
		{name: "2K ultrawide excluded", body: `{"quality":"high","size":"3072x1024"}`},
		{name: "custom dimensions excluded", body: `{"quality":"high","size":"3008x2736"}`},
		{name: "4K label is not pixels", body: `{"quality":"high","size":"4K"}`},
		{name: "missing quality", body: `{"size":"2880x2880"}`},
		{name: "missing size", body: `{"quality":"high"}`},
		{name: "automatic quality excluded", body: `{"quality":"auto","size":"2880x2880"}`},
		{name: "automatic size excluded", body: `{"quality":"high","size":"auto"}`},
		{name: "null quality", body: `{"quality":null,"size":"2880x2880"}`},
		{name: "non string size", body: `{"quality":"high","size":2880}`},
		{name: "duplicate quality", body: `{"quality":"low","quality":"high","size":"2880x2880"}`},
		{name: "duplicate size", body: `{"quality":"high","size":"1024x1024","size":"2880x2880"}`},
		{name: "case variant duplicate", body: `{"quality":"high","Quality":"low","size":"2880x2880"}`},
		{name: "escaped duplicate", body: `{"quality":"high","qualit\u0079":"low","size":"2880x2880"}`},
		{name: "Unicode case folded duplicate", body: `{"quality":"high","size":"2880x2880","ſize":"1024x1024"}`},
		{name: "conflicting model case cannot bypass a rule", body: `{"model":"gpt-image-2","Model":"other-image","quality":"low","size":"2880x2880"}`, modelName: "other-image"},
		{name: "duplicate model cannot bypass a rule", body: `{"model":"gpt-image-2","model":"other-image","quality":"low","size":"2880x2880"}`, modelName: "other-image"},
		{name: "nested values are not top level", body: `{"parameters":{"quality":"high","size":"2880x2880"}}`},
		{name: "malformed JSON", body: `{"quality":"high","size":"2880x2880"`},
		{name: "unrelated model unaffected", body: `{}`, modelName: "other-image", accepted: true},
		{name: "disabled filter unaffected", body: `{}`, setting: `{"image_request_filter":{"enabled":false}}`, accepted: true},
		{name: "unconfigured channel unaffected", body: `{}`, setting: `{}`, accepted: true},
		{name: "malformed setting fails closed", body: `{"quality":"high","size":"2880x2880"}`, setting: `{"image_request_filter":`},
		{name: "alias chain cannot bypass", body: `{"quality":"medium","size":"2880x2880"}`, modelName: "image-alias", mapping: `{"image-alias":"internal-image","internal-image":"gpt-image-2.5-flare"}`},
		{name: "allowed alias chain", body: `{"quality":"max","size":"2880x2880"}`, modelName: "image-alias", mapping: `{"image-alias":"internal-image","internal-image":"gpt-image-2.5-flare"}`, accepted: true},
		{name: "mapped away protected model remains protected", body: `{"quality":"low","size":"2880x2880"}`, mapping: `{"gpt-image-2":"upstream-image"}`},
		{name: "cyclic alias fails closed", body: `{"quality":"high","size":"2880x2880"}`, modelName: "image-alias", mapping: `{"image-alias":"internal-image","internal-image":"image-alias"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *channel
			if tc.setting != "" {
				candidate.Setting = &tc.setting
			}
			if tc.mapping != "" {
				candidate.ModelMapping = &tc.mapping
			}
			modelName := tc.modelName
			if modelName == "" {
				modelName = "gpt-image-2"
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(c)
			originalSetting := *candidate.Setting
			service.AppendImageRequestFilter(c)
			accepted, _ := model.ChannelSatisfiesFilters(&candidate, modelName, service.GetChannelConstraints(c).Filters)
			assert.Equal(t, tc.accepted, accepted)
			assert.Equal(t, originalSetting, *candidate.Setting, "filter evaluation must not erase an invalid setting")
		})
	}

	for _, tc := range []struct {
		name                  string
		fields                [][2]string
		fileField, routeModel string
		accepted              bool
	}{
		{name: "valid edit", fields: [][2]string{{"quality", "high"}, {"size", "2880x2880"}}, accepted: true},
		{name: "multiple quality fields", fields: [][2]string{{"quality", "high"}, {"quality", "low"}, {"size", "2880x2880"}}},
		{name: "multiple size fields", fields: [][2]string{{"quality", "high"}, {"size", "2880x2880"}, {"size", "1024x1024"}}},
		{name: "Unicode size field is ambiguous", fields: [][2]string{{"quality", "high"}, {"size", "2880x2880"}, {"ſize", "1024x1024"}}},
		{name: "conflicting model fields cannot bypass a rule", fields: [][2]string{{"model", "other-image"}, {"quality", "low"}, {"size", "2880x2880"}}, routeModel: "other-image"},
		{name: "quality file is ambiguous", fields: [][2]string{{"quality", "high"}, {"size", "2880x2880"}}, fileField: "quality"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("model", "gpt-image-2"))
			for _, field := range tc.fields {
				require.NoError(t, writer.WriteField(field[0], field[1]))
			}
			fileField := tc.fileField
			if fileField == "" {
				fileField = "image"
			}
			file, err := writer.CreateFormFile(fileField, "input.png")
			require.NoError(t, err)
			_, err = file.Write([]byte("fixture-image-content"))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits?async=true", bytes.NewReader(body.Bytes()))
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			defer common.CleanupBodyStorage(c)
			service.AppendImageRequestFilter(c)
			modelName := tc.routeModel
			if modelName == "" {
				modelName = "gpt-image-2"
			}
			accepted, _ := model.ChannelSatisfiesFilters(channel, modelName, service.GetChannelConstraints(c).Filters)
			assert.Equal(t, tc.accepted, accepted)
			if tc.accepted {
				form, err := common.ParseMultipartFormReusable(c)
				require.NoError(t, err)
				assert.Equal(t, []string{"high"}, form.Value["quality"])
				require.Len(t, form.File["image"], 1)
				part, err := form.File["image"][0].Open()
				require.NoError(t, err)
				defer part.Close()
				content, err := io.ReadAll(part)
				require.NoError(t, err)
				assert.Equal(t, "fixture-image-content", string(content), "admission must preserve reference image bytes")
				task := &model.Task{TaskID: "multipart-filter-worker", Action: constant.TaskActionImageEdit, Properties: model.Properties{OriginModelName: "gpt-image-2"}, PrivateData: model.TaskPrivateData{RequestMethod: http.MethodPost, RequestPath: "/v1/images/edits", RequestContentType: writer.FormDataContentType()}}
				worker, _, imageRequest, _, err := buildInternalAsyncImageContext(context.Background(), task, body.Bytes())
				require.NoError(t, err)
				defer common.CleanupBodyStorage(worker)
				assert.Equal(t, "high", imageRequest.Quality)
				assert.Equal(t, "2880x2880", imageRequest.Size)
				accepted, _ = model.ChannelSatisfiesFilters(channel, imageRequest.Model, service.GetChannelConstraints(worker).Filters)
				assert.True(t, accepted, "recovered edits must retain their original admission parameters")
				require.NotNil(t, worker.Request.MultipartForm)
				require.Len(t, worker.Request.MultipartForm.File["image"], 1)
				workerFile, err := worker.Request.MultipartForm.File["image"][0].Open()
				require.NoError(t, err)
				defer workerFile.Close()
				workerContent, err := io.ReadAll(workerFile)
				require.NoError(t, err)
				assert.Equal(t, content, workerContent, "worker reconstruction must preserve the reference image")
			}
		})
	}
	for _, tc := range []struct {
		body     string
		accepted bool
	}{
		{body: "model=gpt-image-2&quality=medium&size=2880x2880", accepted: true},
		{body: "model=gpt-image-2&quality=medium&quality=low&size=2880x2880"},
		{body: "model=gpt-image-2&quality=high&size=2880x2880&size=1024x1024"},
		{body: "model=gpt-image-2&quality=high&size=2880x2880&%C5%BFize=1024x1024"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		service.AppendImageRequestFilter(c)
		accepted, _ := model.ChannelSatisfiesFilters(channel, "gpt-image-2", service.GetChannelConstraints(c).Filters)
		assert.Equal(t, tc.accepted, accepted, tc.body)
		common.CleanupBodyStorage(c)
	}
}

func TestImageRequestChannelFilterSettingsAndMapping(t *testing.T) {
	for _, tc := range []struct {
		name, config string
	}{
		{name: "empty rules", config: `{"enabled":true,"rules":[]}`},
		{name: "duplicate model", config: `{"enabled":true,"rules":[{"models":["gpt-image-2","gpt-image-2"],"qualities":["high"],"sizes":["2880x2880"]}]}`},
		{name: "automatic quality cannot guarantee admission", config: `{"enabled":true,"rules":[{"models":["gpt-image-2"],"qualities":["auto"],"sizes":["2880x2880"]}]}`},
		{name: "missing sizes", config: `{"enabled":true,"rules":[{"models":["gpt-image-2"],"qualities":["high"],"sizes":[]}]}`},
		{name: "size must be pixels", config: `{"enabled":true,"rules":[{"models":["gpt-image-2"],"qualities":["high"],"sizes":["4K"]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := imageRequestFilterTestChannel(t)
			channel.Setting = common.GetPointer(`{"image_request_filter":` + tc.config + `}`)
			assert.Error(t, validateChannel(channel, true), "creation must validate admission rules")
			assert.Error(t, validateChannel(channel, false), "updates must validate admission rules")
		})
	}
	for _, tc := range []struct {
		name, mapping, want string
		mapped, invalid     bool
	}{
		{name: "direct mapping", mapping: `{"image-alias":"gpt-image-2"}`, want: "gpt-image-2", mapped: true},
		{name: "mapping chain", mapping: `{"image-alias":"internal-image","internal-image":"gpt-image-2"}`, want: "gpt-image-2", mapped: true},
		{name: "terminal self mapping", mapping: `{"image-alias":"gpt-image-2","gpt-image-2":"gpt-image-2"}`, want: "gpt-image-2", mapped: true},
		{name: "original self mapping", mapping: `{"image-alias":"image-alias"}`, want: "image-alias"},
		{name: "cyclic mapping", mapping: `{"image-alias":"internal-image","internal-image":"image-alias"}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("model_mapping", tc.mapping)
			info := &relaycommon.RelayInfo{OriginModelName: "image-alias", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "image-alias"}}
			request := &kitdto.ImageRequest{Model: "image-alias"}
			err := helper.ModelMappedHelper(c, info, request)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, request.Model)
			assert.Equal(t, tc.want, info.UpstreamModelName)
			assert.Equal(t, tc.mapped, info.IsModelMapped)
		})
	}
}

func setupImageRequestFilterTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	if dsn != "" {
		require.Contains(t, dsn, "image_filter_test", "only a dedicated image filter test database is allowed")
	}
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousMemory, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "image_filter_test.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	model.LOG_DB = database
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB = previousDB
		common.SetDatabaseTypes(previousMain, previousLog)
		// InitLogDB also resets the dialect-specific quoted column names.
		require.NoError(t, model.InitLogDB())
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousMemory, previousRedis, previousSQLite
		if previousDB != nil && previousMemory && previousDB.Migrator().HasTable(&model.Channel{}) && previousDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
	})
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Token{}))
	// The caller supplies a disposable database; clear only this fixture's rows.
	require.NoError(t, database.Where("channel_id IN ?", []int{136, 137}).Delete(&model.Ability{}).Error)
	require.NoError(t, database.Where("id IN ?", []int{136, 137}).Delete(&model.Channel{}).Error)
	var version string
	query := "SELECT version()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, database.Raw(query).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
	return database
}

func TestImageRequestChannelRouting(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, dialect := range []struct{ name, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			database := setupImageRequestFilterTestDB(t, dsn)
			restricted := imageRequestFilterTestChannel(t)
			restricted.ModelMapping = common.GetPointer(`{"image-alias":"internal-image","internal-image":"gpt-image-2.5-flare"}`)
			require.NoError(t, validateChannel(restricted, true))
			require.NoError(t, restricted.Insert())
			fallback := *restricted
			fallback.Id, fallback.Name, fallback.Setting, fallback.Priority = 137, "unrestricted-fallback", common.GetPointer(`{}`), common.GetPointer(int64(0))
			require.NoError(t, fallback.Insert())
			// Restarting migration must preserve an existing channel's serialized rules.
			require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Token{}))
			var reloaded model.Channel
			require.NoError(t, database.First(&reloaded, restricted.Id).Error)
			assert.Equal(t, restricted.Setting, reloaded.Setting)
			for _, cacheEnabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("cache=%t", cacheEnabled), func(t *testing.T) {
					common.MemoryCacheEnabled = cacheEnabled
					model.InitChannelCache()
					for _, tc := range []struct {
						name, modelName, quality, size string
						want                           int
					}{
						{"gpt2 medium 4K", "gpt-image-2", "medium", "2880x2880", 136},
						{"gpt2 high 4K", "gpt-image-2", "high", "3840x2160", 136},
						{"gpt2 low 4K", "gpt-image-2", "low", "2880x2880", 137},
						{"gpt2 high 2K", "gpt-image-2", "high", "2048x2048", 137},
						{"sunburst medium 4K", "gpt-image-2.5-sunburst", "medium", "2880x2880", 137},
						{"sunburst extra high 4K", "gpt-image-2.5-sunburst", "xhigh", "3696x1584", 136},
						{"flare max 4K", "gpt-image-2.5-flare", "max", "1280x3840", 136},
						{"alias medium 4K", "image-alias", "medium", "2880x2880", 137},
						{"unrelated model", "other-image", "low", "1024x1024", 136},
					} {
						t.Run(tc.name, func(t *testing.T) {
							body, err := common.Marshal(map[string]any{"model": tc.modelName, "quality": tc.quality, "size": tc.size, "prompt": "routing fixture"})
							require.NoError(t, err)
							c, _ := gin.CreateTestContext(httptest.NewRecorder())
							c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", bytes.NewReader(body))
							c.Request.Header.Set("Content-Type", "application/json")
							defer common.CleanupBodyStorage(c)
							common.SetContextKey(c, constant.ContextKeyUsingGroup, restricted.Group)
							common.SetContextKey(c, constant.ContextKeyUserGroup, restricted.Group)
							middleware.Distribute()(c)
							require.False(t, c.IsAborted(), "initial distribution must find an eligible channel")
							assert.Equal(t, tc.want, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
							// Retry selection must retain the client's original parameters.
							retry := &service.RetryParam{Ctx: c, TokenGroup: restricted.Group, ModelName: tc.modelName, RequestPath: c.Request.URL.Path}
							retry.SetRetry(1)
							next, _, err := service.CacheGetRandomSatisfiedChannel(retry)
							require.NoError(t, err)
							require.NotNil(t, next)
							assert.Equal(t, 137, next.Id)
							// Reconstruct the worker from persisted request bytes, without
							// reusing the initial request's in-memory constraints.
							task := &model.Task{TaskID: "image-filter-worker", Group: restricted.Group, Properties: model.Properties{OriginModelName: tc.modelName}, PrivateData: model.TaskPrivateData{RequestMethod: http.MethodPost, RequestPath: "/v1/images/generations", RequestContentType: "application/json"}}
							worker, _, err := buildInternalAsyncBaseContext(context.Background(), task, body)
							require.NoError(t, err)
							defer common.CleanupBodyStorage(worker)
							selected, _, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Ctx: worker, TokenGroup: task.Group, ModelName: tc.modelName})
							require.NoError(t, err)
							require.NotNil(t, selected)
							assert.Equal(t, tc.want, selected.Id)
						})
					}
					for _, quality := range []string{"low", "high"} {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(fmt.Sprintf(`{"model":"gpt-image-2","quality":%q,"size":"2880x2880"}`, quality)))
						c.Request.Header.Set("Content-Type", "application/json")
						service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 136, Source: dto.PinSourceToken, Rank: dto.PinRankToken})
						middleware.Distribute()(c)
						assert.Equal(t, quality == "low", c.IsAborted(), "an explicit channel pin must still obey admission rules")
						common.CleanupBodyStorage(c)
					}
					ambiguous, _ := gin.CreateTestContext(httptest.NewRecorder())
					ambiguous.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","Model":"other-image","quality":"low","size":"2880x2880"}`))
					ambiguous.Request.Header.Set("Content-Type", "application/json")
					common.SetContextKey(ambiguous, constant.ContextKeyUsingGroup, restricted.Group)
					middleware.Distribute()(ambiguous)
					require.False(t, ambiguous.IsAborted())
					assert.Equal(t, 137, common.GetContextKeyInt(ambiguous, constant.ContextKeyChannelId), "conflicting model fields must exclude the restricted channel")
					common.CleanupBodyStorage(ambiguous)
				})
			}
			settings := reloaded.GetSetting()
			settings.ImageRequestFilter.Enabled = false
			encoded, err := common.Marshal(settings)
			require.NoError(t, err)
			update := &model.Channel{Id: restricted.Id, Setting: common.GetPointer(string(encoded))}
			require.NoError(t, validateChannel(update, false))
			require.NoError(t, update.Update())
			require.NoError(t, database.First(&reloaded, restricted.Id).Error)
			assert.False(t, reloaded.GetSetting().ImageRequestFilter.Enabled)
			assert.Equal(t, restricted.Models, reloaded.Models, "settings-only updates must preserve models")
			assert.Equal(t, restricted.Key, reloaded.Key, "settings-only updates must preserve credentials")
		})
	}
}

func TestAsyncImageRetry(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	for _, tc := range []struct {
		name, failure                                            string
		retries, wantPosts                                       int
		success, queued, backupFails, pinned, interrupted, cache bool
		resume, invalidResult, multipart                         bool
		pollStatus                                               int
	}{
		{name: "mapped native failover", failure: `{"code":"server_error","type":"provider_error","message":"temporary failure"}`, retries: 3, wantPosts: 1, success: true},
		{name: "queued request maps each attempt", failure: `"generation failed: generate error: An error occurred."`, retries: 3, wantPosts: 2, success: true, queued: true, cache: true},
		{name: "message only rate limit", failure: `"请求过于频繁,请稍后重试"`, retries: 3, wantPosts: 1, success: true},
		{name: "content rejection is permanent", failure: `{"code":"server_error","message":"生成图片未通过内容审核"}`, retries: 3},
		{name: "invalid parameter is permanent", failure: `{"status":503,"message":"invalid parameter size"}`, retries: 3},
		{name: "unknown failure is not guessed", failure: `"unrecognized failure"`, retries: 3},
		{name: "explicit no retry", failure: `{"code":"server_error","message":"failure","retryable":false}`, retries: 3},
		{name: "retry disabled", failure: `{"code":"rate_limit_exceeded","message":"busy"}`, retries: 0},
		{name: "retry limit and refund", failure: `{"status_code":503,"message":"busy"}`, retries: 1, wantPosts: 1, backupFails: true},
		{name: "no channel is attempted twice", failure: `{"code":"server_error","message":"busy"}`, retries: 3, wantPosts: 1, backupFails: true, cache: true},
		{name: "pinned channel cannot fail over", failure: `{"code":"server_error","message":"busy"}`, retries: 3, pinned: true},
		{name: "interrupted submission is not replayed", failure: `{"code":"server_error","message":"busy"}`, retries: 3, interrupted: true},
		{name: "accepted backup resumes even with exhausted budget", retries: 1, resume: true, success: true},
		{name: "result storage failure does not generate again", retries: 3, invalidResult: true},
		{name: "poll rejection does not generate again", retries: 3, pollStatus: 401},
		{name: "multipart reference survives failover", failure: `{"code":"server_error","message":"busy"}`, retries: 3, wantPosts: 1, success: true, multipart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			if tc.name == "mapped native failover" {
				query := "SELECT version()"
				if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
					query = "SELECT sqlite_version()"
				}
				var version string
				require.NoError(t, db.Raw(query).Scan(&version).Error)
				t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
			}
			if logDSN := os.Getenv("NATIVE_BILLING_TEST_LOG_DSN"); logDSN != "" {
				require.Contains(t, logDSN, "native_billing_test")
				var dialect gorm.Dialector = mysql.Open(logDSN)
				if os.Getenv("NATIVE_BILLING_TEST_DIALECT") == "postgres" {
					dialect = postgres.Open(logDSN)
				}
				logDB, err := gorm.Open(dialect, &gorm.Config{})
				require.NoError(t, err)
				require.NoError(t, logDB.AutoMigrate(&model.Log{}))
				require.NoError(t, logDB.Where("user_id = ?", 61).Delete(&model.Log{}).Error)
				model.LOG_DB = logDB
				t.Cleanup(func() {
					sqlDB, err := logDB.DB()
					require.NoError(t, err)
					require.NoError(t, sqlDB.Close())
					model.LOG_DB = db
				})
			}
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}))
			require.NoError(t, db.Where("channel_id IN ?", []int{701, 809}).Delete(&model.Ability{}).Error)
			oldRetry, oldMemory, oldBatch, oldLog := common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
			common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = tc.retries, tc.cache, false, true
			t.Cleanup(func() {
				common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRetry, oldMemory, oldBatch, oldLog
			})
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", t.TempDir())
			oldAddress := system_setting.ServerAddress
			system_setting.ServerAddress = "https://relay.test"
			t.Cleanup(func() { system_setting.ServerAddress = oldAddress })
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "native_billing_test", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "retry-user-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			var postedModels []string
			var requestChecks []bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					var request map[string]any
					if err := common.DecodeJson(r.Body, &request); err != nil {
						w.WriteHeader(400)
						return
					}
					mapped, _ := request["model"].(string)
					postedModels = append(postedModels, mapped)
					id := "backup"
					if mapped == "upstream-first" {
						id = "first"
					}
					requestChecks = append(requestChecks, request["detail_level"] == "high" && request["prompt"] == "fixture")
					if tc.multipart {
						references, ok := request["references"].([]any)
						requestChecks = append(requestChecks, ok && len(references) == 1)
					}
					fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, id)
					return
				}
				if tc.pollStatus != 0 {
					w.WriteHeader(tc.pollStatus)
					return
				}
				if tc.invalidResult {
					_, _ = io.WriteString(w, `{"status":"completed","data":[{"b64_json":"invalid image bytes"}]}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/first") || tc.backupFails {
					fmt.Fprintf(w, `{"status":"failed","error":%s}`, tc.failure)
					return
				}
				fmt.Fprintf(w, `{"status":"completed","data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(imageBytes.Bytes()))
			}))
			defer upstream.Close()
			config := &kitdto.ImageAdapterConfig{Enabled: true,
				Request:    kitdto.ImageAdapterRequestConfig{GenerationsPath: "/v1/images/generations", EditsPath: "/v1/images/generations", FieldMap: map[string]string{"quality": "detail_level"}},
				References: &kitdto.ImageAdapterReferenceConfig{Field: "references", Encoding: "url_array", MaxImages: 4, UploadLocal: true},
				Task:       &kitdto.ImageAdapterTaskConfig{IDPaths: []string{"id"}, StatusPath: "status", ErrorPath: "error", ResultB64Path: "data.0.b64_json", PollURLTemplate: "/jobs/{task_id}", PendingStates: []string{"queued"}, SuccessStates: []string{"completed"}, FailureStates: []string{"failed"}},
			}
			require.NoError(t, config.Validate())
			setting, err := common.Marshal(kitdto.ChannelSettings{ImageAdapter: config})
			require.NoError(t, err)
			for i, id := range []int{701, 809} {
				upstreamModel := "upstream-first"
				if i == 1 {
					upstreamModel = "upstream-backup"
				}
				mapping, err := common.Marshal(map[string]string{"custom-image-alias": upstreamModel})
				require.NoError(t, err)
				channel := &model.Channel{Id: id, Name: upstreamModel, Type: constant.ChannelTypeOpenAI, Key: "retry-upstream-key", Models: "custom-image-alias", Group: "default", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(5)), Weight: common.GetPointer(uint(1)), BaseURL: common.GetPointer(upstream.URL), Setting: common.GetPointer(string(setting)), ModelMapping: common.GetPointer(string(mapping))}
				if tc.queued && i == 1 {
					channel.Priority = common.GetPointer(int64(4))
				}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			if tc.cache {
				model.InitChannelCache()
			}
			body := `{"model":"custom-image-alias","prompt":"fixture","quality":"high","size":"1024x1024","n":1}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.multipart {
				var multipartBody bytes.Buffer
				writer := multipart.NewWriter(&multipartBody)
				for field, value := range map[string]string{"model": "custom-image-alias", "prompt": "fixture", "quality": "high", "size": "1024x1024"} {
					require.NoError(t, writer.WriteField(field, value))
				}
				part, err := writer.CreateFormFile("image[]", "reference.png")
				require.NoError(t, err)
				_, err = part.Write(imageBytes.Bytes())
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body = multipartBody.String()
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits?async=true", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			if tc.pinned {
				service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 701, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: "custom-image-alias", FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 701, UpstreamModelName: "upstream-first"}}
			if tc.multipart {
				info.RelayMode = relayconstant.RelayModeImagesEdits
			}
			snapshot := &relay.NativeAsyncImageSnapshot{ID: "first", PollURL: upstream.URL + "/jobs/first", ChannelID: 701, KeyFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte("retry-upstream-key"))), Task: config.Task}
			if tc.queued {
				require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "custom-image-alias"}))
			} else {
				require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "custom-image-alias"}, snapshot))
			}
			common.CleanupBodyStorage(c)
			var task model.Task
			require.NoError(t, db.First(&task).Error)
			require.NotNil(t, task.PrivateData.ImageRetry)
			originalPath := task.PrivateData.ImageRetry.BodyPath
			nativePath := task.PrivateData.RequestBodyPath
			original, err := os.ReadFile(originalPath)
			require.NoError(t, err)
			assert.Equal(t, body, string(original))
			if tc.resume {
				snapshot.ID, snapshot.PollURL, snapshot.ChannelID = "backup", upstream.URL+"/jobs/backup", 809
				require.NoError(t, relay.SaveNativeAsyncImageSnapshot(&task, snapshot))
				task.ChannelId, task.Properties.UpstreamModelName = 809, "upstream-backup"
				task.PrivateData.ChannelRetryPath = []string{"701", "809"}
				task.PrivateData.ImageRetry.Details = []model.AsyncImageRetryDetail{{Attempt: 1, ChannelID: 701, Status: "error", ErrorCode: "server_error"}}
				require.NoError(t, db.Save(&task).Error)
			}
			if tc.interrupted {
				task.PrivateData.ImageRetry.Submitting = true
				require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
			}
			require.True(t, claimInternalAsyncImageTask(&task))
			// Reload exactly as a restarted worker would; no process-local poll registry.
			require.NoError(t, db.First(&task, task.ID).Error)
			err = executeInternalAsyncImageTask(context.Background(), &task)
			if tc.success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.Len(t, postedModels, tc.wantPosts)
			for _, ok := range requestChecks {
				assert.True(t, ok, "retry must reconstruct original parameters")
			}
			if tc.wantPosts > 0 {
				assert.Equal(t, "upstream-backup", postedModels[len(postedModels)-1])
			}
			if tc.queued {
				assert.Equal(t, []string{"upstream-first", "upstream-backup"}, postedModels)
			}
			assert.Equal(t, "custom-image-alias", stored.Properties.OriginModelName)
			var user model.User
			require.NoError(t, db.First(&user, 61).Error)
			var token model.Token
			require.NoError(t, db.First(&token, 62).Error)
			var consumes int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			if tc.success {
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), stored.Status)
				assert.Equal(t, service.TaskBillingStateSettled, stored.PrivateData.BillingState)
				assert.Equal(t, 850000, user.Quota)
				assert.Equal(t, 850000, token.RemainQuota)
				assert.EqualValues(t, 1, consumes)
				assert.Equal(t, []string{"701", "809"}, stored.PrivateData.ChannelRetryPath)
				assert.Equal(t, "upstream-backup", stored.Properties.UpstreamModelName)
				require.Len(t, stored.PrivateData.ImageRetry.Details, 1)
				assert.NotEmpty(t, stored.PrivateData.ImageRetry.Details[0].ErrorCode)
				if tc.name == "mapped native failover" {
					assert.Equal(t, "provider_error", stored.PrivateData.ImageRetry.Details[0].ErrorType)
					assert.Equal(t, "server_error", stored.PrivateData.ImageRetry.Details[0].ErrorCode)
				}
				var log model.Log
				require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).First(&log).Error)
				assert.Contains(t, log.Other, "async_channel_retry_details")
				if tc.wantPosts > 0 {
					var recorded struct {
						Admin struct {
							Delivery []service.UpstreamDelivery `json:"upstream_delivery"`
						} `json:"admin_info"`
					}
					require.NoError(t, common.UnmarshalJsonStr(log.Other, &recorded))
					require.Len(t, recorded.Admin.Delivery, tc.wantPosts)
					last := recorded.Admin.Delivery[len(recorded.Admin.Delivery)-1]
					assert.Equal(t, 809, last.ChannelID)
					assert.True(t, last.RequestWritten)
					assert.True(t, last.ResponseReceived)
					assert.True(t, last.ResponseComplete)
				}

				require.NoError(t, service.ConfirmTaskBillingSettled(context.Background(), &stored, 150000, "duplicate completion"))
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), stored.Status)
				assert.Equal(t, service.TaskBillingStateRefunded, stored.PrivateData.BillingState)
				assert.Equal(t, 1000000, user.Quota)
				assert.Equal(t, 1000000, token.RemainQuota)
				assert.Zero(t, consumes)
				require.NoError(t, service.RefundTaskQuota(context.Background(), &stored, "duplicate failure"))
			}
			var afterUser model.User
			require.NoError(t, db.First(&afterUser, 61).Error)
			assert.Equal(t, user.Quota, afterUser.Quota)
			var afterToken model.Token
			require.NoError(t, db.First(&afterToken, 62).Error)
			assert.Equal(t, token.RemainQuota, afterToken.RemainQuota)
			assert.NoFileExists(t, originalPath)
			assert.NoFileExists(t, nativePath)
			// An old worker may neither resurrect this task nor issue another POST.
			beforePosts := len(postedModels)
			_, _, _, _, err = runRetryableAsyncImageRelay(context.Background(), &task)
			require.Error(t, err)
			assert.Len(t, postedModels, beforePosts)
		})
	}
}

func TestAsyncImageRetryWorkerOwnership(t *testing.T) {
	db := setupAsyncImageWorkerTestDB(t)
	task := &model.Task{TaskID: "retry-owner", Platform: constant.TaskPlatformInternalImage, Status: model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{InternalAsync: true, WorkerAttempts: 1, WorkerHeartbeatAt: time.Now().Add(-time.Hour).Unix(), ImageRetry: &model.AsyncImageRetryState{}},
	}
	require.NoError(t, db.Create(task).Error)
	var stale model.Task
	require.NoError(t, db.First(&stale, task.ID).Error)
	require.True(t, claimInternalAsyncImageTask(task))
	assert.Equal(t, 2, task.PrivateData.WorkerAttempts)
	stale.PrivateData.ImageRetry.Submitting = true
	require.ErrorContains(t, persistAsyncImageAttempt(&stale), "no longer owns")
	var current model.Task
	require.NoError(t, db.First(&current, task.ID).Error)
	assert.False(t, current.PrivateData.ImageRetry.Submitting)
	assert.Equal(t, 2, current.PrivateData.WorkerAttempts)
	assert.False(t, claimInternalAsyncImageTask(&current), "a live worker cannot be reclaimed")
	stale.Status = model.TaskStatusFailure
	won, err := stale.UpdateInternalAsyncImageTask(model.TaskStatusInProgress, 1)
	require.NoError(t, err)
	assert.False(t, won, "a stale worker cannot refund a newer attempt")
	require.NoError(t, db.First(&current, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), current.Status)
}
