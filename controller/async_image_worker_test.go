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
	"image/color"
	"image/png"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAsyncImageMultipleResults(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	oldDownloadLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 10
	t.Cleanup(func() { constant.MaxFileDownloadMB = oldDownloadLimit })
	for _, tc := range []struct {
		name                          string
		count                         int
		mixed, invalid, retryDownload bool
	}{
		{name: "four_urls", count: 4, retryDownload: true},
		{name: "mixed_url_and_base64", count: 4, mixed: true},
		{name: "partial_failure", count: 4, invalid: true},
		{name: "single_image", count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}))
			require.NoError(t, db.Where("channel_id = ?", 703).Delete(&model.Ability{}).Error)
			var version string
			versionQuery := "SELECT version()"
			if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
			oldMemory, oldBatch := common.MemoryCacheEnabled, common.BatchUpdateEnabled
			common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false
			oldAddress := system_setting.ServerAddress
			system_setting.ServerAddress = "https://relay.test"
			prices := ratio_setting.ModelPrice2JSONString()
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj-multi-test":0.3}`))
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldMemory, oldBatch
				system_setting.ServerAddress = oldAddress
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
			})
			t.Setenv("ASYNC_IMAGE_SIGNING_SECRET", "multi-image-test-secret")
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "native_billing_test", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "multi-user-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)
			images := make([][]byte, tc.count)
			for i := range images {
				img := image.NewRGBA(image.Rect(0, 0, 2, 2))
				img.SetRGBA(0, 0, color.RGBA{R: uint8(40 * (i + 1)), A: 255})
				var encoded bytes.Buffer
				require.NoError(t, png.Encode(&encoded, img))
				images[i] = encoded.Bytes()
			}
			var posts atomic.Int32
			var secondDownloads atomic.Int32
			var upstreamURL string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					result := dto.ImageResponse{Created: 123}
					for i := range images {
						item := dto.ImageData{Url: upstreamURL + "/image/" + strconv.Itoa(i), RevisedPrompt: fmt.Sprintf("prompt %d", i)}
						if tc.mixed && i%2 == 1 {
							item.Url, item.B64Json = "", base64.StdEncoding.EncodeToString(images[i])
						}
						result.Data = append(result.Data, item)
					}
					body, err := common.Marshal(result)
					if err != nil {
						w.WriteHeader(500)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
					return
				}
				index, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/image/"))
				if err != nil || index < 0 || index >= len(images) {
					w.WriteHeader(404)
					return
				}
				if index == 1 && secondDownloads.Add(1) == 1 && tc.retryDownload {
					w.WriteHeader(503)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				if tc.invalid && index == 1 {
					_, _ = w.Write([]byte("invalid image"))
					return
				}
				_, _ = w.Write(images[index])
			}))
			defer upstream.Close()
			upstreamURL = upstream.URL
			fetch := system_setting.GetFetchSetting()
			oldFetch := *fetch
			parsedUpstream, err := url.Parse(upstreamURL)
			require.NoError(t, err)
			fetch.AllowPrivateIp = true
			fetch.AllowedPorts = []string{parsedUpstream.Port()}
			t.Cleanup(func() { *fetch = oldFetch })
			channel := &model.Channel{Id: 703, Name: "multi-image", Type: constant.ChannelTypeOpenAI,
				Key: "multi-upstream-key", Models: "mj-multi-test", Group: "default", Status: common.ChannelStatusEnabled,
				BaseURL: &upstreamURL, Weight: common.GetPointer(uint(1))}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			body := `{"model":"mj-multi-test","prompt":"fixture","n":1}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: "mj-multi-test", FinalPreConsumedQuota: 150000,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 703, UpstreamModelName: "mj-multi-test"}}
			require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "mj-multi-test"}))
			common.CleanupBodyStorage(c)
			var task model.Task
			require.NoError(t, db.First(&task).Error)
			require.True(t, claimInternalAsyncImageTask(&task))
			_, activeInput, inputErr := service.ReadImageTaskInput(61, task.TaskID, true)
			require.NoError(t, inputErr)
			assert.Equal(t, body, string(activeInput), "accepted active tasks retain the original request")
			err = executeInternalAsyncImageTask(context.Background(), &task)
			assert.EqualValues(t, 1, posts.Load(), "one generation even when a result download needs retry")
			var restored model.Task
			require.NoError(t, db.First(&restored, task.ID).Error)
			retained, retainedBody, inputErr := service.ReadImageTaskInput(61, task.TaskID, true)
			require.NoError(t, inputErr)
			assert.Equal(t, body, string(retainedBody), "both success and failure preserve reusable input")
			assert.Equal(t, restored.FinishTime+1200, retained.ExpiresAt)
			if tc.invalid {
				require.ErrorContains(t, err, "store image 1 failed")
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), restored.Status)
				assert.Empty(t, restored.PrivateData.ImageAssets)
				files, err := os.ReadDir(service.GetAsyncImageStoragePath())
				require.NoError(t, err)
				assert.Empty(t, files, "partial output must not leak or become a successful task")
				return
			}
			require.NoError(t, err)
			require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), restored.Status)
			require.Len(t, restored.PrivateData.ImageAssets, tc.count)
			var persisted dto.ImageResponse
			require.NoError(t, common.Unmarshal(restored.Data, &persisted))
			require.Len(t, persisted.Data, tc.count)
			assert.NotContains(t, string(restored.Data), upstreamURL)
			assert.NotContains(t, string(restored.Data), "b64_json")
			router := gin.New()
			router.GET("/v1/images/tasks/:task_id/signed-content", GetSignedImageTaskContent)
			router.GET("/v1/images/tasks/:task_id/content", func(c *gin.Context) {
				id, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
				c.Set("id", id)
				GetImageTaskContent(c)
			})
			router.GET("/v1/images/tasks/:task_id", func(c *gin.Context) {
				id, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
				c.Set("id", id)
				GetImageTask(c)
			})
			query := httptest.NewRequest(http.MethodGet, service.BuildAsyncImageStatusURL(restored.TaskID), nil)
			query.Header.Set("X-Test-User", "61")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, query)
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"retry_input"`, "successful task status advertises reusable input")
			var response dto.ImageAsyncTaskResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Len(t, response.Data, tc.count)
			assert.Equal(t, response.Data[0].Url, response.URL)
			seen := map[string]bool{}
			for i, item := range response.Data {
				assert.False(t, seen[item.Url])
				seen[item.Url] = true
				assert.Equal(t, fmt.Sprintf("prompt %d", i), item.RevisedPrompt)
				asset := restored.PrivateData.ImageAssets[i]
				if tc.mixed && i%2 == 1 {
					assert.Empty(t, asset.SourceURL)
				} else {
					assert.Equal(t, upstreamURL+"/image/"+strconv.Itoa(i), asset.SourceURL)
				}
				for _, target := range []string{item.Url, persisted.Data[i].Url} {
					request := httptest.NewRequest(http.MethodGet, target, nil)
					request.Header.Set("X-Test-User", "61")
					result := httptest.NewRecorder()
					router.ServeHTTP(result, request)
					require.Equal(t, http.StatusOK, result.Code)
					assert.Equal(t, images[i], result.Body.Bytes())
				}
			}
			for _, target := range []string{response.ContentURL, service.BuildAsyncImageStatusURL(restored.TaskID)} {
				request := httptest.NewRequest(http.MethodGet, target, nil)
				request.Header.Set("X-Test-User", "99")
				result := httptest.NewRecorder()
				router.ServeHTTP(result, request)
				assert.Equal(t, http.StatusNotFound, result.Code)
			}
			for _, index := range []string{"1", "-1", "999", "../image", ""} {
				parsed, err := url.Parse(response.URL)
				require.NoError(t, err)
				q := parsed.Query()
				q.Set("index", index)
				parsed.RawQuery = q.Encode()
				result := httptest.NewRecorder()
				router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, parsed.String(), nil))
				assert.Equal(t, http.StatusUnauthorized, result.Code, "signature cannot be reused for another index")
			}
			// Historical single-image private data still resolves to the first image.
			legacy := restored
			legacy.PrivateData.ImageAssets = nil
			legacyResponse := buildImageTaskResponse(&legacy)
			require.Len(t, legacyResponse.Data, 1)
			legacyURL, err := url.Parse(legacyResponse.URL)
			require.NoError(t, err)
			assert.True(t, service.VerifyAsyncImageToken(legacyURL.Query().Get("token"), &legacy))
			if tc.retryDownload {
				assert.EqualValues(t, 2, secondDownloads.Load())
			}
			// Compact large historical task payloads without truncating the group.
			restored.Data = []byte(`{"b64_json":"` + strings.Repeat("A", 3000) + `"}`)
			restored.PrivateData.ExpiresAt = time.Now().Unix() - 1
			require.NoError(t, db.Save(&restored).Error)
			result := httptest.NewRecorder()
			router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, response.URL, nil))
			assert.Equal(t, http.StatusUnauthorized, result.Code, "expired content invalidates the token")
			require.NoError(t, service.CleanupExpiredAsyncImages())
			for _, asset := range restored.PrivateData.ImageAssets {
				assert.NoFileExists(t, asset.LocalPath)
			}
			var cleaned model.Task
			require.NoError(t, db.First(&cleaned, task.ID).Error)
			require.NoError(t, common.Unmarshal(cleaned.Data, &persisted))
			assert.Len(t, persisted.Data, tc.count)
			assert.Empty(t, buildImageTaskResponse(&cleaned).Data)
		})
	}
}

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

	if logDSN := os.Getenv("NATIVE_BILLING_TEST_LOG_DSN"); logDSN != "" {
		require.Contains(t, logDSN, "native_billing_test")
		var dialect gorm.Dialector = sqlite.Open(logDSN)
		switch os.Getenv("NATIVE_BILLING_TEST_DIALECT") {
		case "mysql":
			dialect = mysql.Open(logDSN)
		case "postgres":
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

	return db
}

func TestImageTokenReservation(t *testing.T) {
	settings := config.GlobalConfig.ExportAllConfigs()
	ratios, prices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(settings))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(ratios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"quota_setting.image_token_pre_consume_usd": "1.5",
		"quota_setting.pre_consume_multiplier":      "1",
		"group_ratio_setting.group_ratio":           `{"default":1,"discount":0.5,"free":0}`,
		"billing_setting.billing_mode":              `{"reserve-expression":"tiered_expr","reserve-fixed":"tiered_expr"}`,
		"billing_setting.billing_expr":              `{"reserve-expression":"tier(\"image\", p * 12 + c * 66)","reserve-fixed":"tier(\"image\", fixed(0.18)) * image_count"}`,
	}))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-image-2.5-flare":6,"reserve-alias":6,"reserve-free":0}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"reserve-per-call":0.18}`))
	for _, tc := range []struct {
		name, model, group  string
		prompt, count, want int
	}{
		{"incident", "gpt-image-2.5-flare", "default", 1376, 1, 750000},
		{"alias", "reserve-alias", "default", 1376, 1, 750000},
		{"multiple images", "reserve-alias", "default", 1376, 3, 2250000},
		{"higher input estimate", "reserve-alias", "default", 200000, 1, 1200000},
		{"discount", "reserve-alias", "discount", 1376, 1, 375000},
		{"free group", "reserve-alias", "free", 1376, 1, 0},
		{"free model", "reserve-free", "default", 1376, 1, 0},
		{"fixed legacy price", "reserve-per-call", "default", 1376, 1, 90000},
		{"output expression", "reserve-expression", "default", 1376, 2, 1500000},
		{"fixed expression", "reserve-fixed", "default", 1376, 2, 180000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			info := &relaycommon.RelayInfo{OriginModelName: tc.model, UserGroup: tc.group, UsingGroup: tc.group,
				Request: &dto.ImageRequest{Model: tc.model, N: common.GetPointer(uint(tc.count))}}
			price, err := helper.ModelPriceHelper(c, info, tc.prompt, &types.TokenCountMeta{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, price.QuotaToPreConsume)
		})
	}
	t.Run("reject invalid reserve settings", func(t *testing.T) {
		for _, value := range []string{"0", "-1", "NaN", "+Inf", "1e309", ""} {
			assert.Error(t, operation_setting.ValidateQuotaOption("quota_setting.image_token_pre_consume_usd", value))
		}
		assert.NoError(t, operation_setting.ValidateQuotaOption("quota_setting.image_token_pre_consume_usd", "1.5"))
	})
	t.Run("concurrent admission and refunds", func(t *testing.T) {
		db := setupAsyncImageWorkerTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		// All three engines use the same atomic conditional update. Serializing
		// connections also avoids SQLite's unrelated SQLITE_BUSY behavior.
		sqlDB.SetMaxOpenConns(1)
		oldBatch := common.BatchUpdateEnabled
		common.BatchUpdateEnabled = true
		t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })
		user := model.User{Username: "image-reserve-incident", Quota: 1447148, Status: common.UserStatusEnabled}
		require.NoError(t, db.Create(&user).Error)
		token := model.Token{UserId: user.Id, Key: "image-reserve-incident", UnlimitedQuota: true, Status: common.TokenStatusEnabled}
		require.NoError(t, db.Create(&token).Error)
		type attempt struct {
			c    *gin.Context
			info *relaycommon.RelayInfo
			err  *types.NewAPIError
		}
		attempts := make([]attempt, 20)
		for i := range attempts {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", nil)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: true,
				OriginModelName: "gpt-image-2.5-flare", UserGroup: "default", UsingGroup: "default", ForcePreConsume: true,
				UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, Request: &dto.ImageRequest{Model: "gpt-image-2.5-flare"}}
			_, err := helper.ModelPriceHelper(c, info, 1376, &types.TokenCountMeta{})
			require.NoError(t, err)
			attempts[i] = attempt{c: c, info: info}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range attempts {
			wg.Go(func() {
				<-start
				a := &attempts[i]
				a.err = service.PreConsumeBilling(a.c, a.info.PriceData.QuotaToPreConsume, a.info)
			})
		}
		close(start)
		wg.Wait()
		var accepted []*attempt
		for i := range attempts {
			a := &attempts[i]
			if a.err == nil {
				accepted = append(accepted, a)
			} else {
				assert.Equal(t, kittypes.ErrorCodeInsufficientUserQuota, a.err.GetErrorCode())
			}
		}
		require.Len(t, accepted, 1, "the 20 simultaneous jobs may reserve only the available wallet")
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 697148, user.Quota)
		a := accepted[0]
		// A worker reuses the existing task reservation and rejects larger
		// channel overrides before any provider request can be sent.
		a.c.Set("async_image_worker", true)
		require.Nil(t, service.PrepareImageBillingForRequest(a.c, a.info, 1))
		require.NotNil(t, service.PrepareImageBillingForRequest(a.c, a.info, 2))
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 697148, user.Quota)
		require.NoError(t, a.info.Billing.Settle(579383))
		require.NoError(t, a.info.Billing.Settle(579383))
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 867765, user.Quota, "unused reserve is refunded, actual usage is charged once")
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, 579383, token.UsedQuota)
		retry := &attempts[0]
		for i := range attempts {
			if attempts[i].err != nil {
				retry = &attempts[i]
				break
			}
		}
		require.Nil(t, service.PreConsumeBilling(retry.c, 750000, retry.info))
		retry.info.Billing.Refund(retry.c)
		retry.info.Billing.Refund(retry.c)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 867765, user.Quota, "failure restores the full reserve once")
		// A synchronous channel override can top up its active reservation.
		// Failure must refund both the initial reserve and the top-up.
		require.NoError(t, db.Model(&user).Update("quota", 2000000).Error)
		retry.c.Set("async_image_worker", false)
		require.Nil(t, service.PreConsumeBilling(retry.c, 750000, retry.info))
		require.Nil(t, service.PrepareImageBillingForRequest(retry.c, retry.info, 2))
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 500000, user.Quota)
		retry.info.Billing.Refund(retry.c)
		retry.info.Billing.Refund(retry.c)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 2000000, user.Quota)
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, 579383, token.UsedQuota)
	})
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

func TestImageTaskInputRetentionAndResubmission(t *testing.T) {
	for _, outcome := range []string{"failed", "succeeded", "running"} {
		for _, format := range []string{"json", "multipart", "native", "chat", "responses", "gemini"} {
			t.Run(format+"_"+outcome, func(t *testing.T) {
				db := setupAsyncImageWorkerTestDB(t)
				require.NoError(t, db.AutoMigrate(&model.TransferRequest{}))
				require.NoError(t, db.Where("user_id IN ?", []int{61, 63}).Delete(&model.TransferRequest{}).Error)
				require.NoError(t, i18n.Init())
				oldMemory := common.MemoryCacheEnabled
				common.MemoryCacheEnabled = false
				t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory })
				dir := t.TempDir()
				t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", dir)
				require.NoError(t, db.Create(&model.User{Id: 61, Username: "retryowner", AffCode: "retryowner", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}).Error)
				require.NoError(t, db.Create(&model.User{Id: 63, Username: "retryother", AffCode: "retryother", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}).Error)
				tokens := []model.Token{
					{Id: 62, UserId: 61, Key: "retryOwnerKey", Status: common.TokenStatusEnabled, RemainQuota: 1000000, ExpiredTime: -1},
					{Id: 64, UserId: 63, Key: "retryOtherKey", Status: common.TokenStatusEnabled, RemainQuota: 1000000, ExpiredTime: -1},
					{Id: 65, UserId: 61, Key: "retryExhaustedKey", Status: common.TokenStatusExhausted, ExpiredTime: -1},
					{Id: 66, UserId: 61, Key: "retryDisabledKey", Status: common.TokenStatusDisabled, ExpiredTime: -1},
					{Id: 67, UserId: 61, Key: "retryExpiredKey", Status: common.TokenStatusEnabled, RemainQuota: 100, ExpiredTime: 1},
				}
				require.NoError(t, db.Create(&tokens).Error)
				imageBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a3WQAAAAASUVORK5CYII=")
				require.NoError(t, err)
				contentType := "application/json"
				body := []byte(`{"model":"retry-image","prompt":"keep me","n":1,"image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageBytes) + `"}`)
				if format == "multipart" {
					var encoded bytes.Buffer
					writer := multipart.NewWriter(&encoded)
					require.NoError(t, writer.WriteField("model", "retry-image"))
					require.NoError(t, writer.WriteField("prompt", "keep me"))
					for range 2 {
						part, err := writer.CreateFormFile("image[]", "reference.png")
						require.NoError(t, err)
						_, err = part.Write(imageBytes)
						require.NoError(t, err)
					}
					require.NoError(t, writer.Close())
					body, contentType = encoded.Bytes(), writer.FormDataContentType()
				}
				requestPath := "/v1/images/edits"
				switch format {
				case "chat":
					requestPath = "/v1/chat/completions"
				case "responses":
					requestPath = "/v1/responses"
				case "gemini":
					requestPath = "/v1beta/models/image-test:generateContent"
				}
				source := filepath.Join(dir, "original.body")
				require.NoError(t, os.WriteFile(source, body, 0600))
				task := &model.Task{TaskID: "task_failed_references_" + format, UserId: 61, Action: constant.TaskActionImageEdit,
					Status: model.TaskStatusInProgress, PrivateData: model.TaskPrivateData{InternalAsync: true,
						AssetType: service.AsyncImageAssetType, RequestBodyPath: source, RequestContentType: contentType,
						RequestPath: requestPath, RequestQuery: "original_option=preserved&key=old-secret&api_key=old-secret", RequestRelayFormat: string(kittypes.RelayFormatOpenAIImage),
						ImageRetry: &model.AsyncImageRetryState{BodyPath: source, BodySize: int64(len(body))}}}
				if format == "native" {
					task.PrivateData.RequestBodyPath = filepath.Join(dir, "upstream.native")
					task.PrivateData.RequestRelayFormat = relay.NativeAsyncImageFormat
					require.NoError(t, os.WriteFile(task.PrivateData.RequestBodyPath, []byte(`{"id":"provider-private-job"}`), 0600))
				}
				require.NoError(t, db.Create(task).Error)
				if outcome != "running" {
					failInternalAsyncImageTask(context.Background(), task, "too many reference images")
					require.NoFileExists(t, source)
					if outcome == "succeeded" {
						// The real successful worker retention is covered by
						// TestAsyncImageMultipleResults; exercise the public reuse contract here.
						task.Status = model.TaskStatusSuccess
						require.NoError(t, db.Model(task).Update("status", model.TaskStatusSuccess).Error)
					}
				}
				var failed model.Task
				require.NoError(t, db.First(&failed, task.ID).Error)
				if outcome != "running" {
					assert.Empty(t, failed.PrivateData.RequestBodyPath)
					assert.Empty(t, failed.PrivateData.ImageRetry.BodyPath)
				}
				input, recovered, err := service.ReadImageTaskInput(61, task.TaskID, true)
				require.NoError(t, err)
				assert.Equal(t, body, recovered)
				if outcome != "running" {
					assert.Equal(t, failed.FinishTime+int64(service.ReferenceRetention().Seconds()), input.ExpiresAt)
				}
				assert.Equal(t, "original_option=preserved", input.Query)
				require.NoError(t, service.CleanupImageTaskInputs())
				_, recovered, err = service.ReadImageTaskInput(61, task.TaskID, true)
				require.NoError(t, err)
				assert.Equal(t, body, recovered)

				var idleExpiry int64
				if outcome != "running" {
					files, err := filepath.Glob(filepath.Join(dir, "failed-inputs", "*.retry"))
					require.NoError(t, err)
					require.Len(t, files, 1)
					lastUse := time.Now().Add(-5 * time.Minute)
					require.NoError(t, os.Chtimes(files[0], lastUse, lastUse))
					idle, _, err := service.ReadImageTaskInput(61, task.TaskID, false)
					require.NoError(t, err)
					idleExpiry = idle.ExpiresAt
				}
				engine := gin.New()
				engine.GET("/v1/images/tasks/:task_id", middleware.TokenAuth(), GetImageTask)
				engine.GET("/v1/images/tasks/:task_id/retry-input", middleware.TokenAuth(), GetImageTaskRetryInput)
				admissions := 0
				engine.POST(requestPath, middleware.TokenAuth(), middleware.ResolveReferenceAssets(), func(c *gin.Context) {
					admissions++
					assert.Equal(t, "true", c.Query("async"))
					assert.Equal(t, "preserved", c.Query("original_option"))
					assert.Empty(t, c.Query("key"), "never restore original credentials")
					assert.Empty(t, c.Query("api_key"), "never restore original credentials")
					if format == "chat" || format == "responses" {
						assert.Equal(t, "true", c.Query("image_task"))
					}
					storage, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					got, err := io.ReadAll(storage)
					require.NoError(t, err)
					assert.Equal(t, body, got)
					assert.Equal(t, contentType, c.GetHeader("Content-Type"))
					info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, OriginModelName: "retry-image", UsingGroup: "default",
						ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 703, UpstreamModelName: "retry-image"}}
					require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "retry-image"}))
					common.CleanupBodyStorage(c)
				})
				origin := httptest.NewServer(engine)
				defer origin.Close()
				originURL, err := url.Parse(origin.URL)
				require.NoError(t, err)
				bridge := httptest.NewServer(httputil.NewSingleHostReverseProxy(originURL))
				defer bridge.Close()
				request := func(method, path, key, retryID, idempotency string, viaBridge ...bool) *httptest.ResponseRecorder {
					req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
					req.Header.Set("Content-Type", "application/json")
					if key != "" {
						req.Header.Set("Authorization", "Bearer sk-"+key)
					}
					req.Header.Set(service.ImageRetryTaskHeader, retryID)
					req.Header.Set("Idempotency-Key", idempotency)
					reply := httptest.NewRecorder()
					if len(viaBridge) > 0 && viaBridge[0] {
						req.URL, err = url.Parse(bridge.URL + path)
						require.NoError(t, err)
						req.RequestURI = ""
						response, err := bridge.Client().Do(req)
						require.NoError(t, err)
						defer response.Body.Close()
						reply.WriteHeader(response.StatusCode)
						_, err = io.Copy(reply, response.Body)
						require.NoError(t, err)
						return reply
					}
					engine.ServeHTTP(reply, req)
					return reply
				}
				path := "/v1/images/tasks/" + task.TaskID
				for _, tc := range []struct {
					key    string
					status int
				}{
					{"retryOwnerKey", 200}, {"retryExhaustedKey", 200}, {"retryOtherKey", 404},
					{"retryDisabledKey", 401}, {"retryExpiredKey", 401}, {"", 401},
				} {
					reply := request(http.MethodGet, path+"/retry-input", tc.key, "", "")
					require.Equal(t, tc.status, reply.Code, reply.Body.String())
					if tc.status == 200 {
						assert.Equal(t, body, reply.Body.Bytes())
						assert.Equal(t, "no-store", reply.Header().Get("Cache-Control"))
					}
				}
				status := request(http.MethodGet, path, "retryOwnerKey", "", "")
				require.Equal(t, 200, status.Code, status.Body.String())
				assert.Contains(t, status.Body.String(), `"retry_input"`)
				if outcome != "running" {
					idle, _, err := service.ReadImageTaskInput(61, task.TaskID, false)
					require.NoError(t, err)
					assert.Equal(t, idleExpiry, idle.ExpiresAt, "GET must not renew input lifetime")
				}
				assert.NotContains(t, status.Body.String(), "keep me")
				assert.Equal(t, 410, request(http.MethodPost, requestPath+"?async=true", "retryOtherKey", task.TaskID, "").Code)
				assert.Equal(t, 402, request(http.MethodPost, requestPath+"?async=true", "retryExhaustedKey", task.TaskID, "").Code)
				const key = "failed-image-retry-request-0001"
				accepted := request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", task.TaskID, key, true)
				require.Equal(t, 200, accepted.Code, accepted.Body.String())
				var receipt dto.ImageAsyncSubmitResponse
				require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &receipt))
				assert.NotEqual(t, task.TaskID, receipt.TaskID)
				assert.NotEmpty(t, receipt.TaskID)
				renewed, _, err := service.ReadImageTaskInput(61, task.TaskID, false)
				require.NoError(t, err)
				if outcome != "running" {
					assert.Greater(t, renewed.ExpiresAt, idleExpiry, "accepted Generate renews the original input")
					assert.InDelta(t, service.ReferenceRetention().Seconds(), float64(renewed.ExpiresAt-time.Now().Unix()), 2)
				}
				replayed := request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", task.TaskID, key)
				require.Equal(t, 200, replayed.Code, replayed.Body.String())
				assert.JSONEq(t, accepted.Body.String(), replayed.Body.String())
				if outcome != "running" {
					idle, _, err := service.ReadImageTaskInput(61, task.TaskID, false)
					require.NoError(t, err)
					assert.Equal(t, renewed.ExpiresAt, idle.ExpiresAt, "idempotent replay is not another Generate")
				}
				assert.Equal(t, 1, admissions)
				// A deliberate Generate through direct transport creates fresh work;
				// recovering the bridge attempt above preserved its original identity.
				next := request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", task.TaskID, key+"-next")
				require.Equal(t, 200, next.Code, next.Body.String())
				var nextReceipt dto.ImageAsyncSubmitResponse
				require.NoError(t, common.Unmarshal(next.Body.Bytes(), &nextReceipt))
				assert.NotEmpty(t, nextReceipt.TaskID)
				assert.NotEqual(t, receipt.TaskID, nextReceipt.TaskID)
				assert.Equal(t, 2, admissions)
				assert.Equal(t, 409, request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", "another-task", key).Code)
				_, _, err = service.ReadImageTaskInput(61, task.TaskID, true)
				require.NoError(t, err, "reusing input must not consume or delete it")
				var original model.Task
				require.NoError(t, db.First(&original, task.ID).Error)
				assert.Equal(t, task.Status, original.Status)
				if outcome == "running" {
					failInternalAsyncImageTask(context.Background(), task, "finish active fixture")
				}

				// Simulate a deadline persisted before a restart, with no wall-clock sleeps.
				files, err := filepath.Glob(filepath.Join(dir, "failed-inputs", "*.retry"))
				require.NoError(t, err)
				require.Len(t, files, 1)
				expired := *input
				expired.ExpiresAt = time.Now().Unix() - 1
				header, err := common.Marshal(expired)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(files[0], append(append(header, '\n'), body...), 0600))
				past := time.Now().Add(-service.ReferenceRetention() - time.Minute)
				require.NoError(t, os.Chtimes(files[0], past, past))
				assert.Equal(t, 410, request(http.MethodGet, path+"/retry-input", "retryOwnerKey", "", "").Code)
				assert.Equal(t, 410, request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", task.TaskID, "").Code)
				status = request(http.MethodGet, path, "retryOwnerKey", "", "")
				assert.NotContains(t, status.Body.String(), `"retry_input"`, "cached task status must not advertise expired inputs")
				// Recovering a previous acceptance must still work after its source expired.
				assert.Equal(t, 200, request(http.MethodPost, requestPath+"?async=true", "retryOwnerKey", task.TaskID, key).Code)
				assert.Equal(t, 2, admissions)
				require.NoError(t, service.CleanupImageTaskInputs())
				require.NoFileExists(t, files[0])
			})
		}
	}
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
	const modelName = "native-image-billing-test"
	originalCompletion := ratio_setting.CompletionRatio2JSONString()
	originalCache := ratio_setting.CacheRatio2JSONString()
	originalImage := ratio_setting.ImageRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"native-image-billing-test":5}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"native-image-billing-test":0.5}`))
	require.NoError(t, ratio_setting.UpdateImageRatioByJSONString(`{"native-image-billing-test":2}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletion))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(originalCache))
		require.NoError(t, ratio_setting.UpdateImageRatioByJSONString(originalImage))
	})
	const imageUsage = `{"input_tokens":100,"output_tokens":200,"input_tokens_details":{"cached_tokens":10,"image_tokens":20,"cached_tokens_details":{"text_tokens":10,"image_tokens":0}},"output_tokens_details":{"image_tokens":200}}`
	for _, tc := range []struct {
		name        string
		status      string
		invalid     bool
		wantSuccess bool
		configured  bool
		usage       string
		contentOnly bool
		retry       bool
		perCall     bool
		wantQuota   int
		fallback    *int
		wantInput   int
		wantOutput  int
	}{
		{name: "upstream_suspended", status: "failed"},
		{name: "valid_image", status: "succeeded", wantSuccess: true},
		{name: "invalid_image_bytes", status: "succeeded", invalid: true},
		{name: "configured_adapter_image", status: "done", configured: true, wantSuccess: true},
		{name: "configured_adapter_string_failure", status: "broken", configured: true},
		{name: "configured_usage_refund", status: "done", configured: true, wantSuccess: true, usage: imageUsage, wantQuota: 6690, wantInput: 100, wantOutput: 200},
		{name: "configured_usage_content_download", status: "done", configured: true, contentOnly: true, wantSuccess: true, usage: imageUsage, wantQuota: 6690, wantInput: 100, wantOutput: 200},
		{name: "retry_resume_usage", status: "done", configured: true, retry: true, wantSuccess: true, usage: imageUsage, wantQuota: 6690, wantInput: 100, wantOutput: 200},
		{name: "native_usage_extra_charge", status: "succeeded", wantSuccess: true, usage: `{"prompt_tokens":100,"completion_tokens":30000,"total_tokens":30100}`, wantQuota: 900600, wantInput: 100, wantOutput: 30000},
		{name: "usage_saturation_is_audited", status: "done", configured: true, wantSuccess: true, usage: `{"input_tokens":100,"output_tokens":100000000}`, wantQuota: 2147483647, wantInput: 100, wantOutput: 100000000},
		{name: "per_call_keeps_price_and_records_usage", status: "done", configured: true, perCall: true, wantSuccess: true, usage: imageUsage, wantInput: 100, wantOutput: 200},
		{name: "null_usage_keeps_reservation", status: "done", configured: true, wantSuccess: true, usage: `null`},
		{name: "empty_usage_keeps_reservation", status: "done", configured: true, wantSuccess: true, usage: `{}`},
		{name: "invalid_usage_keeps_reservation", status: "done", configured: true, wantSuccess: true, usage: `{"input_tokens":"invalid"}`},
		{name: "missing_usage_excludes_image_reserve", status: "done", configured: true, wantSuccess: true, fallback: common.GetPointer(8256), wantQuota: 8256},
		{name: "zero_fallback_refunds_image_reserve", status: "done", configured: true, wantSuccess: true, fallback: common.GetPointer(0)},
		{name: "failed_image_with_usage_refunds", status: "succeeded", invalid: true, usage: imageUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			if tc.name == "valid_image" {
				query := "SELECT version()"
				if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
					query = "SELECT sqlite_version()"
				}
				var version string
				require.NoError(t, db.Raw(query).Scan(&version).Error)
				t.Logf("database=%s version=%s separate_log_database=%t", common.MainDatabaseType(), version, model.LOG_DB != db)
			}
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
				usageJSON := ""
				if tc.usage != "" {
					usageJSON = `,"usage":` + tc.usage
				}
				if tc.configured {
					if r.URL.Path != "/configured/upstream-test/status" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					encodedImage := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
					if tc.contentOnly {
						encodedImage = ""
					}
					_, _ = fmt.Fprintf(w, `{"job":{"state":%q,"error":"adapter rejected request native-upstream-key","output":{"image":%q}}%s}`, tc.status, encodedImage, usageJSON)
					return
				}
				_, _ = fmt.Fprintf(w, `{"status":%q,"error":{"message":"invalid image_url: upload init failed: You are suspended for violating our user agreement."}%s}`, tc.status, usageJSON)
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
				if tc.contentOnly {
					snapshot.ContentURL = base + "/task/content"
					snapshot.Task.ContentURLTemplate = "/task/{task_id}/content"
				}
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			c.Set("token_name", "native-test")
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: modelName, FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 63}}
			info.PriceData.ModelRatio = 6
			info.ImageTokenFallbackQuota = tc.fallback
			info.PriceData.ModelPrice = -1
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			if tc.perCall {
				info.PriceData.UsePrice = true
				info.PriceData.ModelPrice = 0.3
			}
			// Reservation has already happened in the request BillingSession. Adoption
			// must preserve it without posting a second reserve or a consumption log.
			require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: modelName}, snapshot))
			require.Equal(t, http.StatusOK, recorder.Code)
			var task model.Task
			require.NoError(t, db.First(&task).Error)
			require.Equal(t, service.TaskBillingStatePending, task.PrivateData.BillingState)
			require.Equal(t, 150000, task.Quota)
			var consumes int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
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
			if tc.retry {
				task.PrivateData.ImageRetry = &model.AsyncImageRetryState{}
				require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
				require.NoError(t, db.First(&task, task.ID).Error)
			}
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
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			if tc.wantSuccess {
				wantQuota := tc.wantQuota
				if wantQuota == 0 && tc.fallback == nil {
					wantQuota = 150000
				}
				require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
				require.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
				assert.Equal(t, wantQuota, task.Quota)
				assert.Equal(t, wantQuota, task.PrivateData.ActualQuota)
				assert.Equal(t, 1000000-wantQuota, user.Quota)
				assert.Equal(t, 1000000-wantQuota, token.RemainQuota)
				assert.Equal(t, wantQuota, user.UsedQuota)
				assert.Equal(t, wantQuota, token.UsedQuota)
				assert.Equal(t, tc.wantInput, task.PrivateData.PromptTokens)
				assert.Equal(t, tc.wantOutput, task.PrivateData.CompletionTokens)
				require.EqualValues(t, 1, consumes)
				var consume model.Log
				require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).First(&consume).Error)
				assert.Equal(t, wantQuota, consume.Quota)
				assert.Equal(t, tc.wantInput, consume.PromptTokens)
				assert.Equal(t, tc.wantOutput, consume.CompletionTokens)
				if tc.name == "usage_saturation_is_audited" {
					assert.Contains(t, consume.Other, `"quota_saturation"`)
				}
				if tc.usage == imageUsage {
					require.NotNil(t, task.PrivateData.UsageDetails)
					assert.Equal(t, 300, task.PrivateData.TotalTokens)
					assert.Equal(t, 10, task.PrivateData.UsageDetails.PromptTokensDetails.CachedTokens)
					assert.Equal(t, 20, task.PrivateData.UsageDetails.PromptTokensDetails.ImageTokens)
					assert.Equal(t, 200, task.PrivateData.UsageDetails.CompletionTokenDetails.ImageTokens)
					require.NotNil(t, task.PrivateData.UsageDetails.PromptTokensDetails.CachedTokensDetails)
					require.NotNil(t, task.PrivateData.UsageDetails.PromptTokensDetails.CachedTokensDetails.ImageTokens)
					assert.Zero(t, *task.PrivateData.UsageDetails.PromptTokensDetails.CachedTokensDetails.ImageTokens)
					assert.Contains(t, consume.Other, `"cache_tokens":10`)
					assert.Contains(t, consume.Other, `"image_input":20`)
				}
				stored, err := os.ReadFile(task.PrivateData.LocalPath)
				require.NoError(t, err)
				require.Equal(t, imageBytes.Bytes(), stored)
				require.NoError(t, service.ConfirmTaskBillingSettled(context.Background(), &task, wantQuota, "duplicate terminal notification"))
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
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&finalConsumes).Error)
			require.Equal(t, consumes, finalConsumes)
			require.Zero(t, posts)
			require.Positive(t, gets)
			if tc.configured && !tc.contentOnly {
				require.Equal(t, 1, gets)
			}
		})
	}
}

func TestAliAsyncImageWorkerCompatibility(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	originalMaxDownload := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = originalMaxDownload })
	for _, tc := range []struct {
		name           string
		legacySnapshot bool
		choicesResult  bool
		n              int
		parameterN     int
	}{
		{name: "queued request", n: 1, parameterN: 1},
		{name: "legacy snapshot", legacySnapshot: true, n: 1, parameterN: 1},
		{name: "choices result after text content", choicesResult: true, n: 1, parameterN: 1},
		{name: "legacy snapshot rejects provider count", legacySnapshot: true, n: 1, parameterN: 2},
		{name: "legacy snapshot rejects top-level count", legacySnapshot: true, n: 2, parameterN: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}))
			require.NoError(t, db.Where("channel_id = ?", 910).Delete(&model.Ability{}).Error)
			oldMemory, oldBatch, oldLog := common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
			common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldMemory, oldBatch, oldLog
			})
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "native_billing_test", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "ali-user-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)

			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			var posts, polls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/result.png" {
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(imageBytes.Bytes())
					return
				}
				assert.Equal(t, "Bearer ali-upstream-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					posts.Add(1)
					assert.Equal(t, "/api/v1/services/aigc/text2image/image-synthesis", r.URL.Path)
					assert.Equal(t, "enable", r.Header.Get("X-DashScope-Async"))
					var request struct {
						Model string `json:"model"`
						Input struct {
							Prompt string `json:"prompt"`
						} `json:"input"`
						Parameters struct {
							N int `json:"n"`
						} `json:"parameters"`
					}
					if !assert.NoError(t, common.DecodeJson(r.Body, &request)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Equal(t, "wanx2.1-t2i-turbo", request.Model)
					assert.Equal(t, "fixture", request.Input.Prompt)
					assert.Equal(t, 1, request.Parameters.N)
					_, _ = io.WriteString(w, `{"output":{"task_id":"ali-job","task_status":"PENDING"},"request_id":"submit"}`)
					return
				}
				polls.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/tasks/ali-job", r.URL.Path)
				var persisted model.Task
				if assert.NoError(t, db.Where("channel_id = ?", 910).First(&persisted).Error) {
					assert.Equal(t, relay.NativeAsyncImageFormat, persisted.PrivateData.RequestRelayFormat)
					snapshot, err := os.ReadFile(persisted.PrivateData.RequestBodyPath)
					if assert.NoError(t, err) {
						assert.Contains(t, string(snapshot), "ali-job")
						assert.NotContains(t, string(snapshot), "ali-upstream-key")
					}
				}
				if tc.choicesResult {
					_, _ = fmt.Fprintf(w, `{"output":{"task_id":"ali-job","task_status":"SUCCEEDED","choices":[{"message":{"content":[{"text":"Image generated"},{"image":"http://%s/result.png"}]}}]},"usage":{"image_count":1}}`, r.Host)
					return
				}
				_, _ = fmt.Fprintf(w, `{"output":{"task_id":"ali-job","task_status":"SUCCEEDED","results":[{"url":"http://%s/result.png"}]},"usage":{"image_count":1}}`, r.Host)
			}))
			defer upstream.Close()
			upstreamURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			fetchSetting := system_setting.GetFetchSetting()
			originalFetchSetting := *fetchSetting
			fetchSetting.AllowPrivateIp = true
			fetchSetting.AllowedPorts = []string{upstreamURL.Port()}
			t.Cleanup(func() { *fetchSetting = originalFetchSetting })
			channel := &model.Channel{Id: 910, Name: "ali-native-async", Type: constant.ChannelTypeAli, Key: "ali-upstream-key", Models: "custom-ali-image", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: common.GetPointer(upstream.URL), ModelMapping: common.GetPointer(`{"custom-ali-image":"wanx2.1-t2i-turbo"}`)}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := fmt.Sprintf(`{"model":"custom-ali-image","prompt":"fixture","size":"1024x1024","n":%d,"parameters":{"n":%d}}`, tc.n, tc.parameterN)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: "custom-ali-image", FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 910}}
			require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "custom-ali-image", N: common.GetPointer(uint(1))}))
			common.CleanupBodyStorage(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response dto.ImageAsyncSubmitResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, dto.ImageAsyncStatusSubmitted, response.Status)
			assert.NotEmpty(t, response.TaskID)
			assert.Zero(t, posts.Load(), "admission must not wait on the provider")

			var task model.Task
			require.NoError(t, db.Where("task_id = ?", response.TaskID).First(&task).Error)
			if tc.legacySnapshot {
				// Older persisted requests predate retry metadata but must still run.
				task.PrivateData.ImageRetry = nil
				require.NoError(t, model.UpdateTaskPrivateData(task.ID, task.PrivateData))
			}
			require.True(t, claimInternalAsyncImageTask(&task))
			require.NoError(t, db.First(&task, task.ID).Error)
			err = executeInternalAsyncImageTask(context.Background(), &task)
			if tc.n != 1 || tc.parameterN != 1 {
				require.Error(t, err)
				require.NoError(t, db.First(&task, task.ID).Error)
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				assert.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
				assert.Zero(t, posts.Load())
				assert.Zero(t, polls.Load())
				var user model.User
				require.NoError(t, db.First(&user, 61).Error)
				assert.Equal(t, 1000000, user.Quota)
				var token model.Token
				require.NoError(t, db.First(&token, 62).Error)
				assert.Equal(t, 1000000, token.RemainQuota)
				var consumes int64
				require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
				assert.Zero(t, consumes)
				return
			}
			require.NoError(t, err)
			require.NoError(t, db.First(&task, task.ID).Error)
			assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			assert.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
			assert.Equal(t, int32(1), posts.Load())
			assert.Equal(t, int32(1), polls.Load())
			storedImage, err := os.ReadFile(task.PrivateData.LocalPath)
			require.NoError(t, err)
			assert.Equal(t, imageBytes.Bytes(), storedImage)
			require.NoError(t, service.ConfirmTaskBillingSettled(context.Background(), &task, 150000, "duplicate Ali completion"))
			var user model.User
			require.NoError(t, db.First(&user, 61).Error)
			assert.Equal(t, 850000, user.Quota)
			var token model.Token
			require.NoError(t, db.First(&token, 62).Error)
			assert.Equal(t, 850000, token.RemainQuota)
			assert.Equal(t, 150000, token.UsedQuota)
			var consumes int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			assert.EqualValues(t, 1, consumes)
			_, _, _, _, err = runInternalAsyncImageRelay(context.Background(), &task)
			require.Error(t, err, "a completed task must not resubmit its original request")
			assert.Equal(t, int32(1), posts.Load())
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
		name, failure, retryCodes, terminalStatus                string
		retries, wantPosts                                       int
		success, queued, backupFails, pinned, interrupted, cache bool
		resume, invalidResult, multipart                         bool
		autoSwitch, missingResult                                bool
		guarded                                                  bool
		pollStatus                                               int
	}{
		{name: "mapped native failover", failure: `{"code":"server_error","type":"provider_error","message":"temporary failure"}`, retries: 3, wantPosts: 1, success: true},
		{name: "queued request maps each attempt", failure: `"generation failed: generate error: An error occurred."`, retries: 3, wantPosts: 2, success: true, queued: true, cache: true},
		{name: "message only rate limit", failure: `"请求过于频繁,请稍后重试"`, retries: 3, wantPosts: 1, success: true},
		{name: "upstream content rejection stops", failure: `{"code":"server_error","message":"生成图片未通过内容审核"}`, retries: 3},
		{name: "bridge content rejection type stops", failure: `{"code":"400","type":"content_policy_violation","message":"Task failed"}`, retries: 3, guarded: true},
		{name: "moderation service outage stops by keyword", failure: `{"code":"server_error","message":"审核服务暂不可用"}`, retries: 3},
		{name: "native bare safe stops", failure: `{"code":"server_error","message":"SAFE"}`, retries: 3},
		{name: "bridge native unsafe type stops", failure: `{"code":"server_error","type":"unsafe","message":"Task failed"}`, retries: 3, guarded: true},
		{name: "ordinary service outage retries", failure: `{"code":"server_error","message":"生成服务暂不可用"}`, retries: 3, wantPosts: 1, success: true},
		{name: "upstream invalid parameter obeys configured status", failure: `{"status":503,"message":"invalid parameter size"}`, retries: 3, wantPosts: 1, success: true},
		{name: "confirmed failure without status uses 502", failure: `"unrecognized failure"`, retries: 3, wantPosts: 1, success: true},
		{name: "provider retry hint does not override site policy", failure: `{"code":"server_error","message":"failure","retryable":false}`, retries: 3, wantPosts: 1, success: true},
		{name: "reported queue failure retries", failure: `{"code":"generation_failed","message":"上游接口繁忙，队列超时请重试"}`, retries: 3, wantPosts: 1, success: true},
		{name: "reported queue failure bridge retries", failure: `{"code":"generation_failed","message":"上游接口繁忙，队列超时请重试"}`, retries: 3, wantPosts: 1, success: true, guarded: true},
		{name: "fallback 502 excluded by configuration", failure: `{"code":"generation_failed","message":"busy"}`, retries: 3, retryCodes: "503"},
		{name: "bridge fallback 502 excluded by configuration", failure: `{"code":"generation_failed","message":"busy"}`, retries: 3, retryCodes: "503", guarded: true},
		{name: "upstream 403 never retries", failure: `{"status_code":403,"message":"forbidden"}`, retries: 3},
		{name: "bridge upstream 403 never retries", failure: `{"status_code":403,"message":"forbidden"}`, retries: 3, guarded: true},
		{name: "numeric forbidden code never retries", failure: `{"code":"403","status_code":502,"message":"forbidden"}`, retries: 3},
		{name: "upstream 404 retries", failure: `{"status_code":404,"message":"model not found"}`, retries: 3, wantPosts: 1, success: true},
		{name: "upstream local-looking key code retries", failure: `{"code":"channel:invalid_key","status_code":401,"message":"invalid credential"}`, retries: 3, wantPosts: 1, success: true, guarded: true},
		{name: "upstream local-looking group code retries", failure: `{"code":"get_channel_failed","status_code":503,"message":"no channel"}`, retries: 3, wantPosts: 1, success: true},
		{name: "upstream local-looking response code retries", failure: `{"code":"bad_response_body","status_code":502,"message":"upstream job failed"}`, retries: 3, wantPosts: 1, success: true, guarded: true},
		{name: "expired is not confirmed failed", terminalStatus: "expired", failure: `{"status_code":502,"message":"expired"}`, retries: 3},
		{name: "cancelled is not confirmed failed", terminalStatus: "cancelled", failure: `{"status_code":502,"message":"cancelled"}`, retries: 3},
		{name: "retry disabled", failure: `{"code":"rate_limit_exceeded","message":"busy"}`, retries: 0},
		{name: "retry limit and refund", failure: `{"status_code":503,"message":"busy"}`, retries: 1, wantPosts: 1, backupFails: true},
		{name: "no channel is attempted twice", failure: `{"code":"server_error","message":"busy"}`, retries: 3, wantPosts: 1, backupFails: true, cache: true},
		{name: "pinned channel cannot fail over", failure: `{"code":"server_error","message":"busy"}`, retries: 3, pinned: true},
		{name: "interrupted submission is not replayed", failure: `{"code":"server_error","message":"busy"}`, retries: 3, interrupted: true},
		{name: "accepted backup resumes even with exhausted budget", retries: 1, resume: true, success: true},
		{name: "result storage failure does not generate again", retries: 3, invalidResult: true},
		{name: "poll rejection does not generate again", retries: 3, pollStatus: 401},
		{name: "multipart reference survives failover", failure: `{"code":"server_error","message":"busy"}`, retries: 3, wantPosts: 1, success: true, multipart: true},
		{name: "automatic health follows native terminal failure and backup result", failure: `{"code":"service_unavailable","message":"temporary failure"}`, retries: 3, wantPosts: 1, success: true, autoSwitch: true},
		{name: "automatic health ignores queued acceptance", failure: `{"status_code":503,"message":"unavailable"}`, retries: 3, wantPosts: 2, success: true, queued: true, autoSwitch: true},
		{name: "automatic health ignores content rejection", failure: `{"code":"server_error","message":"content_policy violation"}`, retries: 3, autoSwitch: true},
		{name: "automatic health keeps uncertain native attempt pending", retries: 3, pollStatus: 401, autoSwitch: true},
		{name: "automatic health rejects invalid generated image", retries: 3, invalidResult: true, autoSwitch: true},
		{name: "automatic health releases completed job with missing artifact", retries: 3, missingResult: true, autoSwitch: true},
		{name: "guarded queued task retries confirmed failure", failure: `{"code":"server_error","message":"temporary failure"}`, retries: 3, wantPosts: 2, queued: true, guarded: true, success: true},
		{name: "guarded accepted task retries confirmed failure", failure: `{"code":"server_error","message":"temporary failure"}`, retries: 3, guarded: true, wantPosts: 1, success: true},
		{name: "guarded interrupted submission stays unknown", retries: 3, queued: true, interrupted: true, guarded: true},
		{name: "guarded accepted task resumes polling", retries: 1, resume: true, success: true, guarded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			previousCodes := operation_setting.AutomaticRetryStatusCodesToString()
			t.Cleanup(func() { require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousCodes)) })
			codes := tc.retryCodes
			if codes == "" {
				codes = "400-599"
			}
			require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(codes))
			if tc.name == "mapped native failover" {
				query := "SELECT version()"
				if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
					query = "SELECT sqlite_version()"
				}
				var version string
				require.NoError(t, db.Raw(query).Scan(&version).Error)
				t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
			}

			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}))
			if tc.autoSwitch {
				require.NoError(t, db.AutoMigrate(&model.ChannelAutoSwitchState{}, &model.ChannelAutoSwitchAttempt{}))
				require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.ChannelAutoSwitchAttempt{}).Error)
				require.NoError(t, db.Where("channel_id IN ?", []int{701, 809}).Delete(&model.ChannelAutoSwitchState{}).Error)
			}
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
				if tc.missingResult {
					_, _ = io.WriteString(w, `{"status":"completed","data":[]}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/first") || tc.backupFails {
					status := tc.terminalStatus
					if status == "" {
						status = "failed"
					}
					fmt.Fprintf(w, `{"status":%q,"error":%s}`, status, tc.failure)
					return
				}
				fmt.Fprintf(w, `{"status":"completed","data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(imageBytes.Bytes()))
			}))
			defer upstream.Close()
			config := &kitdto.ImageAdapterConfig{Enabled: true,
				Request:    kitdto.ImageAdapterRequestConfig{GenerationsPath: "/v1/images/generations", EditsPath: "/v1/images/generations", FieldMap: map[string]string{"quality": "detail_level"}},
				References: &kitdto.ImageAdapterReferenceConfig{Field: "references", Encoding: "url_array", MaxImages: 4, UploadLocal: true},
				Task:       &kitdto.ImageAdapterTaskConfig{IDPaths: []string{"id"}, StatusPath: "status", ErrorPath: "error", ResultB64Path: "data.0.b64_json", PollURLTemplate: "/jobs/{task_id}", PendingStates: []string{"queued"}, SuccessStates: []string{"completed"}, FailureStates: []string{"failed", "expired", "cancelled"}},
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
				if (tc.queued || tc.autoSwitch) && i == 1 {
					channel.Priority = common.GetPointer(int64(4))
				}
				if tc.autoSwitch {
					channel.OtherSettings = `{"auto_switch_enabled":true}`
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
			if tc.guarded {
				c.Set("transfer_execution_id", "persisted-transfer-execution")
			}
			var initialTicket *model.ChannelAutoSwitchTicket
			if tc.autoSwitch {
				selected, _, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "custom-image-alias", RequestPath: c.Request.URL.Path})
				require.NoError(t, err)
				require.Equal(t, 701, selected.Id)
				initialTicket = service.AutoSwitchAttempt(c)
				require.NotNil(t, initialTicket)
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
			assert.Equal(t, c.GetString("transfer_execution_id"), task.PrivateData.TransferExecutionID,
				"queue admission must persist the guard before the worker can dispatch")
			if tc.autoSwitch {
				assert.Nil(t, service.AutoSwitchAttempt(c), "HTTP completion must not record queue or native acceptance as success")
				var attempt model.ChannelAutoSwitchAttempt
				require.NoError(t, db.First(&attempt, "id = ?", initialTicket.ID).Error)
				if tc.queued {
					assert.Nil(t, task.PrivateData.AutoSwitch)
					assert.EqualValues(t, "released", attempt.Outcome)
				} else {
					require.NotNil(t, task.PrivateData.AutoSwitch)
					assert.Equal(t, initialTicket.ID, task.PrivateData.AutoSwitch.ID)
					assert.Zero(t, attempt.CompletedAt, "accepted native task remains pending")
				}
			}
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
			if tc.autoSwitch {
				states, err := model.GetChannelAutoSwitchStates([]int{701, 809}, "default", "custom-image-alias", "/v1/images/generations")
				require.NoError(t, err)
				if tc.success {
					assert.EqualValues(t, 1, states[701].WindowFailures)
					assert.EqualValues(t, 1, states[809].WindowTotal)
					assert.Zero(t, states[809].WindowFailures)
				} else {
					assert.Zero(t, states[701].WindowFailures)
					assert.Zero(t, states[701].WindowTotal)
					var attempt model.ChannelAutoSwitchAttempt
					require.NoError(t, db.First(&attempt, "id = ?", initialTicket.ID).Error)
					if tc.pollStatus != 0 {
						assert.Zero(t, attempt.CompletedAt, "uncertain polling must not release an accepted attempt")
					} else {
						assert.Equal(t, model.ChannelAutoSwitchNeutral, attempt.Outcome)
					}
				}
			}
			require.Len(t, postedModels, tc.wantPosts)
			for _, ok := range requestChecks {
				assert.True(t, ok, "retry must reconstruct original parameters")
			}
			if tc.wantPosts > 0 {
				expected := "upstream-backup"
				assert.Equal(t, expected, postedModels[len(postedModels)-1])
			}
			if tc.queued && !tc.interrupted {
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

func TestAsyncImageAcceptedProbeSurvivesSnapshotFailure(t *testing.T) {
	db := setupAsyncImageWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelAutoSwitchState{}, &model.ChannelAutoSwitchAttempt{}))
	oldMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory })
	for i, id := range []int{163, 174} {
		channel := model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: "fixture", Status: common.ChannelStatusEnabled,
			Models: "probe-image", Group: "default", Priority: common.GetPointer(int64(2 - i)), OtherSettings: `{"auto_switch_enabled":true}`}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(db))
	}
	failedAt := time.Now().Add(-10 * time.Minute)
	for range 3 {
		ticket, err := model.BeginChannelAutoSwitchAttempt(163, "default", "probe-image", "/v1/images/generations", failedAt)
		require.NoError(t, err)
		require.NotNil(t, ticket)
		require.NoError(t, model.RecordChannelAutoSwitchResult(*ticket, model.ChannelAutoSwitchFailure, "upstream_unavailable", failedAt))
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"probe-image"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	defer common.CleanupBodyStorage(c)
	channel, _, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "probe-image"})
	require.NoError(t, err)
	require.Equal(t, 163, channel.Id)
	ticket := service.AutoSwitchAttempt(c)
	require.NotNil(t, ticket)
	require.True(t, ticket.Probe)
	blockedDirectory := filepath.Join(t.TempDir(), "file-blocks-request-directory")
	require.NoError(t, os.WriteFile(blockedDirectory, []byte("fixture"), 0600))
	t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", blockedDirectory)
	info := &relaycommon.RelayInfo{UsingGroup: "default", OriginModelName: "probe-image", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 163}}
	apiErr := relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "probe-image"}, &relay.NativeAsyncImageSnapshot{ID: "accepted", ChannelID: 163})
	require.NotNil(t, apiErr)
	service.ReleaseAutoSwitchAttempt(c)
	var attempt model.ChannelAutoSwitchAttempt
	require.NoError(t, db.First(&attempt, "id = ?", ticket.ID).Error)
	assert.Zero(t, attempt.CompletedAt, "a local snapshot error cannot release an already accepted probe")
	states, err := model.GetChannelAutoSwitchStates([]int{163}, "default", "probe-image", "/v1/images/generations")
	require.NoError(t, err)
	assert.Equal(t, ticket.ID, states[163].ProbeAttemptID)
	assert.Zero(t, states[163].ProbeSuccesses)
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

func TestAsyncImageRetryCycleBudget(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	for _, tc := range []struct {
		name                                 string
		cycle, timed, expired, pinned, cache bool
		resumeCycle, submission              bool
		localConfig, localSubmit             bool
		unavailableGroup                     bool
		contentRejected, direct              bool
		safetyKeyword                        bool
		status, retries, wantPosts           int
		success                              bool
	}{
		{name: "task submission cycles within time budget", cycle: true, timed: true, submission: true, status: 503, wantPosts: 4, success: true},
		{name: "bridge content rejection overrides cycle budget", cycle: true, timed: true, contentRejected: true, status: 400, retries: 9, wantPosts: 1},
		{name: "direct content rejection overrides cycle budget", cycle: true, timed: true, contentRejected: true, direct: true, status: 502, retries: 9, wantPosts: 1},
		{name: "bridge safe keyword overrides cycle budget", cycle: true, timed: true, safetyKeyword: true, status: 502, retries: 9, wantPosts: 1},
		{name: "direct safe keyword overrides cycle budget", cycle: true, timed: true, safetyKeyword: true, direct: true, status: 500, retries: 9, wantPosts: 1},
		{name: "other upstream 400 still cycles", cycle: true, timed: true, status: 400, wantPosts: 4, success: true},
		{name: "task local 500 stops despite time budget", cycle: true, timed: true, submission: true, localSubmit: true, status: 500},
		{name: "local model mapping failure never cycles", cycle: true, timed: true, localConfig: true, retries: 9},
		{name: "local unavailable group never cycles", cycle: true, timed: true, unavailableGroup: true, retries: 9},
		{name: "resumed cycle keeps original round", cycle: true, timed: true, resumeCycle: true, status: 503, wantPosts: 1, success: true},
		{name: "disabled stops after all channels", status: 503, retries: 5, wantPosts: 2},
		{name: "cycle remains count limited", cycle: true, status: 503, retries: 2, wantPosts: 3},
		{name: "cycle succeeds next round", cycle: true, status: 503, retries: 3, wantPosts: 4, success: true},
		{name: "time overrides zero count for 500", cycle: true, timed: true, status: 500, wantPosts: 4, success: true},
		{name: "time overrides zero count for upstream 404", cycle: true, timed: true, status: 404, wantPosts: 4, success: true},
		{name: "time overrides count for 502", cycle: true, timed: true, status: 502, retries: 1, wantPosts: 4, success: true},
		{name: "time overrides count for 429", cycle: true, timed: true, status: 429, retries: 1, wantPosts: 4, success: true},
		{name: "time overrides count with cache", cycle: true, timed: true, cache: true, status: 503, retries: 1, wantPosts: 4, success: true},
		{name: "expired resumed budget stops", cycle: true, timed: true, expired: true, status: 503, retries: 9},
		{name: "bridge 403 remains blocked", cycle: true, timed: true, status: 403, retries: 9, wantPosts: 1},
		{name: "pinned channel never cycles", cycle: true, timed: true, pinned: true, status: 503, retries: 9, wantPosts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAsyncImageWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.QuotaData{}, &model.Option{}))
			require.NoError(t, db.Where("channel_id IN ?", []int{701, 809, 810}).Delete(&model.Ability{}).Error)
			common.OptionMapRWMutex.Lock()
			oldOptions := common.OptionMap
			common.OptionMap = maps.Clone(model.CurrentRequestPolicy().Options)
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = oldOptions
				common.OptionMapRWMutex.Unlock()
			})
			previous := model.CurrentRequestPolicy().Options
			previousRetry := common.RetryTimes
			require.NoError(t, model.UpdateRequestPolicyOptions(map[string]string{"RetryCycleEnabled": strconv.FormatBool(tc.cycle), "RetryTimeBudgetEnabled": strconv.FormatBool(tc.timed), "RetryTimeBudgetSeconds": "60", "AutomaticRetryStatusCodes": "400,403-404,429,500-503"}))
			t.Cleanup(func() {
				require.NoError(t, model.UpdateRequestPolicyOptions(previous))
				common.RetryTimes = previousRetry
			})
			oldRetry, oldMemory, oldBatch, oldLog := common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
			common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = tc.retries, tc.cache, false, true
			t.Cleanup(func() {
				common.RetryTimes, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRetry, oldMemory, oldBatch, oldLog
			})
			t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_STORAGE_PATH", t.TempDir())
			t.Setenv("ASYNC_IMAGE_SIGNING_SECRET", "cycle-budget-fixture")
			require.NoError(t, db.Create(&model.User{Id: 61, Username: "native_billing_test", Quota: 850000, Status: common.UserStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.Token{Id: 62, UserId: 61, Key: "retry-user-token", RemainQuota: 850000, UsedQuota: 150000, Status: common.TokenStatusEnabled}).Error)
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))))
			var posts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if posts.Add(1) < 4 && !tc.resumeCycle {
					w.WriteHeader(tc.status)
					if tc.safetyKeyword {
						_, _ = io.WriteString(w, `{"error":{"code":"server_error","message":"SAFE"}}`)
						return
					}
					if tc.contentRejected {
						_, _ = io.WriteString(w, `{"error":{"code":"400","message":"非常抱歉，该提示可能违反了我们的内容政策。如果你认为此判断有误，请重试或修改提示语。"}}`)
						return
					}
					_, _ = io.WriteString(w, `{"error":{"code":"upstream_unavailable","message":"busy"}}`)
					return
				}
				fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(imageBytes.Bytes()))
			}))
			defer upstream.Close()
			for i, id := range []int{701, 809, 810} {
				channel := &model.Channel{Id: id, Name: "retry-cycle", Type: constant.ChannelTypeOpenAI, Key: "fixture", Models: "gpt-image-1", Group: "default", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(5 - i)), Weight: common.GetPointer(uint(1)), BaseURL: common.GetPointer(upstream.URL)}
				if id == 810 {
					channel.Status = common.ChannelStatusManuallyDisabled
				}
				if tc.localConfig && id == 701 {
					channel.ModelMapping = common.GetPointer(`{"gpt-image-1":"alias","alias":"gpt-image-1"}`)
				}
				if tc.unavailableGroup {
					channel.Group = "unrelated-group"
				}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			if tc.cache {
				model.InitChannelCache()
			}
			body := `{"model":"gpt-image-1","prompt":"fixture","size":"1024x1024","n":1}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if !tc.direct {
				c.Set("transfer_execution_id", "cycle-test-bridge")
			}
			if tc.pinned {
				service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 701, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			info := &relaycommon.RelayInfo{UserId: 61, TokenId: 62, UsingGroup: "default", OriginModelName: "gpt-image-1", FinalPreConsumedQuota: 150000, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 701, UpstreamModelName: "gpt-image-1"}}
			if tc.submission {
				events := []string{}
				billing := &taskSubmissionTestBilling{events: &events}
				info.Billing = billing
				info.TokenGroup = "default"
				info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: "task_cycle_fixture"}
				info.ChannelMeta = &relaycommon.ChannelMeta{}
				submissions := 0
				outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
					submissions++
					if tc.localSubmit {
						return nil, service.TaskErrorWrapperLocal(fmt.Errorf("local request setup failed"), "build_request_failed", tc.status)
					}
					response, err := http.Post(upstream.URL, "application/json", c.Request.Body)
					require.NoError(t, err)
					_, err = io.Copy(io.Discard, response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					if response.StatusCode != 200 {
						return nil, &dto.TaskError{Code: "fail_to_fetch_task", Message: "busy", StatusCode: response.StatusCode}
					}
					return &relay.TaskSubmitResult{UpstreamTaskID: "accepted-cycle-task", Platform: "cycle-fixture"}, nil
				})
				common.CleanupBodyStorage(c)
				if tc.localSubmit {
					require.NotNil(t, taskErr)
					assert.True(t, taskErr.LocalError)
					assert.Nil(t, outcome)
					assert.Equal(t, 1, submissions)
					assert.Zero(t, posts.Load())
					assert.Equal(t, []string{"701"}, c.GetStringSlice("use_channel"))
					return
				}
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				assert.EqualValues(t, 4, posts.Load())
				assert.Equal(t, []string{"701", "809", "701", "809"}, c.GetStringSlice("use_channel"))
				assert.Equal(t, []string{"reserve", "settle"}, events)
				assert.Zero(t, billing.refunds)
				var taskCount int64
				require.NoError(t, db.Model(&model.Task{}).Count(&taskCount).Error)
				assert.EqualValues(t, 1, taskCount)
				return
			}
			require.Nil(t, relay.SubmitInternalAsyncImageTask(c, info, &dto.ImageRequest{Model: "gpt-image-1"}))
			common.CleanupBodyStorage(c)
			var task model.Task
			require.NoError(t, db.First(&task).Error)
			if tc.resumeCycle {
				task.PrivateData.ImageRetry.StartedAtMS = time.Now().UnixMilli()
				task.PrivateData.ImageRetry.CycleStart = 2
				task.PrivateData.ChannelRetryPath = []string{"701", "809", "701"}
				require.NoError(t, db.Save(&task).Error)
			}
			if tc.expired {
				task.PrivateData.ImageRetry.StartedAtMS = time.Now().Add(-time.Minute).UnixMilli()
				task.PrivateData.ChannelRetryPath = []string{"701"}
				require.NoError(t, db.Save(&task).Error)
			}
			require.True(t, claimInternalAsyncImageTask(&task))
			require.NoError(t, db.First(&task, task.ID).Error)
			err := executeInternalAsyncImageTask(context.Background(), &task)
			if tc.success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			assert.EqualValues(t, tc.wantPosts, posts.Load())
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			assert.NotContains(t, stored.PrivateData.ChannelRetryPath, "810", "disabled channel must stay excluded in every round")
			if tc.localConfig {
				assert.Equal(t, []string{"701"}, stored.PrivateData.ChannelRetryPath, "local setup failure must stop before selecting the next channel")
			}
			if tc.unavailableGroup {
				assert.Empty(t, stored.PrivateData.ChannelRetryPath, "group rejection must stop before dispatch")
			}
			if tc.wantPosts >= 3 {
				assert.Equal(t, []string{"701", "809", "701"}, stored.PrivateData.ChannelRetryPath[:3])
			}
			var taskCount, consumes int64
			require.NoError(t, db.Model(&model.Task{}).Count(&taskCount).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
			assert.EqualValues(t, 1, taskCount)
			if tc.success {
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), stored.Status)
				assert.EqualValues(t, 1, consumes)
				assert.Equal(t, []string{"701", "809", "701", "809"}, stored.PrivateData.ChannelRetryPath)
				assert.Equal(t, 2, stored.PrivateData.ImageRetry.CycleStart)
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), stored.Status)
			}
		})
	}
}

func TestRetryBudgetDeadline(t *testing.T) {
	start := time.Unix(1000, 0)
	budget := service.RetryBudget{Deadline: start.Add(time.Minute)}
	assert.Equal(t, 1, budget.Remaining(-10, start.Add(59*time.Second)))
	assert.Zero(t, budget.Remaining(99, start.Add(time.Minute)))
	assert.Zero(t, budget.Remaining(99, start.Add(61*time.Second)))
	assert.Equal(t, -1, (service.RetryBudget{}).Remaining(-1, start))
	assert.Equal(t, 3, (service.RetryBudget{}).Remaining(3, start))
}
