package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	ProfitPriceTypeSimpleToken = "simple_token"
	ProfitPriceTypeTieredExpr  = "tiered_expr"
	ProfitPriceTypeFixedPrice  = "fixed_price"
	ProfitFixedUnitRequest     = "request"
	ProfitFixedUnitSecond      = "second"
)

type ProfitCostPrice struct {
	Id               int    `json:"id"`
	ChannelId        int    `json:"channel_id" gorm:"type:int;not null;uniqueIndex:idx_profit_cost_channel_model"`
	ModelName        string `json:"model_name" gorm:"type:varchar(255);not null;uniqueIndex:idx_profit_cost_channel_model"`
	CurrentVersionId int    `json:"current_version_id" gorm:"type:int;not null;default:0"`
	Disabled         bool   `json:"disabled" gorm:"not null;default:false;index"`
	CreatedBy        int    `json:"created_by" gorm:"type:int;not null;default:0"`
	UpdatedBy        int    `json:"updated_by" gorm:"type:int;not null;default:0"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt        int64  `json:"updated_at" gorm:"autoUpdateTime"`
	DisabledAt       int64  `json:"disabled_at" gorm:"not null;default:0;index"`
}

type ProfitCostPriceVersion struct {
	Id              int     `json:"id"`
	CostPriceId     int     `json:"cost_price_id" gorm:"type:int;not null;index"`
	ChannelId       int     `json:"channel_id" gorm:"type:int;not null;index:idx_profit_cost_version_lookup,priority:1"`
	ModelName       string  `json:"model_name" gorm:"type:varchar(255);not null;index:idx_profit_cost_version_lookup,priority:2"`
	PriceType       string  `json:"price_type" gorm:"type:varchar(32);not null"`
	FixedUnit       string  `json:"fixed_unit" gorm:"type:varchar(32);not null;default:''"`
	CipherText      string  `json:"-" gorm:"type:text;not null"`
	Nonce           string  `json:"-" gorm:"type:varchar(255);not null"`
	EncryptionVer   string  `json:"encryption_version" gorm:"type:varchar(32);not null"`
	EffectiveFrom   int64   `json:"effective_from" gorm:"not null;index:idx_profit_cost_version_lookup,priority:3"`
	CreatedBy       int     `json:"created_by" gorm:"type:int;not null;default:0"`
	CreatedAt       int64   `json:"created_at" gorm:"autoCreateTime;index"`
	PriceConfigured bool    `json:"price_configured" gorm:"-"`
	PriceValue      string  `json:"price_value,omitempty" gorm:"-"`
	FixedAmount     float64 `json:"fixed_amount,omitempty" gorm:"-"`
	InputPrice      float64 `json:"input_price,omitempty" gorm:"-"`
	CacheReadPrice  float64 `json:"cache_read_price,omitempty" gorm:"-"`
	OutputPrice     float64 `json:"output_price,omitempty" gorm:"-"`
	RequestPrice    float64 `json:"request_price,omitempty" gorm:"-"`
	SecondPrice     float64 `json:"second_price,omitempty" gorm:"-"`
}

type ProfitCostPriceInput struct {
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
	CreatedBy      int     `json:"created_by"`
}

type ProfitSimpleTokenPrice struct {
	InputPrice     float64 `json:"input_price"`
	CacheReadPrice float64 `json:"cache_read_price"`
	OutputPrice    float64 `json:"output_price"`
	RequestPrice   float64 `json:"request_price"`
	SecondPrice    float64 `json:"second_price"`
}

func validateProfitCostPriceInput(input ProfitCostPriceInput) error {
	if input.ChannelId <= 0 {
		return errors.New("channel_id is required")
	}
	if strings.TrimSpace(input.ModelName) == "" {
		return errors.New("model_name is required")
	}
	switch input.PriceType {
	case ProfitPriceTypeSimpleToken:
		if input.InputPrice < 0 || input.CacheReadPrice < 0 || input.OutputPrice < 0 || input.RequestPrice < 0 || input.SecondPrice < 0 {
			return errors.New("cost price cannot be negative")
		}
		if input.InputPrice == 0 && input.CacheReadPrice == 0 && input.OutputPrice == 0 && input.RequestPrice == 0 && input.SecondPrice == 0 {
			return errors.New("at least one cost price is required")
		}
	case ProfitPriceTypeTieredExpr:
		if strings.TrimSpace(input.PriceValue) == "" {
			return errors.New("price_value is required")
		}
	case ProfitPriceTypeFixedPrice:
		if input.FixedUnit != ProfitFixedUnitRequest && input.FixedUnit != ProfitFixedUnitSecond {
			return errors.New("fixed_unit must be request or second")
		}
		if input.FixedAmount < 0 {
			return errors.New("fixed_amount cannot be negative")
		}
	default:
		return errors.New("unsupported price_type")
	}
	return nil
}

func UpsertProfitCostPrice(input ProfitCostPriceInput) (*ProfitCostPriceVersion, error) {
	if err := validateProfitCostPriceInput(input); err != nil {
		return nil, err
	}
	modelName := strings.TrimSpace(input.ModelName)
	if input.EffectiveFrom <= 0 {
		input.EffectiveFrom = common.GetTimestamp()
	}
	plainValue := input.PriceValue
	switch input.PriceType {
	case ProfitPriceTypeSimpleToken:
		plainValue = common.GetJsonString(ProfitSimpleTokenPrice{
			InputPrice:     input.InputPrice,
			CacheReadPrice: input.CacheReadPrice,
			OutputPrice:    input.OutputPrice,
			RequestPrice:   input.RequestPrice,
			SecondPrice:    input.SecondPrice,
		})
	case ProfitPriceTypeFixedPrice:
		plainValue = common.GetJsonString(map[string]interface{}{
			"amount": input.FixedAmount,
			"unit":   input.FixedUnit,
		})
	}
	cipherText, nonce, version, err := common.EncryptString(plainValue)
	if err != nil {
		return nil, err
	}

	var createdVersion ProfitCostPriceVersion
	err = DB.Transaction(func(tx *gorm.DB) error {
		price := ProfitCostPrice{}
		err := tx.Where("channel_id = ? AND model_name = ?", input.ChannelId, modelName).First(&price).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			price = ProfitCostPrice{
				ChannelId: input.ChannelId,
				ModelName: modelName,
				CreatedBy: input.CreatedBy,
				UpdatedBy: input.CreatedBy,
			}
			if err := tx.Create(&price).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		createdVersion = ProfitCostPriceVersion{
			CostPriceId:   price.Id,
			ChannelId:     input.ChannelId,
			ModelName:     modelName,
			PriceType:     input.PriceType,
			FixedUnit:     input.FixedUnit,
			CipherText:    cipherText,
			Nonce:         nonce,
			EncryptionVer: version,
			EffectiveFrom: input.EffectiveFrom,
			CreatedBy:     input.CreatedBy,
		}
		if err := tx.Create(&createdVersion).Error; err != nil {
			return err
		}

		return tx.Model(&ProfitCostPrice{}).Where("id = ?", price.Id).Updates(map[string]interface{}{
			"current_version_id": createdVersion.Id,
			"disabled":           false,
			"updated_by":         input.CreatedBy,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	createdVersion.PriceConfigured = true
	switch input.PriceType {
	case ProfitPriceTypeSimpleToken:
		createdVersion.InputPrice = input.InputPrice
		createdVersion.CacheReadPrice = input.CacheReadPrice
		createdVersion.OutputPrice = input.OutputPrice
		createdVersion.RequestPrice = input.RequestPrice
		createdVersion.SecondPrice = input.SecondPrice
	case ProfitPriceTypeFixedPrice:
		createdVersion.FixedAmount = input.FixedAmount
	default:
		createdVersion.PriceValue = input.PriceValue
	}
	return &createdVersion, nil
}

func DisableProfitCostPrice(id int, updatedBy int) error {
	if id <= 0 {
		return errors.New("invalid cost price id")
	}
	return DB.Model(&ProfitCostPrice{}).Where("id = ?", id).Updates(map[string]interface{}{
		"disabled":    true,
		"disabled_at": common.GetTimestamp(),
		"updated_by":  updatedBy,
	}).Error
}

type ProfitCostPriceListItem struct {
	Id               int     `json:"id"`
	ChannelId        int     `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	ModelName        string  `json:"model_name"`
	CurrentVersionId int     `json:"current_version_id"`
	PriceType        string  `json:"price_type"`
	FixedUnit        string  `json:"fixed_unit"`
	EffectiveFrom    int64   `json:"effective_from"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
	Disabled         bool    `json:"disabled"`
	DisabledAt       int64   `json:"disabled_at"`
	PriceConfigured  bool    `json:"price_configured"`
	PriceValue       string  `json:"price_value,omitempty"`
	PriceSummary     string  `json:"price_summary,omitempty"`
	InputPrice       float64 `json:"input_price,omitempty"`
	CacheReadPrice   float64 `json:"cache_read_price,omitempty"`
	OutputPrice      float64 `json:"output_price,omitempty"`
	RequestPrice     float64 `json:"request_price,omitempty"`
	SecondPrice      float64 `json:"second_price,omitempty"`
}

func ListProfitCostPrices(reveal bool) ([]ProfitCostPriceListItem, error) {
	var prices []ProfitCostPrice
	if err := DB.Order("id desc").Find(&prices).Error; err != nil {
		return nil, err
	}
	channelIds := make([]int, 0, len(prices))
	for _, price := range prices {
		channelIds = append(channelIds, price.ChannelId)
	}
	channelNames := map[int]string{}
	if len(channelIds) > 0 {
		var channels []struct {
			Id   int
			Name string
		}
		if err := DB.Table("channels").Select("id, name").Where("id IN ?", channelIds).Find(&channels).Error; err == nil {
			for _, channel := range channels {
				channelNames[channel.Id] = channel.Name
			}
		}
	}

	items := make([]ProfitCostPriceListItem, 0, len(prices))
	for _, price := range prices {
		item := ProfitCostPriceListItem{
			Id:               price.Id,
			ChannelId:        price.ChannelId,
			ChannelName:      channelNames[price.ChannelId],
			ModelName:        price.ModelName,
			CurrentVersionId: price.CurrentVersionId,
			CreatedAt:        price.CreatedAt,
			UpdatedAt:        price.UpdatedAt,
			Disabled:         price.Disabled,
			DisabledAt:       price.DisabledAt,
		}
		if price.CurrentVersionId != 0 {
			var version ProfitCostPriceVersion
			if err := DB.Where("id = ?", price.CurrentVersionId).First(&version).Error; err == nil {
				item.PriceType = version.PriceType
				item.FixedUnit = version.FixedUnit
				item.EffectiveFrom = version.EffectiveFrom
				item.PriceConfigured = true
				if plain, err := common.DecryptString(version.CipherText, version.Nonce, version.EncryptionVer); err == nil {
					switch version.PriceType {
					case ProfitPriceTypeSimpleToken:
						var payload ProfitSimpleTokenPrice
						if err := common.UnmarshalJsonStr(plain, &payload); err == nil {
							item.InputPrice = payload.InputPrice
							item.CacheReadPrice = payload.CacheReadPrice
							item.OutputPrice = payload.OutputPrice
							item.RequestPrice = payload.RequestPrice
							item.SecondPrice = payload.SecondPrice
							item.PriceSummary = formatProfitSimpleTokenSummary(payload)
						}
					case ProfitPriceTypeFixedPrice:
						var payload struct {
							Amount float64 `json:"amount"`
							Unit   string  `json:"unit"`
						}
						if err := common.UnmarshalJsonStr(plain, &payload); err == nil {
							item.PriceSummary = formatProfitFixedSummary(payload.Amount, payload.Unit)
						}
					}
					if reveal {
						item.PriceValue = plain
					}
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func FindProfitCostPriceVersion(channelId int, modelName string, ts int64) (*ProfitCostPriceVersion, error) {
	var version ProfitCostPriceVersion
	err := DB.Table("profit_cost_price_versions").
		Select("profit_cost_price_versions.*").
		Joins("JOIN profit_cost_prices ON profit_cost_prices.id = profit_cost_price_versions.cost_price_id").
		Where("profit_cost_price_versions.channel_id = ? AND profit_cost_price_versions.model_name = ? AND profit_cost_price_versions.effective_from <= ?", channelId, modelName, ts).
		Where("(profit_cost_prices.disabled = ? OR profit_cost_prices.disabled_at = 0 OR profit_cost_prices.disabled_at > ?)", false, ts).
		Order("effective_from desc, id desc").
		First(&version).Error
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func formatProfitSimpleTokenSummary(payload ProfitSimpleTokenPrice) string {
	parts := make([]string, 0, 4)
	if payload.InputPrice > 0 {
		parts = append(parts, "input $"+strconvFormatProfitFloat(payload.InputPrice))
	}
	if payload.CacheReadPrice > 0 {
		parts = append(parts, "cache read $"+strconvFormatProfitFloat(payload.CacheReadPrice))
	}
	if payload.OutputPrice > 0 {
		parts = append(parts, "output $"+strconvFormatProfitFloat(payload.OutputPrice))
	}
	if payload.RequestPrice > 0 {
		parts = append(parts, "request $"+strconvFormatProfitFloat(payload.RequestPrice))
	}
	if payload.SecondPrice > 0 {
		parts = append(parts, "second $"+strconvFormatProfitFloat(payload.SecondPrice))
	}
	return strings.Join(parts, " / ")
}

func formatProfitFixedSummary(amount float64, unit string) string {
	if amount <= 0 {
		return ""
	}
	switch unit {
	case ProfitFixedUnitSecond:
		return "second $" + strconvFormatProfitFloat(amount)
	default:
		return "request $" + strconvFormatProfitFloat(amount)
	}
}

func strconvFormatProfitFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 6, 64), "0"), ".")
}
