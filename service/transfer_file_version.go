package service

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// TransferFileVersion hashes the already opened file and restores its offset.
// A strong validator must describe bytes, not a task id, timestamp or URL.
func TransferFileVersion(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
