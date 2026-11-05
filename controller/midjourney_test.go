package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestMidjourneyQuotaFailover(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Midjourney{}, &model.Task{}, &model.Log{}, &model.Option{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	oldRetry, oldDisable, oldErrorLog, oldMaster := common.RetryTimes, common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled, common.IsMasterNode
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	constant.ErrorLogEnabled = false
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = oldMaster
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = oldMaster
		model.LOG_DB = oldLogDB
		common.SetLogDatabaseType(oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
		common.RetryTimes, common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = oldRetry, oldDisable, oldErrorLog
	})
	service.InitHttpClient()
	originalPrices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj_imagine":0.4,"mj_upscale":0.4}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(originalPrices))) })
	root := model.User{Username: "mj_retry_root", AffCode: "mj_retry_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)

	for index, tc := range []struct {
		name          string
		response      string
		status        int
		globalOff     bool
		channelOff    bool
		cache         bool
		noRetry       bool
		timedLimit    bool
		pinned        bool
		strict        bool
		boundTask     bool
		transportFail bool
		noBalance     bool
		fallbackQuota bool
		wantFallback  bool
		wantDisabled  bool
		wantTasks     int
		wantCharge    bool
	}{
		{name: "native quota switches and bills once", response: `{"code":4,"description":"quota_not_enough"}`, wantFallback: true, wantDisabled: true, wantTasks: 1, wantCharge: true},
		{name: "nested HTTP 403 quota switches", status: 403, response: `{"error":{"code":"quota_not_enough","message":"upstream credit exhausted"}}`, wantFallback: true, wantDisabled: true, wantTasks: 1, wantCharge: true},
		{name: "nested HTTP 200 quota switches with cache", response: `{"error":{"type":"quota_not_enough","message":"upstream credit exhausted"}}`, cache: true, wantFallback: true, wantDisabled: true, wantTasks: 1, wantCharge: true},
		{name: "string quota code switches", response: `{"code":"quota_not_enough","message":"upstream credit exhausted"}`, wantFallback: true, wantDisabled: true, wantTasks: 1, wantCharge: true},
		{name: "global disable off still switches", response: `{"code":4,"description":"quota_not_enough"}`, globalOff: true, wantFallback: true, wantTasks: 1, wantCharge: true},
		{name: "channel disable off still switches", response: `{"code":4,"description":"quota_not_enough"}`, channelOff: true, wantFallback: true, wantTasks: 1, wantCharge: true},
		{name: "all providers reject without charge", response: `{"code":4,"description":"quota_not_enough"}`, fallbackQuota: true, wantFallback: true, wantDisabled: true},
		{name: "all exhausted providers stay excluded with global disable off", response: `{"code":4,"description":"quota_not_enough"}`, globalOff: true, fallbackQuota: true, wantFallback: true},
		{name: "disabled retries still disable channel", response: `{"code":4,"description":"quota_not_enough"}`, noRetry: true, wantDisabled: true},
		{name: "time budget retry cap still disables channel", response: `{"code":4,"description":"quota_not_enough"}`, timedLimit: true, wantDisabled: true},
		{name: "token pin stays on its channel", response: `{"code":4,"description":"quota_not_enough"}`, pinned: true, wantDisabled: true},
		{name: "strict session stays on its channel", response: `{"code":4,"description":"quota_not_enough"}`, strict: true, wantDisabled: true},
		{name: "bound action disables actual origin without replay", response: `{"code":4,"description":"quota_not_enough"}`, boundTask: true, wantDisabled: true},
		{name: "accepted submission is never replayed", response: `{"code":1,"description":"quota_not_enough","result":"accepted"}`, wantTasks: 1, wantCharge: true},
		{name: "queued submission is never replayed", response: `{"code":22,"description":"quota_not_enough","result":"queued"}`, wantTasks: 1, wantCharge: true},
		{name: "existing failed task is never replayed", response: `{"code":21,"description":"quota_not_enough","result":"existing","properties":{"status":"FAILURE"}}`, wantTasks: 1},
		{name: "other business failure is never replayed", response: `{"code":24,"description":"prompt rejected"}`, wantTasks: 1},
		{name: "task reference makes replay unsafe", response: `{"code":4,"description":"quota_not_enough","result":"possibly-accepted"}`, wantTasks: 1},
		{name: "longer error token is not channel quota", response: `{"code":4,"description":"token_quota_not_enough"}`, wantTasks: 1},
		{name: "unrelated properties do not trigger quota handling", response: `{"code":4,"description":"other failure","properties":{"prompt":"quota_not_enough"}}`, wantTasks: 1},
		{name: "host-only quota flag cannot be forged", response: `{"code":4,"description":"other failure","UpstreamQuotaExhausted":true}`, wantTasks: 1},
		{name: "malformed response is never replayed", response: `{"error":{"code":"quota_not_enough"}`},
		{name: "ambiguous transport failure is never replayed", transportFail: true},
		{name: "local token shortage never disables upstream", noBalance: true, response: `{"code":4,"description":"quota_not_enough"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const balance, charge = 1_000_000, 200_000
			if tc.timedLimit {
				previousPolicy := model.CurrentRequestPolicy().Options
				common.OptionMapRWMutex.Lock()
				previousOptions := common.OptionMap
				common.OptionMap = maps.Clone(previousPolicy)
				common.OptionMapRWMutex.Unlock()
				restore := map[string]string{
					"RetryTimeBudgetEnabled":    previousPolicy["RetryTimeBudgetEnabled"],
					"RetryTimeBudgetSeconds":    previousPolicy["RetryTimeBudgetSeconds"],
					"RetryTimeBudgetMaxRetries": previousPolicy["RetryTimeBudgetMaxRetries"],
				}
				t.Cleanup(func() {
					require.NoError(t, model.UpdateRequestPolicyOptions(restore))
					common.OptionMapRWMutex.Lock()
					common.OptionMap = previousOptions
					common.OptionMapRWMutex.Unlock()
				})
				require.NoError(t, model.UpdateRequestPolicyOptions(map[string]string{"RetryTimeBudgetEnabled": "true", "RetryTimeBudgetSeconds": "60", "RetryTimeBudgetMaxRetries": "0"}))
			}
			common.RetryTimes, common.AutomaticDisableChannelEnabled, common.MemoryCacheEnabled = 2, !tc.globalOff, tc.cache
			if tc.noRetry {
				common.RetryTimes = 0
			}
			user := model.User{Username: fmt.Sprintf("mj_retry_%d", index), AffCode: fmt.Sprintf("mjr_%d", index), Group: "default", Quota: balance, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			tokenBalance := charge
			if tc.noBalance {
				tokenBalance = 1
			}
			token := model.Token{UserId: user.Id, Name: "test", Key: fmt.Sprintf("mj-retry-token-%d", index), RemainQuota: tokenBalance, Status: common.TokenStatusEnabled, ExpiredTime: -1}
			require.NoError(t, db.Create(&token).Error)
			var calls [2]atomic.Int32
			var channels [2]model.Channel
			for provider := range 2 {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls[provider].Add(1)
					assert.Equal(t, fmt.Sprintf("mj-retry-key-%d", provider), r.Header.Get("mj-api-secret"))
					var request map[string]any
					assert.NoError(t, common.DecodeJson(r.Body, &request))
					assert.Equal(t, "preserve-this-state", request["state"])
					if !tc.boundTask {
						assert.Equal(t, "a cat", request["prompt"])
					}
					if provider == 0 && tc.transportFail {
						conn, _, err := w.(http.Hijacker).Hijack()
						assert.NoError(t, err)
						if err == nil {
							assert.NoError(t, conn.Close())
						}
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if provider == 0 {
						if tc.status != 0 {
							w.WriteHeader(tc.status)
						}
						_, _ = fmt.Fprint(w, tc.response)
						return
					}
					if tc.fallbackQuota {
						_, _ = fmt.Fprint(w, `{"code":4,"description":"quota_not_enough"}`)
						return
					}
					_, _ = fmt.Fprintf(w, `{"code":1,"description":"submitted","result":"fallback-%d"}`, index)
				}))
				t.Cleanup(server.Close)
				channels[provider] = model.Channel{Name: fmt.Sprintf("mj-retry-%d-%d", index, provider), Type: constant.ChannelTypeMidjourney, Key: fmt.Sprintf("mj-retry-key-%d", provider), BaseURL: &server.URL, Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(1), Models: "mj_imagine,mj_upscale", Group: "default"}
				if provider == 0 && tc.channelOff {
					channels[provider].AutoBan = common.GetPointer(0)
				}
				require.NoError(t, channels[provider].Insert())
				t.Cleanup(func() { require.NoError(t, channels[provider].Delete()) })
			}
			if tc.cache {
				model.InitChannelCache()
			}

			path, body, modelName := "/mj/submit/imagine", `{"prompt":"a cat","state":"preserve-this-state"}`, "mj_imagine"
			var origin model.Midjourney
			selected := &channels[0]
			if tc.boundTask {
				origin = model.Midjourney{MjId: "origin", UserId: user.Id, ChannelId: channels[0].Id, Action: constant.MjActionImagine, Prompt: "a cat", Status: "SUCCESS", Progress: "100%"}
				require.NoError(t, db.Create(&origin).Error)
				path, body, modelName = "/mj/submit/change", `{"taskId":"origin","action":"UPSCALE","index":1,"state":"preserve-this-state"}`, "mj_upscale"
				selected = &channels[1]
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("id", user.Id)
			c.Set("username", user.Username)
			c.Set("user_quota", balance)
			c.Set("user_group", "default")
			c.Set("group", "default")
			c.Set("token_group", "default")
			c.Set("token_id", token.Id)
			c.Set("token_key", token.Key)
			c.Set("token_name", token.Name)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, selected, modelName))
			if tc.pinned {
				service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: selected.Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			if tc.strict {
				c.Set("channel_affinity_skip_retry_on_failure", true)
			}
			t.Cleanup(func() { common.CleanupBodyStorage(c) })

			RelayMidjourney(c)

			wantCalls := int32(1)
			if tc.noBalance {
				wantCalls = 0
			}
			assert.Equal(t, wantCalls, calls[0].Load())
			wantFallbackCalls := int32(0)
			if tc.wantFallback {
				wantFallbackCalls = 1
			}
			assert.Equal(t, wantFallbackCalls, calls[1].Load())
			for provider := range 2 {
				var stored model.Channel
				require.NoError(t, db.First(&stored, channels[provider].Id).Error)
				wantStatus := common.ChannelStatusEnabled
				if provider == 0 && tc.wantDisabled || provider == 1 && tc.fallbackQuota && !tc.globalOff {
					wantStatus = common.ChannelStatusAutoDisabled
				}
				assert.Equal(t, wantStatus, stored.Status)
				channelCharge := 0
				if tc.wantCharge && (provider == 0 && !tc.wantFallback || provider == 1 && tc.wantFallback) {
					channelCharge = charge
				}
				assert.EqualValues(t, channelCharge, stored.UsedQuota)
				var ability model.Ability
				require.NoError(t, db.Where("channel_id = ? AND model = ?", stored.Id, modelName).First(&ability).Error)
				assert.Equal(t, wantStatus == common.ChannelStatusEnabled, ability.Enabled)
			}
			var tasks []model.Midjourney
			require.NoError(t, db.Where("user_id = ? AND id <> ?", user.Id, origin.Id).Find(&tasks).Error)
			assert.Len(t, tasks, tc.wantTasks)
			wantCharge := 0
			if tc.wantCharge {
				wantCharge = charge
				require.Len(t, tasks, 1)
				chargedChannel := channels[0].Id
				if tc.wantFallback {
					chargedChannel = channels[1].Id
					assert.Contains(t, recorder.Body.String(), fmt.Sprintf("fallback-%d", index))
				}
				assert.Equal(t, chargedChannel, tasks[0].ChannelId)
				assert.Equal(t, chargedChannel, tasks[0].BillingChannelId)
				assert.Equal(t, charge, tasks[0].Quota)
				assert.Equal(t, http.StatusOK, recorder.Code)
			} else if tc.wantDisabled || tc.fallbackQuota {
				assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, balance-wantCharge, user.Quota)
			assert.Equal(t, wantCharge, user.UsedQuota)
			assert.Equal(t, tokenBalance-wantCharge, token.RemainQuota)
			assert.Equal(t, wantCharge, token.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			wantLogs := 0
			if tc.wantCharge {
				wantLogs = 1
			}
			assert.Len(t, logs, wantLogs)
			if tc.wantFallback && tc.wantCharge {
				require.Len(t, logs, 1)
				var other struct {
					AdminInfo struct {
						UsedChannels []string `json:"use_channel"`
					} `json:"admin_info"`
				}
				require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
				assert.Equal(t, []string{fmt.Sprint(channels[0].Id), fmt.Sprint(channels[1].Id)}, other.AdminInfo.UsedChannels)
			}
		})
	}
}

func TestMidjourneyPollDelayedResults(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Midjourney{}, &model.Task{}, &model.User{}, &model.Channel{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.DataExportEnabled = false, false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldExport
	})
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}

	for index, tc := range []struct {
		name             string
		age              time.Duration
		status           string
		progress         string
		failReason       string
		async            bool
		queryUnavailable bool
		wantStatus       string
		wantProgress     string
		wantReason       string
		wantRefund       bool
	}{
		{name: "legacy delayed success", age: 2 * time.Hour, status: "SUCCESS", progress: "100%", queryUnavailable: true, wantStatus: "SUCCESS", wantProgress: "100%"},
		{name: "async delayed success", age: 2 * time.Hour, status: "SUCCESS", progress: "100%", async: true, wantStatus: "SUCCESS", wantProgress: "100%"},
		{name: "success without progress", age: 2 * time.Hour, status: "SUCCESS", async: true, wantStatus: "SUCCESS", wantProgress: "100%"},
		{name: "upstream failure keeps reason", age: 2 * time.Hour, status: "FAILURE", progress: "20%", failReason: "provider rejected task", async: true, wantStatus: "FAILURE", wantProgress: "100%", wantReason: "provider rejected task", wantRefund: true},
		{name: "failure without reason", age: 2 * time.Hour, status: "FAILURE", wantStatus: "FAILURE", wantProgress: "100%", wantRefund: true},
		{name: "old unfinished task stays pending", age: 2 * time.Hour, status: "IN_PROGRESS", progress: "80%", wantStatus: "IN_PROGRESS", wantProgress: "80%"},
		{name: "recent task remains pending", age: 30 * time.Minute, status: "IN_PROGRESS", progress: "80%", async: true, wantStatus: "IN_PROGRESS", wantProgress: "80%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const charged = 175000
			const balance = 1000000
			user := model.User{Username: fmt.Sprintf("mj_poll_%d", index), AffCode: fmt.Sprintf("mj_%d", index), Quota: balance, UsedQuota: charged}
			require.NoError(t, db.Create(&user).Error)
			submitTime := time.Now().Add(-tc.age).UnixMilli()
			upstream := dto.MidjourneyDto{
				MjId: fmt.Sprintf("mj_poll_%d", index), Action: constant.MjActionImagine,
				SubmitTime: submitTime, StartTime: submitTime + 1000,
				Status: tc.status, Progress: tc.progress, FailReason: tc.failReason,
				ImageUrl: "https://example.com/result.webp",
			}
			if tc.status == "SUCCESS" || tc.status == "FAILURE" {
				upstream.FinishTime = submitTime + 65000
			}
			body, err := common.Marshal([]dto.MidjourneyDto{upstream})
			require.NoError(t, err)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.queryUnavailable && requests.Add(1) <= 2 {
					w.WriteHeader(http.StatusGatewayTimeout)
					return
				}
				assert.Equal(t, "/mj/task/list-by-condition", r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write(body)
				assert.NoError(t, err)
			}))
			defer server.Close()
			channel := model.Channel{Name: tc.name, Type: constant.ChannelTypeMidjourney, BaseURL: &server.URL, UsedQuota: charged}
			require.NoError(t, db.Create(&channel).Error)
			task := model.Midjourney{
				MjId: upstream.MjId, UserId: user.Id, ChannelId: channel.Id, Code: 1,
				Action: constant.MjActionImagine, SubmitTime: submitTime, Status: "SUBMITTED", Progress: "0%", Quota: charged,
			}
			require.NoError(t, db.Create(&task).Error)
			t.Cleanup(func() { require.NoError(t, db.Delete(&task).Error) })
			if tc.async {
				asyncTask := model.Task{
					TaskID: task.MjId, Platform: constant.TaskPlatformMidjourney, UserId: user.Id, ChannelId: channel.Id,
					Action: task.Action, SubmitTime: submitTime / 1000, Status: model.TaskStatusSubmitted, Progress: "0%", Quota: charged,
					PrivateData: model.TaskPrivateData{BillingState: service.TaskBillingStatePending},
				}
				require.NoError(t, db.Create(&asyncTask).Error)
			}
			if tc.queryUnavailable {
				_, pollErr := runMidjourneyTaskUpdateOnce(context.Background(), nil)
				require.Error(t, pollErr)
				var unchanged model.Midjourney
				require.NoError(t, db.First(&unchanged, task.Id).Error)
				assert.Equal(t, "SUBMITTED", unchanged.Status)
				assert.Equal(t, "0%", unchanged.Progress)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, balance, user.Quota)
			}

			_, pollErr := runMidjourneyTaskUpdateOnce(context.Background(), nil)
			require.NoError(t, pollErr)
			var updated model.Midjourney
			require.NoError(t, db.First(&updated, task.Id).Error)
			assert.Equal(t, tc.wantStatus, updated.Status)
			assert.Equal(t, tc.wantProgress, updated.Progress)
			assert.Equal(t, tc.wantReason, updated.FailReason)
			assert.Equal(t, upstream.ImageUrl, updated.ImageUrl)
			assert.Equal(t, upstream.FinishTime, updated.FinishTime)
			if tc.async {
				var asyncTask model.Task
				require.NoError(t, db.Where("task_id = ?", task.MjId).First(&asyncTask).Error)
				assert.Equal(t, model.TaskStatus(tc.wantStatus), asyncTask.Status)
				assert.Equal(t, tc.wantProgress, asyncTask.Progress)
				assert.Equal(t, tc.wantReason, asyncTask.FailReason)
				assert.Equal(t, upstream.ImageUrl, asyncTask.PrivateData.ResultURL)
				wantBilling := service.TaskBillingStatePending
				if tc.wantStatus == "SUCCESS" {
					wantBilling = service.TaskBillingStateSettled
				} else if tc.wantRefund {
					wantBilling = service.TaskBillingStateRefunded
				}
				assert.Equal(t, wantBilling, asyncTask.PrivateData.BillingState)
			}

			// A second pass must neither refund a successful task nor refund a
			// failed task twice, including the asynchronous billing path.
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			wantBalance, wantUsed := balance, charged
			var wantRefundLogs int64
			if tc.wantRefund {
				wantBalance, wantUsed, wantRefundLogs = balance+charged, 0, 1
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			assert.Equal(t, wantBalance, user.Quota)
			assert.Equal(t, wantUsed, user.UsedQuota)
			assert.EqualValues(t, wantUsed, channel.UsedQuota)
			var refundLogs int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Count(&refundLogs).Error)
			assert.Equal(t, wantRefundLogs, refundLogs)
		})
	}
}

// The scheduler uses fixed table names in its lease subqueries. Keep its two
// tables unprefixed while retaining isolated names for the task fixtures.
type midjourneyPollTestNaming struct{ schema.Namer }

func (n midjourneyPollTestNaming) TableName(name string) string {
	if name == "SystemTask" || name == "SystemTaskLock" {
		return schema.NamingStrategy{}.TableName(name)
	}
	return n.Namer.TableName(name)
}

func TestMidjourneyPollRecovery(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Midjourney{}, &model.Task{}, &model.Channel{})
	db.Config.NamingStrategy = midjourneyPollTestNaming{db.Config.NamingStrategy}
	require.False(t, db.Migrator().HasTable(&model.SystemTask{}), "use a dedicated test database")
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.SystemTask{}, &model.SystemTaskLock{})) })
	oldDB := model.DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	model.DB = db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
	})
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	for index, tc := range []struct {
		name         string
		batchStatus  int
		batchBody    string
		singleStatus int
		wrongID      bool
		writeFailure bool
		cancel       bool
		wantFailed   bool
		wantSuccess  int
		wantSingles  int32
	}{
		{name: "batch success", wantSuccess: 2},
		{name: "unsupported batch recovers individually", batchStatus: 404, wantSuccess: 2, wantSingles: 2},
		{name: "missing batch results recover individually", batchBody: "[]", wantSuccess: 2, wantSingles: 2},
		{name: "malformed batch recovers individually", batchBody: "{}", wantSuccess: 2, wantSingles: 2},
		{name: "partial results saved despite fallback failure", batchBody: "partial", singleStatus: 503, wantFailed: true, wantSuccess: 1, wantSingles: 1},
		{name: "upstream unavailable stays pending", batchStatus: 503, singleStatus: 503, wantFailed: true, wantSingles: 1},
		{name: "wrong single task rejected", batchBody: "[]", wrongID: true, wantFailed: true, wantSingles: 1},
		{name: "async write failure rolls back MJ result", writeFailure: true, wantFailed: true},
		{name: "cancelled polling stays pending", cancel: true, wantFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := []string{fmt.Sprintf("recovery_%d_a", index), fmt.Sprintf("recovery_%d_b", index)}
			results := []dto.MidjourneyDto{
				{MjId: ids[0], Status: "SUCCESS", Progress: "100%", ImageUrl: "https://example.com/a.png", FinishTime: time.Now().UnixMilli()},
				{MjId: ids[1], Status: "SUCCESS", Progress: "100%", ImageUrl: "https://example.com/b.png", FinishTime: time.Now().UnixMilli()},
			}
			var singles atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "test-secret", r.Header.Get("mj-api-secret"))
				var body any = results
				if r.Method == http.MethodPost {
					assert.Equal(t, "/mj/task/list-by-condition", r.URL.Path)
					if tc.batchStatus != 0 {
						w.WriteHeader(tc.batchStatus)
						return
					}
					if tc.batchBody == "partial" {
						body = results[:1]
					} else if tc.batchBody != "" {
						_, _ = w.Write([]byte(tc.batchBody))
						return
					}
				} else {
					singles.Add(1)
					if tc.singleStatus != 0 {
						w.WriteHeader(tc.singleStatus)
						return
					}
					item := results[0]
					if r.URL.Path == "/mj/task/"+ids[1]+"/fetch" {
						item = results[1]
					}
					if tc.wrongID {
						item.MjId = "unrelated-task"
					}
					body = item
				}
				encoded, err := common.Marshal(body)
				assert.NoError(t, err)
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			channel := model.Channel{Name: tc.name, Key: "test-secret", BaseURL: &server.URL, Type: constant.ChannelTypeMidjourney}
			require.NoError(t, db.Create(&channel).Error)
			for _, id := range ids {
				mj := model.Midjourney{MjId: id, UserId: 1, ChannelId: channel.Id, Code: 1, SubmitTime: time.Now().Add(-2 * time.Hour).UnixMilli(), Status: "SUBMITTED", Progress: "0%"}
				require.NoError(t, db.Create(&mj).Error)
				task := model.Task{TaskID: id, UserId: 1, ChannelId: channel.Id, Platform: constant.TaskPlatformMidjourney, Status: model.TaskStatusSubmitted, Progress: "0%"}
				require.NoError(t, db.Create(&task).Error)
			}
			t.Cleanup(func() {
				require.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.Midjourney{}).Error)
				require.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.Task{}).Error)
			})
			if tc.writeFailure {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:mj_async_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
						tx.AddError(errors.New("injected async write failure"))
					}
				}))
				defer func() { require.NoError(t, db.Callback().Update().Remove("test:mj_async_failure")) }()
			}
			job, err := model.CreateSystemTask(model.SystemTaskTypeMidjourneyPoll, nil, nil)
			require.NoError(t, err)
			job, claimed, err := model.ClaimSystemTask(job.ID, job.Type, "mj-test", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.True(t, claimed)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			midjourneyPollHandler{}.Run(ctx, job, "mj-test")
			require.NoError(t, db.First(job, job.ID).Error)
			wantStatus := model.SystemTaskStatusSucceeded
			if tc.wantFailed {
				wantStatus = model.SystemTaskStatusFailed
				assert.NotEmpty(t, job.Error)
			} else {
				assert.Empty(t, job.Error)
			}
			assert.Equal(t, wantStatus, job.Status)
			assert.Equal(t, tc.wantSingles, singles.Load())
			for i, id := range ids {
				var mj model.Midjourney
				var task model.Task
				require.NoError(t, db.Where("mj_id = ?", id).First(&mj).Error)
				require.NoError(t, db.Where("task_id = ?", id).First(&task).Error)
				if i < tc.wantSuccess {
					assert.Equal(t, "SUCCESS", mj.Status)
					assert.Equal(t, "100%", mj.Progress)
					assert.Equal(t, results[i].ImageUrl, mj.ImageUrl)
					assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
					assert.Equal(t, results[i].ImageUrl, task.PrivateData.ResultURL)
				} else {
					assert.Equal(t, "SUBMITTED", mj.Status)
					assert.Equal(t, "0%", mj.Progress)
					assert.Empty(t, mj.ImageUrl)
					assert.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), task.Status)
				}
			}
		})
	}
}
