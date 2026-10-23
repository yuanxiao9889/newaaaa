package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Encrypted capability: upstream credentials must never be readable by clients.
// The bridge validates both expiry and every resolved IP/redirect before use.
type TransferDownloadClaim struct {
	ObjectKey   string `json:"object_key,omitempty"`
	UploadID    string `json:"upload_id,omitempty"`
	ChunkIndex  int    `json:"chunk_index,omitempty"`
	ChunkSHA256 string `json:"chunk_sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`

	AuthVersion     int64             `json:"auth_version,omitempty"`
	TokenID         int               `json:"token_id,omitempty"`
	TokenDigest     string            `json:"token_digest,omitempty"`
	Version         int               `json:"v"`
	UserID          int               `json:"user_id"`
	ResourceID      string            `json:"resource_id"`
	ResourceVersion string            `json:"resource_version,omitempty"`
	MediaKind       string            `json:"media_kind,omitempty"`
	Route           string            `json:"route,omitempty"`
	RedirectHosts   []string          `json:"redirect_hosts,omitempty"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers,omitempty"`
	IssuedAt        int64             `json:"iat"`
	ExpiresAt       int64             `json:"exp"`
}

func SealTransferDownload(claim TransferDownloadClaim) (string, error) {
	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("OPICS_BRIDGE_TICKET_SECRET")))
	if err != nil || len(key) != 32 {
		return "", errors.New("bridge download signing is unavailable")
	}
	if claim.Version != 1 || claim.UserID <= 0 || claim.ResourceID == "" ||
		claim.ExpiresAt <= time.Now().Unix() || claim.ExpiresAt-claim.IssuedAt > 300 {
		return "", errors.New("invalid download capability")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	raw, err := common.Marshal(claim)
	if err != nil || len(raw) > 10<<10 {
		return "", errors.New("download capability exceeds limit")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, raw, []byte("opics-download-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
