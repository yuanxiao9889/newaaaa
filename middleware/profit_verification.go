package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const (
	ProfitVerificationPasswordHashOptionKey = "ProfitVerificationPasswordHash"
	ProfitVerificationSessionKey            = "profit_verified_at"
	ProfitVerificationTimeout               = 1800
)

func ProfitPasswordHash() string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return strings.TrimSpace(common.OptionMap[ProfitVerificationPasswordHashOptionKey])
}

func ProfitVerificationRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetInt("id") == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "未登录",
			})
			c.Abort()
			return
		}
		if ProfitPasswordHash() == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "请先配置利润页密码",
				"code":    "PROFIT_PASSWORD_NOT_CONFIGURED",
			})
			c.Abort()
			return
		}

		session := sessions.Default(c)
		verifiedAtRaw := session.Get(ProfitVerificationSessionKey)
		if verifiedAtRaw == nil {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "请输入利润页访问密码",
				"code":    "PROFIT_VERIFICATION_REQUIRED",
			})
			c.Abort()
			return
		}

		verifiedAt, ok := verifiedAtRaw.(int64)
		if !ok {
			clearProfitVerificationSession(session)
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "利润页验证状态异常，请重新输入密码",
				"code":    "PROFIT_VERIFICATION_REQUIRED",
			})
			c.Abort()
			return
		}
		if time.Now().Unix()-verifiedAt >= ProfitVerificationTimeout {
			clearProfitVerificationSession(session)
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "利润页验证已过期，请重新输入密码",
				"code":    "PROFIT_VERIFICATION_EXPIRED",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

func ClearProfitVerification(c *gin.Context) {
	clearProfitVerificationSession(sessions.Default(c))
}

func clearProfitVerificationSession(session sessions.Session) {
	session.Delete(ProfitVerificationSessionKey)
	_ = session.Save()
}
