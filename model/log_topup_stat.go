package model

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

// TopupLogFilter uses the same filters as the log list. UserID is supplied by
// the server for self views, never taken from a query parameter.
type TopupLogFilter struct {
	UserID            int
	StartTimestamp    int64
	EndTimestamp      int64
	Username          string
	ModelName         string
	TokenName         string
	Group             string
	RequestID         string
	UpstreamRequestID string
	Channel           int
}

type TopupStatistics struct {
	Quota     string            `json:"quota"`
	Amounts   map[string]string `json:"amounts"`
	Uncounted int               `json:"uncounted"`
}

// Historical top-ups stored the credited amount only in text. Preserve its
// currency instead of guessing the exchange rate in effect when it was written.
var legacyTopupAmount = regexp.MustCompile(`^(?:使用在线充值成功，充值金额[:：]\s*|管理员补单成功，充值金额[:：]\s*|使用Creem充值成功，充值额度[:：]\s*|Waffo(?: Pancake)?充值成功，充值额度[:：]\s*|通过兑换码充值\s+)([^0-9\s+\-，,:：]*)([0-9]+(?:\.[0-9]+)?)(?:\s*(?:点额度|额度))?(?:，|$)`)

// topupCredit returns raw quota (empty currency), a historical currency amount,
// or an unrecognized record. Decreases and subscription purchases add no credit.
func topupCredit(log Log) (decimal.Decimal, string, bool) {
	// Decode only numeric fields: operation parameters also contain usernames.
	var operation struct {
		Op struct {
			Action string                     `json:"action"`
			Params map[string]json.RawMessage `json:"params"`
		} `json:"op"`
	}
	if log.Other != "" && common.UnmarshalJsonStr(log.Other, &operation) == nil {
		params := make(map[string]decimal.Decimal)
		for _, key := range []string{"from", "to", "quota", "requested_quota"} {
			value := operation.Op.Params[key]
			if len(value) > 0 && common.GetJsonType(value) == "number" {
				amount, err := decimal.NewFromString(string(value))
				if err == nil {
					params[key] = amount
				}
			}
		}
		switch operation.Op.Action {
		case "user.quota_subtract":
			return decimal.Zero, "", true
		case "user.quota_add", "user.quota_override":
			before, hasBefore := params["from"]
			after, hasAfter := params["to"]
			if hasBefore && hasAfter {
				return decimal.Max(after.Sub(before), decimal.Zero), "", true
			}
			if operation.Op.Action == "user.quota_add" {
				for _, key := range []string{"quota", "requested_quota"} {
					if amount, ok := params[key]; ok && !amount.IsNegative() {
						return amount, "", true
					}
				}
			}
			return decimal.Zero, "", false
		}
	}
	if log.Quota > 0 {
		return decimal.NewFromInt(int64(log.Quota)), "", true
	}
	if strings.HasPrefix(log.Content, "订阅购买成功，") || strings.HasPrefix(log.Content, "使用余额购买订阅成功，") {
		return decimal.Zero, "", true
	}
	match := legacyTopupAmount.FindStringSubmatch(log.Content)
	if match == nil {
		return decimal.Zero, "", false
	}
	amount, err := decimal.NewFromString(match[2])
	if err != nil {
		return decimal.Zero, "", false
	}
	currency := match[1]
	if currency == "＄" {
		currency = "$"
	}
	return amount, currency, true
}

func SumTopupLogs(ctx context.Context, filter TopupLogFilter) (TopupStatistics, error) {
	stat := TopupStatistics{Quota: "0", Amounts: map[string]string{}}
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).Select("quota", "content", "other").Where("type = ?", LogTypeTopup)
	if filter.UserID != 0 {
		tx = tx.Where("user_id = ?", filter.UserID)
	}
	var err error
	for column, value := range map[string]string{"username": filter.Username, "model_name": filter.ModelName} {
		tx, err = applyExplicitLogTextFilter(tx, column, value)
		if err != nil {
			return stat, err
		}
	}
	for column, value := range map[string]string{"token_name": filter.TokenName, logGroupCol: filter.Group, "request_id": filter.RequestID, "upstream_request_id": filter.UpstreamRequestID} {
		if value != "" {
			tx = tx.Where(column+" = ?", value)
		}
	}
	if filter.StartTimestamp != 0 {
		tx = tx.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		tx = tx.Where("created_at <= ?", filter.EndTimestamp)
	}
	if filter.Channel != 0 {
		tx = tx.Where("channel_id = ?", filter.Channel)
	}
	rows, err := tx.Rows()
	if err != nil {
		return stat, err
	}
	defer rows.Close()
	totals := map[string]decimal.Decimal{}
	for rows.Next() {
		var log Log
		if err := LOG_DB.ScanRows(rows, &log); err != nil {
			return stat, err
		}
		amount, currency, known := topupCredit(log)
		if !known {
			stat.Uncounted++
			continue
		}
		totals[currency] = totals[currency].Add(amount)
	}
	if err := rows.Err(); err != nil {
		return stat, err
	}
	stat.Quota = totals[""].String()
	for currency, amount := range totals {
		if currency != "" {
			stat.Amounts[currency] = amount.String()
		}
	}
	return stat, nil
}
