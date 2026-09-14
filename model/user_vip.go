package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrUserVIPPermission = errors.New("insufficient permission to change VIP membership")

// SetUserVIP changes only membership, without publishing quota caches or
// changing authentication versions. It serializes with referral settlement.
func SetUserVIP(userID, operatorRole int, isVIP bool) (previous bool, err error) {
	if operatorRole < common.RoleAdminUser {
		return false, ErrUserVIPPermission
	}
	if userID <= 0 {
		return false, gorm.ErrRecordNotFound
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "role", "is_vip").First(&user, userID).Error; err != nil {
			return err
		}
		if operatorRole != common.RoleRootUser && operatorRole <= user.Role {
			return ErrUserVIPPermission
		}
		previous = user.IsVIP
		if previous == isVIP {
			return nil
		}
		return tx.Model(&User{}).Where("id = ?", userID).Update("is_vip", isVIP).Error
	})
	return previous, err
}
