package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModerationPromptLogVisibility(t *testing.T) {
	db := setupAsyncImageWorkerTestDB(t)
	previousConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = previousConsumeEnabled })
	require.NoError(t, model.LOG_DB.Where("user_id = ?", 7).Delete(&model.Log{}).Error)
	require.NoError(t, db.Create(&model.User{Id: 7, Username: "moderation-owner"}).Error)
	for _, tc := range []struct {
		name, code, message     string
		capture, consume, async bool
	}{
		{"google rejection", "400", "提示词被 Google 内容策略拦截（PROHIBITED_CONTENT）: The prompt contains sensitive words that violate Google's Generative AI Prohibited Use policy", true, false, false},
		{"async image rejection", "content_policy_violation", "Image rejected by moderation", true, false, true},
		{"successful HTTP rejection", "", "Content rejected by safety policy", true, true, false},
		{"ambiguous safety", "safe", "Task failed", false, false, false},
		{"unrelated failure", "invalid_value", "Invalid size", false, false, false},
		{"ordinary success", "", "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"private-audit-prompt"}],"api_key":"private-key"}`))
			_, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			c.Set("id", 7)
			c.Set("token_id", 11)
			c.Set("original_model", "public-model")
			c.Set("username", "moderation-owner")
			common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
			if tc.consume {
				other := model.NewLogOther()
				if tc.message != "" {
					other.SetAdmin("reject_reason", tc.message)
				}
				model.RecordConsumeLog(c, 7, model.RecordConsumeLogParams{TokenId: 11, Other: other})
			} else if tc.async {
				recordInternalAsyncImageFinalErrorLog(c, &model.Task{TaskID: "task_moderation", UserId: 7, PrivateData: model.TaskPrivateData{TokenId: 11}}, nil,
					types.NewOpenAIError(errors.New(tc.message), types.ErrorCode(tc.code), 400), nil, nil)
			} else {
				processChannelError(c, types.ChannelError{}, types.NewOpenAIError(errors.New(tc.message), types.ErrorCode(tc.code), 400), nil)
			}
			var stored model.Log
			require.NoError(t, model.LOG_DB.Order("id desc").First(&stored).Error)
			if tc.capture {
				assert.Contains(t, stored.Other, "private-audit-prompt")
				assert.Contains(t, stored.Other, "moderation_prompt")
			} else {
				assert.NotContains(t, stored.Other, "moderation_prompt")
				assert.NotContains(t, stored.Other, "private-audit-prompt")
			}
			assert.NotContains(t, stored.Other, "private-key")
			admin := stored
			model.FormatAdminLogs([]*model.Log{&admin})
			assert.Equal(t, tc.capture, strings.Contains(admin.Other, "private-audit-prompt"))
			user := stored
			model.FormatUserLogsForResponse([]*model.Log{&user}, 0)
			assert.NotContains(t, user.Other, "moderation_prompt")
			assert.NotContains(t, user.Other, "private-audit-prompt")
		})
	}
	logs, err := model.GetLogByTokenId(11)
	require.NoError(t, err)
	require.NotEmpty(t, logs)
	for _, log := range logs {
		assert.NotContains(t, log.Other, "private-audit-prompt")
		assert.NotContains(t, log.Other, "moderation_prompt")
	}
	logs, _, err = model.GetUserLogs(7, 0, 0, 0, "", "", 0, 20, "", "", "")
	require.NoError(t, err)
	require.NotEmpty(t, logs)
	for _, log := range logs {
		assert.NotContains(t, log.Other, "private-audit-prompt")
	}
}

func TestTaskErrorResponsesDoNotExposeProviderBodies(t *testing.T) {
	for _, write := range []func(*gin.Context, *dto.TaskError){respondTaskError, func(c *gin.Context, err *dto.TaskError) { respondTaskPluginImageError(c, err) }} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		taskErr := &dto.TaskError{
			StatusCode: 400, Code: "private-provider-code", Message: "private-model at upstream.invalid",
			Data: map[string]any{"channel": 141, "plugin": "private-plugin"},
		}
		write(c, taskErr)
		assert.Equal(t, 502, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "[generation_failed]")
		for _, private := range []string{"private-", "upstream.invalid", "141"} {
			assert.NotContains(t, recorder.Body.String(), private)
		}
		assert.Equal(t, "private-model at upstream.invalid", taskErr.Message)
		assert.NotNil(t, taskErr.Data)
	}
}

func TestTaskErrorProjectionProvidesChineseDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, wantCode, wantMessage string
		wantStatus                  int
		apiError                    *types.NewAPIError
	}{
		{"missing prompt", "missing_prompt", "缺少提示词（prompt）", http.StatusBadRequest, types.NewErrorWithStatusCode(errors.New("field prompt is required"), types.ErrorCodeInvalidRequest, 400)},
		{"invalid size", "invalid_size", "图片或视频尺寸参数无效", http.StatusBadRequest, types.NewOpenAIError(errors.New("invalid image size for private storage model"), types.ErrorCode("invalid_value"), 400)},
		{"safety rejection", "content_policy_violation", "输入内容或生成结果未通过安全审核", http.StatusBadRequest, types.NewOpenAIError(errors.New("unsafe image"), types.ErrorCode("content_policy_violation"), 403)},
		{"no generated result", "generation_result_empty", "生成服务未返回有效结果", http.StatusBadGateway, types.NewOpenAIError(errors.New("no images generated"), types.ErrorCodeBadResponseBody, 502)},
		{"wallet", "insufficient_user_quota", "账户余额不足", http.StatusPaymentRequired, types.NewErrorWithStatusCode(errors.New("余额不足"), types.ErrorCodeInsufficientUserQuota, 403)},
		{"token", "insufficient_token_quota", "令牌配额不足", http.StatusPaymentRequired, types.NewErrorWithStatusCode(errors.New("令牌额度不足"), types.ErrorCodePreConsumeTokenQuotaFailed, 403)},
		{"provider", "service_quota_exceeded", "生成服务可用额度不足", http.StatusServiceUnavailable, types.NewOpenAIError(errors.New("insufficient quota"), types.ErrorCode("insufficient_user_quota"), 403)},
		{"storage error", "billing_failed", "额度校验或预扣费未能完成", http.StatusInternalServerError, types.NewErrorWithStatusCode(errors.New("private storage failure"), types.ErrorCodePreConsumeTokenQuotaFailed, 403)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := service.TaskErrorFromAPIError(tc.apiError)
			projected := publicTaskError(original)
			assert.Equal(t, tc.wantCode, projected.Code)
			assert.Equal(t, tc.wantStatus, projected.StatusCode)
			assert.Contains(t, projected.Message, "["+tc.wantCode+"]")
			assert.Equal(t, projected, publicTaskError(projected), "repeated host projections must retain the diagnosis")
			for _, renderer := range []struct {
				name  string
				write func(*gin.Context, *dto.TaskError)
			}{
				{"task", respondTaskError},
				{"image", func(c *gin.Context, err *dto.TaskError) { respondTaskPluginImageError(c, err) }},
			} {
				t.Run(renderer.name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
					renderer.write(c, original)
					assert.Equal(t, tc.wantStatus, recorder.Code)
					var body map[string]any
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
					if renderer.name == "image" {
						var ok bool
						body, ok = body["error"].(map[string]any)
						require.True(t, ok)
					}
					assert.Equal(t, tc.wantCode, body["code"])
					assert.Contains(t, body["message"], tc.wantMessage)
					assert.Contains(t, body["message"], "["+tc.wantCode+"]")
					assert.NotContains(t, recorder.Body.String(), "private storage")
				})
			}
		})
	}
}

func TestMidjourneyBillingErrorsRequireLocalProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, localCode, wantCode, wantMessage string
		wantStatus                             int
	}{
		{"wallet", "insufficient_user_quota", "insufficient_user_quota", "账户余额不足", http.StatusPaymentRequired},
		{"token", "insufficient_token_quota", "insufficient_token_quota", "令牌配额不足", http.StatusPaymentRequired},
		{"provider cannot claim local billing", "", "service_quota_exceeded", "生成服务可用额度不足", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mjErr := &dto.MidjourneyResponse{Code: 4, Description: "internal billing detail", LocalErrorCode: tc.localCode}
			if tc.localCode == "" {
				require.NoError(t, common.UnmarshalJsonStr(`{"code":4,"description":"insufficient quota at https://private-provider.invalid","result":"sk-private-key model=private-model","LocalErrorCode":"insufficient_user_quota","local_error_code":"insufficient_user_quota"}`, mjErr))
				require.Empty(t, mjErr.LocalErrorCode)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil)
			c.Set(common.RequestIdKey, "billing-request-id")
			assert.Equal(t, tc.wantStatus, respondMidjourneyError(c, mjErr))
			assert.Equal(t, tc.wantStatus, recorder.Code)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.Equal(t, float64(4), payload["code"], "keep the existing MJ numeric code")
			assert.Contains(t, payload["description"], tc.wantMessage)
			assert.Equal(t, tc.wantCode, recorder.Header().Get("X-Error-Code"))
			assert.Equal(t, tc.wantCode, payload["error_code"])
			assert.Equal(t, payload["description"], payload["message"])
			assert.Contains(t, payload["message"], "["+tc.wantCode+"]")
			assert.Contains(t, payload["message"], "billing-request-id")
			assert.NotContains(t, recorder.Body.String(), "private-")
			assert.NotContains(t, recorder.Body.String(), "internal billing detail")
			if tc.localCode == "" {
				assert.Equal(t, "upstream_error", payload["type"])
			}
		})
	}
}

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"101"}, adminInfo["use_channel"])

	logs, total, err := model.GetUserLogs(7, model.LogTypeError, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Zero(t, logs[0].ChannelId)
	assert.Contains(t, logs[0].Content, "[generation_failed]")
	assert.Contains(t, stored.Content, "upstream failed")
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}
}
