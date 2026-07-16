package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupProfitControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalSQLite := common.UsingSQLite
	originalMySQL := common.UsingMySQL
	originalPostgreSQL := common.UsingPostgreSQL
	originalRedisEnabled := common.RedisEnabled

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Log{},
		&model.Channel{},
		&model.Option{},
		&model.ProfitCostPrice{},
		&model.ProfitCostPriceVersion{},
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
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.UsingSQLite = originalSQLite
		common.UsingMySQL = originalMySQL
		common.UsingPostgreSQL = originalPostgreSQL
		common.RedisEnabled = originalRedisEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	return db
}

func newProfitTestRouter() *gin.Engine {
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("profit-controller-test"))))
	router.GET("/login/:role", func(c *gin.Context) {
		role := common.RoleAdminUser
		if c.Param("role") == "user" {
			role = common.RoleCommonUser
		}
		session := sessions.Default(c)
		session.Set("username", "profit-test")
		session.Set("role", role)
		session.Set("id", 1001)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		if c.Query("verified") == "true" {
			session.Set(middleware.ProfitVerificationSessionKey, time.Now().Unix())
		}
		if c.Query("expired") == "true" {
			session.Set(middleware.ProfitVerificationSessionKey, time.Now().Unix()-middleware.ProfitVerificationTimeout-1)
		}
		if err := session.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		c.Status(http.StatusNoContent)
	})
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
	return loginRecorder.Result().Cookies()
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
	require.NoError(t, model.UpdateOption(middleware.ProfitVerificationPasswordHashOptionKey, hash))
}

func performProfitPost(t *testing.T, router *gin.Engine, path string, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", "1001")
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

	require.Equal(t, http.StatusOK, recorder.Code)
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
}
