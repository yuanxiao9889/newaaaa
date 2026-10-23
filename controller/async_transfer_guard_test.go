package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/helper"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardedVideoTaskDispatchesOnceAndPersistsAcrossReload(t *testing.T) {
	f := newVideoRetryTestFixture(t)
	task := f.submit(t, false, "durable-transfer-execution")
	task = f.load(t, task.TaskID)
	assert.Equal(t, "durable-transfer-execution", task.PrivateData.TransferExecutionID)
	assert.EqualValues(t, 1, f.submitCalls[0].Load(), "the first POST remains permitted")
	// Polling confirms a retryable upstream failure; a second provider exists.
	task = f.poll(t, task)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
	assert.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
	assert.EqualValues(t, 1, f.submitCalls[0].Load())
	assert.Zero(t, f.submitCalls[1].Load(), "a persisted guard suppresses replacement generation")
}

func TestPersistedTransferGuardSurvivesReconstructedContexts(t *testing.T) {
	original := model.TaskPrivateData{TransferExecutionID: "durable-transfer-execution",
		VideoRetry: &model.VideoTaskRetryState{Method: http.MethodPost, Path: "/v1/videos",
			Body: []byte(`{}`), ContentType: "application/json", AcceptedAt: time.Now().Unix(), Ready: true},
	}
	stored, err := original.Value()
	require.NoError(t, err)
	var restored model.TaskPrivateData
	require.NoError(t, restored.Scan(stored))
	task := &model.Task{TaskID: "fixture-task", Status: model.TaskStatusSubmitted, PrivateData: restored}
	c, _, err := buildInternalAsyncBaseContext(context.Background(), task, []byte(`{}`))
	require.NoError(t, err)
	defer common.CleanupBodyStorage(c)
	assert.Equal(t, original.TransferExecutionID, c.GetString("transfer_execution_id"))
	failure := types.NewErrorWithStatusCode(errors.New("uncertain provider submission"), types.ErrorCodeDoRequestFailed, 503)
	assert.Equal(t, "transfer_dispatch_once", service.DecideRelayRetry(c, failure, 3).Reason)
	assert.False(t, canRetryVideoTask(task, &relaycommon.TaskInfo{Status: model.TaskStatusFailure}, time.Now().Unix()),
		"a replay snapshot cannot bypass the guard after a restart")

	// Older rows have no guard and keep their configured retry behavior.
	var legacy model.TaskPrivateData
	require.NoError(t, legacy.Scan(`{"request_path":"/v1/images/generations"}`))
	assert.Empty(t, legacy.TransferExecutionID)
}

func TestSafetyKeywordsStopRetry(t *testing.T) {
	previous := operation_setting.AutomaticRetryStatusCodesToString()
	t.Cleanup(func() { require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previous)) })
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("400-599"))
	for _, tc := range []struct {
		name, body string
		rejected   bool
	}{
		{"production wording", `{"error":{"code":"400","message":"非常抱歉，该提示可能违反了我们的内容政策。如果你认为此判断有误，请重试或修改提示语。"}}`, true},
		{"explicit code", `{"error":{"code":"content_policy_violation","message":"Task failed"}}`, true},
		{"explicit type", `{"error":{"code":"400","type":"content_policy_violation","message":"Task failed"}}`, true},
		{"Chinese rejection", `{"error":{"message":"生成图片未通过内容审核"}}`, true},
		{"English rejection", `{"error":{"message":"Request rejected by content moderation"}}`, true},
		{"service unavailable", `{"error":{"message":"审核服务暂不可用"}}`, true},
		{"passed", `{"error":{"message":"图片已通过内容审核，生成服务暂不可用"}}`, true},
		{"Chinese negation", `{"error":{"message":"没有违反我们的内容政策，生成失败"}}`, true},
		{"English negation", `{"error":{"message":"Prompt does not violate our content policy. Generation failed."}}`, true},
		{"ambiguous", `{"error":{"message":"Image generation failed or was rejected by content moderation"}}`, true},
		{"bare safe", `{"error":{"message":"SAFE"}}`, true},
		{"unsafe code", `{"error":{"code":"unsafe","message":"Task failed"}}`, true},
		{"safe type", `{"error":{"type":"safe","message":"Task failed"}}`, true},
		{"compound safety code", `{"error":{"code":"provider_safety_error","message":"Task failed"}}`, true},
		{"bracketed unsafe", `{"error":{"message":"[UnSaFe]"}}`, true},
		{"nsfw", `{"error":{"message":"NSFW detected"}}`, true},
		{"safe retry instruction", `{"error":{"message":"Task failed; safe retry is permitted"}}`, true},
		{"Chinese safety", `{"error":{"message":"安全检查异常"}}`, true},
		{"Chinese sensitive words", `{"error":{"message":"敏感词检测异常"}}`, true},
		{"ordinary outage", `{"error":{"message":"生成服务暂不可用"}}`, false},
		{"unrelated fragment", `{"error":{"message":"safeguard operation failed"}}`, false},
		{"echoed prompt", `{"error":{"message":"Task failed"},"prompt":"违反内容政策","metadata":{"code":"content_policy_violation"}}`, false},
		{"URL", `{"error":{"message":"Task failed https://example.test/unsafe_image"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.rejected, common.ShouldStopRetryForSafety("", tc.body))
			for _, bridge := range []bool{false, true} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				if bridge {
					c.Set("transfer_execution_id", "content-rejection-fixture")
				}
				for _, status := range []int{400, 502} {
					apiErr := service.RelayErrorHandler(context.Background(), &http.Response{
						StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)),
					}, false)
					decision := service.DecideRelayRetry(c, apiErr, 3)
					assert.Equal(t, tc.rejected, decision.Action == "stop")
					if tc.rejected {
						assert.Equal(t, "safety_related_error", decision.Reason)
					}
					taskDecision := decideTaskRetry(c, &dto.TaskError{Code: "fail_to_fetch_task", StatusCode: status, Message: tc.body}, 3)
					if tc.rejected {
						assert.Equal(t, "safety_related_error", taskDecision.Reason)
						assert.Equal(t, "stop", taskDecision.Action)
					} else if bridge || status != 400 {
						assert.Equal(t, "retry", taskDecision.Action)
					}
				}
			}
		})
	}
}

func TestBridgeRetryPreservesSubmissionBarriers(t *testing.T) {
	previous := operation_setting.AutomaticRetryStatusCodesToString()
	t.Cleanup(func() { require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previous)) })
	for _, tc := range []struct {
		name                string
		status, retries     int
		code                types.ErrorCode
		noRetry, local, pin bool
		confirmed           bool
		mappedStatus        int
		wantRetry           bool
	}{
		{name: "upstream 503", status: 503, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 429", status: 429, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 500", status: 500, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 502", status: 502, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 400", status: 400, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 401", status: 401, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 402", status: 402, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 404", status: 404, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 405", status: 405, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 409", status: 409, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 413", status: 413, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 422", status: 422, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream 505", status: 505, retries: 3, confirmed: true, wantRetry: true},
		{name: "upstream error uses a local-looking code", status: 401, code: types.ErrorCodeChannelInvalidKey, retries: 3, confirmed: true, wantRetry: true},
		{name: "403 mapped to 404 stays blocked", status: 403, mappedStatus: 404, retries: 3, confirmed: true},
		{name: "404 mapped to 403 stays blocked", status: 404, mappedStatus: 403, retries: 3, confirmed: true},
		{name: "404 mapped to 500", status: 404, mappedStatus: 500, retries: 3, confirmed: true, wantRetry: true},
		{name: "local 404 is not an upstream rejection", status: 404, retries: 3, local: true, code: types.ErrorCodeModelNotFound},
		{name: "local group failure 503", status: 503, retries: 3, local: true, code: types.ErrorCodeGetChannelFailed},
		{name: "local key failure 401", status: 401, retries: 3, local: true, code: types.ErrorCodeChannelInvalidKey},
		{name: "local quality failure 400", status: 400, retries: 3, local: true, code: types.ErrorCodeInvalidRequest},
		{name: "synthetic 429 is not confirmed", status: 429, retries: 3},
		{name: "synthetic 503 is not confirmed", status: 503, retries: 3},
		{name: "confirmed timeout 408", status: 408, retries: 3, confirmed: true},
		{name: "confirmed timeout 504", status: 504, retries: 3, confirmed: true},
		{name: "confirmed timeout 524", status: 524, retries: 3, confirmed: true},
		{name: "timeout mapped to 404 stays blocked", status: 504, mappedStatus: 404, retries: 3, confirmed: true},
		{name: "403 stays blocked", status: 403, retries: 3, confirmed: true},
		{name: "403 mapped to 502 stays blocked", status: 403, mappedStatus: 502, retries: 3, confirmed: true},
		{name: "403 mapped to 503 stays blocked", status: 403, mappedStatus: 503, retries: 3, confirmed: true},
		{name: "500 budget exhausted", status: 500, confirmed: true},
		{name: "502 explicit no retry", status: 502, retries: 3, confirmed: true, noRetry: true},
		{name: "502 pinned channel", status: 502, retries: 3, confirmed: true, pin: true},
		{name: "500 transport failure", status: 500, retries: 3, code: types.ErrorCodeDoRequestFailed},
		{name: "502 response read failure", status: 502, retries: 3, code: types.ErrorCodeReadResponseBodyFailed},
		{name: "502 task parse failure", status: 502, retries: 3, code: types.ErrorCode("plugin_submit_response_failed")},
		{name: "502 synthesized generation failure", status: 502, retries: 3, code: types.ErrorCode("generation_failed")},
		{name: "429 budget exhausted", status: 429},
		{name: "429 explicit no retry", status: 429, retries: 3, noRetry: true},
		{name: "429 pinned channel", status: 429, retries: 3, pin: true},
		{name: "transport mapped to 429", status: 429, retries: 3, code: types.ErrorCodeDoRequestFailed, local: true},
		{name: "budget exhausted", status: 503},
		{name: "explicit no retry", status: 503, retries: 3, noRetry: true},
		{name: "pinned channel", status: 503, retries: 3, pin: true},
		{name: "transport mapped to 503", status: 503, retries: 3, code: types.ErrorCodeDoRequestFailed, local: true},
		{name: "response read mapped to 503", status: 503, retries: 3, code: types.ErrorCodeReadResponseBodyFailed, local: true},
		{name: "unconfirmed server error", status: 502, retries: 3, code: types.ErrorCode("unknown_error")},
		{name: "upstream 501", status: 501, retries: 3, confirmed: true, wantRetry: true},
		{name: "timeout", status: 504, retries: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("400-599"))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("transfer_execution_id", "original-bridge-execution")
			if tc.pin {
				service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 703, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			code := tc.code
			if code == "" {
				code = types.ErrorCodeBadResponseStatusCode
			}
			apiErr := types.NewErrorWithStatusCode(errors.New("provider failure"), code, tc.status)
			if tc.confirmed {
				apiErr = service.RelayErrorHandler(context.Background(), &http.Response{
					StatusCode: tc.status, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"message":"provider failure","code":%q}}`, code))),
				}, false)
			}
			if tc.mappedStatus != 0 {
				service.ResetStatusCode(apiErr, fmt.Sprintf(`{"%d":%d}`, tc.status, tc.mappedStatus))
			}
			if tc.noRetry {
				types.ErrOptionWithSkipRetry()(apiErr)
			}
			assert.Equal(t, tc.wantRetry, service.ShouldRetryRelayError(c, apiErr, tc.retries))
			taskErr := &dto.TaskError{StatusCode: tc.status, Code: string(code), LocalError: tc.local, NoRetry: tc.noRetry}
			if tc.confirmed {
				taskErr.Code = "fail_to_fetch_task"
			}
			// Task submissions preserve the raw HTTP status and have no mapping.
			if tc.mappedStatus != 403 {
				assert.Equal(t, tc.wantRetry, decideTaskRetry(c, taskErr, tc.retries).Action == "retry")
			}
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("transfer_execution_id", "original-bridge-execution")
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("418"))
	for _, status := range []int{400, 401, 402, 404, 405, 409, 413, 422, 429, 500, 501, 502, 503, 505} {
		failure := service.RelayErrorHandler(context.Background(), &http.Response{
			StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider unavailable"}}`)),
		}, false)
		assert.Equal(t, "status_not_retryable", service.DecideRelayRetry(c, failure, 3).Reason)
		assert.Equal(t, "status_not_retryable", decideTaskRetry(c, &dto.TaskError{Code: "fail_to_fetch_task", StatusCode: status}, 3).Reason)
	}
	// Even a time budget that overrides an exhausted attempt count must not
	// turn locally produced errors into retryable upstream rejections.
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("400-599"))
	now := time.Unix(1000, 0)
	remaining := (service.RetryBudget{Deadline: now.Add(time.Minute)}).Remaining(-10, now)
	for _, bridge := range []bool{false, true} {
		for _, tc := range []struct {
			code   types.ErrorCode
			status int
		}{
			{types.ErrorCodeChannelInvalidKey, 401},
			{types.ErrorCodeChannelNoAvailableKey, 500},
			{types.ErrorCodeGetChannelFailed, 503},
			{types.ErrorCodeModelNotFound, 404},
			{types.ErrorCodeInvalidRequest, 400},
			{types.ErrorCodeInvalidRequest, 429},
			{types.ErrorCodeModelPriceError, 500},
			{types.ErrorCodeUpdateDataError, 500},
			{types.ErrorCodeInsufficientUserQuota, 403},
		} {
			t.Run(fmt.Sprintf("local_%s_bridge_%t_status_%d", tc.code, bridge, tc.status), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				if bridge {
					c.Set("transfer_execution_id", "local-failure")
				}
				apiErr := types.NewErrorWithStatusCode(errors.New("local failure"), tc.code, tc.status)
				assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, remaining).Action)
				assert.Equal(t, "stop", decideTaskRetry(c, &dto.TaskError{Code: "fail_to_fetch_task", LocalError: true, StatusCode: tc.status}, remaining).Action)
			})
		}
	}
	// A partial HTTP error body is not sufficient evidence to submit again.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":`)
	}))
	t.Cleanup(server.Close)
	response, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	readFailure := service.RelayErrorHandler(context.Background(), response, false)
	assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, readFailure.GetErrorCode())
	assert.Equal(t, "stop", service.DecideRelayRetry(c, readFailure, remaining).Action)
}

// Both channel contracts must accept locally before dispatching upstream, and
// a lost acceptance response must remain recoverable under the original key.
func TestDou5AsyncAcceptanceAndReceiptRecovery(t *testing.T) {
	testManagedImageAcceptance(t, "dou-5-0-pro", false, "")
}

func TestFutureImageBridgeTimeoutRecoversWithoutDuplicateCharge(t *testing.T) {
	testManagedImageAcceptance(t, "vendor/future-image-2099", true, "")
}

func TestCompatibleImageBridgeTimeoutSingleSettlement(t *testing.T) {
	for _, protocol := range []string{"openai", "openai_responses"} {
		t.Run(protocol, func(t *testing.T) { testManagedImageAcceptance(t, "future-compatible-image", true, protocol) })
	}
}

func testManagedImageAcceptance(t *testing.T, modelName string, bridgeTimeout bool, protocol string) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	for _, tc := range []struct {
		native       bool
		rejectStatus int
	}{
		{}, {native: true},
		{rejectStatus: 503}, {native: true, rejectStatus: 503},
		{rejectStatus: 429}, {native: true, rejectStatus: 429},
		{rejectStatus: 500}, {native: true, rejectStatus: 500},
		{rejectStatus: 502}, {native: true, rejectStatus: 502},
		{rejectStatus: 404}, {native: true, rejectStatus: 404},
		{rejectStatus: 401}, {native: true, rejectStatus: 401},
		{rejectStatus: 422}, {native: true, rejectStatus: 422},
		{rejectStatus: 403}, {native: true, rejectStatus: 403},
	} {
		t.Run(fmt.Sprintf("native_%t_rejection_%d", tc.native, tc.rejectStatus), func(t *testing.T) {
			if protocol != "" && tc.native {
				t.Skip("native-202 is covered by the image adapter contract")
			}
			native := tc.native
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}, &model.TransferRequest{}))
			oldMemory, oldBatch, oldAddress := common.MemoryCacheEnabled, common.BatchUpdateEnabled, system_setting.ServerAddress
			prices := ratio_setting.ModelPrice2JSONString()
			oldRetries := common.RetryTimes
			common.RetryTimes = 3
			common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false
			system_setting.ServerAddress = "https://www.oopii.cc"
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(fmt.Sprintf(`{%q:0.3}`, modelName)))
			t.Cleanup(func() {
				common.RetryTimes = oldRetries
				common.MemoryCacheEnabled, common.BatchUpdateEnabled, system_setting.ServerAddress = oldMemory, oldBatch, oldAddress
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
			})
			t.Setenv("ASYNC_IMAGE_SIGNING_SECRET", "dou5-local-fixture-secret")
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			// The real acceptance function receives one already reserved billing quota.
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "local_dou5_fixture", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "fixture-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			path := "/v1/images/generations"
			submitQuery := "?async=true"
			if protocol == "openai" {
				path = "/v1/chat/completions"
				submitQuery += "&image_task=true"
			}
			if protocol == "openai_responses" {
				path = "/v1/responses"
				submitQuery += "&image_task=true"
			}
			bodyFor := func(prompt string) string {
				if protocol == "openai" {
					return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"stream":false,"n":1}`, modelName, prompt)
				}
				if protocol == "openai_responses" {
					return fmt.Sprintf(`{"model":%q,"input":%q,"tools":[{"type":"image_generation"}],"stream":false}`, modelName, prompt)
				}
				return fmt.Sprintf(`{"model":%q,"prompt":%q,"size":"1024x1024","n":1}`, modelName, prompt)
			}
			var posts, polls, admissions atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					attempt := posts.Add(1)
					if tc.rejectStatus != 0 && attempt == 1 {
						w.WriteHeader(tc.rejectStatus)
						fmt.Fprint(w, `{"error":{"code":"transient_failure","message":"provider temporarily unavailable"}}`)
						return
					}
					if native {
						w.WriteHeader(http.StatusAccepted)
						fmt.Fprint(w, `{"id":"provider-original-task","status":"queued"}`)
						return
					}
				} else {
					polls.Add(1)
				}
				encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
				if protocol == "openai" {
					fmt.Fprintf(w, `{"id":"chat-fixture","model":%q,"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":[{"type":"image_url","image_url":{"url":%q}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`, modelName, "data:image/png;base64,"+encoded)
				} else if protocol == "openai_responses" {
					fmt.Fprintf(w, `{"id":"resp-fixture","object":"response","status":"completed","model":%q,"output":[{"type":"image_generation_call","id":"img-fixture","status":"completed","result":%q}],"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`, modelName, encoded)
				} else {
					fmt.Fprintf(w, `{"status":"completed","data":[{"b64_json":%q}]}`, encoded)
				}
			}))
			defer upstream.Close()
			adapter := &kitdto.ImageAdapterConfig{Enabled: true, Request: kitdto.ImageAdapterRequestConfig{GenerationsPath: "/v1/images/generations"}}
			if native {
				adapter.Task = &kitdto.ImageAdapterTaskConfig{IDPaths: []string{"id", "task_id"}, StatusPath: "status", ErrorPath: "error", ResultB64Path: "data.0.b64_json", PollURLTemplate: "/v1/images/generations/{task_id}", PendingStates: []string{"queued", "in_progress"}, SuccessStates: []string{"completed"}, FailureStates: []string{"failed"}}
			}
			require.NoError(t, adapter.Validate())
			setting, err := common.Marshal(kitdto.ChannelSettings{ImageAdapter: adapter})
			require.NoError(t, err)
			channel := &model.Channel{Id: 703, Name: "dou5-local", Type: constant.ChannelTypeOpenAI, Key: "fixture-upstream", Models: modelName, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: common.GetPointer(upstream.URL), Setting: common.GetPointer(string(setting)), Weight: common.GetPointer(uint(1))}
			channel.Priority = common.GetPointer(int64(1))
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			backup := *channel
			backup.Id, backup.Name, backup.Priority = 704, "dou5-backup", common.GetPointer(int64(0))
			require.NoError(t, db.Create(&backup).Error)
			require.NoError(t, backup.AddAbilities(db))
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("id", 61); c.Next() })
			router.POST(path, middleware.TransferRequestGuard, func(c *gin.Context) {
				admissions.Add(1)
				require.True(t, relay.IsAsyncImageRequest(c))
				info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: modelName, FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 703, UpstreamModelName: modelName}}
				if protocol != "" {
					request, err := helper.GetAndValidateRequest(c, types.RelayFormat(protocol))
					require.NoError(t, err)
					info.PriceData.UsePrice = true
					info.PriceData.ModelPrice = 0.3
					info.PriceData.GroupRatioInfo.GroupRatio = 1
					require.Nil(t, relay.SubmitInternalAsyncCompatibleImageTask(c, info, request, types.RelayFormat(protocol)))
				} else {
					require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: modelName}))
				}
				common.CleanupBodyStorage(c)
			})
			router.GET("/v1/transfer/requests/:key", GetTransferRequest)
			router.GET("/v1/images/tasks/:task_id", GetImageTask)
			submit := func(prompt string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, path+submitQuery, strings.NewReader(bodyFor(prompt)))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", "dou5-original-request-0001")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				return response
			}

			var bridgeReceipt []byte
			if bridgeTimeout {
				// The bridge forwards the real request, then withholds the receipt.
				// The origin accepts durably even though the client times out.
				acceptedAtOrigin := make(chan struct{})
				bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					router.ServeHTTP(httptest.NewRecorder(), r)
					close(acceptedAtOrigin)
					<-r.Context().Done()
				}))
				defer bridge.Close()
				req, err := http.NewRequest(http.MethodPost, bridge.URL+path+submitQuery, strings.NewReader(bodyFor("fixture")))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "dou5-original-request-0001")
				ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
				defer cancel()
				// Start the timeout only after origin acceptance: no slow-CI race.
				go func() {
					select {
					case <-acceptedAtOrigin:
						time.Sleep(20 * time.Millisecond)
						cancel()
					case <-ctx.Done():
					}
				}()
				_, err = http.DefaultClient.Do(req.WithContext(ctx))
				require.ErrorIs(t, err, context.Canceled)
				lookupResponse := httptest.NewRecorder()
				router.ServeHTTP(lookupResponse, httptest.NewRequest(http.MethodGet, "/v1/transfer/requests/dou5-original-request-0001", nil))
				require.Equal(t, http.StatusOK, lookupResponse.Code)
				var lookupBody struct {
					ResponseBody string `json:"response_body_base64"`
				}
				require.NoError(t, common.Unmarshal(lookupResponse.Body.Bytes(), &lookupBody))
				bridgeReceipt, err = base64.StdEncoding.DecodeString(lookupBody.ResponseBody)
				require.NoError(t, err)
				require.EqualValues(t, 1, admissions.Load(), "recovery GET cannot create or charge another task")

			}
			// Re-enter by the original route using exactly the same identity/body.
			accepted := submit("fixture")
			if bridgeTimeout {
				assert.JSONEq(t, string(bridgeReceipt), accepted.Body.String(), "origin replay returns the task recovered after the bridge timeout")
			}

			require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
			require.Zero(t, posts.Load(), "the acceptance HTTP request must never wait for upstream generation")
			var receipt dto.ImageAsyncSubmitResponse
			require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &receipt))
			require.NotEmpty(t, receipt.TaskID)
			// Ignore the original response as if it was lost; recover it via GET only.
			recovered := httptest.NewRecorder()
			router.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/v1/transfer/requests/dou5-original-request-0001", nil))
			require.Equal(t, http.StatusOK, recovered.Code, recovered.Body.String())
			var lookup struct {
				ResponseBody string `json:"response_body_base64"`
			}
			require.NoError(t, common.Unmarshal(recovered.Body.Bytes(), &lookup))
			original, err := base64.StdEncoding.DecodeString(lookup.ResponseBody)
			require.NoError(t, err)
			assert.JSONEq(t, accepted.Body.String(), string(original))
			replay := submit("fixture")
			require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
			assert.JSONEq(t, accepted.Body.String(), replay.Body.String())
			require.Equal(t, http.StatusConflict, submit("changed prompt").Code)
			require.EqualValues(t, 1, admissions.Load(), "a replay must not reach quota reservation or task creation")
			var task model.Task
			require.NoError(t, db.First(&task, "task_id = ?", receipt.TaskID).Error)
			require.NotEmpty(t, task.PrivateData.TransferExecutionID)
			require.True(t, claimInternalAsyncImageTask(&task))
			require.NoError(t, db.First(&task, task.ID).Error)
			executionErr := executeInternalAsyncImageTask(context.Background(), &task)
			require.NoError(t, db.First(&task, task.ID).Error)
			if tc.rejectStatus == http.StatusForbidden {
				require.Error(t, executionErr)
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				assert.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
				assert.Equal(t, []string{"703"}, task.PrivateData.ChannelRetryPath)
				require.Equal(t, http.StatusOK, submit("fixture").Code)
				assert.EqualValues(t, 1, posts.Load(), "403 must not dispatch to the backup or replay the original submission")
				assert.EqualValues(t, 1, admissions.Load())
				var token model.Token
				require.NoError(t, db.First(&token, 62).Error)
				assert.Equal(t, 1000000, token.RemainQuota)
				assert.Zero(t, token.UsedQuota)
				return
			}
			require.NoError(t, executionErr)
			require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			wantPosts := 1
			if tc.rejectStatus != 0 {
				wantPosts = 2
				assert.Equal(t, []string{"703", "704"}, task.PrivateData.ChannelRetryPath)
				assert.Equal(t, 704, task.ChannelId)
			}
			require.EqualValues(t, wantPosts, posts.Load())
			if native {
				require.Greater(t, polls.Load(), int32(0))
			} else {
				require.Zero(t, polls.Load())
			}
			// Re-query/replay/final settlement are safe after completion, too.
			query := httptest.NewRecorder()
			router.ServeHTTP(query, httptest.NewRequest(http.MethodGet, receipt.StatusURL, nil))
			require.Equal(t, http.StatusOK, query.Code)
			require.Equal(t, http.StatusOK, submit("fixture").Code)
			require.NoError(t, service.ConfirmTaskBillingSettled(context.Background(), &task, 150000, "duplicate completion"))
			var consumes int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			require.EqualValues(t, 1, consumes)
			var token model.Token
			require.NoError(t, db.First(&token, 62).Error)
			require.Equal(t, 850000, token.RemainQuota)
			require.Equal(t, 150000, token.UsedQuota)
			require.EqualValues(t, wantPosts, posts.Load())
			require.EqualValues(t, 1, admissions.Load(), "replaying the original key after failover cannot create another task")
		})
	}
}
