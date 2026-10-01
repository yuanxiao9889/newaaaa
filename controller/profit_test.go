package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupProfitControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	originalRedisEnabled := common.RedisEnabled
	originalMemoryCacheEnabled := common.MemoryCacheEnabled

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	dialect := os.Getenv("TEST_PROFIT_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, dialect)
	db, _ := newAuditTestDatabase(t, dialect, os.Getenv("TEST_PROFIT_DSN"))
	common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
	t.Cleanup(func() {
		common.SetDatabaseTypes(originalMainType, originalLogType)
	})
	versionQuery := "SELECT version()"
	if dialect == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", dialect, version)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Log{},
		&model.AuditLog{},
		&model.Channel{},
		&model.Option{},
		&model.ProfitCostPrice{},
		&model.ProfitCostPriceVersion{},
		&model.UserSession{},
		&model.UserAccessToken{},
	))
	model.DB = db
	model.LOG_DB = db
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		middleware.ProfitVerificationPasswordHashOptionKey: "",
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.RedisEnabled = originalRedisEnabled
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	return db
}

func newProfitTestRouter(cookieAuth ...bool) *gin.Engine {
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("profit-controller-test"))))
	router.Use(func(c *gin.Context) {
		injectCredential := len(cookieAuth) == 0 || cookieAuth[0]
		if accessToken, ok := sessions.Default(c).Get("access_token").(string); injectCredential && ok && c.GetHeader("Authorization") == "" {
			c.Request.Header.Set("Authorization", "Bearer "+accessToken)
		}
		c.Next()
	})
	router.GET("/login/:role", func(c *gin.Context) {
		role := common.RoleAdminUser
		if c.Param("role") == "user" {
			role = common.RoleCommonUser
		} else if c.Param("role") == "root" {
			role = common.RoleRootUser
		}
		session := sessions.Default(c)
		session.Set("username", "profit-test")
		session.Set("role", role)
		session.Set("id", 1001)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		user := model.User{
			Id:          1001,
			Username:    "profit-test",
			Password:    "profit-test-password",
			Role:        role,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AffCode:     "profit-test",
			AuthVersion: 1,
		}
		if err := model.DB.Save(&user).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		loginSession := model.UserSession{
			SID:             c.DefaultQuery("sid", "profit-test-session"),
			UserID:          user.Id,
			Version:         1,
			UserAuthVersion: user.AuthVersion,
			Status:          model.UserSessionStatusActive,
			RefreshHash:     strings.Repeat("a", 64),
			LoginMethod:     "password",
			LastActiveAt:    time.Now().Unix(),
			ExpiresAt:       time.Now().Add(time.Hour).Unix(),
		}
		if err := model.DB.Save(&loginSession).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		accessToken, _, err := service.IssueAccessToken(service.AuthIdentity{
			UserID:          user.Id,
			SessionID:       loginSession.SID,
			UserAuthVersion: loginSession.UserAuthVersion,
			SessionVersion:  loginSession.Version,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		session.Set("access_token", accessToken)
		if err := session.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		c.Status(http.StatusNoContent)
	})
	router.GET("/test/profit-time/:state", middleware.AdminAuth(), func(c *gin.Context) {
		verifiedAt := time.Now().Unix()
		switch c.Param("state") {
		case "expired":
			verifiedAt -= middleware.ProfitVerificationTimeout + 1
		case "future":
			verifiedAt += middleware.ProfitVerificationTimeout
		}
		session := sessions.Default(c)
		session.Set(middleware.ProfitVerificationSessionKey, verifiedAt)
		if err := session.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/option/", middleware.RootAuth(), GetOptions)
	router.PUT("/api/option/", middleware.RootAuth(), UpdateOption)
	profitRoute := router.Group("/api/profit")
	profitRoute.Use(middleware.AdminAuth())
	{
		profitRoute.GET("/password/status", GetProfitPasswordStatus)
		profitRoute.POST("/password", SetProfitPassword)
		profitRoute.POST("/verify", VerifyProfitPassword)
		protectedRoute := profitRoute.Group("/")
		protectedRoute.Use(middleware.ProfitVerificationRequired())
		{
			protectedRoute.GET("/summary", GetProfitSummary)
		}
	}
	return router
}

func loginProfitTestUser(t *testing.T, router *gin.Engine, role string, query string) []*http.Cookie {
	t.Helper()

	loginRecorder := httptest.NewRecorder()
	loginPath := "/login/" + role
	if query != "" {
		loginPath += "?" + query
	}
	loginRequest := httptest.NewRequest(http.MethodGet, loginPath, nil)
	router.ServeHTTP(loginRecorder, loginRequest)
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)
	cookies := loginRecorder.Result().Cookies()
	if role != "user" && (strings.Contains(query, "verified=true") || strings.Contains(query, "expired=true")) {
		verified := performProfitPost(t, router, "/api/profit/verify", `{"password":"profit-secret"}`, cookies)
		require.Equal(t, http.StatusOK, verified.Code)
		require.Contains(t, verified.Body.String(), `"success":true`)
		cookies = verified.Result().Cookies()
	}
	if strings.Contains(query, "expired=true") {
		changed := performProfitRequest(t, router, http.MethodGet, "/test/profit-time/expired", "", cookies)
		require.Equal(t, http.StatusNoContent, changed.Code)
		cookies = changed.Result().Cookies()
	}
	return cookies
}

func performProfitSummaryRequest(t *testing.T, router *gin.Engine, role string, query string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/profit/summary", nil)
	request.Header.Set("New-Api-User", "1001")
	for _, cookie := range loginProfitTestUser(t, router, role, query) {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func setProfitTestPassword(t *testing.T, password string) {
	t.Helper()
	hash, err := common.Password2Hash(password)
	require.NoError(t, err)
	previous, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	updated, err := model.CompareAndSetProfitPasswordHash(previous, hash)
	require.NoError(t, err)
	require.True(t, updated)
}

func performProfitPost(t *testing.T, router *gin.Engine, path string, body string, cookies []*http.Cookie, accessToken ...string) *httptest.ResponseRecorder {
	t.Helper()
	return performProfitRequest(t, router, http.MethodPost, path, body, cookies, accessToken...)
}

func performProfitRequest(t *testing.T, router *gin.Engine, method string, path string, body string, cookies []*http.Cookie, accessToken ...string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", "1001")
	if len(accessToken) > 0 {
		request.Header.Set("Authorization", "Bearer "+accessToken[0])
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestProfitRouteRequiresProfitVerification(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")

	recorder := performProfitSummaryRequest(t, router, "admin", "")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
}

func TestProfitRouteReportsPasswordNotConfigured(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()

	recorder := performProfitSummaryRequest(t, router, "admin", "")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PROFIT_PASSWORD_NOT_CONFIGURED")
}

func TestProfitRouteRejectsNonAdmin(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")

	recorder := performProfitSummaryRequest(t, router, "user", "verified=true")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestProfitRouteAllowsProfitVerifiedAdmin(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")

	recorder := performProfitSummaryRequest(t, router, "admin", "verified=true")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Contains(t, recorder.Body.String(), `"currency":"USD"`)
}

func TestProfitVerificationWrongPasswordDoesNotUnlock(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	cookies := loginProfitTestUser(t, router, "admin", "")

	verifyRecorder := performProfitPost(t, router, "/api/profit/verify", `{"password":"wrong"}`, cookies)
	require.Equal(t, http.StatusOK, verifyRecorder.Code)
	require.Contains(t, verifyRecorder.Body.String(), `"success":false`)

	summaryRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/profit/summary", nil)
	request.Header.Set("New-Api-User", "1001")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(summaryRecorder, request)
	require.Equal(t, http.StatusForbidden, summaryRecorder.Code)
	require.Contains(t, summaryRecorder.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
}

func TestProfitVerificationCorrectPasswordUnlocksSummary(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	cookies := loginProfitTestUser(t, router, "admin", "")

	verifyRecorder := performProfitPost(t, router, "/api/profit/verify", `{"password":"profit-secret"}`, cookies)
	require.Equal(t, http.StatusOK, verifyRecorder.Code)
	require.Contains(t, verifyRecorder.Body.String(), `"success":true`)

	summaryRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/profit/summary", nil)
	request.Header.Set("New-Api-User", "1001")
	for _, cookie := range verifyRecorder.Result().Cookies() {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(summaryRecorder, request)
	require.Equal(t, http.StatusOK, summaryRecorder.Code)
	require.Contains(t, summaryRecorder.Body.String(), `"currency":"USD"`)
}

func TestProfitVerificationExpiredSessionIsRejected(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")

	recorder := performProfitSummaryRequest(t, router, "admin", "expired=true")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PROFIT_VERIFICATION_EXPIRED")
}

func TestProfitPasswordStoresHashOnly(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	cookies := loginProfitTestUser(t, router, "admin", "")

	recorder := performProfitPost(t, router, "/api/profit/password", `{"password":"new-profit-secret"}`, cookies)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":true`)

	common.OptionMapRWMutex.RLock()
	hash := common.OptionMap[middleware.ProfitVerificationPasswordHashOptionKey]
	common.OptionMapRWMutex.RUnlock()
	require.NotEmpty(t, hash)
	require.NotEqual(t, "new-profit-secret", hash)
	require.True(t, common.ValidatePasswordAndHash("new-profit-secret", hash))
	summary := performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", recorder.Result().Cookies())
	assert.Equal(t, http.StatusOK, summary.Code)
}

func TestProfitPasswordReplacementRequiresVerification(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	cookies := loginProfitTestUser(t, router, "admin", "")
	before, err := model.GetProfitPasswordHash()
	require.NoError(t, err)

	recorder := performProfitPost(t, router, "/api/profit/password", `{"password":"replacement-secret"}`, cookies)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
	after, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProfitPasswordRotationInvalidatesPriorGrants(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	oldHash, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	oldCookies := loginProfitTestUser(t, router, "admin", "verified=true")
	otherCookies := loginProfitTestUser(t, router, "admin", "verified=true&sid=other-browser")

	rotated := performProfitPost(t, router, "/api/profit/password", `{"password":"replacement-secret"}`, oldCookies)
	require.Equal(t, http.StatusOK, rotated.Code)
	require.Contains(t, rotated.Body.String(), `"success":true`)
	currentCookies := rotated.Result().Cookies()

	// Model an old node's cache; grants must use the current database credential.
	common.OptionMapRWMutex.Lock()
	common.OptionMap[model.ProfitVerificationPasswordHashOptionKey] = oldHash
	common.OptionMapRWMutex.Unlock()
	for _, cookies := range [][]*http.Cookie{oldCookies, otherCookies} {
		summary := performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", cookies)
		assert.Equal(t, http.StatusForbidden, summary.Code)
		assert.Contains(t, summary.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
		replay := performProfitPost(t, router, "/api/profit/password", `{"password":"replayed-secret"}`, cookies)
		assert.Equal(t, http.StatusForbidden, replay.Code)
	}
	summary := performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", currentCookies)
	assert.Equal(t, http.StatusOK, summary.Code)
	oldPassword := performProfitPost(t, router, "/api/profit/verify", `{"password":"profit-secret"}`, currentCookies)
	assert.Contains(t, oldPassword.Body.String(), `"success":false`)

	var logs []model.AuditLog
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.NotEmpty(t, logs)
	encoded, err := common.Marshal(logs)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "replacement-secret")
	assert.NotContains(t, string(encoded), oldHash)
}

func TestProfitVerificationRejectsFutureAndLegacyGrants(t *testing.T) {
	for _, state := range []string{"future", "legacy"} {
		t.Run(state, func(t *testing.T) {
			setupProfitControllerTestDB(t)
			router := newProfitTestRouter()
			setProfitTestPassword(t, "profit-secret")
			query := ""
			if state == "future" {
				query = "verified=true"
			}
			cookies := loginProfitTestUser(t, router, "admin", query)
			changed := performProfitRequest(t, router, http.MethodGet, "/test/profit-time/"+state, "", cookies)
			require.Equal(t, http.StatusNoContent, changed.Code)
			summary := performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", changed.Result().Cookies())
			assert.Equal(t, http.StatusForbidden, summary.Code)
			assert.Contains(t, summary.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
		})
	}
}

func TestProfitVerificationCannotMoveToAnotherCredential(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	verifiedCookies := loginProfitTestUser(t, router, "admin", "verified=true")
	loginProfitTestUser(t, router, "admin", "sid=other-browser")
	otherSessionToken, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID: 1001, SessionID: "other-browser", UserAuthVersion: 1, SessionVersion: 1,
	})
	require.NoError(t, err)
	summary := performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", verifiedCookies, otherSessionToken)
	assert.Equal(t, http.StatusForbidden, summary.Code)
	assert.Contains(t, summary.Body.String(), "PROFIT_VERIFICATION_REQUIRED")

	for _, rawToken := range []string{"nap_profit_first", "nap_profit_second"} {
		token := &model.UserAccessToken{TokenHash: model.AccessTokenFingerprint(rawToken), Name: rawToken}
		require.NoError(t, token.SetScopes([]string{"billing:read", "billing:write"}))
		require.NoError(t, model.CreateUserAccessToken(1001, token, 10))
	}
	for _, cookies := range [][]*http.Cookie{nil, verifiedCookies} {
		reset := performProfitPost(t, router, "/api/profit/password", `{"password":"bypass-secret"}`, cookies, "nap_profit_first")
		assert.Equal(t, http.StatusForbidden, reset.Code)
		assert.Contains(t, reset.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
	}
	verifiedPAT := performProfitPost(t, router, "/api/profit/verify", `{"password":"profit-secret"}`, nil, "nap_profit_first")
	require.Equal(t, http.StatusOK, verifiedPAT.Code)
	require.Contains(t, verifiedPAT.Body.String(), `"success":true`)
	patCookies := verifiedPAT.Result().Cookies()
	summary = performProfitRequest(t, router, http.MethodGet, "/api/profit/summary", "", patCookies, "nap_profit_second")
	assert.Equal(t, http.StatusForbidden, summary.Code)
	assert.Contains(t, summary.Body.String(), "PROFIT_VERIFICATION_REQUIRED")
	rotated := performProfitPost(t, router, "/api/profit/password", `{"password":"pat-replacement-secret"}`, patCookies, "nap_profit_first")
	assert.Equal(t, http.StatusOK, rotated.Code)
	assert.Contains(t, rotated.Body.String(), `"success":true`)
}

func TestProfitPasswordGenericOptionsCannotExposeOrReplaceCredential(t *testing.T) {
	setupProfitControllerTestDB(t)
	router := newProfitTestRouter()
	setProfitTestPassword(t, "profit-secret")
	cookies := loginProfitTestUser(t, router, "root", "")
	before, err := model.GetProfitPasswordHash()
	require.NoError(t, err)

	options := performProfitRequest(t, router, http.MethodGet, "/api/option/", "", cookies)
	require.Equal(t, http.StatusOK, options.Code)
	assert.NotContains(t, options.Body.String(), model.ProfitVerificationPasswordHashOptionKey)
	assert.NotContains(t, options.Body.String(), before)
	for _, key := range []string{model.ProfitVerificationPasswordHashOptionKey, "profitverificationpasswordhash", "PrófitVerificationPasswordHash"} {
		body, err := common.Marshal(gin.H{"key": key, "value": "bypass"})
		require.NoError(t, err)
		replaced := performProfitRequest(t, router, http.MethodPut, "/api/option/", string(body), cookies)
		bulkErr := model.UpdateOptionsBulk(map[string]string{key: "bypass"})
		if key != "PrófitVerificationPasswordHash" || common.UsingMainDatabase(common.DatabaseTypeMySQL) {
			assert.Contains(t, replaced.Body.String(), `"success":false`)
			assert.Error(t, bulkErr)
		}
	}
	after, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProfitPasswordCompareAndSetRejectsStaleSetupAndRotation(t *testing.T) {
	for _, existingEmptyOption := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_empty_option_%t", existingEmptyOption), func(t *testing.T) {
			db := setupProfitControllerTestDB(t)
			if existingEmptyOption {
				require.NoError(t, db.Create(&model.Option{Key: model.ProfitVerificationPasswordHashOptionKey}).Error)
			}
			updated, err := model.CompareAndSetProfitPasswordHash("", "installed-hash")
			require.NoError(t, err)
			require.True(t, updated)
			updated, err = model.CompareAndSetProfitPasswordHash("", "stale-setup-hash")
			require.NoError(t, err)
			assert.False(t, updated)
			updated, err = model.CompareAndSetProfitPasswordHash("installed-hash", "rotated-hash")
			require.NoError(t, err)
			require.True(t, updated)
			updated, err = model.CompareAndSetProfitPasswordHash("installed-hash", "stale-rotation-hash")
			require.NoError(t, err)
			assert.False(t, updated)
			current, err := model.GetProfitPasswordHash()
			require.NoError(t, err)
			assert.Equal(t, "rotated-hash", current)
		})
	}
}

func TestProfitPasswordPolicyPreservesLegacyVerificationAndUnicode(t *testing.T) {
	setupProfitControllerTestDB(t)
	t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "argon2id")
	router := newProfitTestRouter()
	setProfitTestPassword(t, "short")
	cookies := loginProfitTestUser(t, router, "admin", "")
	verified := performProfitPost(t, router, "/api/profit/verify", `{"password":"short"}`, cookies)
	require.Equal(t, http.StatusOK, verified.Code)
	require.Contains(t, verified.Body.String(), `"success":true`)
	cookies = verified.Result().Cookies()
	before, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	for _, password := range []string{"", "short", strings.Repeat("a", common.MaxAccountPasswordLength+1)} {
		body, err := common.Marshal(gin.H{"password": password})
		require.NoError(t, err)
		rejected := performProfitPost(t, router, "/api/profit/password", string(body), cookies)
		assert.Contains(t, rejected.Body.String(), `"success":false`)
		after, err := model.GetProfitPasswordHash()
		require.NoError(t, err)
		assert.Equal(t, before, after)
	}

	password := " " + strings.Repeat("密", 64) + " "
	body, err := common.Marshal(gin.H{"password": password})
	require.NoError(t, err)
	rotated := performProfitPost(t, router, "/api/profit/password", string(body), cookies)
	require.Contains(t, rotated.Body.String(), `"success":true`)
	hash, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"))
	assert.True(t, common.ValidatePasswordAndHash(password, hash))
	assert.False(t, common.ValidatePasswordAndHash(strings.TrimSpace(password), hash))
	verified = performProfitPost(t, router, "/api/profit/verify", string(body), rotated.Result().Cookies())
	assert.Contains(t, verified.Body.String(), `"success":true`)
}

func TestProfitCookieAloneCannotAuthenticateOrChangePassword(t *testing.T) {
	setupProfitControllerTestDB(t)
	// Production AdminAuth requires an explicit bearer credential; only the
	// other test fixtures copy that credential out of their login cookie.
	router := newProfitTestRouter(false)
	cookies := loginProfitTestUser(t, router, "admin", "")
	for _, path := range []string{"/api/profit/password", "/api/profit/verify"} {
		response := performProfitPost(t, router, path, `{"password":"csrf-password"}`, cookies)
		assert.Equal(t, http.StatusUnauthorized, response.Code)
	}
	hash, err := model.GetProfitPasswordHash()
	require.NoError(t, err)
	assert.Empty(t, hash)
}
