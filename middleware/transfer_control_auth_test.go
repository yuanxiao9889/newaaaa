package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	appI18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupTransferControlAuthTest(t *testing.T) {
	t.Helper()
	originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	setupDashboardAuthMiddlewareTest(t)
	require.NoError(t, appI18n.Init())
	t.Setenv("LOG_SQL_DSN", "")
	originalMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = originalMaster
	t.Cleanup(func() {
		common.SetMainDatabaseType(originalMainType)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = originalMaster
		common.SetLogDatabaseType(originalLogType)
	})
	// A deliberately unknown credential must not be printed by SQL logging.
	model.DB = model.DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}))
}

func TestTransferControlAuthRecoversQuotaOnlyAndPreservesRestrictions(t *testing.T) {
	setupTransferControlAuthTest(t)
	routes := []struct{ method, pattern, path string }{
		{http.MethodGet, "/v1/transfer/capabilities", "/v1/transfer/capabilities"},
		{http.MethodGet, "/v1/transfer/requests/:key", "/v1/transfer/requests/request123"},
		{http.MethodPost, "/v1/transfer/download-tickets", "/v1/transfer/download-tickets"},
		{http.MethodPost, "/v1/transfer/download-observations", "/v1/transfer/download-observations"},
		{http.MethodGet, "/v1/images/tasks/:task_id", "/v1/images/tasks/paid-task"},
		{http.MethodGet, "/v1/images/tasks/:task_id/content", "/v1/images/tasks/paid-task/content"},
		{http.MethodGet, "/v1/images/generations/:task_id", "/v1/images/generations/paid-task"},
		{http.MethodGet, "/v1/images/generations/:task_id/content", "/v1/images/generations/paid-task/content"},
	}
	for index, tc := range []struct {
		name                                       string
		status, remain, wantStatus                 int
		expires                                    int64
		ips, group, suffix                         string
		unlimited, missing, deleted, banned, noKey bool
	}{
		{name: "zero quota", status: common.TokenStatusEnabled, expires: -1, wantStatus: 204},
		{name: "negative quota", status: common.TokenStatusEnabled, remain: -100, expires: -1, wantStatus: 204},
		{name: "already exhausted", status: common.TokenStatusExhausted, expires: -1, wantStatus: 204},
		{name: "future expiry", status: common.TokenStatusExhausted, expires: common.GetTimestamp() + 3600, wantStatus: 204},
		{name: "positive quota", status: common.TokenStatusEnabled, remain: 1, expires: -1, wantStatus: 204},
		{name: "unlimited valid", status: common.TokenStatusEnabled, remain: -100, unlimited: true, expires: -1, wantStatus: 204},
		{name: "disabled", status: common.TokenStatusDisabled, expires: -1, wantStatus: 401},
		{name: "disabled unlimited", status: common.TokenStatusDisabled, unlimited: true, expires: -1, wantStatus: 401},
		{name: "expired time", status: common.TokenStatusEnabled, expires: 1, wantStatus: 401},
		{name: "exhausted and expired", status: common.TokenStatusExhausted, expires: 1, wantStatus: 401},
		{name: "expired status", status: common.TokenStatusExpired, expires: -1, wantStatus: 401},
		{name: "exhausted status with remaining quota", status: common.TokenStatusExhausted, remain: 10, expires: -1, wantStatus: 401},
		{name: "unknown credential", missing: true, wantStatus: 401},
		{name: "missing credential", noKey: true, status: common.TokenStatusEnabled, expires: -1, wantStatus: 401},
		{name: "deleted credential", deleted: true, status: common.TokenStatusEnabled, expires: -1, wantStatus: 401},
		{name: "banned account", banned: true, status: common.TokenStatusExhausted, expires: -1, wantStatus: 403},
		{name: "forbidden IP", ips: "198.51.100.0/24", status: common.TokenStatusExhausted, expires: -1, wantStatus: 403},
		{name: "allowed IP", ips: "203.0.113.0/24", status: common.TokenStatusExhausted, expires: -1, wantStatus: 204},
		{name: "forbidden group", group: "forbidden-fixture", status: common.TokenStatusExhausted, expires: -1, wantStatus: 403},
		{name: "channel pin requires admin", suffix: "-42", status: common.TokenStatusExhausted, expires: -1, wantStatus: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := createMiddlewarePATUser(t, fmt.Sprintf("transfer-auth-%d", index), fmt.Sprintf("unused-dashboard-token-%d", index))
			if tc.banned {
				require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			}
			token := &model.Token{
				Key: fmt.Sprintf("transferAuthFixture%d", index), UserId: user.Id, Status: tc.status,
				RemainQuota: tc.remain, UsedQuota: 37, ExpiredTime: tc.expires, UnlimitedQuota: tc.unlimited,
				AllowIps: &tc.ips, Group: tc.group, ModelLimitsEnabled: true, ModelLimits: "gpt-4o",
			}
			if !tc.missing {
				require.NoError(t, model.DB.Create(token).Error)
				if tc.deleted {
					require.NoError(t, model.DB.Delete(token).Error)
				}
			}
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			downstreamCalls := 0
			for _, route := range routes {
				auth := TransferControlAuth()
				if strings.HasPrefix(route.pattern, "/v1/images/tasks/") {
					auth = TokenOrUserAuth()
				} else if strings.HasPrefix(route.pattern, "/v1/images/generations/") {
					auth = TokenAuth()
				}
				router.Handle(route.method, route.pattern, auth, func(c *gin.Context) {
					downstreamCalls++
					assert.Equal(t, user.Id, c.GetInt("id"))
					assert.Equal(t, token.Id, c.GetInt("token_id"))
					assert.True(t, c.GetBool("token_model_limit_enabled"))
					limits, exists := c.Get("token_model_limit")
					assert.True(t, exists)
					assert.Equal(t, map[string]bool{"gpt-4o": true}, limits)
					c.Status(http.StatusNoContent)
				})
				request := httptest.NewRequest(route.method, route.path, nil)
				request.RemoteAddr = "203.0.113.5:1234"
				// An untrusted forwarded address must not bypass the IP rule.
				request.Header.Set("X-Forwarded-For", "198.51.100.5")
				if !tc.noKey {
					request.Header.Set("Authorization", "Bearer sk-"+token.Key+tc.suffix)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				assert.Equal(t, tc.wantStatus, response.Code, "%s: %s", route.path, response.Body.String())
				assert.NotContains(t, response.Body.String(), token.Key)
			}
			if tc.wantStatus == http.StatusNoContent {
				assert.Equal(t, len(routes), downstreamCalls)
			} else {
				assert.Zero(t, downstreamCalls)
			}
			if !tc.missing && !tc.deleted {
				var persisted model.Token
				require.NoError(t, model.DB.First(&persisted, token.Id).Error)
				assert.Equal(t, tc.remain, persisted.RemainQuota)
				assert.Equal(t, 37, persisted.UsedQuota)
				if tc.wantStatus == http.StatusNoContent && !tc.unlimited && tc.remain <= 0 {
					assert.Equal(t, common.TokenStatusExhausted, persisted.Status, "recovery must not re-enable generation")
				}
			}
		})
	}
}

func TestTransferControlAuthCannotAuthorizeNewWorkOrOtherMethods(t *testing.T) {
	setupTransferControlAuthTest(t)
	user := createMiddlewarePATUser(t, "transfer-auth-route-user", "unused-dashboard-token")
	token := &model.Token{Key: "transferRouteFixture", UserId: user.Id,
		Status: common.TokenStatusExhausted, ExpiredTime: -1}
	require.NoError(t, model.DB.Create(token).Error)
	for _, route := range []struct {
		method, path string
		strictAuth   bool
	}{
		{http.MethodPost, "/v1/images/generations", false},
		{http.MethodPost, "/v1/responses", false},
		{http.MethodPost, "/v1/files", false},
		{http.MethodPost, "/v1/references/upload", false},
		{http.MethodPost, "/v1/transfer/capabilities", false},
		{http.MethodPost, "/v1/transfer/requests/request123", false},
		{http.MethodGet, "/v1/transfer/download-tickets", false},
		{http.MethodGet, "/v1/transfer/download-observations", false},
		{http.MethodGet, "/other/transfer/capabilities", false},
		{http.MethodGet, "/v1/transfer/capabilities", true},
		{http.MethodPost, "/v1/images/tasks/:task_id", true},
		{http.MethodDelete, "/v1/images/tasks/:task_id/content", true},
		{http.MethodGet, "/other/images/tasks/:task_id", true},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			auth := TransferControlAuth()
			if route.strictAuth {
				auth = TokenAuth()
			}
			router := gin.New()
			router.Handle(route.method, route.path, auth, func(c *gin.Context) {
				t.Error("exhausted token reached a generation/upload or non-recovery handler")
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer sk-"+token.Key)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusPaymentRequired, response.Code)
			assert.Equal(t, "insufficient_token_quota", response.Header().Get("X-Error-Code"))
		})
	}
}
