package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRechargeRecordControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	previous := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.Redemption{},
		&model.SubscriptionOrder{},
		&model.RechargeRecordInvoice{},
		&model.RechargeRecordInvoiceHistory{},
		&model.RechargeRecordInvoiceRequest{}, &model.HistoricalRecharge{}, &model.RechargeImportJob{},
	))
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	return db
}

func TestSelfRechargeRecordsForcesSessionUserAndHidesAdminFields(t *testing.T) {
	db := setupRechargeRecordControllerTest(t)
	now := common.GetTimestamp()
	user := model.User{
		Username: "recharge-self-user", Password: "unused", AffCode: "recharge-self-user",
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)
	topUp := model.TopUp{
		UserId: user.Id, TradeNo: "ADMUSR-SELF-1", PaymentProvider: model.PaymentProviderAdmin,
		PaymentMethod: model.PaymentMethodOfficialWebsite, Status: common.TopUpStatusSuccess,
		Amount: 10, QuotaAmount: 5000, Money: 73, CreateTime: now - 100, CompleteTime: now - 100,
	}
	require.NoError(t, db.Create(&topUp).Error)
	require.NoError(t, db.Create(&model.RechargeRecordInvoice{
		SourceType: model.RechargeSourceTopUp, SourceID: topUp.Id, UserID: user.Id,
		Status: model.InvoiceStatusMarked, InvoiceNo: "INV-SELF-1", InvoiceDate: "2026-10-01",
		InternalNote: "internal-only", Verification: "verification-only",
		ReceiptChecked: true, Version: 1, MarkedAt: now, MarkedBy: 99,
		MarkedByName: "root-admin", SourceStatus: common.TopUpStatusSuccess,
		SourceRecordNo: topUp.TradeNo, SourceKind: model.RechargeKindAdmin,
		SourceTime: now - 100, SourceQuota: 5000, SourceSnapshot: "{}",
		CreatedAt: now, UpdatedAt: now,
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/recharge-records/self?user_id=999&page_size=10", nil)
	context.Set("id", user.Id)
	GetSelfRechargeRecords(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "ADMUSR-SELF-1")
	assert.Contains(t, body, `"user_id":`+strconv.Itoa(user.Id))
	assert.NotContains(t, body, "internal-only")
	assert.NotContains(t, body, "verification-only")
	assert.NotContains(t, body, "root-admin")
}

func TestParseRechargeRecordFilterRejectsInvalidBoundsAndScopes(t *testing.T) {
	tests := []struct {
		name  string
		query string
		ok    bool
	}{
		{name: "valid", query: "?p=1&page_size=100", ok: true},
		{name: "oversized page", query: "?page_size=101", ok: false},
		{name: "invalid page", query: "?p=0", ok: false},
		{name: "invalid source", query: "?source=card", ok: false},
		{name: "invalid status", query: "?status=refunded", ok: false},
		{name: "invalid invoice status", query: "?invoice_status=none", ok: false},
		{name: "window over 366 days", query: "?start_timestamp=100&end_timestamp=100000000", ok: false},
		{name: "start after end", query: "?start_timestamp=200&end_timestamp=100", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/api/recharge-records/self"+test.query, nil)
			filter, err := parseRechargeRecordFilter(context, 42)
			if test.ok {
				require.NoError(t, err)
				assert.Equal(t, 42, filter.UserID)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestCreateRechargeInvoiceMarkingRequiresDashboardSession(t *testing.T) {
	t.Setenv("RECHARGE_RECORD_INVOICE_WRITE_ENABLED", "true")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/recharge-records/invoice-markings", nil)
	context.Set("id", 1)
	context.Set("username", "admin")
	context.Set("use_access_token", true)
	context.Set("session_id", "")

	CreateRechargeInvoiceMarking(context)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "AUTH_SESSION_REQUIRED")
}
