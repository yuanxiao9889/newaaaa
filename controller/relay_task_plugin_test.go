package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
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
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

type videoRetryTestFixture struct {
	db            *gorm.DB
	user          model.User
	channels      [2]model.Channel
	submitCalls   [2]atomic.Int32
	pollStatus    [2]string
	pollHTTP      [2]int
	submitStatus  [2]string
	malformed     [2]bool
	received      [2]map[string]any
	submitStarted chan struct{}
	releaseSubmit chan struct{}
}

func newVideoRetryTestFixture(t *testing.T) *videoRetryTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{})
	f := &videoRetryTestFixture{db: db, pollStatus: [2]string{"FAILURE", "SUCCESS"}}
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	oldRetry, oldRegistry, oldAdaptor := common.RetryTimes, pluginruntime.DefaultRegistry, service.GetTaskAdaptorFunc
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	common.RetryTimes = 3
	pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(platform) }
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = previousMaster
		model.LOG_DB = oldLogDB
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
		common.RetryTimes, pluginruntime.DefaultRegistry, service.GetTaskAdaptorFunc = oldRetry, oldRegistry, oldAdaptor
	})
	withTieredBillingConfig(t, map[string]string{"retry-video": "tiered_expr"}, map[string]string{"retry-video": `tier("video", u("seconds") * 0.01)`})
	f.user = model.User{Username: "video-retry-user", AffCode: "video-retry-aff", Group: "default", Status: common.UserStatusEnabled, Quota: 1_000_000}
	require.NoError(t, db.Create(&f.user).Error)
	for i := range 2 {
		key := "retry-provider-" + strconv.Itoa(i)
		source := fmt.Sprintf(`
export const meta={apiVersion:1,key:%q,name:"Retry video provider",version:"1.0.0",author:{name:"Test"},models:["retry-video",%q],fetchMode:"per_task",protocols:["openai_video"],usageSchema:{seconds:{type:"number",unit:"second",description:{en:"Video generation unit price",zh:"视频生成单价"}}}};
export const protocols={openai_video:{decodeRequest:function(ctx){let body=ctx.body.value;if(ctx.body.kind==="multipart"){body={model:ctx.model,prompt:ctx.body.fields.prompt[0],seconds:Number(ctx.body.fields.seconds[0]),reference:{__fileRef:ctx.body.files[0].ref,encoding:"base64"}};}return {kind:"submit",model:ctx.model,action:"text_to_video",requestBody:body};},render:function(ctx,task){return {id:task.task_id,status:task.status};}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",body:{...ctx.requestBody,model:ctx.upstreamModel||ctx.model}};}
export function parseSubmitResponse(ctx,resp){let result={taskId:resp.body.id,taskData:resp.body};if(resp.body.immediate){result.immediate={status:resp.body.immediate,reason:"replacement rejected"};}return result;}
export function extractUsage(ctx){return {seconds:Number(ctx.requestBody.seconds)};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/query/"+ctx.taskId,method:"GET"};}
export function parseTaskResult(ctx,body){return {status:body.status,reason:"provider rejected video",url:body.url};}
export function extractUsageOnComplete(ctx,result,body){return {seconds:body.seconds};}
export function listArtifacts(){return [];}
export function buildContentRequest(){return {};}
`, key, "upstream-video-"+strconv.Itoa(i))
		_, err := pluginruntime.DefaultRegistry.Register(source, pluginruntime.Options{})
		require.NoError(t, err)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost {
				f.submitCalls[i].Add(1)
				if !assert.NoError(t, common.DecodeJson(r.Body, &f.received[i])) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if i == 1 && f.submitStarted != nil {
					close(f.submitStarted)
					<-f.releaseSubmit
				}
				if f.malformed[i] {
					_, _ = w.Write([]byte(`{"accepted":true}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":"upstream-%d","immediate":%q,"seconds":3}`, i, f.submitStatus[i])
				return
			}
			if f.pollHTTP[i] > 0 {
				w.WriteHeader(f.pollHTTP[i])
			}
			_, _ = fmt.Fprintf(w, `{"status":%q,"url":"https://cdn.example/video.mp4","seconds":3}`, f.pollStatus[i])
		}))
		t.Cleanup(server.Close)
		channel := model.Channel{Type: constant.ChannelTypeTaskPlugin, Name: key, Key: "test-key", BaseURL: &server.URL, Status: common.ChannelStatusEnabled, Models: "retry-video", Group: "default", ModelMapping: common.GetPointer(fmt.Sprintf(`{"retry-video":"upstream-video-%d"}`, i))}
		channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: key})
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "retry-video", ChannelId: channel.Id, Enabled: true}).Error)
		f.channels[i] = channel
	}
	return f
}

func (f *videoRetryTestFixture) submit(t *testing.T, multipartBody bool) *model.Task {
	t.Helper()
	body := []byte(`{"model":"retry-video","prompt":"a lighthouse","seconds":4}`)
	contentType := "application/json"
	if multipartBody {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		require.NoError(t, writer.WriteField("model", "retry-video"))
		require.NoError(t, writer.WriteField("prompt", "a lighthouse"))
		require.NoError(t, writer.WriteField("seconds", "4"))
		file, err := writer.CreateFormFile("input_reference", "reference.png")
		require.NoError(t, err)
		_, err = file.Write([]byte{0, 1, 2, 3, 255})
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		body, contentType = buffer.Bytes(), writer.FormDataContentType()
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos?client=retry-test", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Set("username", f.user.Username)
	common.SetContextKey(c, constant.ContextKeyUserId, f.user.Id)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserQuota, f.user.Quota)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	middleware.PinTaskPluginEndpoint()(c)
	middleware.PrepareTaskPluginEndpoint()(c)
	require.False(t, c.IsAborted())
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &f.channels[0], "retry-video"))
	info := &relaycommon.RelayInfo{UserId: f.user.Id, UserGroup: "default", UsingGroup: "default", TokenGroup: "default", UserQuota: f.user.Quota, OriginModelName: "retry-video", IsPlayground: true, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: model.GenerateTaskID(), Action: c.GetString("task_action")}}
	info.UserSetting.BillingPreference = "wallet_only"
	outcome, taskErr := executeTaskSubmission(c, info)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	common.CleanupBodyStorage(c)
	stored := f.load(t, outcome.Task.TaskID)
	require.NotNil(t, stored.PrivateData.VideoRetry)
	assert.Equal(t, body, stored.PrivateData.VideoRetry.Body, "persist the original body before any provider mapping")
	assert.True(t, stored.PrivateData.VideoRetry.Ready)
	assert.Equal(t, common.QuotaRound(4*0.01*common.QuotaPerUnit), stored.Quota)
	return stored
}

func (f *videoRetryTestFixture) load(t *testing.T, taskID string) *model.Task {
	t.Helper()
	var task model.Task
	require.NoError(t, f.db.Where("task_id = ?", taskID).First(&task).Error)
	return &task
}

func (f *videoRetryTestFixture) poll(t *testing.T, task *model.Task) *model.Task {
	t.Helper()
	upstreamID := task.GetUpstreamTaskID()
	require.NoError(t, service.UpdateVideoTasks(context.Background(), task.Platform, map[int][]string{task.ChannelId: {upstreamID}}, map[string]*model.Task{upstreamID: task}))
	return f.load(t, task.TaskID)
}

func TestVideoTaskRetryAcrossChannelsAndFinalSettlement(t *testing.T) {
	for _, multipartBody := range []bool{false, true} {
		t.Run(fmt.Sprintf("multipart=%t", multipartBody), func(t *testing.T) {
			f := newVideoRetryTestFixture(t)
			task := f.submit(t, multipartBody)
			publicID, rowID, reserved := task.TaskID, task.ID, task.Quota
			stale := f.load(t, publicID)
			task = f.poll(t, task)
			assert.Equal(t, publicID, task.TaskID)
			assert.Equal(t, rowID, task.ID)
			assert.Equal(t, f.channels[1].Id, task.ChannelId)
			assert.Equal(t, "upstream-1", task.GetUpstreamTaskID())
			assert.Equal(t, constant.TaskPlatform("retry-provider-1"), task.Platform)
			assert.Equal(t, "upstream-video-1", task.Properties.UpstreamModelName)
			assert.Equal(t, "retry-video", task.Properties.OriginModelName)
			assert.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), task.Status)
			assert.Equal(t, []string{strconv.Itoa(f.channels[0].Id), strconv.Itoa(f.channels[1].Id)}, task.PrivateData.ChannelRetryPath)
			assert.Equal(t, reserved, task.Quota)
			var user model.User
			require.NoError(t, f.db.First(&user, f.user.Id).Error)
			assert.Equal(t, f.user.Quota-reserved, user.Quota, "replacement reuses the original reservation")
			assert.Zero(t, user.UsedQuota)
			assert.Equal(t, "upstream-video-0", f.received[0]["model"])
			assert.Equal(t, "upstream-video-1", f.received[1]["model"])
			assert.Equal(t, f.received[0]["prompt"], f.received[1]["prompt"])
			if multipartBody {
				assert.Equal(t, "AAECA/8=", f.received[1]["reference"], "file references must be rebuilt from saved upload bytes")
			}
			stale.Status = model.TaskStatusFailure
			won, err := stale.UpdateWithStatus(model.TaskStatusNotStart)
			require.NoError(t, err)
			assert.False(t, won, "a stale poll cannot finalize the previous upstream attempt")
			task = f.poll(t, task)
			assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			assert.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
			assert.Empty(t, task.PrivateData.VideoRetry.Body)
			wantQuota := common.QuotaRound(3 * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, wantQuota, task.Quota)
			require.NoError(t, f.db.First(&user, f.user.Id).Error)
			assert.Equal(t, f.user.Quota-wantQuota, user.Quota)
			assert.Equal(t, wantQuota, user.UsedQuota)
			var logs []model.Log
			require.NoError(t, f.db.Where("user_id = ? AND type = ?", f.user.Id, model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, f.channels[1].Id, logs[0].ChannelId)
			assert.Equal(t, wantQuota, logs[0].Quota)
			assert.Equal(t, int32(1), f.submitCalls[0].Load())
			assert.Equal(t, int32(1), f.submitCalls[1].Load())
		})
	}
}

func TestVideoTaskRetryFailureBoundaries(t *testing.T) {
	for _, name := range []string{"late failure", "retry budget exhausted", "no unused channel", "all channels fail", "malformed replacement receipt", "saturated retry charge", "poll transport failure", "unknown interrupted submit"} {
		t.Run(name, func(t *testing.T) {
			f := newVideoRetryTestFixture(t)
			task := f.submit(t, false)
			wantSecondSubmit := int32(0)
			wantFailure := true
			switch name {
			case "late failure":
				task.PrivateData.VideoRetry.AcceptedAt = time.Now().Unix() - 46
			case "retry budget exhausted":
				common.RetryTimes = 0
			case "no unused channel":
				require.NoError(t, f.db.Model(&model.Ability{}).Where("channel_id = ?", f.channels[1].Id).Update("enabled", false).Error)
			case "all channels fail":
				f.pollStatus[1] = "FAILURE"
				wantSecondSubmit = 1
			case "malformed replacement receipt":
				f.malformed[1] = true
				wantSecondSubmit = 1
			case "saturated retry charge":
				f.user.Quota = common.MaxWalletQuota
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.user.Id).Update("quota", f.user.Quota-task.Quota).Error)
				withTieredBillingConfig(t, map[string]string{"retry-video": "tiered_expr"}, map[string]string{"retry-video": `tier("video", u("seconds") * 1000000000)`})
			case "poll transport failure":
				f.pollHTTP[0] = http.StatusBadGateway
				wantFailure = false
			case "unknown interrupted submit":
				task.PrivateData.VideoRetry.Submitting = true
				task.PrivateData.VideoRetry.SubmitStartedAt = time.Now().Unix() - service.VideoTaskRetrySubmitTimeoutSeconds - 1
			}
			won, err := task.UpdateWithStatus(task.Status)
			require.NoError(t, err)
			require.True(t, won)
			task = f.poll(t, task)
			if name == "all channels fail" {
				task = f.poll(t, task)
			}
			var user model.User
			require.NoError(t, f.db.First(&user, f.user.Id).Error)
			if wantFailure {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
				assert.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
				assert.Empty(t, task.PrivateData.VideoRetry.Body)
				assert.Equal(t, f.user.Quota, user.Quota)
				require.NoError(t, service.RefundTaskQuota(context.Background(), task, "duplicate final notification"))
				require.NoError(t, f.db.First(&user, f.user.Id).Error)
				assert.Equal(t, f.user.Quota, user.Quota)
				var count int64
				require.NoError(t, f.db.Model(&model.Log{}).Where("user_id = ? AND type = ?", f.user.Id, model.LogTypePreConsumeRollback).Count(&count).Error)
				assert.Equal(t, int64(1), count, "only the final failure refunds")
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusNotStart), task.Status)
				assert.Equal(t, f.user.Quota-task.Quota, user.Quota)
			}
			assert.Zero(t, user.UsedQuota)
			assert.Equal(t, int32(1), f.submitCalls[0].Load())
			assert.Equal(t, wantSecondSubmit, f.submitCalls[1].Load())
		})
	}
}

func TestVideoTaskRetryImmediateReplacementSettlesOnce(t *testing.T) {
	for _, status := range []string{model.TaskStatusSuccess, model.TaskStatusFailure} {
		t.Run(status, func(t *testing.T) {
			f := newVideoRetryTestFixture(t)
			task := f.submit(t, false)
			reserved, publicID := task.Quota, task.TaskID
			f.submitStatus[1] = status
			task = f.poll(t, task)
			assert.Equal(t, publicID, task.TaskID)
			assert.Equal(t, model.TaskStatus(status), task.Status)
			assert.Empty(t, task.PrivateData.VideoRetry.Body)
			assert.Equal(t, int32(1), f.submitCalls[0].Load())
			assert.Equal(t, int32(1), f.submitCalls[1].Load())
			wantQuota, wantLogType := 0, model.LogTypePreConsumeRollback
			if status == model.TaskStatusSuccess {
				wantQuota, wantLogType = common.QuotaRound(3*0.01*common.QuotaPerUnit), model.LogTypeConsume
				assert.Equal(t, service.TaskBillingStateSettled, task.PrivateData.BillingState)
				assert.Contains(t, task.GetResultURL(), publicID, "an immediate success without an upstream URL uses the public task proxy")
			} else {
				assert.Equal(t, service.TaskBillingStateRefunded, task.PrivateData.BillingState)
				assert.Equal(t, "replacement rejected", task.FailReason)
			}
			assert.Equal(t, wantQuota, task.Quota)
			var user model.User
			require.NoError(t, f.db.First(&user, f.user.Id).Error)
			assert.Equal(t, f.user.Quota-wantQuota, user.Quota)
			assert.Equal(t, wantQuota, user.UsedQuota)
			var logs []model.Log
			require.NoError(t, f.db.Where("user_id = ? AND type = ?", f.user.Id, wantLogType).Find(&logs).Error)
			require.Len(t, logs, 1)
			if status == model.TaskStatusFailure {
				assert.Equal(t, reserved, logs[0].Quota, "the final rollback records the entire original reservation")
			} else {
				assert.Equal(t, wantQuota, logs[0].Quota)
			}
		})
	}
}

func TestVideoTaskRetryClaimsOneReplacementUnderCompetingPolls(t *testing.T) {
	f := newVideoRetryTestFixture(t)
	task := f.submit(t, false)
	stale := f.load(t, task.TaskID)
	f.submitStarted, f.releaseSubmit = make(chan struct{}), make(chan struct{})
	defer close(f.releaseSubmit)
	type retryOutcome struct {
		handled bool
		err     error
	}
	completed := make(chan retryOutcome, 1)
	go func() {
		handled, err := retryVideoTask(context.Background(), task, &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "generation rejected"})
		completed <- retryOutcome{handled: handled, err: err}
	}()
	select {
	case <-f.submitStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "replacement submission did not start")
	}
	handled, err := retryVideoTask(context.Background(), stale, &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "same generation rejected"})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, int32(1), f.submitCalls[1].Load())
	// Release the one accepted POST without closing twice in deferred cleanup.
	f.releaseSubmit <- struct{}{}
	select {
	case outcome := <-completed:
		require.NoError(t, outcome.err)
		assert.True(t, outcome.handled)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "replacement submission did not finish")
	}
	stored := f.load(t, task.TaskID)
	assert.Equal(t, "upstream-1", stored.GetUpstreamTaskID())
	assert.Equal(t, int32(1), f.submitCalls[1].Load())
}

func TestVideoTaskRetryCaptureExcludesBoundAndNonVideoRequests(t *testing.T) {
	oldRetry := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = oldRetry })
	for _, path := range []string{"/v1/videos", "/v1/video/generations"} {
		t.Run(path, func(t *testing.T) {
			c := taskSubmissionTestContext()
			c.Request.URL.Path = path
			defer common.CleanupBodyStorage(c)
			assert.NotNil(t, captureVideoTaskRetry(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}))
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*gin.Context, *relaycommon.RelayInfo)
	}{
		{"locked channel", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.LockedChannel = &model.Channel{Id: 7} }},
		{"origin task", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.OriginTaskID = "original-video" }},
		{"token channel pin", func(c *gin.Context, _ *relaycommon.RelayInfo) {
			service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 7, Source: dto.PinSourceToken})
		}},
		{"remix", func(c *gin.Context, _ *relaycommon.RelayInfo) { c.Request.URL.Path = "/v1/videos/video_original/remix" }},
		{"image", func(c *gin.Context, _ *relaycommon.RelayInfo) { c.Request.URL.Path = "/v1/images/generations" }},
		{"responses", func(c *gin.Context, _ *relaycommon.RelayInfo) {
			c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Protocol: "openai_responses"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := taskSubmissionTestContext()
			c.Request.URL.Path = "/v1/videos"
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			tc.edit(c, info)
			assert.Nil(t, captureVideoTaskRetry(c, info))
		})
	}
}

func TestVideoTaskRetryEligibilityWindowAndBudget(t *testing.T) {
	previousRetry := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = previousRetry })
	const accepted int64 = 1_700_000_000
	for _, tc := range []struct {
		name     string
		age      int64
		attempts int
		status   model.TaskStatus
		want     bool
	}{
		{"first accepted second", 0, 1, model.TaskStatusSubmitted, true},
		{"45 seconds inclusive", 45, 1, model.TaskStatusSubmitted, true},
		{"46 seconds expired", 46, 1, model.TaskStatusSubmitted, false},
		{"future timestamp", -1, 1, model.TaskStatusSubmitted, false},
		{"one remaining attempt", 2, 3, model.TaskStatusInProgress, true},
		{"initial attempts consume budget", 2, 4, model.TaskStatusSubmitted, false},
		{"already failed", 2, 1, model.TaskStatusFailure, false},
		{"already succeeded", 2, 1, model.TaskStatusSuccess, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{Status: tc.status, PrivateData: model.TaskPrivateData{ChannelRetryPath: make([]string, tc.attempts), VideoRetry: &model.VideoTaskRetryState{Body: []byte(`{}`), AcceptedAt: accepted, Ready: true}}}
			assert.Equal(t, tc.want, canRetryVideoTask(task, &relaycommon.TaskInfo{Status: model.TaskStatusFailure}, accepted+tc.age))
			assert.False(t, canRetryVideoTask(task, &relaycommon.TaskInfo{Status: model.TaskStatusUnknown}, accepted+tc.age), "an uncertain poll cannot create another upstream job")
		})
	}
}

func TestVideoTaskRetryCaptureBoundedRequest(t *testing.T) {
	previousRetry := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = previousRetry })
	for _, size := range []int{8 << 20, (8 << 20) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			c := taskSubmissionTestContext()
			body := bytes.Repeat([]byte("x"), size)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
			defer common.CleanupBodyStorage(c)
			retry := captureVideoTaskRetry(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
			if size > 8<<20 {
				assert.Nil(t, retry)
				return
			}
			require.NotNil(t, retry)
			assert.Equal(t, body, retry.Body)
			common.RetryTimes = 0
			assert.Nil(t, captureVideoTaskRetry(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}))
			common.RetryTimes = 3
		})
	}
}

func TestVideoTaskRetryQuotaDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Task{}, &model.UserSubscription{})
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	previousMaster := common.IsMasterNode
	t.Setenv("LOG_SQL_DSN", "")
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled = false
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled, common.RDB = previousRedis, previousClient
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = previousMaster
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	for index, tc := range []struct {
		name           string
		target         int
		walletShort    bool
		tokenShort     bool
		unlimited      bool
		subscription   bool
		subShort       bool
		stale          bool
		expiredToken   bool
		disabledRefund bool
		expiredSub     bool
		redisWallet    bool
		redisToken     bool
		persistFailure bool
		freeSource     bool
		wantError      string
	}{
		{name: "wallet difference", target: 150},
		{name: "unchanged reservation", target: 100},
		{name: "zero price", target: 0},
		{name: "free task cannot choose wallet implicitly", target: 150, freeSource: true, wantError: "no paid funding source"},
		{name: "wallet insufficient", target: 150, walletShort: true, wantError: "wallet quota insufficient"},
		{name: "token insufficient", target: 150, tokenShort: true, wantError: "token quota insufficient"},
		{name: "unlimited token", target: 150, unlimited: true},
		{name: "original subscription", target: 150, subscription: true},
		{name: "subscription insufficient", target: 150, subscription: true, subShort: true, wantError: "subscription quota insufficient"},
		{name: "stale claim", target: 150, stale: true, wantError: "claim is no longer current"},
		{name: "expired token", target: 150, expiredToken: true, wantError: "token is no longer usable"},
		{name: "refund disabled user and token", target: 0, disabledRefund: true},
		{name: "refund expired subscription", target: 0, subscription: true, expiredSub: true},
		{name: "unflushed wallet usage", target: 150, redisWallet: true, wantError: "wallet quota insufficient"},
		{name: "unflushed token usage compensates wallet", target: 150, redisToken: true, wantError: "token quota insufficient"},
		{name: "persistence failure rolls back all balances", target: 150, persistFailure: true, wantError: "injected task persistence failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("vretryquota_%d", index), AffCode: fmt.Sprintf("vretryquota_%d", index), Status: common.UserStatusEnabled, Quota: 200, AuthVersion: 1}
			if tc.walletShort {
				user.Quota = 20
			}
			if tc.disabledRefund {
				user.Status = common.UserStatusDisabled
			}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Name: "video retry quota test", Key: fmt.Sprintf("video-retry-quota-test-token-%d", index), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 200, UsedQuota: 100}
			if tc.tokenShort {
				token.RemainQuota = 20
			}
			if tc.unlimited {
				token.UnlimitedQuota, token.RemainQuota = true, 0
			}
			if tc.expiredToken {
				token.ExpiredTime = time.Now().Add(-time.Minute).Unix()
			}
			if tc.disabledRefund {
				token.Status = common.TokenStatusDisabled
			}
			require.NoError(t, db.Create(&token).Error)
			task := model.Task{TaskID: fmt.Sprintf("video-retry-quota-%d", index), UserId: user.Id, TokenId: token.Id, Quota: 100, Status: model.TaskStatusInProgress,
				PrivateData: model.TaskPrivateData{BillingSource: service.BillingSourceWallet, TokenId: token.Id, UsageAccountingMode: model.TaskUsageAccountingFinal,
					VideoRetry: &model.VideoTaskRetryState{Ready: true, Submitting: true, Revision: 2}}}
			if tc.freeSource {
				task.Quota, task.PrivateData.BillingSource = 0, ""
			}
			initialQuota := task.Quota
			subscription := model.UserSubscription{UserId: user.Id, AmountTotal: 300, AmountUsed: 100, Status: "active", EndTime: time.Now().Add(time.Hour).Unix()}
			if tc.subscription {
				if tc.subShort {
					subscription.AmountTotal = 120
				}
				if tc.expiredSub {
					subscription.Status, subscription.EndTime = "expired", time.Now().Add(-time.Minute).Unix()
				}
				require.NoError(t, db.Create(&subscription).Error)
				task.PrivateData.BillingSource, task.PrivateData.SubscriptionId = service.BillingSourceSubscription, subscription.Id
			}
			require.NoError(t, db.Create(&task).Error)
			if tc.stale {
				task.PrivateData.VideoRetry.Revision--
			}
			useRedis := tc.redisWallet || tc.redisToken || tc.persistFailure
			if useRedis {
				server := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: server.Addr()})
				common.RedisEnabled, common.RDB = true, client
				t.Cleanup(func() { common.RedisEnabled = false; require.NoError(t, client.Close()) })
				_, err := model.GetUserCache(user.Id)
				require.NoError(t, err)
				_, err = model.GetTokenByKey(token.Key, true)
				require.NoError(t, err)
				if tc.redisWallet {
					require.NoError(t, client.HIncrBy(context.Background(), fmt.Sprintf("user:%d", user.Id), "Quota", -180).Err())
				}
				if tc.redisToken {
					require.NoError(t, client.HIncrBy(context.Background(), "token:"+common.GenerateHMAC(token.Key), "RemainQuota", -180).Err())
				}
			}
			if tc.persistFailure {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("video_retry_quota_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
						tx.AddError(errors.New("injected task persistence failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("video_retry_quota_failure")) })
			}
			err := model.SetVideoTaskRetryQuota(context.Background(), &task, tc.target)
			wantQuota := tc.target
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				wantQuota = initialQuota
			} else {
				require.NoError(t, err)
			}
			delta := wantQuota - initialQuota
			var savedTask model.Task
			var savedUser model.User
			var savedToken model.Token
			require.NoError(t, db.First(&savedTask, task.ID).Error)
			require.NoError(t, db.First(&savedUser, user.Id).Error)
			require.NoError(t, db.First(&savedToken, token.Id).Error)
			assert.Equal(t, wantQuota, savedTask.Quota)
			assert.Equal(t, wantQuota, task.Quota)
			assert.Equal(t, token.RemainQuota-delta, savedToken.RemainQuota)
			assert.Equal(t, token.UsedQuota+delta, savedToken.UsedQuota)
			assert.Zero(t, savedUser.UsedQuota, "retry reservation is not a second consumption")
			assert.Zero(t, savedUser.RequestCount)
			walletDelta := delta
			if tc.subscription {
				walletDelta = 0
				var savedSubscription model.UserSubscription
				require.NoError(t, db.First(&savedSubscription, subscription.Id).Error)
				assert.Equal(t, int64(100+delta), savedSubscription.AmountUsed)
			}
			assert.Equal(t, user.Quota-walletDelta, savedUser.Quota)
			if useRedis {
				cachedUser, err := model.GetUserCache(user.Id)
				require.NoError(t, err)
				cachedToken, err := model.GetTokenByKey(token.Key, false)
				require.NoError(t, err)
				wantUserCache, wantTokenCache := user.Quota-walletDelta, token.RemainQuota-delta
				if tc.redisWallet {
					wantUserCache -= 180
				}
				if tc.redisToken {
					wantTokenCache -= 180
				}
				assert.Equal(t, wantUserCache, cachedUser.Quota)
				assert.Equal(t, wantTokenCache, cachedToken.RemainQuota)
				assert.Equal(t, token.UsedQuota+delta, cachedToken.UsedQuota)
			}
		})
	}
}
