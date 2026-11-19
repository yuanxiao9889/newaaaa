package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginImageRetry(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprintf("guarded=%t", guarded), func(t *testing.T) { testPluginImageRetry(t, guarded) })
	}
}

func testPluginImageRetry(t *testing.T, guarded bool) {
	oldMaxDownload := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() { constant.MaxFileDownloadMB = oldMaxDownload })
	oldGroupRatios := ratio_setting.GroupGroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"vip":{"default":0.9}}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(oldGroupRatios)) })
	for _, scenario := range []string{"451", "ordinary_moderation", "ordinary_tokens", "ordinary_first", "ordinary_backup", "ordinary_cheaper", "ordinary_wallet_short", "ordinary_429", "single_channel", "category_limit", "limit_ten", "legacy_no_budget", "expired", "immediate_failure", "poll_failure", "503", "429", "vip", "cheaper", "all_rejected", "no_budget", "time_budget", "cycle_limit", "pinned", "strict", "400", "504", "524", "truncated", "disconnect", "invalid_success", "poll_error", "download_error", "resume", "uncertain", "wallet_short", "token_short", "stale_worker", "stale_completion"} {
		t.Run(scenario, func(t *testing.T) {
			mixed := strings.HasPrefix(scenario, "ordinary_")
			ordinaryFirst := scenario == "ordinary_first" || scenario == "ordinary_moderation"
			db := setupAsyncImageWorkerTestDB(t)
			if guarded {
				require.NoError(t, db.AutoMigrate(&model.TransferRequest{}))
				require.NoError(t, db.Where("user_id = ?", 61).Delete(&model.TransferRequest{}).Error)
				// PostgreSQL index names share a schema namespace. Release this
				// fixture's explicit unique index before prefixed ledger fixtures run.
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.TransferRequest{})) })
			}
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}, &model.Option{}, &model.ChannelAutoSwitchState{}, &model.ChannelAutoSwitchAttempt{}))
			require.NoError(t, db.Where("channel_id IN ?", []int{911, 912}).Delete(&model.Ability{}).Error)
			// A fresh state starts generation 1 again; old attempt rows would
			// otherwise contaminate its health window on shared test databases.
			require.NoError(t, db.Where("1 = 1").Delete(&model.ChannelAutoSwitchAttempt{}).Error)
			require.NoError(t, db.Where("channel_id IN ?", []int{911, 912}).Delete(&model.ChannelAutoSwitchState{}).Error)
			oldRegistry, oldMemory, oldBatch, oldLog, oldEnabled := pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled
			pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
			common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled = false, false, true, true
			t.Cleanup(func() {
				pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled = oldRegistry, oldMemory, oldBatch, oldLog, oldEnabled
			})
			common.OptionMapRWMutex.Lock()
			oldOptions := common.OptionMap
			common.OptionMap = maps.Clone(model.CurrentRequestPolicy().Options)
			common.OptionMapRWMutex.Unlock()
			oldPolicy, oldRetries := model.CurrentRequestPolicy().Options, common.RetryTimes
			t.Cleanup(func() {
				require.NoError(t, model.UpdateRequestPolicyOptions(oldPolicy))
				common.RetryTimes = oldRetries
				common.OptionMapRWMutex.Lock()
				common.OptionMap = oldOptions
				common.OptionMapRWMutex.Unlock()
			})
			require.NoError(t, model.UpdateRequestPolicyOptions(map[string]string{"RetryTimes": "2", "RetryCycleEnabled": strconv.FormatBool(scenario == "cycle_limit"), "RetryTimeBudgetEnabled": strconv.FormatBool(scenario == "time_budget" || scenario == "cycle_limit"), "RetryTimeBudgetSeconds": "60", "RetryTimeBudgetMaxRetries": "1", "AutomaticRetryStatusCodes": "429,500-503"}))
			if scenario == "no_budget" || scenario == "legacy_no_budget" {
				common.RetryTimes = 0
			}
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			withTieredBillingConfig(t, map[string]string{"retry-image": "tiered_expr"}, map[string]string{"retry-image": `u("images") * 0.1`})
			billing := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
			oldOverrides := billing.PluginBillingExpr
			groupRatio := 1.0
			if scenario == "vip" {
				groupRatio = 0.9
			}
			price := 0.3
			if scenario == "cheaper" || scenario == "stale_completion" || scenario == "ordinary_cheaper" {
				price = 0.05
			}
			billing.PluginBillingExpr = map[string]string{"retry-b::retry-image": fmt.Sprintf(`u("images") * %g`, price)}
			if mixed {
				ordinaryPrice := price
				if ordinaryFirst {
					ordinaryPrice = 0.1
				}
				withTieredBillingConfig(t, map[string]string{"retry-image": "tiered_expr"}, map[string]string{"retry-image": fmt.Sprintf(`tier("base", fixed(%g)) * image_count`, ordinaryPrice)})
				billing.PluginBillingExpr["retry-a::retry-image"] = `u("images") * 0.1`
				if scenario == "ordinary_tokens" {
					withTieredBillingConfig(t, map[string]string{"retry-image": "tiered_expr"}, map[string]string{"retry-image": `param("size") == "1024x1024" ? tier("tokens", p * 1000 + c * 1000) : tier("other", p * 3000 + c * 3000)`})
				}
			}

			t.Cleanup(func() { billing.PluginBillingExpr = oldOverrides })
			for _, key := range []string{"retry-a", "retry-b"} {
				source := fmt.Sprintf(`export const meta={apiVersion:1,key:%q,name:"Retry image",version:"1.0.0",author:{name:"Test"},models:["retry-image","upstream-0","upstream-1"],protocols:["openai_image"],fetchMode:"per_task",usageSchema:{images:{type:"number",unit:"count",description:{en:"Image generation unit price",zh:"图片生成单价"}}}};
export const protocols={openai_image:{decodeRequest(ctx){return {kind:"submit",model:ctx.model,action:"text_to_image",requestBody:ctx.body.value};},render(ctx,task){return task.data;}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",body:{model:ctx.upstreamModel,prompt:ctx.requestBody.prompt}};}
export function parseSubmitResponse(ctx,r){if(r.body.failure)return {taskId:ctx.publicTaskId,taskData:r.body,immediate:{status:"FAILURE",reason:"content_policy"}};if(r.body.pending)return {taskId:"provider-job",taskData:r.body};if(!r.body.data)throw new Error("missing image");return {taskId:ctx.publicTaskId,taskData:r.body,immediate:{status:"SUCCESS",url:r.body.data[0].url}};}
export function extractUsage(){return {images:1};}
export function extractUsageOnComplete(){return {images:1};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/poll",method:"GET"};}
export function parseTaskResult(ctx,r){return {status:r.status||"UNKNOWN",reason:r.reason};}`, key)
				_, err := pluginruntime.DefaultRegistry.Register(source, pluginruntime.Options{})
				require.NoError(t, err)
			}
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "image-retry", Status: common.UserStatusEnabled, Quota: 1000000}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "retry-token", Name: "test", Status: common.TokenStatusEnabled, RemainQuota: 1000000, ExpiredTime: -1}).Error)
			var img bytes.Buffer
			require.NoError(t, png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))))
			var posts [2]atomic.Int32
			var completionReclaimed atomic.Bool
			var task model.Task
			fetch := system_setting.GetFetchSetting()
			oldFetch := *fetch
			fetch.AllowPrivateIp = true
			fetch.AllowedPorts = nil
			t.Cleanup(func() { *fetch = oldFetch })
			for i := range 2 {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/result.png" {
						if scenario == "stale_completion" && completionReclaimed.CompareAndSwap(false, true) {
							var current model.Task
							require.NoError(t, db.First(&current, task.ID).Error)
							current.PrivateData.WorkerAttempts++
							require.NoError(t, db.Model(&current).Update("private_data", current.PrivateData).Error)
						}
						if scenario == "download_error" {
							w.WriteHeader(500)
							return
						}
						w.Header().Set("Content-Type", "image/png")
						_, _ = w.Write(img.Bytes())
						return
					}
					if r.URL.Path == "/poll" {
						if scenario == "poll_failure" {
							_, _ = w.Write([]byte(`{"status":"FAILURE","reason":"content_policy"}`))
							return
						}
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":"poll unavailable"}`))
						return
					}
					posts[i].Add(1)
					var request map[string]any
					require.NoError(t, common.DecodeJson(r.Body, &request))
					assert.Equal(t, fmt.Sprintf("upstream-%d", i), request["model"])
					assert.Equal(t, "original prompt", request["prompt"])
					if i == 0 {
						if scenario == "451" || scenario == "ordinary_moderation" {
							w.WriteHeader(http.StatusUnavailableForLegalReasons)
							_, _ = w.Write([]byte(`{"error":{"code":"unknown_error","type":"openai_error","message":"本次请求未通过内容安全审核，请调整提示词或参考图后重新提交"}}`))
							return
						}
						if scenario == "immediate_failure" {
							_, _ = w.Write([]byte(`{"failure":true}`))
							return
						}
						if scenario == "poll_failure" {
							_, _ = w.Write([]byte(`{"pending":true}`))
							return
						}
						if scenario == "category_limit" {
							w.WriteHeader(400)
							_, _ = w.Write([]byte(`{"error":{"code":"invalid_parameter","message":"size rejected"}}`))
							return
						}
						if scenario == "stale_worker" {
							var current model.Task
							require.NoError(t, db.First(&current, task.ID).Error)
							current.PrivateData.WorkerAttempts++
							require.NoError(t, db.Model(&current).Update("private_data", current.PrivateData).Error)
						}
						if scenario == "disconnect" {
							conn, _, err := w.(http.Hijacker).Hijack()
							require.NoError(t, err)
							require.NoError(t, conn.Close())
							return
						}
						if scenario == "invalid_success" {
							_, _ = w.Write([]byte(`{}`))
							return
						}
						if scenario == "poll_error" {
							_, _ = w.Write([]byte(`{"pending":true}`))
							return
						}
						code := 503
						if scenario == "429" || scenario == "ordinary_429" {
							code = 429
						}
						if scenario == "400" {
							code = 400
						}
						if scenario == "504" || scenario == "524" {
							code, _ = strconv.Atoi(scenario)
						}
						if scenario == "truncated" {
							w.Header().Set("Content-Length", "500")
						}
						w.WriteHeader(code)
						_, _ = w.Write([]byte(`{"error":{"code":"service_model_unavailable","message":"unavailable"}}`))
						return
					}
					var reserved model.Task
					require.NoError(t, db.First(&reserved, task.ID).Error)
					reservedPrice := max(price, 0.1)
					if scenario == "ordinary_tokens" {
						reservedPrice = 1.5
					}
					assert.Equal(t, common.QuotaRound(reservedPrice*common.QuotaPerUnit*groupRatio), reserved.Quota, "replacement is covered before POST")
					if scenario == "category_limit" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":{"code":"invalid_parameter","message":"second wording"}}`))
						return
					}
					if scenario == "all_rejected" || scenario == "cycle_limit" || scenario == "limit_ten" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":"unavailable"}`))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if scenario == "ordinary_tokens" {
						_, _ = fmt.Fprintf(w, `{"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300},"data":[{"url":"http://%s/result.png"}]}`, r.Host)
					} else {
						_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/result.png"}]}`, r.Host)
					}
				}))
				t.Cleanup(upstream.Close)
				u, err := url.Parse(upstream.URL)
				require.NoError(t, err)
				fetch.AllowedPorts = append(fetch.AllowedPorts, u.Port())
				mapping := fmt.Sprintf(`{"retry-image":"upstream-%d"}`, i)
				priority := int64(2 - i)
				ch := model.Channel{Id: 911 + i, Name: fmt.Sprintf("retry-%d", i), Type: constant.ChannelTypeTaskPlugin, Key: "test-key", BaseURL: &upstream.URL, Models: "retry-image", ModelMapping: &mapping, Group: "default", Status: common.ChannelStatusEnabled, Priority: &priority}
				ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: []string{"retry-a", "retry-b"}[i]})
				if mixed && (ordinaryFirst && i == 0 || !ordinaryFirst && i == 1) {
					ch.Type = constant.ChannelTypeOpenAI
					ch.SetSetting(kitdto.ChannelSettings{})
				}

				settings := dto.ChannelOtherSettings{AutoSwitchEnabled: true}
				if scenario == "category_limit" || scenario == "limit_ten" {
					settings.AutoSwitchMaxAttempts = common.GetPointer(10)
				}
				ch.SetOtherSettings(settings)
				if i == 1 && scenario == "single_channel" {
					ch.Status = common.ChannelStatusManuallyDisabled
				}
				require.NoError(t, db.Create(&ch).Error)
				require.NoError(t, ch.AddAbilities(db))
				t.Cleanup(func() { require.NoError(t, ch.DeleteAbilities()) })
			}
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				c.Set("token_name", "test")
				common.SetContextKey(c, constant.ContextKeyUserId, 61)
				common.SetContextKey(c, constant.ContextKeyTokenId, 62)
				common.SetContextKey(c, constant.ContextKeyTokenKey, "retry-token")
				common.SetContextKey(c, constant.ContextKeyUserQuota, 1000000)
				common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
				userGroup := "default"
				if scenario == "vip" {
					userGroup = "vip"
				}
				common.SetContextKey(c, constant.ContextKeyUserGroup, userGroup)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
				c.Next()
			})
			if guarded {
				engine.Use(middleware.TransferRequestGuard)
			}
			engine.POST("/v1/images/generations", middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(c *gin.Context) {
				if v, plugin := c.Get(pluginruntime.ContextKeyPinnedEndpoint); plugin {
					serveTaskPluginImageProtocol(c, v.(pluginruntime.PinnedEndpoint), pluginProtocolBridgeDeps{})
				} else {
					Relay(c, types.RelayFormatOpenAIImage)
				}
			})
			req := httptest.NewRequest("POST", "/v1/images/generations?async=true", strings.NewReader(`{"model":"retry-image","prompt":"original prompt","size":"1024x1024"}`))
			req.Header.Set("Content-Type", "application/json")
			if guarded {
				req.Header.Set("Idempotency-Key", "bridge-image-retry-0001")
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)
			require.Equal(t, 200, recorder.Code, recorder.Body.String())
			var submitted dto.ImageAsyncSubmitResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &submitted))
			require.NoError(t, db.Where("task_id = ?", submitted.TaskID).First(&task).Error)
			if !ordinaryFirst {
				require.Equal(t, 911, task.ChannelId)
			}
			if guarded {
				require.NotEmpty(t, task.PrivateData.TransferExecutionID)
				require.NotNil(t, task.PrivateData.MediaRetry, "guarded request adopts the selected channel budget")
				executionID := task.PrivateData.TransferExecutionID
				// Replaying the original request before and after worker execution
				// must return the same receipt without reserving or posting again.
				checkReplay := func() {
					t.Helper()
					beforePosts := posts[0].Load() + posts[1].Load()
					var before, after model.User
					require.NoError(t, db.First(&before, 61).Error)
					replay := httptest.NewRequest(http.MethodPost, req.URL.String(), strings.NewReader(`{"model":"retry-image","prompt":"original prompt","size":"1024x1024"}`))
					replay.Header = req.Header.Clone()
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, replay)
					require.Equal(t, recorder.Code, response.Code, response.Body.String())
					assert.Equal(t, "true", response.Header().Get("X-Transfer-Replayed"))
					assert.Equal(t, executionID, response.Header().Get("X-Transfer-Execution-Id"))
					assert.JSONEq(t, recorder.Body.String(), response.Body.String())
					require.NoError(t, db.First(&after, 61).Error)
					assert.Equal(t, before.Quota, after.Quota)
					assert.Equal(t, before.UsedQuota, after.UsedQuota)
					assert.Equal(t, beforePosts, posts[0].Load()+posts[1].Load())
					var count int64
					require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
					assert.EqualValues(t, 1, count)
				}
				checkReplay()
				defer checkReplay()
			}
			require.True(t, claimInternalAsyncImageTask(&task))
			if scenario == "legacy_no_budget" {
				task.PrivateData.MediaRetry = nil
			}
			if scenario == "expired" {
				task.PrivateData.MediaRetry.DeadlineMS = time.Now().Add(-time.Second).UnixMilli()
			}
			if scenario == "uncertain" {
				task.PrivateData.ImageRetry.Submitting = true
			}
			if scenario == "strict" {
				task.PrivateData.ImageRetry.SkipRetry = true
			}
			if scenario == "pinned" {
				task.PrivateData.ImageRetry.Constraints.AddPin(dto.ChannelPin{ChannelId: 911, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			if scenario == "time_budget" {
				task.PrivateData.ImageRetry.StartedAtMS = time.Now().Add(-2 * time.Minute).UnixMilli()
			}
			if scenario == "wallet_short" || scenario == "ordinary_wallet_short" {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 61).Update("quota", 0).Error)
			}
			if scenario == "token_short" {
				require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 62).Update("remain_quota", 0).Error)
			}
			require.NoError(t, persistAsyncImageAttempt(&task))
			if scenario == "resume" {
				_, _, err := runQueuedTaskPluginImage(t.Context(), &task)
				require.NoError(t, err)
				require.NoError(t, db.First(&task, task.ID).Error)
			}
			err := executeInternalAsyncImageTask(t.Context(), &task)
			succeeded := mixed && scenario != "ordinary_wallet_short" && scenario != "ordinary_moderation" || scenario == "vip" || scenario == "503" || scenario == "429" || scenario == "cheaper" || scenario == "resume" || scenario == "no_budget" || scenario == "time_budget" || scenario == "400"
			if succeeded {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, db.First(&task, task.ID).Error)
			wantFirst := int32(1)
			if scenario == "all_rejected" || scenario == "cycle_limit" {
				wantFirst = 2
			}
			if scenario == "category_limit" {
				wantFirst = 2
			}
			if scenario == "single_channel" {
				wantFirst = 3
			}
			if scenario == "limit_ten" {
				wantFirst = 5
			}
			if scenario == "uncertain" || scenario == "expired" {
				wantFirst = 0
			}
			assert.Equal(t, wantFirst, posts[0].Load())
			wantBackup := int32(0)
			if succeeded || scenario == "all_rejected" || scenario == "download_error" || scenario == "cycle_limit" || scenario == "stale_completion" {
				wantBackup = 1
			}
			if scenario == "category_limit" {
				wantBackup = 2
			}
			if scenario == "limit_ten" {
				wantBackup = 5
			}
			assert.Equal(t, wantBackup, posts[1].Load())
			if scenario == "451" || scenario == "ordinary_moderation" || scenario == "immediate_failure" || scenario == "poll_failure" {
				require.NotNil(t, task.PrivateData.MediaRetry)
				assert.Equal(t, "safety_related_error", task.PrivateData.MediaRetry.StopReason)
			}
			if state := task.PrivateData.MediaRetry; state != nil && scenario != "stale_worker" {
				assert.EqualValues(t, posts[0].Load()+posts[1].Load(), state.Attempts)
			}
			var user model.User
			var token model.Token
			require.NoError(t, db.First(&user, 61).Error)
			require.NoError(t, db.First(&token, 62).Error)
			var consumes, refunds, tasks int64
			require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypePreConsumeRollback).Count(&refunds).Error)
			assert.EqualValues(t, 1, tasks)
			if scenario == "stale_completion" {
				assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), task.Status)
				assert.Equal(t, 50000, task.Quota, "stale completion cannot change the durable reservation")
				assert.Equal(t, 950000, user.Quota)
				assert.Equal(t, 950000, token.RemainQuota)
				assert.Zero(t, consumes)
				assert.Zero(t, refunds)
				// Recovery renders the accepted replacement and settles its lower
				// price exactly once without posting either generation again.
				require.NoError(t, executeInternalAsyncImageTask(t.Context(), &task))
				require.NoError(t, db.First(&user, 61).Error)
				require.NoError(t, db.First(&token, 62).Error)
				assert.Equal(t, 975000, user.Quota)
				assert.Equal(t, 975000, token.RemainQuota)
				assert.EqualValues(t, 1, posts[0].Load())
				assert.EqualValues(t, 1, posts[1].Load())
				require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
				assert.EqualValues(t, 1, consumes)
				return
			}
			if scenario == "stale_worker" {
				assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), task.Status)
				assert.Equal(t, 950000, user.Quota)
				assert.Zero(t, refunds)
				return
			}
			charged := 0
			if succeeded {
				charged = common.QuotaRound(price * common.QuotaPerUnit * groupRatio)
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
				assert.EqualValues(t, 1, consumes)
				assert.Zero(t, refunds)
				if mixed && scenario != "ordinary_first" {
					if task.PrivateData.Execution != nil {
						assert.Nil(t, task.PrivateData.Execution.TaskPlugin)
					}
				} else {
					require.NotNil(t, task.PrivateData.Execution.TaskPlugin)
					assert.Equal(t, "retry-b", task.PrivateData.Execution.TaskPlugin.Key)
				}
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				assert.Zero(t, consumes)
				assert.EqualValues(t, 1, refunds)
			}
			wantUser, wantToken := 1000000-charged, 1000000-charged
			if scenario == "wallet_short" || scenario == "ordinary_wallet_short" {
				wantUser = 50000
			}
			if scenario == "token_short" {
				wantToken = 50000
			}
			assert.Equal(t, wantUser, user.Quota)
			assert.Equal(t, wantToken, token.RemainQuota)
			if !succeeded {
				require.NoError(t, service.RefundTaskQuota(t.Context(), &task, "repeat recovery"))
				var again model.User
				require.NoError(t, db.First(&again, 61).Error)
				assert.Equal(t, wantUser, again.Quota)
			}
		})
	}
}

func TestHyImageQueueLifecycle(t *testing.T) {
	testImageQueueLifecycle(t, false)
}

func TestKuaiaiImageQueueLifecycle(t *testing.T) {
	testImageQueueLifecycle(t, true)
}

func testImageQueueLifecycle(t *testing.T, kuaiai bool) {
	oldMaxDownload := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() { constant.MaxFileDownloadMB = oldMaxDownload })
	scenarios := []string{"generate", "edit", "rejected", "resume", "uncertain", "mapping_changed", "insufficient_quota", "async_plugin"}
	if kuaiai {
		scenarios = []string{"edit_fallback", "edit_ordinary_first", "generate", "edit", "edit_auto", "edit_duplicate_size", "edit_json", "flare", "empty_result", "b64", "b64_large", "vip", "price_changed", "ordinary", "rejected", "resume", "uncertain", "mapping_changed", "insufficient_quota"}
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			mixedEdit := scenario == "edit_fallback" || scenario == "edit_ordinary_first"
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}))
			oldRegistry, oldMemory, oldBatch, oldLog, oldEnabled := pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled
			pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
			common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled = false, false, true, true
			t.Cleanup(func() {
				pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.AsyncImageInternalTaskEnabled = oldRegistry, oldMemory, oldBatch, oldLog, oldEnabled
			})
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			modelName := "hy-image-v3.5-preview"
			pluginKey := "tencent-hy-image"
			withTieredBillingConfig(t, map[string]string{modelName: "tiered_expr"}, map[string]string{modelName: `u("tokens") * 0.000001`})
			upstreamModel := modelName
			if kuaiai {
				modelName, upstreamModel, pluginKey = "all-image-2.5-sunburst-H", "gpt-image-2.5-sunburst", "kuaiai-image25"
				if scenario == "flare" {
					modelName, upstreamModel = "all-image-2.5-flare-H", "gpt-image-2.5-flare"
				}
				withTieredBillingConfig(t, map[string]string{modelName: "ratio", upstreamModel: "ratio"}, map[string]string{})
				groupRatios := ratio_setting.GroupGroupRatio2JSONString()
				require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"vip":{"default":0.9}}`))
				t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(groupRatios)) })
				prices := ratio_setting.ModelPrice2JSONString()
				t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"all-image-2.5-sunburst-H":0.2,"all-image-2.5-flare-H":0.2}`))
			}
			source, err := os.ReadFile("../plugins/tasks/" + pluginKey + "/plugin.js")
			require.NoError(t, err)
			if scenario == "async_plugin" {
				pluginKey = "queued-image-fixture"
				source = []byte(`
export const meta={apiVersion:1,key:"queued-image-fixture",name:"Async image fixture",version:"1.0.0",author:{name:"Test"},models:["hy-image-v3.5-preview"],fetchMode:"per_task",protocols:["openai_image"],usageSchema:{tokens:{type:"number",unit:"token"}},usageExamples:[{label:"test",facts:{tokens:100000}}]};
export const protocols={openai_image:{decodeRequest(ctx){return {kind:"submit",model:ctx.model,requestBody:ctx.body.value};},render(ctx,task){return {data:[{url:task.data.choices[0].delta.image.url}]};}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/v1/wand/hunyuan-image/v35-generation",body:{model:ctx.model,messages:[]}};}
export function parseSubmitResponse(){return {taskId:"provider-job",taskData:{status:"PENDING"}};}
export function extractUsage(){return {tokens:100000};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/tasks/"+ctx.taskId,method:"GET"};}
export function parseTaskResult(ctx,body){return {status:"SUCCESS",url:body.choices[0].delta.image.url};}
export function extractUsageOnComplete(){return {tokens:20000};}
`)
			}
			_, err = pluginruntime.DefaultRegistry.Register(string(source), pluginruntime.Options{})
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "hy_queue", Quota: 1000000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "hy-test-token", Name: "canvas", RemainQuota: 1000000, Status: common.TokenStatusEnabled}).Error)
			if scenario == "insufficient_quota" {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 61).Update("quota", 1).Error)
			}
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			if scenario == "b64_large" {
				// PNG permits ancillary text chunks. A large metadata payload
				// exercises real image delivery beyond the task JSON row limit.
				data := imageBytes.Bytes()
				chunk := append([]byte("Comment\x00"), bytes.Repeat([]byte("image metadata "), 100000)...)
				var large bytes.Buffer
				large.Write(data[:len(data)-12])
				require.NoError(t, binary.Write(&large, binary.BigEndian, uint32(len(chunk))))
				large.WriteString("tEXt")
				large.Write(chunk)
				require.NoError(t, binary.Write(&large, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("tEXt"), chunk...))))
				large.Write(data[len(data)-12:])
				imageBytes = large
			}

			var posts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/result.png" {
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(imageBytes.Bytes())
					return
				}
				if r.Method == http.MethodGet {
					assert.Equal(t, "/tasks/provider-job", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"choices":[{"delta":{"image":{"url":"http://%s/result.png"}}}]}`, r.Host)
					return
				}
				posts.Add(1)
				if kuaiai {
					assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
					if strings.HasPrefix(scenario, "edit") && scenario != "edit_json" {
						assert.Equal(t, "/v1/images/edits", r.URL.Path)
						require.NoError(t, r.ParseMultipartForm(1<<20))
						defer r.MultipartForm.RemoveAll()
						assert.Equal(t, upstreamModel, r.FormValue("model"))
						assert.Equal(t, "adobe", r.FormValue("image_backend"))
						assert.Equal(t, []string{"one", "two"}, r.MultipartForm.Value["extra"])
						pluginAttempt := mixedEdit && (scenario == "edit_fallback" && posts.Load() == 1 || scenario == "edit_ordinary_first" && posts.Load() == 2)
						if scenario == "edit" || pluginAttempt {
							assert.NotContains(t, r.MultipartForm.Value, "aspect_ratio")
						} else {
							assert.Equal(t, "16:9", r.FormValue("aspect_ratio"))
						}
						for _, field := range []string{"image", "image[]", "mask"} {
							if mixedEdit && field == "image[]" {
								continue
							}
							require.Len(t, r.MultipartForm.File[field], 1)
							file := r.MultipartForm.File[field][0]
							assert.Equal(t, "image/png", file.Header.Get("Content-Type"))
							content, err := file.Open()
							require.NoError(t, err)
							data, err := io.ReadAll(content)
							require.NoError(t, err)
							require.NoError(t, content.Close())
							assert.Equal(t, imageBytes.Bytes(), data)
						}
					} else {
						expectedPath := "/v1/images/generations"
						if scenario == "edit_json" {
							expectedPath = "/v1/images/edits"
						}
						assert.Equal(t, expectedPath, r.URL.Path)
						assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
						var payload map[string]any
						require.NoError(t, common.DecodeJson(r.Body, &payload))
						assert.Equal(t, upstreamModel, payload["model"])
						assert.Equal(t, "16:9", payload["aspect_ratio"])
					}
				} else {
					assert.Equal(t, "/v1/wand/hunyuan-image/v35-generation", r.URL.Path)
					var payload map[string]any
					assert.NoError(t, common.DecodeJson(r.Body, &payload))
					assert.Equal(t, modelName, payload["model"])
					assert.Contains(t, payload, "messages")
				}
				w.Header().Set("Content-Type", "application/json")
				if mixedEdit && posts.Load() == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"error":{"message":"capacity unavailable"}}`)
					return
				}
				if scenario == "rejected" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"rejected"}}`)
					return
				}
				if kuaiai {
					if scenario == "b64" || scenario == "b64_large" {
						_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(imageBytes.Bytes()))
					} else if scenario == "empty_result" {
						_, _ = io.WriteString(w, `{"data":[]}`)
					} else {
						_, _ = fmt.Fprintf(w, `{"created":123,"data":[{"url":"http://%s/result.png"}],"usage":{"total_tokens":777}}`, r.Host)
					}
				} else {
					_, _ = fmt.Fprintf(w, `{"choices":[{"delta":{"image":{"url":"http://%s/result.png"}}}],"tokenhub_usage":{"total_tokens":20000}}`, r.Host)
				}
			}))
			t.Cleanup(upstream.Close)
			upstreamURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			fetch := system_setting.GetFetchSetting()
			oldFetch := *fetch
			fetch.AllowPrivateIp, fetch.AllowedPorts = true, []string{upstreamURL.Port()}
			t.Cleanup(func() { *fetch = oldFetch })
			channel := model.Channel{Id: 911, Type: constant.ChannelTypeTaskPlugin, Name: "hy", Key: "test-key", BaseURL: &upstream.URL, Models: modelName, Group: "default", Status: common.ChannelStatusEnabled}
			if kuaiai {
				channel.Id = 139
				mapping := fmt.Sprintf(`{%q:%q}`, modelName, upstreamModel)
				channel.ModelMapping = &mapping
			}
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: pluginKey})
			if mixedEdit {
				channel.Priority = common.GetPointer(int64(2))
				channel.SetOtherSettings(dto.ChannelOtherSettings{AutoSwitchEnabled: true})
				backup := channel
				backup.Id = 140
				backup.Priority = common.GetPointer(int64(1))
				if scenario == "edit_ordinary_first" {
					channel.Type = constant.ChannelTypeOpenAI
					channel.SetSetting(kitdto.ChannelSettings{})
				} else {
					backup.Type = constant.ChannelTypeOpenAI
					backup.SetSetting(kitdto.ChannelSettings{})
				}
				require.NoError(t, db.Create(&backup).Error)
				require.NoError(t, backup.DeleteAbilities())
				require.NoError(t, backup.AddAbilities(db))
				t.Cleanup(func() { require.NoError(t, backup.DeleteAbilities()) })
			}
			if scenario == "ordinary" {
				channel.Id = 140
				channel.Type = constant.ChannelTypeOpenAI
				channel.SetSetting(kitdto.ChannelSettings{})
			}
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, channel.DeleteAbilities())
			require.NoError(t, channel.AddAbilities(db))
			t.Cleanup(func() { require.NoError(t, channel.DeleteAbilities()) })
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"lighthouse","size":"1536x1536","response_format":"url","aspect_ratio":"16:9"}`, modelName))
			path, contentType := "/v1/images/generations", "application/json"
			if scenario == "edit_json" {
				path = "/v1/images/edits"
				body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"lighthouse","image":"https://example.test/ref.png","size":"1536x1536","aspect_ratio":"16:9"}`, modelName))
			}
			if (strings.HasPrefix(scenario, "edit") && scenario != "edit_json") || scenario == "ordinary" {
				var form bytes.Buffer
				writer := multipart.NewWriter(&form)
				size := "1536x1536"
				if scenario == "edit_auto" {
					size = "auto"
				}
				for key, value := range map[string]string{"model": modelName, "prompt": "lighthouse", "size": size, "response_format": "url"} {
					require.NoError(t, writer.WriteField(key, value))
				}
				part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image"; filename="reference.png"`}, "Content-Type": {"image/png"}})
				require.NoError(t, err)
				_, err = part.Write(imageBytes.Bytes())
				require.NoError(t, err)
				if kuaiai {
					require.NoError(t, writer.WriteField("aspect_ratio", "16:9"))
					require.NoError(t, writer.WriteField("image_backend", "adobe"))
					require.NoError(t, writer.WriteField("extra", "one"))
					require.NoError(t, writer.WriteField("extra", "two"))
					if scenario == "edit_duplicate_size" {
						require.NoError(t, writer.WriteField("size", "1024x1024"))
					}
					for _, field := range []string{"image[]", "mask"} {
						if mixedEdit && field == "image[]" {
							continue
						}
						extra, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="reference.png"`, field)}, "Content-Type": {"image/png"}})
						require.NoError(t, err)
						_, err = extra.Write(imageBytes.Bytes())
						require.NoError(t, err)
					}
				}
				require.NoError(t, writer.Close())
				body, path, contentType = form.Bytes(), "/v1/images/edits", writer.FormDataContentType()
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path+"?async=true", bytes.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				c.Set("token_name", "canvas")
				common.SetContextKey(c, constant.ContextKeyUserId, 61)
				common.SetContextKey(c, constant.ContextKeyTokenId, 62)
				common.SetContextKey(c, constant.ContextKeyTokenKey, "hy-test-token")
				common.SetContextKey(c, constant.ContextKeyUserQuota, 1000000)
				common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
				userGroup := "default"
				if scenario == "vip" {
					userGroup = "vip"
				}
				common.SetContextKey(c, constant.ContextKeyUserGroup, userGroup)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
				c.Next()
			})
			// Exercise production dispatch: async requests must retain plugin identity
			// before channel selection, or a valid plugin channel is rejected with 503.
			engine.POST(path, middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(c *gin.Context) {
				value, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
				if scenario == "ordinary" {
					assert.False(t, exists, "same-model ordinary channel must bypass the plugin")
					require.NoError(t, c.Request.ParseMultipartForm(1<<20))
					assert.Equal(t, "16:9", c.Request.FormValue("aspect_ratio"))
					assert.Equal(t, "1536x1536", c.Request.FormValue("size"))
					c.JSON(http.StatusOK, gin.H{"ordinary": true})
					return
				}
				if mixedEdit && !exists {
					Relay(c, types.RelayFormatOpenAIImage)
					return
				}
				require.True(t, exists)
				serveTaskPluginImageProtocol(c, value.(pluginruntime.PinnedEndpoint), pluginProtocolBridgeDeps{})
			})
			engine.ServeHTTP(recorder, request)
			if scenario == "ordinary" {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.JSONEq(t, `{"ordinary":true}`, recorder.Body.String())
				assert.Zero(t, posts.Load())
				return
			}
			if scenario == "insufficient_quota" {
				assert.Equal(t, http.StatusPaymentRequired, recorder.Code, recorder.Body.String())
				var response struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Equal(t, "insufficient_user_quota", response.Error.Code)
				assert.Contains(t, response.Error.Message, "账户余额不足")
				assert.Zero(t, posts.Load())
				var tasks int64
				require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
				assert.Zero(t, tasks)
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var submission dto.ImageAsyncSubmitResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &submission))
			require.NotEmpty(t, submission.TaskID)
			assert.Zero(t, posts.Load(), "admission returns before generation starts")
			var task model.Task
			require.NoError(t, db.Where("task_id = ?", submission.TaskID).First(&task).Error)
			assert.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), task.Status)
			if scenario != "edit_ordinary_first" {
				assert.Equal(t, relay.PluginAsyncImageFormat, task.PrivateData.RequestRelayFormat)
			}
			assert.Equal(t, "canvas", task.TokenName)
			if kuaiai {
				assert.Nil(t, task.PrivateData.BillingContext.TieredSnapshot)
				expected := 0.2
				if scenario == "vip" {
					expected *= 0.9
				}
				assert.Equal(t, common.QuotaRound(expected*common.QuotaPerUnit), task.Quota)
				if scenario != "edit_ordinary_first" {
					assert.Equal(t, 139, task.ChannelId)
				}
			} else {
				require.NotNil(t, task.PrivateData.BillingContext.TieredSnapshot)
			}
			// Submission must be visible in the async task APIs before the worker
			// starts. Usage logs remain empty until settlement or failure.
			for _, endpoint := range []struct {
				path    string
				handler gin.HandlerFunc
			}{
				{"/api/task", GetAllTask},
				{"/api/task/self", GetUserTask},
			} {
				listRecorder := httptest.NewRecorder()
				listContext, _ := gin.CreateTestContext(listRecorder)
				listContext.Request = httptest.NewRequest(http.MethodGet, endpoint.path+"?model_name="+modelName+"&task_id="+task.TaskID, nil)
				listContext.Set("id", 61)
				listContext.Set("role", common.RoleAdminUser)
				endpoint.handler(listContext)
				require.Equal(t, http.StatusOK, listRecorder.Code)
				var listed struct {
					Success bool `json:"success"`
					Data    struct {
						Total int           `json:"total"`
						Items []dto.TaskDto `json:"items"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(listRecorder.Body.Bytes(), &listed))
				require.True(t, listed.Success, endpoint.path)
				require.Equal(t, 1, listed.Data.Total, endpoint.path)
				require.Len(t, listed.Data.Items, 1, endpoint.path)
				assert.Equal(t, task.TaskID, listed.Data.Items[0].TaskID)
				assert.Equal(t, modelName, listed.Data.Items[0].ModelName)
				assert.Equal(t, string(model.TaskStatusSubmitted), listed.Data.Items[0].Status)
				assert.Zero(t, listed.Data.Items[0].FinishTime)
			}
			var submissionLogs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ?", 61).Count(&submissionLogs).Error)
			assert.Zero(t, submissionLogs, "submission belongs in tasks, not usage logs")
			require.True(t, claimInternalAsyncImageTask(&task))
			if scenario == "price_changed" {
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"all-image-2.5-sunburst-H":0.9}`))
			}
			if scenario == "mapping_changed" {
				require.NoError(t, db.Model(&channel).Update("model_mapping", fmt.Sprintf(`{%q:"different-model"}`, modelName)).Error)
			}
			if scenario == "uncertain" {
				task.PrivateData.ImageRetry.Submitting = true
				require.NoError(t, persistAsyncImageAttempt(&task))
			}
			if scenario == "resume" {
				result, _, err := runQueuedTaskPluginImage(context.Background(), &task)
				require.NoError(t, err)
				assert.Contains(t, string(result), upstream.URL+"/result.png")
				assert.NotContains(t, string(result), "b64_json")
				require.NoError(t, db.First(&task, task.ID).Error)
			}
			var acceptedResultPath string
			if scenario == "b64_large" {
				result, _, err := runQueuedTaskPluginImage(t.Context(), &task)
				require.NoError(t, err)
				assert.Contains(t, string(result), "b64_json")
				require.NoError(t, db.First(&task, task.ID).Error)
				require.NotNil(t, task.PrivateData.PluginImage)
				acceptedResultPath = task.PrivateData.PluginImage.ResponseBodyPath
				require.NotEmpty(t, acceptedResultPath)
				assert.FileExists(t, acceptedResultPath)
				assert.Empty(t, task.Data, "large result bytes stay out of database task rows")
				assert.EqualValues(t, 1, posts.Load())
			}
			// Fixed historic start verifies displayed duration without sleeping.
			task.SubmitTime, task.StartTime = time.Now().Add(-37*time.Second).Unix(), time.Now().Add(-35*time.Second).Unix()
			require.NoError(t, persistAsyncImageAttempt(&task))
			err = executeInternalAsyncImageTask(context.Background(), &task)
			failed := scenario == "empty_result" || scenario == "rejected" || scenario == "uncertain" || scenario == "mapping_changed"
			if failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, db.First(&task, task.ID).Error)
			assert.GreaterOrEqual(t, task.FinishTime-task.SubmitTime, int64(37))
			var tasks, consumes int64
			require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			assert.EqualValues(t, 1, tasks)
			var user model.User
			require.NoError(t, db.First(&user, 61).Error)
			var token model.Token
			require.NoError(t, db.First(&token, 62).Error)
			if failed {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				assert.Equal(t, 1000000, user.Quota)
				assert.Equal(t, 1000000, token.RemainQuota)
				assert.Zero(t, consumes)
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
				price := 0.02
				if kuaiai {
					price = 0.2
					if scenario == "vip" {
						price *= 0.9
					}
				}
				want := common.QuotaRound(price * common.QuotaPerUnit)
				assert.Equal(t, 1000000-want, user.Quota)
				assert.Equal(t, 1000000-want, token.RemainQuota)
				assert.EqualValues(t, 1, consumes)
				assert.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
				assert.False(t, task.PrivateData.ResultDiscarded)
				assert.Contains(t, string(task.Data), `"url":`)
				assert.False(t, strings.Contains(string(task.Data), "b64_json"))
			}
			if acceptedResultPath != "" {
				assert.NoFileExists(t, acceptedResultPath, "settlement removes the temporary completion checkpoint")
			}
			if scenario == "uncertain" || scenario == "mapping_changed" {
				assert.Zero(t, posts.Load())
			} else if mixedEdit {
				assert.EqualValues(t, 2, posts.Load())
				assert.Equal(t, []string{"139", "140"}, task.PrivateData.ChannelRetryPath)
			} else {
				assert.EqualValues(t, 1, posts.Load())
			}
		})
	}
}

func TestImageTaskPriorityIsolation(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache=%t", cached), func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelAutoSwitchState{}, &model.ChannelAutoSwitchAttempt{}))
			require.NoError(t, db.Where("channel_id IN ?", []int{931, 932, 933}).Delete(&model.Ability{}).Error)
			require.NoError(t, db.Where("state_key IN (?)", db.Model(&model.ChannelAutoSwitchState{}).Select("state_key").Where("channel_id IN ?", []int{931, 932, 933})).Delete(&model.ChannelAutoSwitchAttempt{}).Error)
			require.NoError(t, db.Where("channel_id IN ?", []int{931, 932, 933}).Delete(&model.ChannelAutoSwitchState{}).Error)
			oldMemory := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = cached
			t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory })
			for i, priority := range []int64{17, 15, 9} {
				channel := model.Channel{Id: 931 + i, Name: "image priority", Type: constant.ChannelTypeOpenAI, Key: "fixture", Status: common.ChannelStatusEnabled,
					Models: "priority-image", Group: "default", Priority: &priority}
				channel.SetOtherSettings(dto.ChannelOtherSettings{AutoSwitchEnabled: i != 1})
				if i == 2 {
					channel.Type = constant.ChannelTypeTaskPlugin
				}
				require.NoError(t, db.Create(&channel).Error)
				require.NoError(t, channel.AddAbilities(db))
				t.Cleanup(func() { require.NoError(t, channel.DeleteAbilities()) })
			}
			for range 3 {
				ticket, err := model.BeginChannelAutoSwitchAttempt(931, "default", "priority-image", "/v1/images/edits", time.Now())
				require.NoError(t, err)
				require.NotNil(t, ticket)
				require.NoError(t, model.RecordChannelAutoSwitchResult(*ticket, model.ChannelAutoSwitchFailure, "upstream_unavailable", time.Now()))
			}
			if cached {
				model.InitChannelCache()
			}
			state := &model.MediaRetryState{Version: 1, WindowMinutes: 10, MaxAttempts: 3}
			cycle := &service.ChannelRetryCycle{Enabled: true, Budget: service.MediaRetryBudget(state, time.Now())}
			for i, want := range []int{931, 932, 933} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
				service.BindMediaRetry(c, state)
				channel, _, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Cycle: cycle, Ctx: c, TokenGroup: "default", ModelName: "priority-image"})
				require.NoError(t, err)
				require.NotNil(t, channel)
				assert.Equal(t, want, channel.Id)
				assert.Nil(t, service.AutoSwitchAttempt(c))
				require.True(t, state.BeginAttempt(time.Now()))
				cycle.History = append(cycle.History, strconv.Itoa(channel.Id))
				assert.Equal(t, i+1, state.Attempts)
			}
			fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
			fresh.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
			channel, _, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Ctx: fresh, TokenGroup: "default", ModelName: "priority-image"})
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, 931, channel.Id, "a new request starts at the configured priority")
			for _, request := range []struct {
				path, body string
				media      bool
			}{
				{"/v1beta/models/priority-image:generateContent", `{"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`, true},
				{"/v1/models/priority-image:streamGenerateContent", `{"generationConfig":{"responseModalities":["IMAGE"]}}`, true},
				{"/v1beta/models/priority-image:generateContent", `{"generationConfig":{"responseModalities":["TEXT"]}}`, false},
			} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body))
				c.Request.Header.Set("Content-Type", "application/json")
				assert.Equal(t, request.media, service.IsMediaTaskRequest(c))
				remaining, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				assert.JSONEq(t, request.body, string(remaining), "classification preserves the upstream request body")
				common.CleanupBodyStorage(c)
			}
		})
	}
}
