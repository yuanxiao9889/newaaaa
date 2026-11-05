package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncAudioLifecycle(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, scenario := range []string{"success", "rejected", "invalid_audio", "uncertain", "resume", "stored_resume", "settled_resume", "plugin_changed", "insufficient_quota", "invalid_request", "unsupported_plugin"} {
		t.Run(scenario, func(t *testing.T) {
			db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{})
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			oldRegistry, oldMemory, oldRedis, oldBatch, oldLog, oldExport := pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(dialect, dialect)
			t.Setenv("LOG_SQL_DSN", "")
			oldMaster := common.IsMasterNode
			common.IsMasterNode = false
			require.NoError(t, model.InitLogDB())
			common.IsMasterNode = oldMaster
			pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
			common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMain, oldLogType)
				pluginruntime.DefaultRegistry, common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRegistry, oldMemory, oldRedis, oldBatch, oldLog, oldExport
			})
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			withTieredBillingConfig(t, map[string]string{"seed-audio-1.0": "tiered_expr"}, map[string]string{"seed-audio-1.0": `tier("base", u("seconds") * 0.015)`})
			source, err := os.ReadFile("../plugins/tasks/jieapi-audio/plugin.js")
			require.NoError(t, err)
			if scenario == "unsupported_plugin" {
				source = bytes.ReplaceAll(source, []byte(`requiredCapabilities: ["native-audio-queue@1"],`), nil)
			}
			plugin, err := pluginruntime.DefaultRegistry.Register(string(source), pluginruntime.Options{})
			require.NoError(t, err)
			const initial = 2000000
			quota := initial
			if scenario == "insufficient_quota" {
				quota = 1
			}
			user := model.User{Username: "audio-owner", AffCode: "audio-owner", Quota: quota, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "audio-test-token", Name: "audio", RemainQuota: initial, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			// A valid 10ms mono PCM WAV; deterministic fixture, no network media.
			wav := make([]byte, 44+480)
			copy(wav, "RIFF")
			binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
			copy(wav[8:], "WAVEfmt ")
			binary.LittleEndian.PutUint32(wav[16:], 16)
			binary.LittleEndian.PutUint16(wav[20:], 1)
			binary.LittleEndian.PutUint16(wav[22:], 1)
			binary.LittleEndian.PutUint32(wav[24:], 24000)
			binary.LittleEndian.PutUint32(wav[28:], 48000)
			binary.LittleEndian.PutUint16(wav[32:], 2)
			binary.LittleEndian.PutUint16(wav[34:], 16)
			copy(wav[36:], "data")
			binary.LittleEndian.PutUint32(wav[40:], 480)
			var posts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				assert.Equal(t, "/audios/v1/audio/speech", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if scenario == "rejected" {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"message":"generation rejected"}}`))
					return
				}
				audio := wav
				if scenario == "invalid_audio" {
					audio = []byte("this is not audio")
				}
				body, marshalErr := common.Marshal(gin.H{"audio": base64.StdEncoding.EncodeToString(audio), "url": "https://cdn.example/audio.wav", "duration": 0.01, "original_duration": 35.37785})
				assert.NoError(t, marshalErr)
				_, _ = w.Write(body)
			}))
			t.Cleanup(upstream.Close)
			channel := model.Channel{Name: "audio", Type: constant.ChannelTypeTaskPlugin, Key: "fixture-provider-key", BaseURL: &upstream.URL, Models: "seed-audio-1.0", Group: "default", Status: common.ChannelStatusEnabled}
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "jieapi-audio"})
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			engine := gin.New()
			engine.POST("/jieapi/v1/audio/speech", func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				c.Set("id", user.Id)
				c.Set("token_name", token.Name)
				common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
				common.SetContextKey(c, constant.ContextKeyTokenId, token.Id)
				common.SetContextKey(c, constant.ContextKeyTokenKey, token.Key)
				common.SetContextKey(c, constant.ContextKeyUserQuota, quota)
				common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
				common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin, Generation: pluginruntime.DefaultRegistry.Generation()})
				c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Generation: pluginruntime.DefaultRegistry.Generation(), Route: plugin.Meta.Routes[0]})
				c.Next()
			}, middleware.PrepareTaskPluginRoute(), middleware.Distribute(), RelayTask)
			body := `{"model":"seed-audio-1.0","text_prompt":"A story","audio_config":{"format":"wav","sample_rate":24000}}`
			if scenario == "invalid_request" {
				body = `{"model":"seed-audio-1.0","text_prompt":"A story","seconds":1e20}`
			}
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/jieapi/v1/audio/speech?async=true", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, req)
			assert.Zero(t, posts.Load(), "admission must finish before the upstream call")
			if scenario == "insufficient_quota" || scenario == "invalid_request" || scenario == "unsupported_plugin" {
				assert.GreaterOrEqual(t, recorder.Code, 400, recorder.Body.String())
				var count int64
				require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
				assert.Zero(t, count)
				return
			}
			require.Equal(t, 200, recorder.Code, recorder.Body.String())
			var submitted struct {
				TaskID string `json:"task_id"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &submitted))
			require.NotEmpty(t, submitted.TaskID)
			var task model.Task
			require.NoError(t, db.Where("task_id = ?", submitted.TaskID).First(&task).Error)
			assert.False(t, task.PrivateData.ResultDiscarded)
			assert.Equal(t, 900000, task.Quota)
			requestBytes, err := os.ReadFile(task.PrivateData.RequestBodyPath)
			require.NoError(t, err)
			assert.NotContains(t, string(requestBytes), token.Key)
			assert.NotContains(t, string(requestBytes), channel.Key)
			if scenario == "uncertain" {
				task.PrivateData.AudioQueue.Submitting = true
				require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
			}
			if scenario == "plugin_changed" {
				changed := strings.Replace(string(source), `version: "1.1.0"`, `version: "1.2.0"`, 1)
				_, err = pluginruntime.DefaultRegistry.Register(changed, pluginruntime.Options{})
				require.NoError(t, err)
			}
			won, err := model.ClaimAsyncAudioTask(&task)
			require.NoError(t, err)
			require.True(t, won)
			contender := task
			won, err = model.ClaimAsyncAudioTask(&contender)
			require.NoError(t, err)
			require.False(t, won, "only one worker may own the task")
			if scenario == "resume" || scenario == "stored_resume" || scenario == "settled_resume" {
				require.NoError(t, submitQueuedAudio(t.Context(), &task))
				require.NoError(t, model.SaveAsyncAudioTask(t.Context(), &task))
				if scenario != "resume" {
					require.NoError(t, service.StoreAsyncAudioResult(t.Context(), &task))
					require.NoError(t, model.SaveAsyncAudioTask(t.Context(), &task))
				}
				if scenario == "settled_resume" {
					require.NoError(t, service.AdjustTaskQuotaForFinalSettlement(t.Context(), &task, task.PrivateData.ActualQuota, "fixture settlement"))
					require.NoError(t, service.ConfirmTaskBillingSettled(t.Context(), &task, task.Quota, "fixture stored audio"))
				}
				stale := task
				task.PrivateData.AudioQueue.LeaseUntil = time.Now().Add(-time.Second).Unix()
				require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
				var recovered model.Task
				require.NoError(t, db.First(&recovered, task.ID).Error)
				task = recovered
				won, err = model.ClaimAsyncAudioTask(&task)
				require.NoError(t, err)
				require.True(t, won)
				require.Error(t, model.SaveAsyncAudioTask(t.Context(), &stale), "a recovered worker fences the old attempt")
			}
			require.NoError(t, executeAsyncAudioTask(&task))
			require.NoError(t, db.First(&task, task.ID).Error)
			wantCharge := 0
			if scenario == "success" || scenario == "resume" || scenario == "stored_resume" || scenario == "settled_resume" {
				wantCharge = 265334
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-wantCharge, updated.Quota)
			assert.Equal(t, wantCharge, updated.UsedQuota)
			var updatedToken model.Token
			require.NoError(t, db.First(&updatedToken, token.Id).Error)
			assert.Equal(t, initial-wantCharge, updatedToken.RemainQuota)
			assert.Equal(t, wantCharge, task.Quota)
			var consumeLogs int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&consumeLogs).Error)
			if wantCharge > 0 {
				assert.EqualValues(t, 1, consumeLogs, "recovery must not create another consume log")
			} else {
				assert.Zero(t, consumeLogs)
			}
			won, err = model.ClaimAsyncAudioTask(&task)
			require.NoError(t, err)
			assert.False(t, won, "terminal tasks cannot be charged or submitted again")
			if scenario == "uncertain" || scenario == "plugin_changed" {
				assert.Zero(t, posts.Load())
			} else {
				assert.EqualValues(t, 1, posts.Load())
			}
			_, err = os.Stat(task.PrivateData.RequestBodyPath)
			assert.True(t, os.IsNotExist(err))
			query := gin.New()
			query.Use(func(c *gin.Context) {
				id := user.Id
				if c.GetHeader("X-Test-Owner") == "foreign" {
					id++
				}
				c.Set("id", id)
			})
			query.GET("/v1/audio/tasks/:task_id", GetAudioTask)
			query.GET("/v1/audio/tasks/:task_id/content", GetAudioTaskContent)
			for _, foreign := range []bool{false, true} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest("GET", "/v1/audio/tasks/"+task.TaskID, nil)
				if foreign {
					request.Header.Set("X-Test-Owner", "foreign")
				}
				query.ServeHTTP(response, request)
				if foreign {
					assert.Equal(t, 404, response.Code)
				} else {
					assert.Equal(t, 200, response.Code)
					assert.NotContains(t, response.Body.String(), "fixture-provider-key")
				}
			}
			if wantCharge == 0 {
				return
			}
			stored, err := os.ReadFile(task.PrivateData.LocalPath)
			require.NoError(t, err)
			assert.Equal(t, wav, stored)
			assert.NotContains(t, string(task.Data), base64.StdEncoding.EncodeToString(wav))
			artifacts, err := projectTaskArtifacts(&task)
			require.NoError(t, err)
			require.Len(t, artifacts, 1)
			assert.Equal(t, "audio", artifacts[0].Type)
			response := httptest.NewRecorder()
			request := httptest.NewRequest("GET", service.AudioTaskContentURL(task.TaskID), nil)
			query.ServeHTTP(response, request)
			assert.Equal(t, 200, response.Code)
			assert.True(t, bytes.Equal(wav, response.Body.Bytes()))
			response = httptest.NewRecorder()
			request = httptest.NewRequest("GET", service.AudioTaskContentURL(task.TaskID), nil)
			request.Header.Set("Range", "bytes=0-43")
			query.ServeHTTP(response, request)
			assert.Equal(t, http.StatusPartialContent, response.Code)
			assert.Equal(t, wav[:44], response.Body.Bytes())
			response = httptest.NewRecorder()
			request = httptest.NewRequest("GET", service.AudioTaskContentURL(task.TaskID), nil)
			request.Header.Set("X-Test-Owner", "foreign")
			query.ServeHTTP(response, request)
			assert.Equal(t, 404, response.Code)
			task.PrivateData.ExpiresAt = time.Now().Add(-time.Hour).Unix()
			require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
			response = httptest.NewRecorder()
			query.ServeHTTP(response, httptest.NewRequest("GET", service.AudioTaskContentURL(task.TaskID), nil))
			assert.Equal(t, 410, response.Code)
			task.FinishTime = time.Now().Add(-service.GetAsyncImageRetention() - time.Hour).Unix()
			require.NoError(t, db.Model(&task).Update("finish_time", task.FinishTime).Error)
			require.NoError(t, service.CleanupExpiredAudioTasks(context.Background()))
			_, err = os.Stat(task.PrivateData.LocalPath)
			assert.True(t, os.IsNotExist(err))
		})
	}
}
