package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func resetRechargeRecordTestTables(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &RechargeRecordInvoice{}, &RechargeRecordInvoiceHistory{}, &RechargeRecordInvoiceRequest{}, &HistoricalRecharge{}, &RechargeImportJob{}))
	for _, table := range []string{"historical_recharges", "recharge_import_jobs", "recharge_record_invoice_histories", "recharge_record_invoices", "recharge_record_invoice_requests", "redemptions", "top_ups", "subscription_orders", "users"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"historical_recharges", "recharge_import_jobs", "recharge_record_invoice_histories", "recharge_record_invoices", "recharge_record_invoice_requests", "redemptions", "top_ups", "subscription_orders", "users"} {
			DB.Exec("DELETE FROM " + table)
		}
	})
}

func createRechargeRecordUser(t *testing.T, username string) *User {
	t.Helper()
	user := &User{Username: username, Password: "unused", AffCode: username, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createRechargeRecordTopUp(t *testing.T, userID int, tradeNo string, provider string, status string, money float64, amount int64, quotaAmount int, created int64) *TopUp {
	t.Helper()
	topUp := &TopUp{
		UserId:          userID,
		TradeNo:         tradeNo,
		PaymentProvider: provider,
		PaymentMethod:   provider,
		Status:          status,
		Money:           money,
		Amount:          amount,
		QuotaAmount:     quotaAmount,
		CreateTime:      created,
		CompleteTime:    created,
	}
	require.NoError(t, DB.Create(topUp).Error)
	return topUp
}

func TestListRechargeRecordsSourcesSubscriptionExclusionAndFilters(t *testing.T) {
	resetRechargeRecordTestTables(t)

	now := common.GetTimestamp()
	user := createRechargeRecordUser(t, "recharge-reader")
	other := createRechargeRecordUser(t, "recharge-other")
	online := createRechargeRecordTopUp(t, user.Id, "ONLINE-REC-1", PaymentProviderEpay, common.TopUpStatusSuccess, 36.5, 5, 0, now-100)
	admin := createRechargeRecordTopUp(t, user.Id, "ADMUSR-REC-1", PaymentProviderAdmin, common.TopUpStatusSuccess, 999, 0, 5000, now-90)
	createRechargeRecordTopUp(t, user.Id, "PENDING-REC-1", PaymentProviderStripe, common.TopUpStatusPending, 10, 2, 0, now-80)
	old := createRechargeRecordTopUp(t, user.Id, "OLD-REC-1", PaymentProviderStripe, common.TopUpStatusSuccess, 10, 2, 0, now-40*24*60*60)
	subscriptionTopUp := createRechargeRecordTopUp(t, other.Id, "sub-order-1", "", common.TopUpStatusSuccess, 20, 0, 0, now-70)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		UserId: other.Id, PlanId: 1, TradeNo: subscriptionTopUp.TradeNo, Money: 20,
		Status: common.TopUpStatusSuccess, CreateTime: now - 70, CompleteTime: now - 70,
	}).Error)
	redemption := &Redemption{
		Name: "recharge-redemption", Key: "20000000000000000000000000000001",
		Status: common.RedemptionCodeStatusUsed, Quota: 800, UsedUserId: user.Id,
		RedeemedTime: now - 60, CreatedTime: now - 200,
	}
	require.NoError(t, DB.Create(redemption).Error)
	deleted := &Redemption{
		Name: "deleted-redemption", Key: "20000000000000000000000000000002",
		Status: common.RedemptionCodeStatusUsed, Quota: 800, UsedUserId: user.Id,
		RedeemedTime: now - 50, CreatedTime: now - 200,
	}
	require.NoError(t, DB.Create(deleted).Error)
	require.NoError(t, DB.Delete(deleted).Error)

	filter := RechargeRecordFilter{StartTime: now - 30*24*60*60, EndTime: now, Page: 1, PageSize: 20}
	records, total, err := ListRechargeRecords(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, records, 4)
	for _, record := range records {
		assert.NotEqual(t, old.Id, record.SourceID, "outside the default window")
		assert.NotEqual(t, subscriptionTopUp.Id, record.SourceID, "subscription top-up must be excluded")
	}

	var onlineRecord, adminRecord, redemptionRecord *RechargeRecord
	for _, record := range records {
		switch {
		case record.Source == RechargeSourceTopUp && record.SourceID == online.Id:
			onlineRecord = record
		case record.Source == RechargeSourceTopUp && record.SourceID == admin.Id:
			adminRecord = record
		case record.Source == RechargeSourceRedemption && record.SourceID == redemption.Id:
			redemptionRecord = record
		}
	}
	require.NotNil(t, onlineRecord)
	assert.Equal(t, RechargeKindOnline, onlineRecord.SourceKind)
	assert.Equal(t, 5*int(common.QuotaPerUnit), onlineRecord.CreditedQuota)
	require.NotNil(t, onlineRecord.PaidAmount)
	assert.Equal(t, 36.5, *onlineRecord.PaidAmount)
	assert.Equal(t, "CNY", onlineRecord.Currency)
	assert.True(t, onlineRecord.PaidConfirmed)
	assert.Equal(t, "payment", onlineRecord.AmountSource)
	assert.Equal(t, InvoiceStatusUnmarked, onlineRecord.InvoiceStatus)

	require.NotNil(t, adminRecord)
	assert.Equal(t, RechargeKindAdmin, adminRecord.SourceKind)
	assert.Nil(t, adminRecord.PaidAmount)
	assert.False(t, adminRecord.PaidConfirmed)
	assert.Equal(t, "quota_conversion", adminRecord.AmountSource)
	assert.True(t, adminRecord.RequiresVerify)

	require.NotNil(t, redemptionRecord)
	assert.Equal(t, RechargeKindRedemption, redemptionRecord.SourceKind)
	assert.Equal(t, "RDM-"+fmt.Sprint(redemption.Id), redemptionRecord.RecordNo)
	assert.Equal(t, 800, redemptionRecord.CreditedQuota)
	assert.Nil(t, redemptionRecord.PaidAmount)
	assert.Equal(t, RechargeStatusUsed, redemptionRecord.Status)
	assert.True(t, redemptionRecord.RequiresVerify)

	adminRecords, adminTotal, err := ListRechargeRecords(context.Background(), RechargeRecordFilter{
		StartTime: now - 30*24*60*60, EndTime: now, SourceKind: RechargeKindAdmin, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), adminTotal)
	require.Len(t, adminRecords, 1)
	assert.Equal(t, admin.Id, adminRecords[0].SourceID)

	keywordRecords, keywordTotal, err := ListRechargeRecords(context.Background(), RechargeRecordFilter{
		StartTime: now - 30*24*60*60, EndTime: now, Keyword: "RDM-" + fmt.Sprint(redemption.Id), Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), keywordTotal)
	require.Len(t, keywordRecords, 1)
	assert.Equal(t, redemption.Id, keywordRecords[0].SourceID)

	own, ownTotal, err := ListRechargeRecords(context.Background(), RechargeRecordFilter{
		UserID: user.Id, StartTime: now - 30*24*60*60, EndTime: now, Page: 1, PageSize: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), ownTotal)
	require.Len(t, own, 2)

	pageTwo, pageTwoTotal, err := ListRechargeRecords(context.Background(), RechargeRecordFilter{
		UserID: user.Id, StartTime: now - 30*24*60*60, EndTime: now, Page: 2, PageSize: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), pageTwoTotal)
	require.Len(t, pageTwo, 2)
	for _, firstPageRecord := range own {
		for _, secondPageRecord := range pageTwo {
			assert.False(t, firstPageRecord.Source == secondPageRecord.Source && firstPageRecord.SourceID == secondPageRecord.SourceID, "pages must not share the same source record")
		}
	}

	detail, err := GetRechargeRecord(context.Background(), RechargeSourceTopUp, online.Id)
	require.NoError(t, err)
	assert.Equal(t, online.TradeNo, detail.RecordNo)
}

func TestMarkRechargeInvoicesLifecycleIsolationAndRollback(t *testing.T) {
	resetRechargeRecordTestTables(t)

	now := common.GetTimestamp()
	user := createRechargeRecordUser(t, "invoice-owner")
	other := createRechargeRecordUser(t, "invoice-other")
	online := createRechargeRecordTopUp(t, user.Id, "INVOICE-ONLINE-1", PaymentProviderEpay, common.TopUpStatusSuccess, 36.5, 5, 0, now-100)
	admin := createRechargeRecordTopUp(t, user.Id, "INVOICE-ADMIN-1", PaymentProviderAdmin, common.TopUpStatusSuccess, 999, 0, 5000, now-90)
	pending := createRechargeRecordTopUp(t, user.Id, "INVOICE-PENDING-1", PaymentProviderStripe, common.TopUpStatusPending, 10, 2, 0, now-80)
	otherOnline := createRechargeRecordTopUp(t, other.Id, "INVOICE-OTHER-1", PaymentProviderStripe, common.TopUpStatusSuccess, 10, 2, 0, now-70)
	redemption := &Redemption{
		Name: "invoice-redemption", Key: "20000000000000000000000000000003",
		Status: common.RedemptionCodeStatusUsed, Quota: 800, UsedUserId: user.Id,
		RedeemedTime: now - 60, CreatedTime: now - 200,
	}
	require.NoError(t, DB.Create(redemption).Error)

	var userBefore User
	require.NoError(t, DB.First(&userBefore, user.Id).Error)
	var onlineBefore TopUp
	require.NoError(t, DB.First(&onlineBefore, online.Id).Error)
	invoiceDate := time.Now().Format("2006-01-02")

	first := InvoiceMarkingRequest{
		Action:    InvoiceActionMark,
		Records:   []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: online.Id, ExpectedVersion: 0}},
		InvoiceNo: "INV-1001", InvoiceDate: invoiceDate,
		InternalNote: "checked bank receipt", RequestID: "invoice-request-001",
		OperatorID: 900, OperatorName: "invoice-admin",
	}
	result, err := MarkRechargeInvoices(context.Background(), first)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Replayed)
	require.Len(t, result.Records, 1)
	assert.Equal(t, 1, result.Records[0].Version)

	marked, err := GetRechargeRecord(context.Background(), RechargeSourceTopUp, online.Id)
	require.NoError(t, err)
	assert.Equal(t, InvoiceStatusMarked, marked.InvoiceStatus)
	assert.Equal(t, 1, marked.InvoiceVersion)
	assert.Equal(t, "INV-1001", marked.InvoiceNo)
	assert.Equal(t, invoiceDate, marked.InvoiceDate)

	withoutConfirmation := InvoiceMarkingRequest{
		Action:    InvoiceActionMark,
		Records:   []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: admin.Id, ExpectedVersion: 0}},
		RequestID: "invoice-request-002", OperatorID: 900, OperatorName: "invoice-admin",
	}
	_, err = MarkRechargeInvoices(context.Background(), withoutConfirmation)
	require.ErrorIs(t, err, ErrInvoiceValidation)

	withConfirmation := withoutConfirmation
	withConfirmation.RequestID = "invoice-request-003"
	withConfirmation.ReceiptConfirmed = true
	withConfirmation.VerificationNote = "verified offline transfer and invoice"
	_, err = MarkRechargeInvoices(context.Background(), withConfirmation)
	require.NoError(t, err)

	redemptionMark := InvoiceMarkingRequest{
		Action:    InvoiceActionMark,
		Records:   []InvoiceMarkingRecord{{Source: RechargeSourceRedemption, ID: redemption.Id, ExpectedVersion: 0}},
		RequestID: "invoice-request-004", OperatorID: 900, OperatorName: "invoice-admin",
		ReceiptConfirmed: true, VerificationNote: "verified redemption receipt and invoice",
	}
	_, err = MarkRechargeInvoices(context.Background(), redemptionMark)
	require.NoError(t, err)

	pendingMark := InvoiceMarkingRequest{
		Action:    InvoiceActionMark,
		Records:   []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: pending.Id, ExpectedVersion: 0}},
		RequestID: "invoice-request-005", OperatorID: 900, OperatorName: "invoice-admin",
		ReceiptConfirmed: true, VerificationNote: "attempted pending order",
	}
	_, err = MarkRechargeInvoices(context.Background(), pendingMark)
	var conflict *InvoiceMarkingConflictError
	require.True(t, errors.As(err, &conflict))

	correct := InvoiceMarkingRequest{
		Action:    InvoiceActionCorrect,
		Records:   []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: online.Id, ExpectedVersion: 1}},
		InvoiceNo: "INV-1002", InvoiceDate: invoiceDate, Reason: "corrected invoice number",
		RequestID: "invoice-request-006", OperatorID: 900, OperatorName: "invoice-admin",
	}
	result, err = MarkRechargeInvoices(context.Background(), correct)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Records[0].Version)

	_, err = MarkRechargeInvoices(context.Background(), InvoiceMarkingRequest{
		Action:  InvoiceActionCorrect,
		Records: []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: online.Id, ExpectedVersion: 1}},
		Reason:  "stale correction", RequestID: "invoice-request-007", OperatorID: 900, OperatorName: "invoice-admin",
	})
	require.Error(t, err)
	require.True(t, errors.As(err, &conflict))

	revoke := InvoiceMarkingRequest{
		Action:  InvoiceActionRevoke,
		Records: []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: admin.Id, ExpectedVersion: 1}},
		Reason:  "issued in error", RequestID: "invoice-request-008", OperatorID: 900, OperatorName: "invoice-admin",
	}
	_, err = MarkRechargeInvoices(context.Background(), revoke)
	require.NoError(t, err)
	revoked, err := GetRechargeRecord(context.Background(), RechargeSourceTopUp, admin.Id)
	require.NoError(t, err)
	assert.Equal(t, InvoiceStatusRevoked, revoked.InvoiceStatus)
	assert.Empty(t, revoked.InvoiceNo)
	assert.Empty(t, revoked.InvoiceDate)

	replayed, err := MarkRechargeInvoices(context.Background(), first)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, 1, replayed.Records[0].Version)

	changedBody := first
	changedBody.InvoiceNo = "INV-OTHER"
	_, err = MarkRechargeInvoices(context.Background(), changedBody)
	require.ErrorIs(t, err, ErrInvoiceIdempotency)

	beforeRollback, err := GetRechargeRecord(context.Background(), RechargeSourceTopUp, online.Id)
	require.NoError(t, err)
	rollbackRequest := InvoiceMarkingRequest{
		Action: InvoiceActionMark,
		Records: []InvoiceMarkingRecord{
			{Source: RechargeSourceTopUp, ID: online.Id, ExpectedVersion: beforeRollback.InvoiceVersion},
			{Source: RechargeSourceTopUp, ID: pending.Id, ExpectedVersion: 0},
		},
		RequestID: "invoice-request-009", OperatorID: 900, OperatorName: "invoice-admin",
		ReceiptConfirmed: true, VerificationNote: "batch includes pending order",
	}
	_, err = MarkRechargeInvoices(context.Background(), rollbackRequest)
	require.Error(t, err)
	afterRollback, err := GetRechargeRecord(context.Background(), RechargeSourceTopUp, online.Id)
	require.NoError(t, err)
	assert.Equal(t, beforeRollback.InvoiceVersion, afterRollback.InvoiceVersion)
	assert.Equal(t, "INV-1002", afterRollback.InvoiceNo)

	_, err = MarkRechargeInvoices(context.Background(), InvoiceMarkingRequest{
		Action: InvoiceActionMark,
		Records: []InvoiceMarkingRecord{
			{Source: RechargeSourceTopUp, ID: online.Id, ExpectedVersion: afterRollback.InvoiceVersion},
			{Source: RechargeSourceTopUp, ID: otherOnline.Id, ExpectedVersion: 0},
		},
		RequestID: "invoice-request-010", OperatorID: 900, OperatorName: "invoice-admin",
	})
	require.ErrorIs(t, err, ErrInvoiceRecordMismatch)

	var userAfter User
	require.NoError(t, DB.First(&userAfter, user.Id).Error)
	assert.Equal(t, userBefore.Quota, userAfter.Quota)
	var onlineAfter TopUp
	require.NoError(t, DB.First(&onlineAfter, online.Id).Error)
	assert.Equal(t, onlineBefore.Status, onlineAfter.Status)
	assert.Equal(t, onlineBefore.QuotaAmount, onlineAfter.QuotaAmount)
	assert.Equal(t, onlineBefore.Money, onlineAfter.Money)

	var historyCount int64
	require.NoError(t, DB.Model(&RechargeRecordInvoiceHistory{}).Count(&historyCount).Error)
	assert.Equal(t, int64(5), historyCount)
	history, err := ListRechargeInvoiceHistory(context.Background(), RechargeSourceTopUp, online.Id)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, InvoiceActionCorrect, history[0].Action)
	assert.Equal(t, "corrected invoice number", history[0].Reason)
	assert.Equal(t, "INV-1001", history[1].After.InvoiceNo)
}

func TestRechargeRecordInvoiceSchemaMigratesIdempotently(t *testing.T) {
	resetRechargeRecordTestTables(t)
	require.NoError(t, DB.AutoMigrate(&RechargeRecordInvoice{}, &RechargeRecordInvoiceHistory{}, &RechargeRecordInvoiceRequest{}, &HistoricalRecharge{}, &RechargeImportJob{}))
	require.NoError(t, DB.AutoMigrate(&RechargeRecordInvoice{}, &RechargeRecordInvoiceHistory{}, &RechargeRecordInvoiceRequest{}, &HistoricalRecharge{}, &RechargeImportJob{}))
	assert.True(t, DB.Migrator().HasTable("recharge_record_invoices"))
	assert.True(t, DB.Migrator().HasTable("recharge_record_invoice_histories"))
	assert.True(t, DB.Migrator().HasTable("recharge_record_invoice_requests"))
	assert.True(t, DB.Migrator().HasIndex("recharge_record_invoices", "idx_recharge_invoice_source"))
}

func TestRechargeRecordsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "recharge-records.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			} else {
				sqlDB.SetMaxOpenConns(4)
			}
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var databaseVersion string
			require.NoError(t, db.Raw(versionQuery).Scan(&databaseVersion).Error)
			t.Logf("%s database version: %s", dialect, databaseVersion)

			previousDB := DB
			previousLogDB := LOG_DB
			previousType := common.MainDatabaseType()
			previousLogType := common.LogDatabaseType()
			DB = db
			LOG_DB = db
			common.SetDatabaseTypes(dbType, dbType)
			common.RedisEnabled = false
			t.Cleanup(func() {
				DB = previousDB
				LOG_DB = previousLogDB
				common.SetDatabaseTypes(previousType, previousLogType)
			})

			// Exact TopUp schema from local release tag v1.0.0-rc.41;
			// seed before adding quota_amount or the new date indexes.
			require.NoError(t, db.AutoMigrate(&releasedRechargeTopUp{}))
			releaseRow := releasedRechargeTopUp{TradeNo: "RELEASE-PRESERVED", Amount: 3, Money: 3, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess, CreateTime: 1, CompleteTime: 1}
			require.NoError(t, db.Create(&releaseRow).Error)
			legacyModels := []any{&User{}, &TopUp{}, &Redemption{}, &SubscriptionOrder{}, &Log{}}
			require.NoError(t, db.AutoMigrate(legacyModels...))
			require.NoError(t, db.AutoMigrate(legacyModels...))
			var preserved TopUp
			require.NoError(t, db.First(&preserved, releaseRow.Id).Error)
			assert.Equal(t, "RELEASE-PRESERVED", preserved.TradeNo)
			assert.Zero(t, preserved.QuotaAmount)
			assert.True(t, db.Migrator().HasIndex(&TopUp{}, "idx_top_ups_complete_time"))
			newModels := []any{&RechargeRecordInvoice{}, &RechargeRecordInvoiceHistory{}, &RechargeRecordInvoiceRequest{}, &HistoricalRecharge{}, &RechargeImportJob{}}
			allModels := append(append([]any{}, legacyModels...), newModels...)
			t.Cleanup(func() {
				for i := len(allModels) - 1; i >= 0; i-- {
					require.NoError(t, db.Migrator().DropTable(allModels[i]))
				}
			})

			now := common.GetTimestamp()
			user := User{Username: "matrix-owner", Password: "unused", AffCode: "matrix-owner", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			online := TopUp{
				UserId: user.Id, TradeNo: "MATRIX-ONLINE-1", PaymentProvider: PaymentProviderEpay,
				PaymentMethod: "alipay", Status: common.TopUpStatusSuccess,
				Amount: 5, Money: 36.5, CreateTime: now - 30, CompleteTime: now - 30,
			}
			admin := TopUp{
				UserId: user.Id, TradeNo: "MATRIX-ADMIN-1", PaymentProvider: PaymentProviderAdmin,
				PaymentMethod: PaymentMethodOfficialWebsite, Status: common.TopUpStatusSuccess,
				Amount: 10, QuotaAmount: 5000, Money: 73, CreateTime: now - 20, CompleteTime: now - 20,
			}
			subscription := TopUp{
				UserId: user.Id, TradeNo: "MATRIX-SUB-1", Status: common.TopUpStatusSuccess,
				Amount: 0, Money: 10, CreateTime: now - 10, CompleteTime: now - 10,
			}
			require.NoError(t, db.Create(&online).Error)
			require.NoError(t, db.Create(&admin).Error)
			require.NoError(t, db.Create(&subscription).Error)
			require.NoError(t, db.Create(&SubscriptionOrder{
				UserId: user.Id, PlanId: 1, TradeNo: subscription.TradeNo,
				Status: common.TopUpStatusSuccess, CreateTime: now - 10, CompleteTime: now - 10,
			}).Error)
			redemption := Redemption{
				Name: "matrix-redemption", Key: "30000000000000000000000000000001",
				Status: common.RedemptionCodeStatusUsed, Quota: 800, UsedUserId: user.Id,
				RedeemedTime: now - 15, CreatedTime: now - 100,
			}
			require.NoError(t, db.Create(&redemption).Error)

			// Upgrade from the legacy schema with existing recharge rows, and
			// run the additive migration twice to prove idempotency.
			require.NoError(t, db.AutoMigrate(newModels...))
			require.NoError(t, db.AutoMigrate(newModels...))
			if dialect == "mysql" {
				// Existing imported tables can predate the database's current
				// default collation. The cross-source query must still work.
				require.NoError(t, db.Exec("ALTER TABLE historical_recharges MODIFY source_key varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL").Error)
				require.NoError(t, db.Exec("ALTER TABLE recharge_record_invoices MODIFY source_record_no varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL").Error)
			}

			records, total, err := ListRechargeRecords(context.Background(), RechargeRecordFilter{
				StartTime: now - 3600, EndTime: now, Page: 1, PageSize: 20,
			})
			require.NoError(t, err)
			assert.Equal(t, int64(3), total)
			require.Len(t, records, 3)

			result, err := MarkRechargeInvoices(context.Background(), InvoiceMarkingRequest{
				Action:    InvoiceActionMark,
				Records:   []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: admin.Id, ExpectedVersion: 0}},
				InvoiceNo: "MATRIX-INV-1", InvoiceDate: time.Now().Format("2006-01-02"),
				ReceiptConfirmed: true, VerificationNote: "verified matrix receipt",
				RequestID: "matrix-request-001", OperatorID: 7, OperatorName: "matrix-admin",
			})
			require.NoError(t, err)
			assert.Equal(t, 1, result.Count)
			history, err := ListRechargeInvoiceHistory(context.Background(), RechargeSourceTopUp, admin.Id)
			require.NoError(t, err)
			require.Len(t, history, 1)
			assert.Equal(t, InvoiceActionMark, history[0].Action)
			verifyRechargeRecordSafety(t)
			verifyRechargeHistoricalReview(t)
			require.NoError(t, db.AutoMigrate(allModels...))
			var retained RechargeRecordInvoice
			require.NoError(t, db.Where("source_type = ? AND source_id = ?", RechargeSourceTopUp, admin.Id).First(&retained).Error)
			assert.Equal(t, "MATRIX-INV-1", retained.InvoiceNo)
			duplicate := retained
			duplicate.Id = 0
			require.Error(t, db.Create(&duplicate).Error)
			// Also exercise fresh current-schema creation on each real engine.
			for i := len(allModels) - 1; i >= 0; i-- {
				require.NoError(t, db.Migrator().DropTable(allModels[i]))
			}
			require.NoError(t, db.AutoMigrate(allModels...))
			require.NoError(t, db.AutoMigrate(allModels...))
		})
	}
}

func TestRechargeRecordInvoiceUniqueSource(t *testing.T) {
	resetRechargeRecordTestTables(t)
	invoice := RechargeRecordInvoice{
		SourceType: RechargeSourceTopUp, SourceID: 1, UserID: 1, Status: InvoiceStatusMarked,
		Version: 1, CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(&invoice).Error)
	duplicate := invoice
	duplicate.Id = 0
	require.Error(t, DB.Create(&duplicate).Error)
	var count int64
	require.NoError(t, DB.Model(&RechargeRecordInvoice{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestRechargeRecordSafetyRegression(t *testing.T) {
	resetRechargeRecordTestTables(t)
	verifyRechargeRecordSafety(t)
}

func TestRechargeRecordsOnlyShowCompletedCredits(t *testing.T) {
	resetRechargeRecordTestTables(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	user := createRechargeRecordUser(t, "completed-only")
	now := common.GetTimestamp()
	for _, status := range []string{common.TopUpStatusSuccess, common.TopUpStatusPending, common.TopUpStatusFailed, common.TopUpStatusExpired} {
		createRechargeRecordTopUp(t, user.Id, "STATUS-"+status, PaymentProviderEpay, status, 7, 1, 0, now)
	}
	ctx := context.Background()
	records, count, err := ListRechargeRecords(ctx, RechargeRecordFilter{UserID: user.Id})
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	assert.Equal(t, "STATUS-success", records[0].RecordNo)
	_, err = RechargeEpay("STATUS-pending", "alipay", "127.0.0.1")
	require.NoError(t, err)
	_, count, err = ListRechargeRecords(ctx, RechargeRecordFilter{UserID: user.Id})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	var orders int64
	require.NoError(t, DB.Model(&TopUp{}).Where("user_id = ?", user.Id).Count(&orders).Error)
	assert.Equal(t, int64(4), orders, "original payment orders remain available for callbacks and reconciliation")
}

// Shared by the SQLite suite and the real MySQL/PostgreSQL matrix below.
func verifyRechargeRecordSafety(t *testing.T) {
	ctx := context.Background()
	now := common.GetTimestamp()
	user := createRechargeRecordUser(t, "safety-owner")
	stripe := createRechargeRecordTopUp(t, user.Id, "SAFETY-STRIPE", PaymentProviderStripe, common.TopUpStatusSuccess, 8, 10, 0, now)
	record, err := GetRechargeRecord(ctx, RechargeSourceTopUp, stripe.Id)
	require.NoError(t, err)
	assert.Equal(t, int(8*common.QuotaPerUnit), record.CreditedQuota)
	assert.Nil(t, record.PaidAmount)
	assert.False(t, record.PaidConfirmed)
	assert.True(t, record.RequiresVerify)
	creem := createRechargeRecordTopUp(t, user.Id, "SAFETY-CREEM", PaymentProviderCreem, common.TopUpStatusSuccess, 10, 5000000, 0, now)
	record, err = GetRechargeRecord(ctx, RechargeSourceTopUp, creem.Id)
	require.NoError(t, err)
	assert.Equal(t, 5000000, record.CreditedQuota)

	req := InvoiceMarkingRequest{Action: InvoiceActionMark, Records: []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: stripe.Id}}, InvoiceNo: "SAFE-INV", RequestID: "safety-invoice-001", OperatorID: 7, OperatorName: "admin"}
	_, err = MarkRechargeInvoices(ctx, req)
	require.ErrorIs(t, err, ErrInvoiceValidation)
	req.ReceiptConfirmed = true
	req.VerificationNote = "verified actual receipt"
	_, err = MarkRechargeInvoices(ctx, req)
	require.NoError(t, err)
	// Simulate a later order metadata save by a payment callback.
	var saved TopUp
	require.NoError(t, DB.First(&saved, stripe.Id).Error)
	saved.CompleteTime = now + 1
	require.NoError(t, DB.Save(&saved).Error)
	record, err = GetRechargeRecord(ctx, RechargeSourceTopUp, stripe.Id)
	require.NoError(t, err)
	assert.Equal(t, "SAFE-INV", record.InvoiceNo)

	// The batch must roll back writes already applied to its first target when
	// appending the second audit entry fails (not merely fail validation first).
	first := createRechargeRecordTopUp(t, user.Id, "ROLLBACK-FIRST", PaymentProviderEpay, common.TopUpStatusSuccess, 10, 1, 0, now)
	second := createRechargeRecordTopUp(t, user.Id, "ROLLBACK-SECOND", PaymentProviderEpay, common.TopUpStatusSuccess, 10, 1, 0, now)
	callbackName := "review:fail_second_history"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if history, ok := tx.Statement.Dest.(*RechargeRecordInvoiceHistory); ok && history.SourceID == second.Id {
			tx.AddError(errors.New("injected audit write failure"))
		}
	}))
	_, err = MarkRechargeInvoices(ctx, InvoiceMarkingRequest{Action: InvoiceActionMark, Records: []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: first.Id}, {Source: RechargeSourceTopUp, ID: second.Id}}, RequestID: "rollback-audit-001", OperatorID: 7, OperatorName: "admin"})
	require.NoError(t, DB.Callback().Create().Remove(callbackName))
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&RechargeRecordInvoice{}).Where("source_type = ? AND source_id IN ?", RechargeSourceTopUp, []int{first.Id, second.Id}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&RechargeRecordInvoiceHistory{}).Where("request_id = ?", "rollback-audit-001").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&RechargeRecordInvoiceRequest{}).Where("request_id = ?", "rollback-audit-001").Count(&count).Error)
	assert.Zero(t, count)

	ready := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Go(func() {
			<-ready
			_, err := MarkRechargeInvoices(ctx, InvoiceMarkingRequest{Action: InvoiceActionMark, Records: []InvoiceMarkingRecord{{Source: RechargeSourceTopUp, ID: first.Id}}, RequestID: fmt.Sprintf("concurrent-mark-%d", i), OperatorID: 7, OperatorName: "admin"})
			results <- err
		})
	}
	close(ready)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			var conflict *InvoiceMarkingConflictError
			assert.ErrorAs(t, err, &conflict)
		}
	}
	assert.Equal(t, 1, winners)
	require.NoError(t, DB.Model(&RechargeRecordInvoiceHistory{}).Where("source_type = ? AND source_id = ?", RechargeSourceTopUp, first.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	assert.Equal(t, user.Quota, after.Quota)
}

func TestRechargeHistoricalReviewIsolation(t *testing.T) {
	resetRechargeRecordTestTables(t)
	// A physically separate log database, with its own retention lifecycle.
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "separate-logs.db")), &gorm.Config{})
	require.NoError(t, err)
	old := LOG_DB
	LOG_DB = logDB
	t.Cleanup(func() {
		LOG_DB = old
		sqlDB, err := logDB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	verifyRechargeHistoricalReview(t)
}

func verifyRechargeHistoricalReview(t *testing.T) {
	ctx := context.Background()
	now := common.GetTimestamp()
	user := createRechargeRecordUser(t, "history-owner")
	data, err := common.Marshal(map[string]any{"op": map[string]any{"action": "user.quota_add", "params": map[string]any{"target_user_id": user.Id, "quota_value": 5000}}})
	require.NoError(t, err)
	entry := Log{UserId: 999, CreatedAt: now - 1, Type: LogTypeManage, Content: "管理员增加用户额度 $0.01", Other: string(data), RequestId: "review-source-001"}
	require.NoError(t, LOG_DB.Create(&entry).Error)
	duplicateLog := entry
	duplicateLog.Id = 0
	require.NoError(t, LOG_DB.Create(&duplicateLog).Error)
	job, err := CreateRechargeImportJob(ctx, now-100, now, 7)
	require.NoError(t, err)
	job, err = AdvanceRechargeImportJob(ctx, job.Id)
	require.NoError(t, err)
	assert.True(t, job.Done)
	_, err = AdvanceRechargeImportJob(ctx, job.Id)
	require.NoError(t, err)
	page, err := ListRechargeImportReview(ctx, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	candidate := page.Items[0]
	assert.Equal(t, user.Id, candidate.UserID)
	_, total, err := ListRechargeRecords(ctx, RechargeRecordFilter{UserID: user.Id})
	require.NoError(t, err)
	assert.Zero(t, total)
	require.NoError(t, ReviewHistoricalRecharge(ctx, candidate.Id, user.Id, 5000, 7, "verified user and original quota", false))
	require.ErrorIs(t, ReviewHistoricalRecharge(ctx, candidate.Id, user.Id, 5000, 7, "duplicate request", false), ErrRechargeImportConflict)
	records, total, err := ListRechargeRecords(ctx, RechargeRecordFilter{UserID: user.Id})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, RechargeSourceHistorical, records[0].Source)
	assert.Nil(t, records[0].PaidAmount)
	assert.True(t, records[0].RequiresVerify)
	var topups int64
	require.NoError(t, DB.Model(&TopUp{}).Where("user_id = ?", user.Id).Count(&topups).Error)
	assert.Zero(t, topups)
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	assert.Equal(t, user.Quota, after.Quota)
	require.NoError(t, LOG_DB.Delete(&entry).Error)
	_, err = GetRechargeRecord(ctx, RechargeSourceHistorical, candidate.Id)
	require.NoError(t, err)
	_, err = MarkRechargeInvoices(ctx, InvoiceMarkingRequest{Action: InvoiceActionMark, Records: []InvoiceMarkingRecord{{Source: RechargeSourceHistorical, ID: candidate.Id}}, RequestID: "historical-invoice-001", ReceiptConfirmed: true, VerificationNote: "verified invoice and receipt", OperatorID: 7, OperatorName: "admin"})
	require.NoError(t, err)
}

// Released v1.0.0-rc.41 business table, intentionally independent of current
// struct tags so upgrade tests cannot silently become fresh-schema tests.
type releasedRechargeTopUp struct {
	Id              int `gorm:"primaryKey"`
	UserId          int `gorm:"index"`
	Amount          int64
	Money           float64
	TradeNo         string `gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string `gorm:"type:varchar(50)"`
	PaymentProvider string `gorm:"type:varchar(50);default:''"`
	CreateTime      int64
	CompleteTime    int64
	Status          string
}

func (releasedRechargeTopUp) TableName() string { return "top_ups" }

func TestRechargeInvoiceHistoryCursor(t *testing.T) {
	resetRechargeRecordTestTables(t)
	rows := make([]RechargeRecordInvoiceHistory, 101)
	for i := range rows {
		rows[i] = RechargeRecordInvoiceHistory{SourceType: RechargeSourceTopUp, SourceID: 42, UserID: 1, RequestID: fmt.Sprintf("history-page-%d", i), BeforeSnapshot: "{}", AfterSnapshot: "{}"}
	}
	require.NoError(t, DB.Create(&rows).Error)
	first, err := ListRechargeInvoiceHistory(context.Background(), RechargeSourceTopUp, 42)
	require.NoError(t, err)
	require.Len(t, first, 100)
	second, err := ListRechargeInvoiceHistory(context.Background(), RechargeSourceTopUp, 42, first[99].Id)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, rows[0].Id, second[0].Id)
	assert.Greater(t, first[99].Id, second[0].Id)
}
