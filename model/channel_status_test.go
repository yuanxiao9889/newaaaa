package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func setupChannelStatusTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
	})
}

func TestUpdateChannelStatusPersistsMultiKeyState(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "multi-key-status",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, "provider rejected key", stored.ChannelInfo.MultiKeyDisabledReason[0])
	assert.NotZero(t, stored.ChannelInfo.MultiKeyDisabledTime[0])
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
}

func TestSaveStatusStateFromSingleKeySnapshotPreservesUnownedColumns(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:        "single-key-status",
		Key:         "original-key",
		Status:      common.ChannelStatusEnabled,
		Models:      "original-model",
		Group:       "default",
		UsedQuota:   100,
		ChannelInfo: ChannelInfo{},
	}
	require.NoError(t, DB.Create(&channel).Error)

	stale, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)

	concurrentChannelInfo := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         2,
		MultiKeyMode:         constant.MultiKeyModePolling,
		MultiKeyPollingIndex: 1,
	}
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"key":          "rotated-key",
		"used_quota":   gorm.Expr("used_quota + ?", 250),
		"models":       "concurrent-model",
		"channel_info": concurrentChannelInfo,
	}).Error)

	stale.Status = common.ChannelStatusManuallyDisabled
	stale.SetOtherInfo(map[string]any{
		"status_reason": "manual operation",
		"status_time":   int64(1234),
	})
	require.NoError(t, stale.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(350), stored.UsedQuota)
	assert.Equal(t, "concurrent-model", stored.Models)
	assert.Equal(t, concurrentChannelInfo, stored.ChannelInfo)

	otherInfo := stored.GetOtherInfo()
	assert.Equal(t, "manual operation", otherInfo["status_reason"])
	assert.Equal(t, float64(1234), otherInfo["status_time"])
}

func TestChannelAutoSwitchDatabase(t *testing.T) {
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(filepath.Join(t.TempDir(), "auto-switch.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("as_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	oldDB := DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB = db
	common.SetDatabaseTypes(dialect, oldLog)
	t.Cleanup(func() {
		DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		require.NoError(t, db.Migrator().DropTable(&ChannelAutoSwitchAttempt{}, &ChannelAutoSwitchState{}, &Ability{}, &Channel{}))
		require.NoError(t, sqlDB.Close())
	})
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)

	// These persisted Channel/Ability fields and tags are unchanged from release
	// v1.0.0-rc.41. First verify a fresh schema, then an upgrade from that schema.
	require.NoError(t, db.AutoMigrate(&ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
	require.NoError(t, db.AutoMigrate(&ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
	require.NoError(t, db.Migrator().DropTable(&ChannelAutoSwitchAttempt{}, &ChannelAutoSwitchState{}))
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	priority := int64(10)
	legacy := Channel{Id: 163, Name: "primary", Key: "fixture-key", Models: "video-903", Group: "default", Priority: &priority, OtherSettings: `{"force_format":true}`, UsedQuota: 1234}
	require.NoError(t, db.Create(&legacy).Error)
	ability := Ability{ChannelId: legacy.Id, Model: "video-903", Group: "default", Enabled: true, Priority: &priority}
	require.NoError(t, db.Create(&ability).Error)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
	var preserved Channel
	require.NoError(t, db.First(&preserved, legacy.Id).Error)
	assert.Equal(t, legacy.Priority, preserved.Priority)
	assert.Equal(t, legacy.OtherSettings, preserved.OtherSettings)
	assert.Equal(t, legacy.UsedQuota, preserved.UsedQuota)
	assert.Error(t, db.Create(&ability).Error, "upgrade preserves ability uniqueness")
	assert.True(t, db.Migrator().HasIndex(&Channel{}, "Name"))
	assert.True(t, db.Migrator().HasIndex(&Ability{}, "ChannelId"))
	assert.True(t, db.Migrator().HasIndex(&ChannelAutoSwitchAttempt{}, "idx_auto_switch_window"))

	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Run("failure fallback probes and restoration", func(t *testing.T) {
		states, err := GetChannelAutoSwitchStates([]int{163, 174}, "default", "video-903", "/v1/videos")
		require.NoError(t, err)
		assert.Empty(t, states, "read-only inspection does not create health state")
		late, err := BeginChannelAutoSwitchAttempt(163, "default", "video-903", "/v1/videos", now)
		require.NoError(t, err)
		require.NotNil(t, late)
		for i := range 3 {
			attempt := recordAutoSwitchTestAttempt(t, 163, "video-903", now.Add(time.Duration(i)*time.Second), ChannelAutoSwitchFailure)
			require.NoError(t, RecordChannelAutoSwitchResult(*attempt, ChannelAutoSwitchFailure, "duplicate", now.Add(3*time.Second)))
		}
		state := readAutoSwitchTestState(t, 163, "video-903")
		assert.Equal(t, ChannelAutoSwitchCooling, state.Status)
		assert.Equal(t, int64(3), state.WindowTotal)
		assert.Equal(t, int64(3), state.WindowFailures)
		assert.Equal(t, int64(300), state.CooldownSeconds)
		require.NoError(t, RecordChannelAutoSwitchResult(*late, ChannelAutoSwitchSuccess, "", now.Add(4*time.Second)))
		assert.Equal(t, ChannelAutoSwitchCooling, readAutoSwitchTestState(t, 163, "video-903").Status)
		blocked, err := BeginChannelAutoSwitchAttempt(163, "default", "video-903", "/v1/videos", now.Add(time.Minute))
		require.NoError(t, err)
		assert.Nil(t, blocked)
		standby := recordAutoSwitchTestAttempt(t, 174, "video-903", now.Add(time.Minute), ChannelAutoSwitchSuccess)
		assert.False(t, standby.Probe)

		probeAt := time.Unix(state.CooldownUntil, 0)
		var tickets [2]*ChannelAutoSwitchTicket
		var errs [2]error
		var workers sync.WaitGroup
		start := make(chan struct{})
		for i := range 2 {
			workers.Go(func() {
				<-start
				tickets[i], errs[i] = BeginChannelAutoSwitchAttempt(163, "default", "video-903", "/v1/videos", probeAt)
			})
		}
		close(start)
		workers.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		var owned *ChannelAutoSwitchTicket
		for _, ticket := range tickets {
			if ticket != nil {
				require.Nil(t, owned, "only one concurrent caller can own the recovery probe")
				owned = ticket
			}
		}
		require.NotNil(t, owned)
		assert.True(t, owned.Probe)
		require.NoError(t, ReleaseChannelAutoSwitchAttempt(*owned, probeAt))
		for i := range 3 {
			probe := recordAutoSwitchTestAttempt(t, 163, "video-903", probeAt.Add(time.Duration(i)*time.Second), ChannelAutoSwitchSuccess)
			assert.True(t, probe.Probe)
			require.NoError(t, RecordChannelAutoSwitchResult(*probe, ChannelAutoSwitchSuccess, "", probeAt.Add(10*time.Second)))
			state = readAutoSwitchTestState(t, 163, "video-903")
			if i < 2 {
				assert.Equal(t, ChannelAutoSwitchProbing, state.Status)
				assert.Equal(t, i+1, state.ProbeSuccesses)
			} else {
				assert.Equal(t, ChannelAutoSwitchHealthy, state.Status)
				assert.Zero(t, state.CooldownUntil)
			}
		}
		restored := recordAutoSwitchTestAttempt(t, 163, "video-903", probeAt.Add(time.Minute), ChannelAutoSwitchSuccess)
		assert.False(t, restored.Probe, "normal routing resumes only after three completed probes")
		require.NoError(t, db.First(&preserved, legacy.Id).Error)
		assert.Equal(t, legacy.Priority, preserved.Priority, "health never rewrites manual priority")
	})

	t.Run("window threshold and scope isolation", func(t *testing.T) {
		for i := range 10 {
			outcome := ChannelAutoSwitchSuccess
			if i%2 == 1 {
				outcome = ChannelAutoSwitchFailure
			}
			recordAutoSwitchTestAttempt(t, 163, "ratio", now.Add(time.Duration(i)*time.Second), outcome)
		}
		state := readAutoSwitchTestState(t, 163, "ratio")
		assert.Equal(t, ChannelAutoSwitchCooling, state.Status)
		assert.Equal(t, int64(10), state.WindowTotal)
		assert.Equal(t, int64(5), state.WindowFailures)
		for _, scope := range []struct{ group, model, protocol string }{
			{"another-group", "ratio", "/v1/videos"}, {"default", "other-model", "/v1/videos"}, {"default", "ratio", "/v1/images"},
			{"default", "RATIO", "/v1/videos"},
		} {
			ticket, err := BeginChannelAutoSwitchAttempt(163, scope.group, scope.model, scope.protocol, now)
			require.NoError(t, err)
			require.NotNil(t, ticket)
			assert.False(t, ticket.Probe)
		}
		assert.Equal(t, ChannelAutoSwitchCooling, readAutoSwitchTestState(t, 163, "ratio").Status)
		assert.Equal(t, ChannelAutoSwitchHealthy, readAutoSwitchTestState(t, 163, "RATIO").Status)
		for i := range 3 {
			recordAutoSwitchTestAttempt(t, 163, "old-failures", now.Add(time.Duration(i)*11*time.Minute), ChannelAutoSwitchFailure)
		}
		state = readAutoSwitchTestState(t, 163, "old-failures")
		assert.Equal(t, ChannelAutoSwitchHealthy, state.Status)
		assert.Equal(t, 1, state.ConsecutiveFailures)
		assert.Equal(t, int64(1), state.WindowTotal)
		for _, outcome := range []ChannelAutoSwitchOutcome{ChannelAutoSwitchFailure, ChannelAutoSwitchSuccess, ChannelAutoSwitchFailure, ChannelAutoSwitchFailure} {
			recordAutoSwitchTestAttempt(t, 163, "simultaneous-order", now, outcome)
		}
		state = readAutoSwitchTestState(t, 163, "simultaneous-order")
		assert.Equal(t, ChannelAutoSwitchHealthy, state.Status)
		assert.Equal(t, 2, state.ConsecutiveFailures, "success resets the streak even when every completion shares a timestamp")
		for _, seconds := range []int{0, 599, 601, 602} {
			recordAutoSwitchTestAttempt(t, 163, "sliding-streak", now.Add(time.Duration(seconds)*time.Second), ChannelAutoSwitchFailure)
		}
		state = readAutoSwitchTestState(t, 163, "sliding-streak")
		assert.Equal(t, ChannelAutoSwitchCooling, state.Status, "expiring the first failure must preserve the three recent consecutive failures")
	})

	t.Run("failed probes back off and unknown results never heal", func(t *testing.T) {
		for range 3 {
			recordAutoSwitchTestAttempt(t, 163, "backoff", now, ChannelAutoSwitchFailure)
		}
		for _, seconds := range []int64{600, 1200, 1800, 1800} {
			state := readAutoSwitchTestState(t, 163, "backoff")
			probeAt := time.Unix(state.CooldownUntil, 0)
			recordAutoSwitchTestAttempt(t, 163, "backoff", probeAt, ChannelAutoSwitchSuccess)
			recordAutoSwitchTestAttempt(t, 163, "backoff", probeAt.Add(time.Second), ChannelAutoSwitchFailure)
			state = readAutoSwitchTestState(t, 163, "backoff")
			assert.Equal(t, ChannelAutoSwitchCooling, state.Status)
			assert.Zero(t, state.ProbeSuccesses)
			assert.Equal(t, seconds, state.CooldownSeconds)
		}
		state := readAutoSwitchTestState(t, 163, "backoff")
		probeAt := time.Unix(state.CooldownUntil, 0)
		recordAutoSwitchTestAttempt(t, 163, "backoff", probeAt, ChannelAutoSwitchSuccess)
		recordAutoSwitchTestAttempt(t, 163, "backoff", probeAt.Add(time.Second), ChannelAutoSwitchNeutral)
		state = readAutoSwitchTestState(t, 163, "backoff")
		assert.Equal(t, ChannelAutoSwitchCooling, state.Status)
		assert.Zero(t, state.ProbeSuccesses)
		probe, err := BeginChannelAutoSwitchAttempt(163, "default", "backoff", "/v1/videos", time.Unix(state.CooldownUntil, 0))
		require.NoError(t, err)
		require.NotNil(t, probe)
		state = readAutoSwitchTestState(t, 163, "backoff")
		expiredAt := time.Unix(state.ProbeDeadline, 0)
		assert.True(t, state.CanProbe(expiredAt))
		replacement, err := BeginChannelAutoSwitchAttempt(163, "default", "backoff", "/v1/videos", expiredAt)
		require.NoError(t, err)
		assert.Nil(t, replacement, "orphan expiry schedules cooldown instead of immediate resubmission")
		require.NoError(t, RecordChannelAutoSwitchResult(*probe, ChannelAutoSwitchSuccess, "", expiredAt.Add(time.Second)))
		assert.Equal(t, ChannelAutoSwitchCooling, readAutoSwitchTestState(t, 163, "backoff").Status)
	})

	t.Run("reset migration and pruning fence old observations", func(t *testing.T) {
		old, err := BeginChannelAutoSwitchAttempt(174, "default", "reset", "/v1/videos", now)
		require.NoError(t, err)
		require.NotNil(t, old)
		require.NoError(t, ResetChannelAutoSwitch(174))
		require.NoError(t, RecordChannelAutoSwitchResult(*old, ChannelAutoSwitchFailure, "late", now.Add(time.Second)))
		state := readAutoSwitchTestState(t, 174, "reset")
		assert.Zero(t, state.WindowTotal)
		assert.Equal(t, ChannelAutoSwitchHealthy, state.Status)
		current := recordAutoSwitchTestAttempt(t, 174, "reset", now.Add(2*time.Second), ChannelAutoSwitchSuccess)
		before := readAutoSwitchTestState(t, 174, "reset")
		require.NoError(t, db.AutoMigrate(&ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
		require.NoError(t, db.AutoMigrate(&ChannelAutoSwitchState{}, &ChannelAutoSwitchAttempt{}))
		assert.Equal(t, before, readAutoSwitchTestState(t, 174, "reset"), "restart migration preserves circuit and generation")
		assert.Error(t, db.Create(&before).Error, "state identity remains unique")
		var storedAttempt ChannelAutoSwitchAttempt
		require.NoError(t, db.First(&storedAttempt, "id = ?", current.ID).Error)
		assert.Error(t, db.Create(&storedAttempt).Error, "attempt identity remains unique")
		require.NoError(t, PruneChannelAutoSwitchAttempts(now.Add(24*time.Hour)))
		var remaining int64
		require.NoError(t, db.Model(&ChannelAutoSwitchAttempt{}).Where("id = ?", current.ID).Count(&remaining).Error)
		assert.Zero(t, remaining)
		require.NoError(t, RecordChannelAutoSwitchResult(*current, ChannelAutoSwitchFailure, "duplicate after pruning", now.Add(25*time.Hour)))
		after := readAutoSwitchTestState(t, 174, "reset")
		assert.Equal(t, before.Status, after.Status)
		assert.Equal(t, before.Generation, after.Generation)
		assert.Equal(t, before.WindowTotal, after.WindowTotal)
		assert.Equal(t, before.WindowFailures, after.WindowFailures)
	})

	t.Run("configuration toggle and health reset commit together", func(t *testing.T) {
		before := readAutoSwitchTestState(t, 163, "video-903")
		patch := Channel{Id: 163, OtherSettings: `{"auto_switch_enabled":true,"force_format":true}`}
		require.NoError(t, patch.Update())
		state := readAutoSwitchTestState(t, 163, "video-903")
		assert.Greater(t, state.Generation, before.Generation)
		assert.Equal(t, ChannelAutoSwitchHealthy, state.Status)
		assert.Zero(t, state.WindowTotal)
		omittedSettings := Channel{Id: 163, Name: "renamed primary"}
		require.NoError(t, omittedSettings.Update())
		assert.Equal(t, state.Generation, readAutoSwitchTestState(t, 163, "video-903").Generation)
		require.NoError(t, db.First(&preserved, 163).Error)
		assert.True(t, preserved.GetOtherSettings().AutoSwitchEnabled)
		assert.Equal(t, legacy.Priority, preserved.Priority)
		failure := errors.New("injected health reset failure")
		const callbackName = "test:auto_switch_reset_failure"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			if _, ok := tx.Statement.Model.(*ChannelAutoSwitchState); ok {
				tx.AddError(failure)
			}
		}))
		failedPatch := Channel{Id: 163, OtherSettings: `{"auto_switch_enabled":false}`}
		assert.ErrorIs(t, failedPatch.Update(), failure)
		require.NoError(t, db.Callback().Update().Remove(callbackName))
		require.NoError(t, db.First(&preserved, 163).Error)
		assert.True(t, preserved.GetOtherSettings().AutoSwitchEnabled, "failed health reset rolls back the saved toggle")
		assert.Equal(t, state.Generation, readAutoSwitchTestState(t, 163, "video-903").Generation)
		old, err := BeginChannelAutoSwitchAttempt(163, "default", "video-903", "/v1/videos", now)
		require.NoError(t, err)
		require.NotNil(t, old)
		require.NoError(t, failedPatch.Update())
		require.NoError(t, RecordChannelAutoSwitchResult(*old, ChannelAutoSwitchFailure, "late", now.Add(time.Second)))
		state = readAutoSwitchTestState(t, 163, "video-903")
		assert.Zero(t, state.WindowFailures, "disabling invalidates previously accepted task observations")
		var abilityAfter Ability
		require.NoError(t, db.Where("channel_id = ?", 163).First(&abilityAfter).Error)
		assert.Equal(t, ability.Priority, abilityAfter.Priority)
		assert.Equal(t, ability.Enabled, abilityAfter.Enabled)
	})

	t.Run("configured membership includes disabled compatible channels", func(t *testing.T) {
		exact := Channel{Id: 201, Name: "configured disabled exact", Key: "fixture-key", Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4-gizmo-example", Group: "membership", OtherSettings: `{"auto_switch_enabled":true}`}
		fallback := Channel{Id: 202, Name: "configured fallback", Key: "fixture-key", Status: common.ChannelStatusEnabled, Models: "gpt-4-gizmo-*", Group: "membership", OtherSettings: `{"auto_switch_enabled":true}`}
		for _, channel := range []*Channel{&exact, &fallback} {
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, channel.AddAbilities(db))
		}
		filters := []dto.ChannelFilter{{Kind: dto.FilterChannelIdentity}}
		configured, err := GetConfiguredChannelsForModel("membership", "gpt-4-gizmo-example", filters)
		require.NoError(t, err)
		require.Len(t, configured, 1, "compatible exact configuration wins over normalized fallback")
		assert.Equal(t, exact.Id, configured[0].Id)
		assert.Equal(t, common.ChannelStatusManuallyDisabled, configured[0].Status, "inventory must not disguise disabled channels as routable")
		filters[0].ExcludedChannelIDs = []int{exact.Id}
		configured, err = GetConfiguredChannelsForModel("membership", "gpt-4-gizmo-example", filters)
		require.NoError(t, err)
		require.Len(t, configured, 1)
		assert.Equal(t, fallback.Id, configured[0].Id, "filters apply before normalized fallback")
		configured, err = GetConfiguredChannelsForModel("other-membership", "gpt-4-gizmo-example", nil)
		require.NoError(t, err)
		assert.Empty(t, configured)
		malformed := "{malformed-json"
		require.NoError(t, db.Model(&exact).Update("setting", malformed).Error)
		configured, err = GetConfiguredChannelsForModel("membership", "gpt-4-gizmo-example", nil)
		require.NoError(t, err)
		require.Len(t, configured, 1)
		assert.Equal(t, fallback.Id, configured[0].Id)
		var original Channel
		require.NoError(t, db.First(&original, exact.Id).Error)
		require.NotNil(t, original.Setting)
		assert.Equal(t, malformed, *original.Setting, "membership queries do not repair settings or enable a disabled channel")
		assert.Equal(t, common.ChannelStatusManuallyDisabled, original.Status)
	})
}

func recordAutoSwitchTestAttempt(t *testing.T, channelID int, model string, now time.Time, outcome ChannelAutoSwitchOutcome) *ChannelAutoSwitchTicket {
	t.Helper()
	ticket, err := BeginChannelAutoSwitchAttempt(channelID, "default", model, "/v1/videos", now)
	require.NoError(t, err)
	require.NotNil(t, ticket)
	require.NoError(t, RecordChannelAutoSwitchResult(*ticket, outcome, "upstream_unavailable", now))
	return ticket
}

func readAutoSwitchTestState(t *testing.T, channelID int, model string) ChannelAutoSwitchState {
	t.Helper()
	states, err := GetChannelAutoSwitchStates([]int{channelID}, "default", model, "/v1/videos")
	require.NoError(t, err)
	state, found := states[channelID]
	require.True(t, found)
	return state
}
