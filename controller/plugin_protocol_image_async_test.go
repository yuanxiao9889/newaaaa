package controller

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
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
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHyImageQueueLifecycle(t *testing.T) {
	oldMaxDownload := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() { constant.MaxFileDownloadMB = oldMaxDownload })
	for _, scenario := range []string{"generate", "edit", "rejected", "resume", "uncertain", "mapping_changed", "insufficient_quota", "async_plugin"} {
		t.Run(scenario, func(t *testing.T) {
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
			const modelName = "hy-image-v3.5-preview"
			pluginKey := "tencent-hy-image"
			withTieredBillingConfig(t, map[string]string{modelName: "tiered_expr"}, map[string]string{modelName: `u("tokens") * 0.000001`})
			source, err := os.ReadFile("../plugins/tasks/tencent-hy-image/plugin.js")
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
				assert.Equal(t, "/v1/wand/hunyuan-image/v35-generation", r.URL.Path)
				var payload map[string]any
				assert.NoError(t, common.DecodeJson(r.Body, &payload))
				assert.Equal(t, modelName, payload["model"])
				assert.Contains(t, payload, "messages")
				w.Header().Set("Content-Type", "application/json")
				if scenario == "rejected" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"rejected"}}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"choices":[{"delta":{"image":{"url":"http://%s/result.png"}}}],"tokenhub_usage":{"total_tokens":20000}}`, r.Host)
			}))
			t.Cleanup(upstream.Close)
			upstreamURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			fetch := system_setting.GetFetchSetting()
			oldFetch := *fetch
			fetch.AllowPrivateIp, fetch.AllowedPorts = true, []string{upstreamURL.Port()}
			t.Cleanup(func() { *fetch = oldFetch })
			channel := model.Channel{Id: 911, Type: constant.ChannelTypeTaskPlugin, Name: "hy", Key: "test-key", BaseURL: &upstream.URL, Models: modelName, Group: "default", Status: common.ChannelStatusEnabled}
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: pluginKey})
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, channel.DeleteAbilities())
			require.NoError(t, channel.AddAbilities(db))
			t.Cleanup(func() { require.NoError(t, channel.DeleteAbilities()) })
			body := []byte(`{"model":"hy-image-v3.5-preview","prompt":"lighthouse","size":"1536x1536","response_format":"url"}`)
			path, contentType := "/v1/images/generations", "application/json"
			if scenario == "edit" {
				var form bytes.Buffer
				writer := multipart.NewWriter(&form)
				for key, value := range map[string]string{"model": modelName, "prompt": "lighthouse", "size": "1536x1536", "response_format": "url"} {
					require.NoError(t, writer.WriteField(key, value))
				}
				part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image"; filename="reference.png"`}, "Content-Type": {"image/png"}})
				require.NoError(t, err)
				_, err = part.Write(imageBytes.Bytes())
				require.NoError(t, err)
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
				common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
				c.Next()
			})
			// Exercise production dispatch: async requests must retain plugin identity
			// before channel selection, or a valid plugin channel is rejected with 503.
			engine.POST(path, middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(c *gin.Context) {
				value, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
				require.True(t, exists)
				serveTaskPluginImageProtocol(c, value.(pluginruntime.PinnedEndpoint), pluginProtocolBridgeDeps{})
			})
			engine.ServeHTTP(recorder, request)
			if scenario == "insufficient_quota" {
				assert.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
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
			assert.Equal(t, relay.PluginAsyncImageFormat, task.PrivateData.RequestRelayFormat)
			assert.Equal(t, "canvas", task.TokenName)
			require.NotNil(t, task.PrivateData.BillingContext.TieredSnapshot)
			require.True(t, claimInternalAsyncImageTask(&task))
			if scenario == "mapping_changed" {
				require.NoError(t, db.Model(&channel).Update("model_mapping", `{"hy-image-v3.5-preview":"different-model"}`).Error)
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
			// Fixed historic start verifies displayed duration without sleeping.
			task.SubmitTime, task.StartTime = time.Now().Add(-37*time.Second).Unix(), time.Now().Add(-35*time.Second).Unix()
			require.NoError(t, persistAsyncImageAttempt(&task))
			err = executeInternalAsyncImageTask(context.Background(), &task)
			failed := scenario == "rejected" || scenario == "uncertain" || scenario == "mapping_changed"
			if failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, db.First(&task, task.ID).Error)
			assert.GreaterOrEqual(t, task.FinishTime-task.SubmitTime, int64(37))
			var tasks, consumes int64
			require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
			require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
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
				want := common.QuotaRound(0.02 * common.QuotaPerUnit)
				assert.Equal(t, 1000000-want, user.Quota)
				assert.Equal(t, 1000000-want, token.RemainQuota)
				assert.EqualValues(t, 1, consumes)
				assert.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
				assert.False(t, task.PrivateData.ResultDiscarded)
				assert.Contains(t, string(task.Data), `"url":`)
				assert.False(t, strings.Contains(string(task.Data), "b64_json"))
			}
			if scenario == "uncertain" || scenario == "mapping_changed" {
				assert.Zero(t, posts.Load())
			} else {
				assert.EqualValues(t, 1, posts.Load())
			}
		})
	}
}
