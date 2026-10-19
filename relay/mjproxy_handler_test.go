package relay

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupMidjourneyQuotaDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB, originalLogDB := model.DB, model.LOG_DB
	originalMain, originalLog := common.MainDatabaseType(), common.LogDatabaseType()
	originalRedis, originalBatch, originalLogEnabled := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	var dialect gorm.Dialector = sqlite.Open(":memory:")
	if dsn := os.Getenv("MJ_QUOTA_TEST_DSN"); dsn != "" {
		require.Contains(t, dsn, "mj_quota_test", "only a dedicated test database is allowed")
		switch os.Getenv("MJ_QUOTA_TEST_DIALECT") {
		case "mysql":
			dialect = mysql.Open(dsn)
			common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
		case "postgres":
			dialect = postgres.Open(dsn)
			common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		default:
			t.Fatal("unsupported test dialect")
		}
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// Keep SQLite's in-memory database on one connection. Concurrent requests
	// still exercise the atomic reservation through independent handlers.
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		sqlDB.SetMaxOpenConns(1)
	}
	model.DB, model.LOG_DB = db, db
	logDSN := os.Getenv("MJ_QUOTA_TEST_LOG_DSN")
	if logDSN != "" {
		require.Contains(t, logDSN, "mj_quota_test", "only a dedicated log test database is allowed")
	}
	t.Setenv("LOG_SQL_DSN", logDSN)
	originalMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = originalMaster
	logSQLDB, err := model.LOG_DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		if logSQLDB != sqlDB {
			require.NoError(t, logSQLDB.Close())
		}
		require.NoError(t, os.Setenv("LOG_SQL_DSN", ""))
		model.DB = originalDB
		common.SetDatabaseTypes(originalMain, originalLog)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = originalMaster
		model.LOG_DB = originalLogDB
		common.SetLogDatabaseType(originalLog)
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = originalRedis, originalBatch, originalLogEnabled
		require.NoError(t, sqlDB.Close())
	})
	tables := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Midjourney{}, &model.Task{}, &model.Log{}}
	require.NoError(t, db.AutoMigrate(tables...))
	for _, table := range tables {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	if model.LOG_DB != db {
		require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
		require.NoError(t, model.LOG_DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Log{}).Error)
	}
	var version string
	query := "SELECT version()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
	service.InitHttpClient()
	originalPrices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj_imagine":0.4,"swap_face":0.4}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(originalPrices))) })
	return db
}

func midjourneyQuotaRequest(user *model.User, token *model.Token, upstream string, swap bool) *dto.MidjourneyResponse {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body, path, modelName := `{"prompt":"cat"}`, "/mj/submit/imagine", "mj_imagine"
	mode := relayconstant.RelayModeMidjourneyImagine
	if swap {
		body, path, modelName = `{"sourceBase64":"source","targetBase64":"target"}`, "/mj/insight-face/swap", "swap_face"
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("base_url", upstream)
	c.Set("channel_id", 1)
	c.Set("token_name", token.Name)
	info := &relaycommon.RelayInfo{
		UserId: user.Id, UserQuota: 1000000000, TokenId: token.Id, TokenKey: token.Key,
		TokenUnlimited: token.UnlimitedQuota, OriginModelName: modelName,
		UsingGroup: "default", RelayMode: mode, StartTime: time.Now(),
	}
	if swap {
		return RelaySwapFace(c, info)
	}
	return RelayMidjourneySubmit(c, info)
}

func TestMidjourneyTokenQuota(t *testing.T) {
	db := setupMidjourneyQuotaDB(t)
	sequence := 0
	for _, swap := range []bool{false, true} {
		for _, tc := range []struct {
			name                           string
			remain, wallet, code, status   int
			unlimited, insertFailure, free bool
			wantCalls                      int32
			wantCharge                     int
			wantError                      string
			wantLocalCode                  string
		}{
			{name: "insufficient", remain: 73083, wallet: 1000000, wantError: "令牌配额不足", wantLocalCode: "insufficient_token_quota"},
			{name: "exhausted", wallet: 1000000, wantError: "令牌配额不足", wantLocalCode: "insufficient_token_quota"},
			{name: "exact balance", remain: 200000, wallet: 1000000, code: 1, status: 200, wantCalls: 1, wantCharge: 200000},
			{name: "queued", remain: 200000, wallet: 1000000, code: 22, status: 200, wantCalls: 1, wantCharge: 200000},
			{name: "free model", free: true, remain: 1, wallet: 1, code: 1, status: 200, wantCalls: 1},
			{name: "unlimited", remain: -100, unlimited: true, wallet: 1000000, code: 1, status: 200, wantCalls: 1, wantCharge: 200000},
			{name: "wallet insufficient rolls token back", remain: 200000, wallet: 100, wantError: "账户余额不足", wantLocalCode: "insufficient_user_quota"},
			{name: "upstream rejects", remain: 200000, wallet: 1000000, code: 24, status: 200, wantCalls: 1},
			{name: "upstream HTTP failure", remain: 200000, wallet: 1000000, code: 1, status: 500, wantCalls: 1},
			{name: "malformed upstream response", remain: 200000, wallet: 1000000, code: -1, status: 200, wantCalls: 1, wantError: "unmarshal_response_body_failed"},
			{name: "insert failure refunds", remain: 200000, wallet: 1000000, code: 1, status: 200, wantCalls: 1, insertFailure: true, wantError: "insert_midjourney_task_failed"},
		} {
			if swap && tc.code == 22 {
				continue // Swap-face only accepts code 1 for billing.
			}
			t.Run(fmt.Sprintf("swap=%t/%s", swap, tc.name), func(t *testing.T) {
				if tc.free {
					require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj_imagine":0,"swap_face":0}`))
					t.Cleanup(func() {
						require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj_imagine":0.4,"swap_face":0.4}`))
					})
				}
				sequence++
				name := fmt.Sprintf("mj-quota-%d", sequence)
				user := &model.User{Username: name, AffCode: name, Quota: tc.wallet, Status: common.UserStatusEnabled}
				require.NoError(t, db.Create(user).Error)
				token := &model.Token{UserId: user.Id, Key: fmt.Sprintf("mj-test-%d", user.Id), Name: "test", RemainQuota: tc.remain, UnlimitedQuota: tc.unlimited, Status: common.TokenStatusEnabled, ExpiredTime: -1}
				require.NoError(t, db.Create(token).Error)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(tc.status)
					if tc.code == -1 {
						fmt.Fprint(w, "invalid json")
						return
					}
					fmt.Fprintf(w, `{"code":%d,"result":"task-%d","description":"test"}`, tc.code, user.Id)
				}))
				defer server.Close()
				if tc.insertFailure {
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("mj_test_insert_failure", func(tx *gorm.DB) {
						if tx.Statement.Table == "midjourneys" {
							tx.AddError(errors.New("test insert failed"))
						}
					}))
					defer db.Callback().Create().Remove("mj_test_insert_failure")
				}
				response := midjourneyQuotaRequest(user, token, server.URL, swap)
				if tc.wantError != "" {
					require.NotNil(t, response)
					assert.Equal(t, tc.wantLocalCode, response.LocalErrorCode)
					if tc.wantLocalCode != "" {
						assert.Contains(t, response.Description, tc.wantError)
						assert.Contains(t, response.Description, "["+tc.wantLocalCode+"]")
					} else {
						assert.Equal(t, tc.wantError, response.Description)
					}
				} else {
					assert.Nil(t, response)
				}
				assert.Equal(t, tc.wantCalls, calls.Load())
				var gotToken model.Token
				var gotUser model.User
				require.NoError(t, db.First(&gotToken, token.Id).Error)
				require.NoError(t, db.First(&gotUser, user.Id).Error)
				assert.Equal(t, tc.remain-tc.wantCharge, gotToken.RemainQuota)
				assert.Equal(t, tc.wantCharge, gotToken.UsedQuota)
				assert.Equal(t, tc.wallet-tc.wantCharge, gotUser.Quota)
				assert.Equal(t, tc.wantCharge, gotUser.UsedQuota)
				if tc.wantCharge > 0 {
					var task model.Midjourney
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&task).Error)
					assert.Equal(t, token.Id, task.TokenId, "persist the token for later failure refunds")
					assert.Equal(t, tc.wantCharge, task.Quota)
				}
			})
		}
	}
}

func TestMidjourneyConcurrentTokenQuota(t *testing.T) {
	db := setupMidjourneyQuotaDB(t)
	user := &model.User{Username: "concurrent-mj", Quota: 1000000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: "concurrent-mj-token", RemainQuota: 200000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(token).Error)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, `{"code":1,"result":"concurrent-task"}`)
	}))
	defer server.Close()
	var once sync.Once
	defer once.Do(func() { close(release) })
	results := make(chan *dto.MidjourneyResponse, 2)
	go func() { results <- midjourneyQuotaRequest(user, token, server.URL, false) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first request did not reach local upstream")
	}
	// The first upstream call is still in flight when the second starts.
	go func() { results <- midjourneyQuotaRequest(user, token, server.URL, false) }()
	select {
	case response := <-results:
		require.NotNil(t, response)
		assert.Equal(t, "insufficient_token_quota", response.LocalErrorCode)
		assert.Contains(t, response.Description, "[insufficient_token_quota]")
	case <-entered:
		t.Fatal("both requests reached upstream using the same token balance")
	case <-time.After(10 * time.Second):
		t.Fatal("second request did not reject insufficient quota")
	}
	once.Do(func() { close(release) })
	require.Nil(t, <-results)
	var got model.Token
	require.NoError(t, db.First(&got, token.Id).Error)
	assert.Zero(t, got.RemainQuota)
	assert.Equal(t, 200000, got.UsedQuota)
}

func TestMidjourneyFailureDiagnostics(t *testing.T) {
	db := setupMidjourneyQuotaDB(t)
	originalErrorLogs := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = originalErrorLogs })
	for i, tc := range []struct {
		name, reason, want string
		status             int
		swap, async        bool
	}{
		{"prompt limit", "提示词过长,目前支持1024以下字符", "[input_too_large] 提示词过长,目前支持1024以下字符", 200, false, false},
		{"parameter", "Invalid parameter: --ar must be 16:9 or 1:1", "[invalid_parameter] Invalid parameter: --ar must be 16:9 or 1:1", 400, false, false},
		{"execution", "执行报错\n图片格式不支持，请使用 PNG", "[invalid_image_input] 执行报错\n图片格式不支持，请使用 PNG", 200, true, false},
		{"redaction", "图片下载失败 https://private.invalid/image?token=secret mj-api-secret=hidden", "[image_download_failed] 图片下载失败 [URL REDACTED] [REDACTED]", 200, false, false},
		{"provider quota", "insufficient quota (remaining 0)", "[service_quota_exceeded] 生成服务可用额度不足：insufficient quota (remaining 0)；请联系管理员处理服务额度；无需为此调整您的账户或令牌额度", 200, false, false},
		{"async failure", "提示词过长,目前支持1024以下字符", "[input_too_large] 提示词过长,目前支持1024以下字符", 200, false, true},
		{"empty failure", "", "[generation_failed] 生成失败，服务未提供可公开确认的具体原因；请联系管理员并提供请求 ID 或任务 ID，以查询详细错误", 200, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("mj-diagnostic-%d", i)
			user := &model.User{Username: name, AffCode: name, Quota: 1000000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(user).Error)
			token := &model.Token{UserId: user.Id, Key: name, RemainQuota: 1000000, Status: common.TokenStatusEnabled, ExpiredTime: -1}
			require.NoError(t, db.Create(token).Error)
			code, description := 4, tc.reason
			if tc.async {
				code, description = 1, "提交成功"
			}
			body, err := common.Marshal(dto.MidjourneyResponse{Code: code, Description: description, Result: name})
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			path, request, modelName := "/mj/submit/imagine", `{"prompt":"cat"}`, "mj_imagine"
			mode := relayconstant.RelayModeMidjourneyImagine
			if tc.swap {
				path, request, modelName = "/mj/insight-face/swap", `{"sourceBase64":"source","targetBase64":"target"}`, "swap_face"
				mode = relayconstant.RelayModeSwapFace
			}
			ctx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(request))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("base_url", server.URL)
			ctx.Set("channel_id", 1)
			ctx.Set("id", user.Id)
			ctx.Set("username", name)
			info := &relaycommon.RelayInfo{UserId: user.Id, UserQuota: user.Quota, TokenId: token.Id, TokenKey: token.Key, OriginModelName: modelName, UsingGroup: "default", RelayMode: mode, StartTime: time.Now()}
			if tc.swap {
				require.Nil(t, RelaySwapFace(ctx, info))
			} else {
				require.Nil(t, RelayMidjourneySubmit(ctx, info))
			}
			assert.Equal(t, tc.status, recorder.Code)
			if !tc.async {
				var response map[string]any
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Equal(t, float64(4), response["code"])
				assert.Equal(t, tc.want, response["description"])
				assert.Equal(t, tc.want, response["message"])
			}
			var mj model.Midjourney
			require.NoError(t, db.Where("mj_id = ?", name).First(&mj).Error)
			logType := model.LogTypeError
			if tc.async {
				var task model.Task
				require.NoError(t, db.Where("task_id = ?", name).First(&task).Error)
				update := dto.MidjourneyDto{Status: "FAILURE", FailReason: tc.reason, Progress: "100%"}
				require.True(t, service.ApplyMidjourneyUpdateToAsyncTask(&task, update))
				require.NoError(t, task.Update())
				assert.Equal(t, tc.want, TaskModel2DtoWithOptions(&task, false, false).FailReason)
				assert.Equal(t, tc.reason, TaskModel2DtoWithOptions(&task, false, true).FailReason)
				require.NoError(t, service.RefundTaskQuota(ctx, &task, "构图失败"))
				mj.Status, mj.FailReason = "FAILURE", tc.reason
				require.NoError(t, mj.Update())
				logType = model.LogTypeRefund
			}
			assert.Equal(t, tc.reason, mj.FailReason, "administrator diagnostics remain intact")
			assert.Equal(t, "FAILURE", mj.Status)
			assert.Equal(t, tc.want, mj.UserFailReason(), "drawing log diagnostic")
			legacy := mj
			legacy.Status = ""
			assert.Equal(t, tc.want, legacy.UserFailReason(), "historical rejected submissions without a status")
			fetchRecorder := httptest.NewRecorder()
			fetch, _ := gin.CreateTestContext(fetchRecorder)
			fetch.Request = httptest.NewRequest(http.MethodGet, "/mj/task/"+name+"/fetch", nil)
			fetch.Params = gin.Params{{Key: "id", Value: name}}
			fetch.Set("id", user.Id)
			require.Nil(t, RelayMidjourneyTask(fetch, relayconstant.RelayModeMidjourneyTaskFetch))
			var fetched dto.MidjourneyDto
			require.NoError(t, common.Unmarshal(fetchRecorder.Body.Bytes(), &fetched))
			assert.Equal(t, tc.want, fetched.FailReason)
			assert.Equal(t, tc.want, fetched.Description)
			logs, total, err := model.GetUserLogs(user.Id, logType, 0, 0, "", "", 0, 10, "", "", "")
			require.NoError(t, err)
			require.Equal(t, int64(1), total)
			require.Len(t, logs, 1)
			assert.Equal(t, tc.want, logs[0].Content)
			assert.NotContains(t, logs[0].Other, "admin_info")
			var stored model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", user.Id, logType).First(&stored).Error)
			var metadata struct {
				AdminInfo struct {
					Reason string `json:"midjourney_error"`
				} `json:"admin_info"`
			}
			require.NoError(t, common.UnmarshalJsonStr(stored.Other, &metadata))
			assert.Equal(t, tc.reason, metadata.AdminInfo.Reason)
		})
	}
}

func TestMidjourneyExhaustedTokenResponse(t *testing.T) {
	db := setupMidjourneyQuotaDB(t)
	for index, tc := range []struct {
		name                                    string
		status, remain                          int
		expires                                 int64
		unlimited, missing, deleted, otherRoute bool
		wantStatus                              int
	}{
		{name: "zero", status: common.TokenStatusEnabled, expires: -1, wantStatus: http.StatusPaymentRequired},
		{name: "negative legacy balance", status: common.TokenStatusEnabled, remain: -100, expires: -1, wantStatus: http.StatusPaymentRequired},
		{name: "already exhausted", status: common.TokenStatusExhausted, expires: -1, wantStatus: http.StatusPaymentRequired},
		{name: "disabled", status: common.TokenStatusDisabled, expires: -1, wantStatus: http.StatusUnauthorized},
		{name: "expired", status: common.TokenStatusEnabled, expires: 1, wantStatus: http.StatusUnauthorized},
		{name: "unlimited disabled", status: common.TokenStatusDisabled, unlimited: true, expires: -1, wantStatus: http.StatusUnauthorized},
		{name: "unknown", missing: true, wantStatus: http.StatusUnauthorized},
		{name: "deleted", status: common.TokenStatusEnabled, expires: -1, deleted: true, wantStatus: http.StatusUnauthorized},
		{name: "other API reports quota", status: common.TokenStatusEnabled, expires: -1, otherRoute: true, wantStatus: http.StatusPaymentRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := &model.Token{Key: fmt.Sprintf("mjAuthTest%d", index), Status: tc.status, RemainQuota: tc.remain, ExpiredTime: tc.expires, UnlimitedQuota: tc.unlimited}
			if !tc.missing {
				require.NoError(t, db.Create(token).Error)
				if tc.deleted {
					require.NoError(t, db.Delete(token).Error)
				}
			}
			var downstreamCalls int
			router := gin.New()
			router.Use(middleware.TokenAuth())
			handler := func(c *gin.Context) { downstreamCalls++; c.Status(http.StatusNoContent) }
			router.POST("/mj/submit/imagine", handler)
			router.POST("/:mode/mj/submit/imagine", handler)
			router.POST("/v1/images/generations", handler)
			for _, path := range []string{"/mj/submit/imagine", "/fast/mj/submit/imagine"} {
				if tc.otherRoute {
					path = "/v1/images/generations"
				}
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"prompt":"cat"}`))
				request.Header.Set("Authorization", "Bearer sk-"+token.Key)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				assert.Equal(t, tc.wantStatus, recorder.Code)
				assert.NotContains(t, recorder.Body.String(), token.Key)
				if tc.wantStatus == http.StatusPaymentRequired {
					assert.Equal(t, "insufficient_token_quota", recorder.Header().Get("X-Error-Code"))
					if tc.otherRoute {
						assert.Contains(t, recorder.Body.String(), `"code":"insufficient_token_quota"`)
					} else {
						var response dto.MidjourneyResponse
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
						assert.Equal(t, 4, response.Code)
						assert.Contains(t, response.Description, "[insufficient_token_quota]")
					}
				} else {
					assert.NotContains(t, recorder.Body.String(), "令牌配额不足")
				}
			}
			assert.Zero(t, downstreamCalls, "rejected credentials must never reach the relay")
		})
	}
}
