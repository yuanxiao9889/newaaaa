package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func GetProfitSummary(c *gin.Context) {
	report, err := service.GetProfitReport(parseProfitQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func GetProfitBreakdown(c *gin.Context) {
	report, err := service.GetProfitReport(parseProfitQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"summary":   report.Summary,
		"breakdown": report.Breakdown,
		"series":    report.Series,
	})
}

func parseProfitQuery(c *gin.Context) service.ProfitQuery {
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	return service.ProfitQuery{
		Range:          service.ProfitRange(c.DefaultQuery("range", "day")),
		StartTimestamp: start,
		EndTimestamp:   end,
		ChannelId:      channelId,
		ModelName:      c.Query("model_name"),
	}
}

func ListProfitCostPrices(c *gin.Context) {
	reveal := c.Query("reveal") == "true"
	items, err := model.ListProfitCostPrices(reveal)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func GetProfitCostPricePrefill(c *gin.Context) {
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	if channelId <= 0 {
		common.ApiErrorMsg(c, "channel is required")
		return
	}
	prefill, err := service.GetProfitCostPricePrefill(channelId, c.Query("model_name"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, prefill)
}

func SaveProfitCostPrice(c *gin.Context) {
	var req struct {
		ChannelId      int     `json:"channel_id"`
		ModelName      string  `json:"model_name"`
		PriceType      string  `json:"price_type"`
		PriceValue     string  `json:"price_value"`
		FixedUnit      string  `json:"fixed_unit"`
		FixedAmount    float64 `json:"fixed_amount"`
		InputPrice     float64 `json:"input_price"`
		CacheReadPrice float64 `json:"cache_read_price"`
		OutputPrice    float64 `json:"output_price"`
		RequestPrice   float64 `json:"request_price"`
		SecondPrice    float64 `json:"second_price"`
		EffectiveFrom  int64   `json:"effective_from"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	version, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:      req.ChannelId,
		ModelName:      req.ModelName,
		PriceType:      req.PriceType,
		PriceValue:     req.PriceValue,
		FixedUnit:      req.FixedUnit,
		FixedAmount:    req.FixedAmount,
		InputPrice:     req.InputPrice,
		CacheReadPrice: req.CacheReadPrice,
		OutputPrice:    req.OutputPrice,
		RequestPrice:   req.RequestPrice,
		SecondPrice:    req.SecondPrice,
		EffectiveFrom:  req.EffectiveFrom,
		CreatedBy:      c.GetInt("id"),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, version)
}

func DeleteProfitCostPrice(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := model.DisableProfitCostPrice(id, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func GetProfitPasswordStatus(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"configured": middleware.ProfitPasswordHash() != "",
	})
}

func SetProfitPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	password := strings.TrimSpace(req.Password)
	if password == "" {
		common.ApiErrorMsg(c, "password is required")
		return
	}
	hash, err := common.Password2Hash(password)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption(middleware.ProfitVerificationPasswordHashOptionKey, hash); err != nil {
		common.ApiError(c, err)
		return
	}
	setProfitVerifiedSession(c)
	common.ApiSuccess(c, gin.H{"configured": true})
}

func VerifyProfitPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	hash := middleware.ProfitPasswordHash()
	if hash == "" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "请先配置利润页密码",
			"code":    "PROFIT_PASSWORD_NOT_CONFIGURED",
		})
		return
	}
	if !common.ValidatePasswordAndHash(req.Password, hash) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "利润页密码错误",
		})
		return
	}
	setProfitVerifiedSession(c)
	common.ApiSuccess(c, gin.H{"verified": true})
}

func setProfitVerifiedSession(c *gin.Context) {
	session := sessions.Default(c)
	session.Set(middleware.ProfitVerificationSessionKey, time.Now().Unix())
	_ = session.Save()
}
