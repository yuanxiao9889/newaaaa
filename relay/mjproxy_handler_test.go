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
	t.Setenv("LOG_SQL_DSN", "")
	originalMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = originalMaster
	t.Cleanup(func() {
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
	tables := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Midjourney{}, &model.Task{}}
	require.NoError(t, db.AutoMigrate(tables...))
	for _, table := range tables {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
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
