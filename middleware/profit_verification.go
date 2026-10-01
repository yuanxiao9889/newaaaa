package middleware

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const (
	ProfitVerificationPasswordHashOptionKey = model.ProfitVerificationPasswordHashOptionKey
	ProfitVerificationSessionKey            = "profit_verified_at"
	profitVerificationBindingKey            = "profit_verified_binding"
	ProfitVerificationTimeout               = 1800
)

func ProfitVerificationRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		hash, err := model.GetProfitPasswordHash()
		if err != nil {
			common.ApiError(c, err)
			c.Abort()
			return
		}
		if !RequireProfitVerification(c, hash) {
			return
		}
		c.Next()
	}
}

// RequireProfitVerification applies the same gate to data access and password
// replacement. passwordHash must come from the authoritative options record.
func RequireProfitVerification(c *gin.Context, passwordHash string) bool {
	if c.GetInt("id") == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return false
	}
	if passwordHash == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "请先配置利润页密码", "code": "PROFIT_PASSWORD_NOT_CONFIGURED"})
		return false
	}
	session := sessions.Default(c)
	verifiedAt, validTime := session.Get(ProfitVerificationSessionKey).(int64)
	actualBinding, _ := session.Get(profitVerificationBindingKey).(string)
	expectedBinding := profitVerificationBinding(c, passwordHash)
	now := time.Now().Unix()
	if !validTime || verifiedAt <= 0 || verifiedAt > now || expectedBinding == "" || subtle.ConstantTimeCompare([]byte(actualBinding), []byte(expectedBinding)) != 1 {
		clearProfitVerificationSession(session)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "请输入利润页访问密码", "code": "PROFIT_VERIFICATION_REQUIRED"})
		return false
	}
	if now-verifiedAt >= ProfitVerificationTimeout {
		clearProfitVerificationSession(session)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "利润页验证已过期，请重新输入密码", "code": "PROFIT_VERIFICATION_EXPIRED"})
		return false
	}
	return true
}

// MarkProfitVerified binds a short-lived grant to the authenticated credential
// and password generation. Neither the password nor its usable hash enters the cookie.
func MarkProfitVerified(c *gin.Context, passwordHash string) error {
	binding := profitVerificationBinding(c, passwordHash)
	if binding == "" {
		return errors.New("profit verification identity is unavailable")
	}
	session := sessions.Default(c)
	session.Set(ProfitVerificationSessionKey, time.Now().Unix())
	session.Set(profitVerificationBindingKey, binding)
	return session.Save()
}

func profitVerificationBinding(c *gin.Context, passwordHash string) string {
	identity, ok := GetAuthIdentity(c)
	if !ok || identity.UserID <= 0 || identity.UserAuthVersion <= 0 || passwordHash == "" {
		return ""
	}
	credential := "session:" + identity.SessionID
	if c.GetBool("use_access_token") {
		ref := c.GetString("access_token_ref")
		if ref == "" {
			return ""
		}
		credential = "token:" + ref
	} else if identity.SessionID == "" {
		return ""
	}
	return common.GenerateHMACWithKey([]byte(common.SessionSecret), fmt.Sprintf("profit-verification:%d:%d:%d:%s:%s", identity.UserID, identity.UserAuthVersion, identity.SessionVersion, credential, passwordHash))
}

func ClearProfitVerification(c *gin.Context) {
	clearProfitVerificationSession(sessions.Default(c))
}

func clearProfitVerificationSession(session sessions.Session) {
	session.Delete(ProfitVerificationSessionKey)
	session.Delete(profitVerificationBindingKey)
	_ = session.Save()
}
