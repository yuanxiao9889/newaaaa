package model

type AffiliateRewardRecord struct {
	Id            int     `json:"id"`
	InviterId     int     `json:"inviter_id" gorm:"type:int;not null;index:idx_affiliate_reward_inviter"`
	InviteeId     int     `json:"invitee_id" gorm:"type:int;not null;index:idx_affiliate_reward_invitee"`
	TopUpId       int     `json:"top_up_id" gorm:"type:int;not null;uniqueIndex"`
	TradeNo       string  `json:"trade_no" gorm:"type:varchar(255);not null;uniqueIndex"`
	PaymentAmount float64 `json:"payment_amount" gorm:"type:decimal(18,6);not null;default:0"`
	RewardQuota   int     `json:"reward_quota" gorm:"type:int;not null;default:0"`
	CreatedAt     int64   `json:"created_at" gorm:"autoCreateTime;index"`
}
