package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchChannelsExactModelFilter(t *testing.T) {
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousMaster, previousSQLite := common.IsMasterNode, common.SQLitePath
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.SQLitePath = previousMaster, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel-search.db")
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.Channel{})) })
	var version string
	versionQuery := "SELECT version()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", common.MainDatabaseType(), version)

	tag := "image-channels"
	channels := []model.Channel{
		{Id: 1, Models: "all-image-2"},
		{Id: 2, Models: "all-image-2,other"},
		{Id: 3, Models: "other,all-image-2,another"},
		{Id: 4, Models: "other,all-image-2", Status: common.ChannelStatusManuallyDisabled, Type: 2},
		{Id: 5, Models: "all-image-2-H"},
		{Id: 6, Models: "all-image-2.5-sunburst--H"},
		{Id: 7, Models: "prefix-all-image-2"},
		{Id: 8, Models: "other"},
		{Id: 9, Models: "ALL-IMAGE-2", Status: common.ChannelStatusAutoDisabled},
		{Id: 10, Models: "image_%"},
		{Id: 11, Models: "image_X"},
	}
	for i := range channels {
		channels[i].Name = "image-channel-" + strconv.Itoa(channels[i].Id)
		channels[i].Key = "fixture-key"
		channels[i].Group = "default"
		channels[i].Tag = &tag
		if channels[i].Status == 0 {
			channels[i].Status = common.ChannelStatusEnabled
		}
		if channels[i].Type == 0 {
			channels[i].Type = 1
		}
	}
	require.NoError(t, db.Create(&channels).Error)

	for _, tagMode := range []bool{false, true} {
		t.Run("tag_mode="+strconv.FormatBool(tagMode), func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				model       string
				status      string
				channelType string
				page        string
				pageSize    string
				wantIDs     []int
				wantTotal   int
				wantCounts  map[int]int
			}{
				{name: "complete model matches every list position only", model: "all-image-2", wantIDs: []int{4, 3, 2, 1}, wantTotal: 4, wantCounts: map[int]int{1: 3, 2: 1}},
				{name: "suffix model remains separately searchable", model: "all-image-2-H", wantIDs: []int{5}, wantTotal: 1, wantCounts: map[int]int{1: 1}},
				{name: "partial name has no exact match", model: "all-image", wantIDs: []int{}, wantCounts: map[int]int{}},
				{name: "SQL wildcards match literally", model: "image_%", wantIDs: []int{10}, wantTotal: 1, wantCounts: map[int]int{1: 1}},
				{name: "pagination follows exact matching", model: "all-image-2", page: "2", pageSize: "2", wantIDs: []int{2, 1}, wantTotal: 4, wantCounts: map[int]int{1: 3, 2: 1}},
				{name: "enabled status combines with exact matching", model: "all-image-2", status: "enabled", wantIDs: []int{3, 2, 1}, wantTotal: 3, wantCounts: map[int]int{1: 3}},
				{name: "auto disabled excludes manual disabled", status: "auto_disabled", wantIDs: []int{9}, wantTotal: 1, wantCounts: map[int]int{1: 1}},
				{name: "numeric auto disabled status", status: "3", wantIDs: []int{9}, wantTotal: 1, wantCounts: map[int]int{1: 1}},
				{name: "disabled still includes manual and automatic", status: "disabled", wantIDs: []int{9, 4}, wantTotal: 2, wantCounts: map[int]int{1: 1, 2: 1}},
				{name: "type filter preserves exact type counts", model: "all-image-2", channelType: "2", wantIDs: []int{4}, wantTotal: 1, wantCounts: map[int]int{1: 3, 2: 1}},
				{name: "empty model leaves filtering disabled", wantIDs: []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, wantTotal: 11, wantCounts: map[int]int{1: 10, 2: 1}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					query := url.Values{
						"model": {tc.model}, "tag_mode": {strconv.FormatBool(tagMode)},
						"id_sort": {"true"}, "status": {tc.status}, "type": {tc.channelType},
					}
					if tc.page != "" {
						query.Set("p", tc.page)
						query.Set("page_size", tc.pageSize)
					}
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/search?"+query.Encode(), nil)
					SearchChannels(ctx)
					require.Equal(t, http.StatusOK, recorder.Code)
					var response struct {
						Success bool `json:"success"`
						Data    struct {
							Items      []*model.Channel `json:"items"`
							Total      int              `json:"total"`
							TypeCounts map[int]int      `json:"type_counts"`
						} `json:"data"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					require.True(t, response.Success, recorder.Body.String())
					ids := make([]int, 0, len(response.Data.Items))
					for _, channel := range response.Data.Items {
						ids = append(ids, channel.Id)
					}
					assert.Equal(t, tc.wantIDs, ids)
					assert.Equal(t, tc.wantTotal, response.Data.Total)
					assert.Equal(t, tc.wantCounts, response.Data.TypeCounts)
				})
			}
		})
	}
	for _, tagMode := range []bool{false, true} {
		for _, status := range []string{"auto_disabled", "disabled", "enabled", "all"} {
			t.Run("list/tag_mode="+strconv.FormatBool(tagMode)+"/"+status, func(t *testing.T) {
				query := url.Values{"status": {status}, "tag_mode": {strconv.FormatBool(tagMode)}, "id_sort": {"true"}, "page_size": {"100"}}
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/?"+query.Encode(), nil)
				GetAllChannels(ctx)
				require.Equal(t, http.StatusOK, recorder.Code)
				var response struct {
					Success bool
					Data    struct {
						Items      []model.Channel
						Total      int
						TypeCounts map[int]int `json:"type_counts"`
					}
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success, recorder.Body.String())
				wantIDs := []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
				wantCounts := map[int]int{1: 10, 2: 1}
				switch status {
				case "auto_disabled":
					wantIDs, wantCounts = []int{9}, map[int]int{1: 1}
				case "disabled":
					wantIDs, wantCounts = []int{9, 4}, map[int]int{1: 1, 2: 1}
				case "enabled":
					wantIDs, wantCounts = []int{11, 10, 8, 7, 6, 5, 3, 2, 1}, map[int]int{1: 9}
				}
				ids := make([]int, 0, len(response.Data.Items))
				for _, channel := range response.Data.Items {
					ids = append(ids, channel.Id)
				}
				assert.Equal(t, wantIDs, ids)
				assert.Equal(t, wantCounts, response.Data.TypeCounts)
				wantTotal := len(wantIDs)
				if tagMode {
					wantTotal = 1
				}
				assert.Equal(t, wantTotal, response.Data.Total)
			})
		}
	}
}

func TestBatchSetChannelAutoSwitch(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Channel{}, &model.Ability{}, &model.User{})
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "batch-audit.db")))
	require.NoError(t, err)
	conn, err := logDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, logDB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	oldMain, oldLogType, oldCache := common.MainDatabaseType(), common.LogDatabaseType(), common.MemoryCacheEnabled
	model.DB, model.LOG_DB, common.MemoryCacheEnabled = db, logDB, false
	common.SetDatabaseTypes(dialect, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.MemoryCacheEnabled = oldDB, oldLog, oldCache
		common.SetDatabaseTypes(oldMain, oldLogType)
	})
	for _, ch := range []model.Channel{
		{Id: 1, Type: 1, Status: common.ChannelStatusManuallyDisabled, OtherSettings: `{"future":{"large":9007199254740993},"auto_switch_max_attempts":7}`},
		{Id: 2, Type: 1, OtherSettings: `{"auto_switch_enabled":true,"auto_switch_retry_window_minutes":4,"auto_switch_max_attempts":2}`},
		{Id: 3, Type: 2},
	} {
		require.NoError(t, db.Create(&ch).Error)
	}
	router := gin.New()
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "batch-admin", Role: common.RoleRootUser}).Error)
	router.POST("/batch", func(c *gin.Context) { c.Set("id", 1); c.Set("role", common.RoleRootUser) }, BatchSetChannelAutoSwitch)
	post := func(body string, success bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(body)))
		require.Equal(t, 200, rec.Code)
		var response struct {
			Success bool `json:"success"`
			Data    int  `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.Equal(t, success, response.Success, rec.Body.String())
		if success {
			require.Equal(t, 2, response.Data)
		}
	}
	read := func(id int) model.Channel {
		t.Helper()
		var ch model.Channel
		require.NoError(t, db.First(&ch, id).Error)
		return ch
	}
	off := `{"ids":[2,1],"auto_switch_enabled":false}`
	post(off, true)
	ch1, ch2 := read(1), read(2)
	a, b := ch1.GetOtherSettings(), ch2.GetOtherSettings()
	assert.False(t, a.AutoSwitchEnabled)
	assert.False(t, b.AutoSwitchEnabled)
	assert.Nil(t, a.AutoSwitchRetryWindowMinutes)
	assert.Equal(t, 7, *a.AutoSwitchMaxAttempts)
	assert.Equal(t, 2, *b.AutoSwitchMaxAttempts)
	assert.Equal(t, 4, *b.AutoSwitchRetryWindowMinutes)
	valid := `{"ids":[2,1,1],"auto_switch_enabled":true,"auto_switch_retry_window_minutes":120,"auto_switch_max_attempts":10}`
	for _, invalid := range []string{strings.Replace(valid, "2,1,1", "1,999", 1), strings.Replace(valid, "2,1,1", "1,3", 1), strings.Replace(valid, "120", "121", 1), strings.Replace(valid, "120", "0", 1), strings.Replace(valid, "10}", "11}", 1), strings.Replace(valid, "10}", "1.5}", 1), strings.Replace(valid, "10}", "null}", 1), strings.Replace(valid, "true", "null", 1)} {
		post(invalid, false)
		assert.Equal(t, ch1.OtherSettings, read(1).OtherSettings)
		assert.Equal(t, ch2.OtherSettings, read(2).OtherSettings)
	}
	post(valid, true)
	post(valid, true)
	for _, id := range []int{1, 2} {
		ch := read(id)
		settings := ch.GetOtherSettings()
		assert.True(t, settings.AutoSwitchEnabled)
		assert.Equal(t, 120, *settings.AutoSwitchRetryWindowMinutes)
		assert.Equal(t, 10, *settings.AutoSwitchMaxAttempts)
	}
	assert.Contains(t, read(1).OtherSettings, "9007199254740993")
	assert.Equal(t, common.ChannelStatusManuallyDisabled, read(1).Status)
	assert.Empty(t, read(3).OtherSettings)
	post(off, true)
	for _, id := range []int{1, 2} {
		ch := read(id)
		settings := ch.GetOtherSettings()
		assert.False(t, settings.AutoSwitchEnabled)
		assert.Equal(t, 120, *settings.AutoSwitchRetryWindowMinutes)
		assert.Equal(t, 10, *settings.AutoSwitchMaxAttempts)
	}
	var audits int64
	require.NoError(t, logDB.Model(&model.AuditLog{}).Where("action = ?", "channel.auto_switch_batch_set").Count(&audits).Error)
	assert.EqualValues(t, 4, audits)
}
