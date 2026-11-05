package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel-search.db")
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

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
		{Id: 9, Models: "ALL-IMAGE-2"},
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
}
