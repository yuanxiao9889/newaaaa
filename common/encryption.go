package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

const EncryptionVersionAESGCM = "aes-256-gcm-v1"

func encryptionKey() []byte {
	sum := sha256.Sum256([]byte(CryptoSecret))
	return sum[:]
}

func EncryptString(plainText string) (cipherText string, nonce string, version string, err error) {
	block, err := aes.NewCipher(encryptionKey())
	if err != nil {
		return "", "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", "", err
	}
	nonceBytes := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return "", "", "", err
	}
	sealed := gcm.Seal(nil, nonceBytes, []byte(plainText), nil)
	return base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(nonceBytes), EncryptionVersionAESGCM, nil
}

func DecryptString(cipherText string, nonce string, version string) (string, error) {
	if version != "" && version != EncryptionVersionAESGCM {
		return "", errors.New("unsupported encryption version")
	}
	cipherBytes, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return "", err
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plainBytes, err := gcm.Open(nil, nonceBytes, cipherBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plainBytes), nil
}
