package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// These capabilities are scoped to one MJ image and the owner's current auth
// version. Unlike task IDs, they may be used by an unauthenticated image tag.
func midjourneyImageSignature(task *model.Midjourney, version, expires int64) string {
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	fmt.Fprintf(mac, "mj-image-v1\x00%d\x00%s\x00%d\x00%d", task.UserId, task.MjId, version, expires)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func BuildMidjourneyImageURL(task *model.Midjourney) string {
	if task == nil || task.MjId == "" || common.CryptoSecret == "" {
		return ""
	}
	user, err := model.GetUserById(task.UserId, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		return ""
	}
	expires := time.Now().Add(5 * time.Minute).Unix()
	query := url.Values{"uid": {strconv.Itoa(task.UserId)}, "expires": {strconv.FormatInt(expires, 10)}, "access": {midjourneyImageSignature(task, user.AuthVersion, expires)}}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/mj/image/" + url.PathEscape(task.MjId) + "?" + query.Encode()
}

func VerifyMidjourneyImageAccess(task *model.Midjourney, query url.Values, now time.Time) bool {
	if task == nil || common.CryptoSecret == "" || len(query["access"]) != 1 || len(query["expires"]) != 1 ||
		len(query["uid"]) != 1 || query.Get("uid") != strconv.Itoa(task.UserId) {
		return false
	}
	expires, err := strconv.ParseInt(query.Get("expires"), 10, 64)
	if err != nil || expires <= now.Unix() || expires > now.Add(5*time.Minute).Unix() || len(query.Get("access")) != 43 {
		return false
	}
	user, err := model.GetUserById(task.UserId, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		return false
	}
	return hmac.Equal([]byte(query.Get("access")), []byte(midjourneyImageSignature(task, user.AuthVersion, expires)))
}
