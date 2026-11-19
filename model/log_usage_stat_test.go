package model

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSumUsedQuotaReturnsNetBilledQuota(t *testing.T) {
	truncateTables(t)

	logs := []Log{
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 100},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 101},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 102},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 103},
		{Type: LogTypeRefund, ModelName: "video-model", Quota: 650000, CreatedAt: 104},
		// Final-accounting rollbacks have no matching consume log and must not
		// reduce billed usage.
		{Type: LogTypePreConsumeRollback, ModelName: "video-model", Quota: 650000, CreatedAt: 105},
		{Type: LogTypeConsume, ModelName: "other-model", Quota: 900000, CreatedAt: 106},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	stat, err := SumUsedQuota(LogTypeUnknown, 100, 110, "video-model", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 1950000, stat.Quota)
}

func TestTopupCredit(t *testing.T) {
	for _, tc := range []struct {
		name             string
		log              Log
		amount, currency string
		known            bool
	}{
		{"online credit, not payment", Log{Content: "使用在线充值成功，充值金额: ¥12.500000 额度，支付金额：10.000000"}, "12.5", "¥", true},
		{"stripe", Log{Content: "使用在线充值成功，充值金额: ＄0.100000，支付金额：10"}, "0.1", "$", true},
		{"redemption", Log{Content: "通过兑换码充值 500000 点额度，兑换码ID 42"}, "500000", "", true},
		{"creem raw quota", Log{Content: "使用Creem充值成功，充值额度: 500000，支付金额：1.00"}, "500000", "", true},
		{"waffo", Log{Content: "Waffo充值成功，充值额度: €2.5，支付金额: 2.00"}, "2.5", "€", true},
		{"pancake", Log{Content: "Waffo Pancake充值成功，充值额度: ＄2.5，支付金额: 2.00"}, "2.5", "$", true},
		{"admin compensation", Log{Content: "管理员补单成功，充值金额: ¥70.000000，支付金额：50.000000"}, "70", "¥", true},
		{"structured credit", Log{Other: `{"op":{"action":"user.quota_add","params":{"from":500000,"to":1500000,"quota":1000000,"target_username":"alice"}}}`}, "1000000", "", true},
		{"legacy structured credit", Log{Other: `{"op":{"action":"user.quota_add","params":{"quota":500000}}}`}, "500000", "", true},
		{"override increase", Log{Other: `{"op":{"action":"user.quota_override","params":{"from":500000,"to":750000}}}`}, "250000", "", true},
		{"override decrease", Log{Other: `{"op":{"action":"user.quota_override","params":{"from":500000,"to":0}}}`}, "0", "", true},
		{"subtract", Log{Other: `{"op":{"action":"user.quota_subtract","params":{"quota":500000}}}`}, "0", "", true},
		{"subscription", Log{Content: "订阅购买成功，套餐: example，支付金额: 100.00，支付方式: stripe"}, "0", "", true},
		{"balance subscription", Log{Content: "使用余额购买订阅成功，套餐: example，支付金额: 10.00，扣除额度: 5000000"}, "0", "", true},
		{"stored quota", Log{Quota: 250000}, "250000", "", true},
		{"unknown", Log{Content: "old topup"}, "0", "", false},
		{"malformed", Log{Other: `{`, Content: "使用在线充值成功，充值金额: ¥NaN，支付金额：5"}, "0", "", false},
		{"incomplete override", Log{Other: `{"op":{"action":"user.quota_override","params":{"quota":100}}}`}, "0", "", false},
		{"stored quota takes precedence over message", Log{Quota: 250000, Other: `{"content_parts":[{"key":"Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}","params":{"quota":"¥10"}}]}`}, "250000", "", true},
		{"invalid metadata preserves legacy fallback", Log{Other: `{`, Content: "使用在线充值成功，充值金额: ¥2.500000，支付金额：1"}, "2.5", "¥", true},
		{"partially decoded metadata is not credit", Log{Other: `{"content_parts":[{"key":"Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}","params":{"quota":"¥10"}}],"op":{"action":123}}`}, "0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			amount, currency, known := topupCredit(tc.log)
			assert.Equal(t, tc.amount, amount.String())
			assert.Equal(t, tc.currency, currency)
			assert.Equal(t, tc.known, known)
		})
	}
}

func TestTopupCreditMessages(t *testing.T) {
	const online = "Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}"
	const provider = "{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}"
	for _, tc := range []struct {
		name             string
		key              string
		quota            any
		amount, currency string
		known            bool
	}{
		{"online credit not payment", online, "¥12.500000", "12.5", "¥", true},
		{"stripe dollars", online, "＄0.100000", "0.1", "$", true},
		{"token display", online, "500000", "500000", "", true},
		{"custom currency", online, "HK$2.500000", "2.5", "HK$", true},
		{"zero", online, "¥0.000000", "0", "¥", true},
		{"admin compensation", "Administrator completed the order, amount added: {{quota}}, payment amount: {{amount}}", "¥70.000000", "70", "¥", true},
		{"creem numeric quota", provider, 500000, "500000", "", true},
		{"large numeric quota precision", provider, int64(9007199254740991), "9007199254740991", "", true},
		{"waffo currency", provider, "€2.500000", "2.5", "€", true},
		{"redemption", "Topped up {{quota}} with a redemption code, code ID {{id}}", "¥7.000000", "7", "¥", true},
		{"subscription", "Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}", nil, "0", "", true},
		{"balance subscription", "Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}", 5000000, "0", "", true},
		{"unknown message", "Unrecognized {{quota}}", "¥10", "0", "", false},
		{"missing quota", online, nil, "0", "", false},
		{"negative quota", online, -1, "0", "", false},
		{"negative currency amount", online, "¥-1", "0", "", false},
		{"invalid amount", online, "¥NaN", "0", "", false},
		{"trailing text", online, "¥10 invalid", "0", "", false},
		{"wrong quota type", online, true, "0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"amount": "99", "provider": "Creem", "id": 42, "plan": "example", "method": "stripe"}
			if tc.quota != nil {
				params["quota"] = tc.quota
			}
			other := NewLogOther()
			content := other.setContent(common.NewMessage(tc.key, params))
			amount, currency, known := topupCredit(Log{Content: content, Other: other.JSONString()})
			assert.Equal(t, tc.amount, amount.String())
			assert.Equal(t, tc.currency, currency)
			assert.Equal(t, tc.known, known)
		})
	}
}

func TestSumTopupLogsDatabase(t *testing.T) {
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("topup_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldLogDB := LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	LOG_DB = db
	common.SetDatabaseTypes(oldMain, dialect)
	initCol()
	t.Cleanup(func() {
		LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		initCol()
		require.NoError(t, db.Migrator().DropTable(&Log{}))
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Log{}))
	var version string
	versionSQL := "SELECT version()"
	if dialect == common.DatabaseTypeSQLite {
		versionSQL = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Logf("database version: %s", version)
	logs := []Log{
		{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 100, Quota: 500000, Group: "default", TokenName: "key", ModelName: "model", ChannelId: 3, RequestId: "one", UpstreamRequestId: "up-one"},
		{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 200, Content: "使用在线充值成功，充值金额: ¥0.100000 额度，支付金额：99"},
		{UserId: 2, Username: "bob", Type: LogTypeTopup, CreatedAt: 150, Content: "使用在线充值成功，充值金额: ¥0.200000 额度，支付金额：99"},
		{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 99, Quota: 999},
		{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 201, Quota: 999},
		{UserId: 1, Username: "alice", Type: LogTypeConsume, CreatedAt: 150, Quota: 999},
		{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 150, Content: "unrecognized"},
	}
	// Use the same content serialization as RecordTopupLog/RecordLog so the
	// aggregate exercises current writers together with historical rows.
	for _, message := range []*common.Message{
		common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"quota": "¥12.500000", "amount": "10"}),
		common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"quota": "＄0.100000", "amount": "10"}),
		common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"provider": "Creem", "quota": 250000, "amount": "1"}),
		common.NewMessage("Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}", map[string]any{"plan": "example", "amount": "10", "quota": 5000000}),
	} {
		other := NewLogOther()
		content := other.setContent(message)
		logs = append(logs, Log{UserId: 1, Username: "alice", Type: LogTypeTopup, CreatedAt: 150, Content: content, Other: other.JSONString()})
	}
	require.NoError(t, db.Create(&logs).Error)
	for _, tc := range []struct {
		name      string
		filter    TopupLogFilter
		quota     string
		amounts   map[string]string
		uncounted int
	}{
		{"all matching rows including time boundaries", TopupLogFilter{StartTimestamp: 100, EndTimestamp: 200}, "750000", map[string]string{"¥": "12.8", "$": "0.1"}, 1},
		{"self owner scope", TopupLogFilter{UserID: 1, StartTimestamp: 100, EndTimestamp: 200}, "750000", map[string]string{"¥": "12.6", "$": "0.1"}, 1},
		{"admin username", TopupLogFilter{Username: "bob", StartTimestamp: 100, EndTimestamp: 200}, "0", map[string]string{"¥": "0.2"}, 0},
		{"all list filters", TopupLogFilter{StartTimestamp: 100, EndTimestamp: 200, Group: "default", TokenName: "key", ModelName: "model", Channel: 3, RequestID: "one", UpstreamRequestID: "up-one"}, "500000", map[string]string{}, 0},
		{"empty", TopupLogFilter{UserID: 99}, "0", map[string]string{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stat, err := SumTopupLogs(context.Background(), tc.filter)
			require.NoError(t, err)
			assert.Equal(t, tc.quota, stat.Quota)
			assert.Equal(t, tc.amounts, stat.Amounts)
			assert.Equal(t, tc.uncounted, stat.Uncounted)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = SumTopupLogs(ctx, TopupLogFilter{})
	require.Error(t, err)
}
