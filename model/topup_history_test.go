package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserTopUpsCanQueryAllHistoryAndCalendarMonth(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 901, Username: "topup_history_user"}).Error)

	monthTime := func(month time.Month, day, hour, minute int) int64 {
		return time.Date(2024, month, day, hour, minute, 0, 0, time.Local).Unix()
	}
	records := []*TopUp{
		{UserId: 901, TradeNo: "JAN", CreateTime: monthTime(time.January, 31, 23, 59)},
		{UserId: 901, TradeNo: "FEB-1", CreateTime: monthTime(time.February, 1, 0, 0)},
		{UserId: 901, TradeNo: "FEB-29", CreateTime: monthTime(time.February, 29, 12, 0)},
	}
	require.NoError(t, DB.Create(&records).Error)

	all, total, err := GetUserTopUps(901, &common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, all, 3)

	february, total, err := GetUserTopUpsByMonth(901, &common.PageInfo{Page: 1, PageSize: 10}, "2024-02")
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"FEB-29", "FEB-1"}, []string{february[0].TradeNo, february[1].TradeNo})

	search, total, err := SearchUserTopUpsByMonth(901, "29", &common.PageInfo{Page: 1, PageSize: 10}, "2024-02")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "FEB-29", search[0].TradeNo)

	_, _, err = GetUserTopUpsByMonth(901, &common.PageInfo{Page: 1, PageSize: 10}, "2024-13")
	assert.True(t, errors.Is(err, ErrInvalidTopUpMonth))
}

func TestAllTopUpsCanFilterByCalendarMonthAndKeyword(t *testing.T) {
	truncateTables(t)
	monthTime := func(month time.Month, day, hour, minute int) int64 {
		return time.Date(2024, month, day, hour, minute, 0, 0, time.Local).Unix()
	}
	records := []*TopUp{
		{UserId: 901, TradeNo: "JAN-ORDER", CreateTime: monthTime(time.January, 31, 23, 59)},
		{UserId: 902, TradeNo: "FEB-ORDER-1", CreateTime: monthTime(time.February, 1, 0, 0)},
		{UserId: 903, TradeNo: "FEB-ORDER-2", CreateTime: monthTime(time.February, 29, 12, 0)},
	}
	require.NoError(t, DB.Create(&records).Error)

	all, total, err := GetAllTopUps(&common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, all, 3)

	february, total, err := GetAllTopUpsByMonth(&common.PageInfo{Page: 1, PageSize: 10}, "2024-02")
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"FEB-ORDER-2", "FEB-ORDER-1"}, []string{february[0].TradeNo, february[1].TradeNo})

	search, total, err := SearchAllTopUpsByMonth("2", &common.PageInfo{Page: 1, PageSize: 10}, "2024-02")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "FEB-ORDER-2", search[0].TradeNo)

	_, _, err = GetAllTopUpsByMonth(&common.PageInfo{Page: 1, PageSize: 10}, "2024-13")
	assert.ErrorIs(t, err, ErrInvalidTopUpMonth)
}
