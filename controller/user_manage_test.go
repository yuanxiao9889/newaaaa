package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func performVIPRequest(t *testing.T, role int, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.PUT("/api/user/:id/vip", func(c *gin.Context) {
		c.Set("id", 9999)
		c.Set("role", role)
		c.Set("username", "root-operator")
		UpdateUserVIP(c)
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/user/"+target+"/vip", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestUserVIPChangesOnlyMembershipAndRecordsAudit(t *testing.T) {
	db := setupManageUserTestDB(t)
	createQuotaTestOperator(t, db, common.RoleRootUser)
	user := model.User{Username: "vip-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: "default", Quota: 1000, AffQuota: 200, AffHistoryQuota: 300, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{SID: "vip-session", UserID: user.Id, Version: 1,
		UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "vip-refresh",
		LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}).Error)
	for _, enabled := range []bool{true, false} {
		response := performVIPRequest(t, common.RoleAdminUser, strconv.Itoa(user.Id), fmt.Sprintf(`{"is_vip":%t}`, enabled))
		require.Contains(t, response.Body.String(), `"success":true`)
		profile, err := model.GetSelfUserById(user.Id)
		require.NoError(t, err)
		assert.Equal(t, enabled, buildSelfUserData(profile)["is_vip"])
		assert.Equal(t, common.RoleCommonUser, profile.Role)
		assert.Equal(t, common.UserStatusEnabled, profile.Status)
		assert.Equal(t, "default", profile.Group)
		assert.Equal(t, 1000, profile.Quota)
		assert.Equal(t, 200, profile.AffQuota)
		assert.Equal(t, 300, profile.AffHistoryQuota)
		assert.EqualValues(t, 1, profile.AuthVersion)
	}
	var session model.UserSession
	require.NoError(t, db.First(&session, "sid = ?", "vip-session").Error)
	assert.Equal(t, model.UserSessionStatusActive, session.Status)
	var audits []model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "user.vip_update").Order("id").Find(&audits).Error)
	require.Len(t, audits, 2)
	for i, audit := range audits {
		assert.Equal(t, 9999, audit.UserId)
		other := audit.Other
		require.NotNil(t, other.Op)
		params, err := common.Marshal(other.Op.Params)
		require.NoError(t, err)
		assert.JSONEq(t, fmt.Sprintf(`{"target_user_id":%d,"previous_is_vip":%t,"is_vip":%t}`, user.Id, i == 1, i == 0), string(params))
	}
}

func TestUserVIPRejectsInvalidOrUnauthorizedChanges(t *testing.T) {
	db := setupManageUserTestDB(t)
	for _, user := range []model.User{
		{Id: 1, Username: "vip-common", Role: common.RoleCommonUser, AffCode: "vip-common"},
		{Id: 2, Username: "vip-admin", Role: common.RoleAdminUser, AffCode: "vip-admin"},
		{Id: 3, Username: "vip-root", Role: common.RoleRootUser, AffCode: "vip-root"},
		{Id: 4, Username: "vip-deleted", Role: common.RoleCommonUser, AffCode: "vip-deleted"},
	} {
		require.NoError(t, db.Create(&user).Error)
	}
	require.NoError(t, db.Delete(&model.User{}, 4).Error)
	for _, tc := range []struct {
		name     string
		role     int
		id, body string
	}{
		{"ordinary_user", common.RoleCommonUser, "1", `{"is_vip":true}`},
		{"admin_peer", common.RoleAdminUser, "2", `{"is_vip":true}`},
		{"admin_root", common.RoleAdminUser, "3", `{"is_vip":true}`},
		{"deleted", common.RoleRootUser, "4", `{"is_vip":true}`},
		{"missing_user", common.RoleRootUser, "12345", `{"is_vip":true}`},
		{"zero_id", common.RoleRootUser, "0", `{"is_vip":true}`},
		{"negative_id", common.RoleRootUser, "-1", `{"is_vip":true}`},
		{"invalid_id", common.RoleRootUser, "invalid", `{"is_vip":true}`},
		{"missing_flag", common.RoleRootUser, "1", `{}`},
		{"null_flag", common.RoleRootUser, "1", `{"is_vip":null}`},
		{"string_flag", common.RoleRootUser, "1", `{"is_vip":"true"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performVIPRequest(t, tc.role, tc.id, tc.body)
			assert.NotContains(t, response.Body.String(), `"success":true`)
		})
	}
	var count int64
	require.NoError(t, db.Model(&model.User{}).Where("is_vip = ?", true).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUserVIPRouteRequiresAuthenticatedAdministrator(t *testing.T) {
	db := setupManageUserTestDB(t)
	previousSecret := common.SessionSecret
	common.SessionSecret = "vip-route-test-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	member := model.User{Username: "vip-route-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "vip-route-member"}
	require.NoError(t, db.Create(&member).Error)
	admin := model.User{Username: "vip-route-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "vip-route-admin"}
	require.NoError(t, db.Create(&admin).Error)
	router := gin.New()
	router.PUT("/api/user/:id/vip", middleware.AdminAuth(), UpdateUserVIP)
	for _, tc := range []struct {
		name       string
		id, status int
	}{
		{"anonymous", 0, http.StatusUnauthorized},
		{"member", member.Id, http.StatusForbidden},
		{"administrator", admin.Id, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/user/%d/vip", member.Id), strings.NewReader(`{"is_vip":true}`))
			req.Header.Set("Content-Type", "application/json")
			if tc.id != 0 {
				session, err := service.CreateLoginSession(tc.id, "password", "127.0.0.1", "vip-test")
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+session.AccessToken)
				req.Header.Set("X-Auth-Session", session.Session.SID)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
	require.NoError(t, db.First(&member, member.Id).Error)
	assert.True(t, member.IsVIP)
}

func TestUserVIPCannotBeChangedThroughProfileUpdates(t *testing.T) {
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_vip_%t", enabled), func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, authz.Init(db))
			user := model.User{Username: "vip-profile", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
				Group: "default", IsVIP: enabled, AuthVersion: 1}
			require.NoError(t, db.Create(&user).Error)
			for _, handler := range []gin.HandlerFunc{UpdateSelf, UpdateUser} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", user.Id)
				c.Set("role", common.RoleRootUser)
				c.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(fmt.Sprintf(
					`{"id":%d,"username":"vip-profile","display_name":"Updated profile","group":"default","is_vip":%t}`, user.Id, !enabled)))
				handler(c)
				require.Contains(t, recorder.Body.String(), `"success":true`)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, enabled, user.IsVIP)
				assert.Equal(t, "Updated profile", user.DisplayName)
			}
		})
	}
}

func TestVIPReferralLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		membership []bool
		want       int
		wantQuota  int
	}{
		{"ordinary_inviter_vip_invitee", []bool{false, false, false, false}, 3, 1500},
		{"vip_inviter", []bool{true, true, true, true, true, true}, 6, 6000},
		{"upgrade_after_limit_and_cancel", []bool{false, false, false, false, true, true, false}, 5, 3500},
		{"cancel_before_limit", []bool{true, true, false, false}, 3, 2500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.AffiliateRewardRecord{}))
			oldQuotaPerUnit, oldBatch := common.QuotaPerUnit, common.BatchUpdateEnabled
			common.QuotaPerUnit, common.BatchUpdateEnabled = 1000, false
			t.Cleanup(func() { common.QuotaPerUnit, common.BatchUpdateEnabled = oldQuotaPerUnit, oldBatch })
			inviter := model.User{Username: "lifecycle-inviter", Role: common.RoleCommonUser, AffCode: "lifecycle-inviter"}
			require.NoError(t, db.Create(&inviter).Error)
			invitee := model.User{Username: "lifecycle-invitee", Role: common.RoleCommonUser, AffCode: "lifecycle-invitee", InviterId: inviter.Id, IsVIP: true}
			require.NoError(t, db.Create(&invitee).Error)
			for i, vip := range tc.membership {
				order := model.TopUp{UserId: invitee.Id, TradeNo: fmt.Sprintf("vip-lifecycle-%d", i), Amount: 20, Money: 10,
					PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending}
				require.NoError(t, db.Create(&order).Error)
				_, err := model.SetUserVIP(inviter.Id, common.RoleRootUser, vip)
				require.NoError(t, err)
				done, err := model.RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
				require.NoError(t, err)
				assert.False(t, done)
			}
			// Enabling membership and replaying all callbacks never backfills
			// skipped orders or duplicates a previously granted reward.
			_, err := model.SetUserVIP(inviter.Id, common.RoleRootUser, true)
			require.NoError(t, err)
			for i := range tc.membership {
				done, err := model.RechargeEpay(fmt.Sprintf("vip-lifecycle-%d", i), "alipay", "127.0.0.1")
				require.NoError(t, err)
				assert.True(t, done)
			}
			require.NoError(t, db.First(&inviter, inviter.Id).Error)
			assert.Equal(t, tc.wantQuota, inviter.AffQuota)
			assert.Equal(t, tc.wantQuota, inviter.AffHistoryQuota)
			require.NoError(t, db.First(&invitee, invitee.Id).Error)
			assert.Equal(t, len(tc.membership)*20000, invitee.Quota)
			var count int64
			require.NoError(t, db.Model(&model.AffiliateRewardRecord{}).Count(&count).Error)
			assert.EqualValues(t, tc.want, count)
		})
	}
}

// The persisted user schema in v1.0.0-rc.37 differs from the existing rc.33
// fixture only by access_token_created_at (verified against the release source).
type releasedVIPUser struct {
	User                 releasedAuditUser `gorm:"embedded"`
	AccessTokenCreatedAt *int64            `gorm:"type:bigint;column:access_token_created_at"`
}

func (releasedVIPUser) TableName() string { return "users" }

func TestUserVIPMigration(t *testing.T) {
	dialect := os.Getenv("TEST_MANAGE_USER_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade_rc37_%t", upgrade), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, dialect, os.Getenv("TEST_"+strings.ToUpper(dialect)+"_DSN"))
			if upgrade {
				require.NoError(t, db.AutoMigrate(&releasedVIPUser{}))
				require.NoError(t, db.Create(&releasedVIPUser{User: releasedAuditUser{
					Id: 1, Username: "legacy-vip", Password: "retained-password-hash", Group: "default",
					Quota: 1000, AffQuota: 200, AffHistoryQuota: 300, AffCount: 2, AffCode: "legacy-aff", AuthVersion: 4,
				}}).Error)
			}
			require.NoError(t, db.AutoMigrate(&model.User{}))
			if !upgrade {
				require.NoError(t, db.Create(&model.User{Id: 1, Username: "legacy-vip", Password: "retained-password-hash", Group: "default",
					Quota: 1000, AffQuota: 200, AffHistoryQuota: 300, AffCount: 2, AffCode: "legacy-aff", AuthVersion: 4}).Error)
			}
			for _, enabled := range []bool{false, true} {
				if enabled {
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("is_vip", true).Error)
				}
				require.NoError(t, db.AutoMigrate(&model.User{}))
				var user model.User
				require.NoError(t, db.First(&user, 1).Error)
				assert.Equal(t, enabled, user.IsVIP)
				assert.Equal(t, "retained-password-hash", user.Password)
				assert.Equal(t, "default", user.Group)
				assert.Equal(t, 1000, user.Quota)
				assert.Equal(t, 200, user.AffQuota)
				assert.Equal(t, 300, user.AffHistoryQuota)
				assert.Equal(t, 2, user.AffCount)
				assert.EqualValues(t, 4, user.AuthVersion)
			}
			for _, duplicate := range []model.User{
				{Username: "legacy-vip", AffCode: "different-aff"},
				{Username: "different-user", AffCode: "legacy-aff"},
			} {
				assert.Error(t, db.Create(&duplicate).Error, "existing uniqueness guarantees must survive migration")
			}
			assert.True(t, db.Migrator().HasIndex(&model.User{}, "idx_users_inviter_id"))
		})
	}
}

func TestVIPReferralConcurrentSettlement(t *testing.T) {
	for _, vip := range []bool{false, true} {
		t.Run(fmt.Sprintf("vip_%t", vip), func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.AffiliateRewardRecord{}))
			if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
				connection, err := db.DB()
				require.NoError(t, err)
				connection.SetMaxOpenConns(1)
			}
			previous := common.QuotaPerUnit
			common.QuotaPerUnit = 1000
			t.Cleanup(func() { common.QuotaPerUnit = previous })
			inviter := model.User{Username: "concurrent-inviter", AffCode: "concurrent-inviter", IsVIP: vip}
			require.NoError(t, db.Create(&inviter).Error)
			invitee := model.User{Username: "concurrent-invitee", AffCode: "concurrent-invitee", InviterId: inviter.Id}
			require.NoError(t, db.Create(&invitee).Error)
			for i := range 4 {
				order := model.TopUp{UserId: invitee.Id, TradeNo: fmt.Sprintf("vip-concurrent-%d", i), Amount: 10, Money: 10,
					PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending}
				require.NoError(t, db.Create(&order).Error)
				if i < 2 {
					_, err := model.RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
					require.NoError(t, err)
				}
			}
			start := make(chan struct{})
			results := make(chan error, 4)
			var workers sync.WaitGroup
			// Two distinct orders, each with a duplicate callback.
			for _, tradeNo := range []string{"vip-concurrent-2", "vip-concurrent-2", "vip-concurrent-3", "vip-concurrent-3"} {
				workers.Go(func() {
					<-start
					_, err := model.RechargeEpay(tradeNo, "alipay", "127.0.0.1")
					results <- err
				})
			}
			close(start)
			workers.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			want := 1500
			if vip {
				want = 4000
			}
			require.NoError(t, db.First(&inviter, inviter.Id).Error)
			assert.Equal(t, want, inviter.AffQuota)
			assert.Equal(t, want, inviter.AffHistoryQuota)
			require.NoError(t, db.First(&invitee, invitee.Id).Error)
			assert.Equal(t, 40000, invitee.Quota)
		})
	}
}

func TestVIPReferralFailureRollsBackRechargeAndCanRetry(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AffiliateRewardRecord{}))
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	inviter := model.User{Username: "rollback-inviter", AffCode: "rollback-inviter", IsVIP: true}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := model.User{Username: "rollback-invitee", AffCode: "rollback-invitee", InviterId: inviter.Id}
	require.NoError(t, db.Create(&invitee).Error)
	order := model.TopUp{UserId: invitee.Id, TradeNo: "vip-rollback-order", Amount: 10, Money: 10,
		PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:vip_reward_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "affiliate_reward_records" {
			tx.AddError(errors.New("reward storage unavailable"))
		}
	}))
	_, err := model.RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.Error(t, err)
	require.NoError(t, db.First(&invitee, invitee.Id).Error)
	assert.Zero(t, invitee.Quota)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	assert.Zero(t, inviter.AffQuota)
	require.NoError(t, db.First(&order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	var count int64
	require.NoError(t, db.Model(&model.AffiliateRewardRecord{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Callback().Create().Remove("test:vip_reward_failure"))
	done, err := model.RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	assert.Equal(t, 1000, inviter.AffQuota)
	require.NoError(t, db.First(&invitee, invitee.Id).Error)
	assert.Equal(t, 10000, invitee.Quota)
}

func setupManageUserTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled, previousSecret := common.RedisEnabled, common.SessionSecret
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	dialect := os.Getenv("TEST_MANAGE_USER_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}
	databaseTypes := map[string]common.DatabaseType{
		"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL,
	}
	require.Contains(t, databaseTypes, dialect)
	dsn := os.Getenv("TEST_" + strings.ToUpper(dialect) + "_DSN")
	db, _ := newAuditTestDatabase(t, dialect, dsn)
	logDB := db
	if os.Getenv("TEST_MANAGE_USER_SEPARATE_LOG_DB") == "1" {
		logDB, _ = newAuditTestDatabase(t, dialect, dsn)
	}
	model.DB, model.LOG_DB = db, logDB
	common.RedisEnabled = false
	common.SessionSecret = "manage-user-test-secret"
	common.SetDatabaseTypes(databaseTypes[dialect], databaseTypes[dialect])

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.SessionSecret = previousRedisEnabled, previousSecret
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		if logDB != db {
			sqlLogDB, err := logDB.DB()
			if err == nil {
				_ = sqlLogDB.Close()
			}
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.UserAccessToken{}, &model.TopUp{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
	versionQuery := "SELECT version()"
	if dialect == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s, separate log database: %v", dialect, version, logDB != db)
	return db
}

func performManageUserRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return performVerifiedManageUserRequest(t, body, service.AuthIdentity{UserID: 9999}, "")
}

// performVerifiedManageUserRequest drives ManageUser as the root operator from
// a dashboard login session, carrying the step-up proof status or role changes need.
func performVerifiedManageUserRequest(t *testing.T, body string, identity service.AuthIdentity, proof string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if proof != "" {
		c.Request.Header.Set("X-Security-Proof", proof)
	}
	c.Set("id", identity.UserID)
	c.Set("role", common.RoleRootUser)
	c.Set("username", "root-operator")
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Set(common.RequestIdKey, "quota-test-request")
	ManageUser(c)
	return recorder
}

// manageUserProof signs the root operator in and issues a password-method proof
// bound to one ManageUser operation.
func manageUserProof(t *testing.T, db *gorm.DB, operation service.VerificationOperation) (service.AuthIdentity, string) {
	t.Helper()
	operator := createQuotaTestOperator(t, db, common.RoleRootUser)
	require.NoError(t, model.PublishUserAuthCache(operator.Id))
	bundle, err := service.CreateLoginSession(operator.Id, "password", "127.0.0.1", "manage-user-test")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	binding, err := service.BindVerificationOperation(operation)
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, service.VerificationMethodPassword, binding)
	require.NoError(t, err)
	return identity, proof
}

func TestManageUserDisableAdvancesAuthVersionOnceAndRevokesSession(t *testing.T) {
	db := setupManageUserTestDB(t)
	now := time.Now().Unix()
	user := model.User{
		Username: "managed-disable-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.UserSession{
		SID: "managed-disable-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"disable"}`, user.Id))})
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"disable"}`, user.Id), identity, proof)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, updated.Status)
	assert.EqualValues(t, 2, updated.AuthVersion)
	var session model.UserSession
	require.NoError(t, db.First(&session, "sid = ?", "managed-disable-session").Error)
	assert.Equal(t, model.UserSessionStatusRevoked, session.Status)
}

func TestManageUserDemoteAdvancesAuthVersionAndRevokesSessionsOnce(t *testing.T) {
	db := setupManageUserTestDB(t)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))

	now := time.Now().Unix()
	user := model.User{
		Username: "managed-demote-user", Password: "password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	for _, sid := range []string{"managed-demote-session-one", "managed-demote-session-two"} {
		require.NoError(t, db.Create(&model.UserSession{
			SID: sid, UserID: user.Id, Version: 1, UserAuthVersion: 1,
			Status: model.UserSessionStatusActive, RefreshHash: "refresh-" + sid, LoginMethod: "password",
			LastActiveAt: now, ExpiresAt: now + 3600,
		}).Error)
	}

	sessionUpdateCount := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:count_demote_session_updates", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "user_sessions" {
			sessionUpdateCount++
		}
	}))

	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"demote"}`, user.Id))})
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"demote"}`, user.Id), identity, proof)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, updated.Role)
	assert.EqualValues(t, 2, updated.AuthVersion)
	var sessions []model.UserSession
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("sid asc").Find(&sessions).Error)
	require.Len(t, sessions, 2)
	for _, session := range sessions {
		assert.Equal(t, model.UserSessionStatusRevoked, session.Status)
		assert.Equal(t, "admin_demote", session.RevokedReason)
	}
	assert.Equal(t, 1, sessionUpdateCount)
}

func TestManageUserDeleteReturnsImmediatelyAndUnknownActionFails(t *testing.T) {
	db := setupManageUserTestDB(t)
	deleted := model.User{
		Username: "managed-delete-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "delete-aff",
	}
	require.NoError(t, db.Create(&deleted).Error)

	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserDelete, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, deleted.Id))})
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"delete"}`, deleted.Id), identity, proof)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var deletedCount int64
	require.NoError(t, db.Unscoped().Model(&model.User{}).Where("id = ? AND deleted_at IS NOT NULL", deleted.Id).Count(&deletedCount).Error)
	assert.EqualValues(t, 1, deletedCount)

	unchanged := model.User{
		Username: "managed-unknown-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "unknown-aff",
	}
	require.NoError(t, db.Create(&unchanged).Error)
	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"unknown"}`, unchanged.Id))
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, db.First(&unchanged, unchanged.Id).Error)
	assert.EqualValues(t, 1, unchanged.AuthVersion)
	assert.Equal(t, common.UserStatusEnabled, unchanged.Status)
}

func createQuotaTestOperator(t *testing.T, db *gorm.DB, role int) model.User {
	t.Helper()
	if role == 0 {
		role = common.RoleRootUser
	}
	operator := model.User{Id: 9999, Username: "root-operator", Password: "root-operator-hash", Role: role, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "root-operator-aff"}
	require.NoError(t, db.Create(&operator).Error)
	return operator
}

func TestManageUserQuotaRecordsTopupAndAudit(t *testing.T) {
	for _, tc := range []struct {
		name, mode, action, content string
		value, wantQuota            int
	}{
		{"add", "add", "user.quota_add", "Increased user quota by 500", 500, 1500},
		{"subtract", "subtract", "user.quota_subtract", "Decreased user quota by 500", 500, 500},
		{"override_up", "override", "user.quota_override", "Overrode user quota from 1000 to 1500", 1500, 1500},
		{"override_down", "override", "user.quota_override", "Overrode user quota from 1000 to 500", 500, 500},
		{"override_unchanged", "override", "user.quota_override", "Overrode user quota from 1000 to 1000", 1000, 1000},
		{"override_zero", "override", "user.quota_override", "Overrode user quota from 1000 to 0", 0, 0},
		{"override_negative", "override", "user.quota_override", "Overrode user quota from 1000 to -1", -1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			user := model.User{Username: "quota-owner", Role: common.RoleCommonUser, Quota: 1000, AffCode: "quota-owner-aff"}
			require.NoError(t, db.Create(&user).Error)
			createQuotaTestOperator(t, db, common.RoleRootUser)

			recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":%d}`, user.Id, tc.mode, tc.value))
			assert.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"success":true`)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, tc.wantQuota, user.Quota)

			logs, total, err := model.GetAllLogs(model.LogTypeTopup, 0, 0, "", "", "", 0, 20, 0, "", "", "")
			require.NoError(t, err)
			assert.EqualValues(t, 1, total)
			require.Len(t, logs, 1)
			assert.Equal(t, user.Id, logs[0].UserId)
			assert.Equal(t, user.Username, logs[0].Username)
			assert.Equal(t, tc.content, logs[0].Content)
			model.FormatAdminLogs(logs)
			var other model.AuditOther
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, &model.AuditAdminInfo{AdminID: 9999, AdminUsername: "root-operator", AdminRole: common.RoleRootUser, AuthMethod: "session"}, other.AdminInfo)

			logs, total, err = model.GetUserLogs(user.Id, model.LogTypeTopup, 0, 0, "", "", 0, 20, "", "", "")
			require.NoError(t, err)
			assert.EqualValues(t, 1, total)
			require.Len(t, logs, 1)
			other = model.AuditOther{}
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Nil(t, other.AdminInfo)
			require.NotNil(t, other.Op)
			assert.Equal(t, tc.action, other.Op.Action)
			params, err := common.Marshal(other.Op.Params)
			require.NoError(t, err)
			expectedParams := model.AuditFields{"target_user_id": user.Id, "target_username": user.Username, "mode": tc.mode, "requested_quota": tc.value, "from": 1000, "to": tc.wantQuota}
			if tc.mode != "override" {
				expectedParams["quota"] = tc.value
			}
			expected, err := common.Marshal(expectedParams)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(params))
			assert.Equal(t, "quota-test-request", logs[0].RequestId)
			assert.Empty(t, logs[0].Ip, "recipient logs must not disclose the administrator IP")

			logs, total, err = model.GetUserLogs(9999, model.LogTypeTopup, 0, 0, "", "", 0, 20, "", "", "")
			require.NoError(t, err)
			assert.Zero(t, total)
			assert.Empty(t, logs)
			var audits []model.AuditLog
			require.NoError(t, model.LOG_DB.Find(&audits).Error)
			require.Len(t, audits, 1)
			assert.Equal(t, 9999, audits[0].UserId)
			assert.Equal(t, "root-operator", audits[0].Username)
			assert.Equal(t, tc.action, audits[0].Action)
			assert.True(t, audits[0].Success)
			params, err = common.Marshal(audits[0].Other.Op.Params)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(params))
			assert.Equal(t, "quota-test-request", audits[0].RequestId)
		})
	}
}

func TestManageUserQuotaFailuresDoNotRecordTopup(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		value      int
		failUpdate bool
	}{
		{"zero_add", "add", 0, false},
		{"negative_subtract", "subtract", -1, false},
		{"invalid_mode", "invalid", 500, false},
		{"add_update_error", "add", 500, true},
		{"subtract_update_error", "subtract", 500, true},
		{"override_update_error", "override", 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			createQuotaTestOperator(t, db, common.RoleRootUser)
			user := model.User{Username: "quota-owner", Role: common.RoleCommonUser, Quota: 1000}
			require.NoError(t, db.Create(&user).Error)
			if tc.failUpdate {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:fail_quota_update", func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						tx.AddError(errors.New("quota update unavailable"))
					}
				}))
			}
			recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":%d}`, user.Id, tc.mode, tc.value))
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, 1000, user.Quota)
			var logCount, auditCount int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&logCount).Error)
			require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Count(&auditCount).Error)
			assert.Zero(t, logCount)
			assert.EqualValues(t, 1, auditCount)
			var audit model.AuditLog
			require.NoError(t, model.LOG_DB.First(&audit).Error)
			assert.False(t, audit.Success)
			params, err := common.Marshal(audit.Other.Op.Params)
			require.NoError(t, err)
			assert.NotContains(t, string(params), `"from"`)
			assert.NotContains(t, string(params), `"to"`)
			assert.NotContains(t, string(params), `"target_username"`)
			assert.NotContains(t, string(params), "quota update unavailable")
			reason := "invalid_parameters"
			if tc.failUpdate {
				reason = "database_error"
			}
			assert.Contains(t, string(params), `"failure_reason":"`+reason+`"`)
			assert.Contains(t, string(params), fmt.Sprintf(`"requested_quota":%d`, tc.value))
		})
	}
}

func TestManageUserQuotaLogFailureKeepsSuccessfulAdjustment(t *testing.T) {
	for _, failedTable := range []string{"logs", "audit_logs"} {
		t.Run(failedTable, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			createQuotaTestOperator(t, db, common.RoleRootUser)
			user := model.User{Username: "quota-owner", Role: common.RoleCommonUser, Quota: 1000}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, model.LOG_DB.Callback().Create().Before("gorm:create").Register("test:fail_quota_log", func(tx *gorm.DB) {
				if tx.Statement.Table == failedTable {
					tx.AddError(errors.New("quota log unavailable"))
				}
			}))
			recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":500}`, user.Id))
			assert.Contains(t, recorder.Body.String(), `"success":true`)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, 1500, user.Quota)
			var logCount, auditCount int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&logCount).Error)
			require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Count(&auditCount).Error)
			if failedTable == "logs" {
				assert.Zero(t, logCount)
				assert.EqualValues(t, 1, auditCount)
			} else {
				assert.EqualValues(t, 1, logCount)
				assert.Zero(t, auditCount)
			}
		})
	}
}

func TestManageUserQuotaTargetsAndWalletBounds(t *testing.T) {
	for _, tc := range []struct {
		name, mode, reason                                string
		before, value, targetID, targetRole, operatorRole int
		deleted, failRead                                 bool
	}{
		{name: "zero_id", mode: "add", value: 1, reason: "invalid_parameters"},
		{name: "negative_id", mode: "subtract", value: 1, targetID: -1, reason: "invalid_parameters"},
		{name: "missing", mode: "override", value: 1, targetID: 12345, reason: "target_not_found"},
		{name: "deleted", mode: "add", value: 1, targetID: 1, deleted: true, reason: "target_not_found"},
		{name: "peer_admin", mode: "add", value: 1, targetID: 1, targetRole: common.RoleAdminUser, operatorRole: common.RoleAdminUser, reason: "permission_denied"},
		{name: "higher_role", mode: "override", value: 1, targetID: 1, targetRole: common.RoleRootUser, operatorRole: common.RoleAdminUser, reason: "permission_denied"},
		{name: "read_error", mode: "add", value: 1, targetID: 1, failRead: true, reason: "database_error"},
		{name: "add_overflow", mode: "add", before: common.MaxWalletQuota, value: 1, targetID: 1, reason: "quota_limit_exceeded"},
		{name: "subtract_underflow", mode: "subtract", before: -common.MaxWalletQuota, value: 1, targetID: 1, reason: "quota_limit_exceeded"},
		{name: "oversized_add", mode: "add", value: common.MaxWalletQuota + 1, targetID: 1, reason: "quota_limit_exceeded"},
		{name: "oversized_subtract", mode: "subtract", value: common.MaxWalletQuota + 1, targetID: 1, reason: "quota_limit_exceeded"},
		{name: "oversized_override", mode: "override", value: -common.MaxWalletQuota - 1, targetID: 1, reason: "quota_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			createQuotaTestOperator(t, db, tc.operatorRole)
			user := model.User{Id: 1, Username: "private-target-name", Role: tc.targetRole, Quota: tc.before}
			require.NoError(t, db.Create(&user).Error)
			if tc.deleted {
				require.NoError(t, db.Delete(&user).Error)
			}
			if tc.failRead {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:quota_read_error", func(tx *gorm.DB) {
					if tx.Statement.Table == "users" && len(tx.Statement.Selects) == 0 {
						tx.AddError(errors.New("private database error"))
					}
				}))
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":%d}`, tc.targetID, tc.mode, tc.value)))
			role := tc.operatorRole
			if role == 0 {
				role = common.RoleRootUser
			}
			c.Set("id", 9999)
			c.Set("role", role)
			ManageUser(c)
			require.Contains(t, recorder.Body.String(), `"success":false`)
			if tc.failRead {
				require.NoError(t, db.Callback().Query().Remove("test:quota_read_error"))
			}
			require.NoError(t, db.Unscoped().First(&user, user.Id).Error)
			assert.Equal(t, tc.before, user.Quota)
			var audits []model.AuditLog
			require.NoError(t, model.LOG_DB.Find(&audits).Error)
			require.Len(t, audits, 1)
			assert.False(t, audits[0].Success)
			params, err := common.Marshal(audits[0].Other.Op.Params)
			require.NoError(t, err)
			expected, err := common.Marshal(model.AuditFields{"target_user_id": tc.targetID, "mode": tc.mode, "requested_quota": tc.value, "failure_reason": tc.reason})
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(params))
			assert.NotContains(t, audits[0].Content, user.Username)
			var count int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestManageUserQuotaMiddlewareKeepsOneOperationPerRequest(t *testing.T) {
	db := setupManageUserTestDB(t)
	pat := "quota-middleware-test-token"
	operator := model.User{Id: 9999, Username: "root-operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &pat, Quota: 1000}
	require.NoError(t, db.Create(&operator).Error)
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	router.POST("/api/user/manage", middleware.AdminAuth(), ManageUser)
	for _, tc := range []struct {
		body, action string
		success      bool
	}{
		{`{"id":9999,"action":"add_quota","mode":"add","value":100}`, "user.quota_add", true},
		{`{"id":9999,"action":"add_quota","mode":"subtract","value":0}`, "user.quota_subtract", false},
		{`{"id":9999,"action":"add_quota","mode":"invalid","value":1}`, "generic", false},
		{`{"id":`, "generic", false},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer "+pat)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`"success":%t`, tc.success))
		requestID := recorder.Header().Get(common.RequestIdKey)
		require.NotEmpty(t, requestID)
		var audits []model.AuditLog
		require.NoError(t, model.LOG_DB.Where("request_id = ?", requestID).Find(&audits).Error)
		require.Len(t, audits, 2, "one operation audit and one PAT request audit")
		for _, audit := range audits {
			assert.Equal(t, tc.success, audit.Success)
			if audit.Category != model.AuditCategoryOperation {
				continue
			}
			assert.Equal(t, tc.action, audit.Action)
			assert.Equal(t, operator.Id, audit.UserId)
			assert.Equal(t, "/api/user/manage", audit.Route)
			if tc.success {
				params, err := common.Marshal(audit.Other.Op.Params)
				require.NoError(t, err)
				assert.Contains(t, string(params), `"target_user_id":9999`)
				assert.Contains(t, string(params), `"target_username":"root-operator"`)
			}
		}
		var count int64
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("request_id = ?", requestID).Count(&count).Error)
		if tc.success {
			assert.EqualValues(t, 1, count)
		} else {
			assert.Zero(t, count)
		}
	}
	require.NoError(t, db.First(&operator, operator.Id).Error)
	assert.Equal(t, 1100, operator.Quota)
}

func TestManageUserQuotaConcurrentSnapshots(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{Username: "concurrent-quota", Quota: 1000}
	require.NoError(t, db.Create(&user).Error)
	var ready sync.WaitGroup
	ready.Add(2)
	release := make(chan struct{})
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:concurrent_quota_start", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			ready.Done()
			<-release
		}
	}))
	type result struct {
		adjustment *model.UserQuotaAdjustment
		err        error
		value      int
	}
	results := make(chan result, 2)
	for _, value := range []int{10, 20} {
		go func(value int) {
			adjustment, err := model.AdjustUserQuota(user.Id, common.RoleRootUser, "add", value)
			results <- result{adjustment, err, value}
		}(value)
	}
	ready.Wait()
	close(release)
	var committed []model.UserQuotaAdjustment
	for range 2 {
		result := <-results
		if result.err != nil {
			require.True(t, common.UsingMainDatabase(common.DatabaseTypeSQLite), "row-locking databases must serialize both adjustments: %v", result.err)
			assert.Contains(t, strings.ToLower(result.err.Error()), "locked")
			assert.Nil(t, result.adjustment)
			continue
		}
		require.NotNil(t, result.adjustment)
		assert.Equal(t, result.value, result.adjustment.After-result.adjustment.Before)
		committed = append(committed, *result.adjustment)
	}
	require.NoError(t, db.Callback().Query().Remove("test:concurrent_quota_start"))
	require.NotEmpty(t, committed)
	sort.Slice(committed, func(i, j int) bool { return committed[i].Before < committed[j].Before })
	balance := 1000
	for _, adjustment := range committed {
		assert.Equal(t, balance, adjustment.Before)
		balance = adjustment.After
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, balance, user.Quota)
}

func TestManageUserQuotaCacheUsesCommittedIntegerDifference(t *testing.T) {
	for _, tc := range []struct {
		name, mode                               string
		before, cached, value, after, wantCached int
		failUpdate, failCache, missingCache      bool
	}{
		{name: "add_preserves_reservations", mode: "add", before: 1000, cached: 900, value: 500, after: 1500, wantCached: 1400},
		{name: "subtract_preserves_reservations", mode: "subtract", before: 1000, cached: 900, value: 500, after: 500, wantCached: 400},
		{name: "override_preserves_reservations", mode: "override", before: 1000, cached: 900, value: 2000, after: 2000, wantCached: 1900},
		{name: "large_odd_difference", mode: "override", before: common.MaxWalletQuota - 1, cached: common.MaxWalletQuota - 1, value: -common.MaxWalletQuota, after: -common.MaxWalletQuota, wantCached: -common.MaxWalletQuota},
		{name: "rollback_does_not_change_cache", mode: "subtract", before: 1000, cached: 900, value: 500, after: 1000, wantCached: 900, failUpdate: true},
		{name: "cache_error_keeps_committed_change", mode: "add", before: 1000, value: 500, after: 1500, failCache: true},
		{name: "missing_cache_is_not_partially_created", mode: "add", before: 1000, value: 500, after: 1500, missingCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			operator := createQuotaTestOperator(t, db, common.RoleRootUser)
			server := miniredis.RunT(t)
			oldRDB := common.RDB
			common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			common.RedisEnabled = true
			t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = oldRDB })
			user := model.User{Username: "cached-quota", Quota: tc.before, AuthVersion: 1}
			require.NoError(t, db.Create(&user).Error)
			cache, err := model.GetUserCache(user.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.before, cache.Quota)
			_, err = model.GetUserCache(operator.Id)
			require.NoError(t, err)
			keys := server.Keys()
			var quotaKey string
			for _, key := range keys {
				if server.HGet(key, "Id") == strconv.Itoa(user.Id) && server.HGet(key, "Quota") != "" {
					quotaKey = key
					break
				}
			}
			require.NotEmpty(t, quotaKey)
			server.HSet(quotaKey, "Quota", strconv.Itoa(tc.cached))
			if tc.missingCache {
				server.Del(quotaKey)
			}
			if tc.failCache {
				server.SetError("ERR quota cache unavailable")
			}
			if tc.failUpdate {
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:cache_quota_rollback", func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						tx.AddError(errors.New("quota rollback"))
					}
				}))
			}
			recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":%d}`, user.Id, tc.mode, tc.value))
			assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`"success":%t`, !tc.failUpdate))
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, tc.after, user.Quota)
			if tc.missingCache {
				// Log username lookup may hydrate the whole user after commit.
				if server.Exists(quotaKey) {
					assert.Equal(t, strconv.Itoa(user.Id), server.HGet(quotaKey, "Id"))
					assert.NotEmpty(t, server.HGet(quotaKey, "CacheSchema"))
					assert.Equal(t, strconv.Itoa(tc.after), server.HGet(quotaKey, "Quota"))
				}
			} else if !tc.failCache {
				assert.Equal(t, strconv.Itoa(tc.wantCached), server.HGet(quotaKey, "Quota"))
			}
		})
	}
}
