package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelKeySuffix(t *testing.T) {
	previousDB := model.DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousSQLite := common.IsMasterNode, common.SQLitePath
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.SQLitePath = previousMaster, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel-suffix.db")
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	versionQuery := "SELECT version()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", common.MainDatabaseType(), version)

	for _, tc := range []struct {
		name, key, suffix string
		multi             bool
	}{
		{name: "only last four characters", key: "sk-private-prefix-A1B2", suffix: "A1B2"},
		{name: "trim surrounding whitespace", key: "  sk-private-prefix-C3D4\r\n", suffix: "C3D4"},
		{name: "unicode suffix", key: "private-甲乙丙丁", suffix: "甲乙丙丁"},
		{name: "empty key"},
		{name: "short key stays hidden", key: "abc"},
		{name: "four character key stays hidden", key: "1234"},
		{name: "multiple keys have no single suffix", key: "sk-first-A1B2\nsk-second-C3D4", multi: true},
		{name: "JSON credential stays hidden", key: `{"access_token":"sk-private-A1B2"}`},
		{name: "JSON credential array stays hidden", key: `[{"private_key":"sk-private-C3D4"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &model.Channel{Name: tc.name, Type: 1, Key: tc.key, ChannelInfo: model.ChannelInfo{IsMultiKey: tc.multi}}
			require.NoError(t, db.Create(channel).Error)
			t.Cleanup(func() { require.NoError(t, db.Delete(channel).Error) })
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/"+strconv.Itoa(channel.Id), nil)
			ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
			GetChannel(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					ID        int    `json:"id"`
					Key       string `json:"key"`
					KeySuffix string `json:"key_suffix"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.True(t, response.Success)
			assert.Equal(t, channel.Id, response.Data.ID)
			assert.Empty(t, response.Data.Key)
			assert.Equal(t, tc.suffix, response.Data.KeySuffix)
			assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			if tc.key != "" {
				encoded, err := common.Marshal(tc.key)
				require.NoError(t, err)
				assert.NotContains(t, recorder.Body.String(), string(encoded))
			}
			saved, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, tc.key, saved.Key)
		})
	}
}
