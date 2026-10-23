package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Separate purpose from gateway administration; never sent to desktop clients.
func BridgeDownloadAuthorizationKey() string {
	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("OPICS_BRIDGE_TICKET_SECRET")))
	if err != nil || len(key) != 32 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("opics-download-authorization-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

// Called on every new download/Range request, including cache hits. No cached
// account/token state: disabling/deleting either immediately denies new reads.
func OpenAuthorizedTransferDownload(ticket string, now time.Time) (TransferDownloadClaim, error) {
	denied := errors.New("download authorization denied")
	if len(ticket) == 0 || len(ticket) > 16<<10 {
		return TransferDownloadClaim{}, denied
	}
	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("OPICS_BRIDGE_TICKET_SECRET")))
	if err != nil || len(key) != 32 {
		return TransferDownloadClaim{}, denied
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return TransferDownloadClaim{}, denied
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return TransferDownloadClaim{}, denied
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(ticket)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return TransferDownloadClaim{}, denied
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("opics-download-v1"))
	if err != nil {
		return TransferDownloadClaim{}, denied
	}
	var claim TransferDownloadClaim
	if common.Unmarshal(plain, &claim) != nil || claim.Version != 1 || claim.UserID <= 0 || claim.TokenID <= 0 ||
		claim.AuthVersion <= 0 || claim.IssuedAt <= 0 || claim.IssuedAt > now.Unix()+30 || claim.ExpiresAt <= now.Unix() ||
		claim.ExpiresAt <= claim.IssuedAt || claim.ExpiresAt-claim.IssuedAt > 300 {
		return TransferDownloadClaim{}, denied
	}
	user, err := model.GetUserById(claim.UserID, false)
	if err != nil || user.Status != common.UserStatusEnabled || user.AuthVersion != claim.AuthVersion {
		return TransferDownloadClaim{}, denied
	}
	token, err := model.GetTokenByIds(claim.TokenID, claim.UserID)
	if err != nil || (token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted) ||
		(token.ExpiredTime != -1 && token.ExpiredTime <= now.Unix()) {
		return TransferDownloadClaim{}, denied
	}
	digest := sha256.Sum256([]byte(token.Key))
	if !hmac.Equal([]byte(claim.TokenDigest), []byte(hex.EncodeToString(digest[:]))) {
		return TransferDownloadClaim{}, denied
	}
	return claim, nil
}

func AuthorizeTransferDownload(ticket string, now time.Time) error {
	claim, err := OpenAuthorizedTransferDownload(ticket, now)
	if err != nil {
		return err
	}
	if claim.Route != "" && claim.Route != "relay" && claim.Route != "upstream" {
		return errors.New("download authorization denied")
	}
	return nil
}
