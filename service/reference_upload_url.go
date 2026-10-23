package service

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Reuse metadata follows the configurable idle retention. Signed URLs keep a
// separate fixed validity so reducing cache retention never breaks active jobs;
// the external bucket's immutable object lifecycle remains independent.
type referenceURLRecord struct {
	Ticket referenceTicket `json:"ticket"`
	Until  int64           `json:"until"`
	Bucket string          `json:"bucket"`
	Origin string          `json:"origin"`
}

func (s *ReferenceUploadStore) signedObjectURL(key string, until int64) string {
	address := strings.TrimRight(s.config.DownloadURL, "/") + "/" + key + "?e=" + strconv.FormatInt(until, 10)
	return address + "&token=" + s.qiniuSign(address)
}

func (s *ReferenceUploadStore) preparedURL(user int, input ReferencePrepareRequest) (ReferenceUploadResult, bool) {
	path := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.url", user, input.SHA256))
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return ReferenceUploadResult{}, false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || time.Since(stat.ModTime()) >= ReferenceRetention() {
		return ReferenceUploadResult{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	var record referenceURLRecord
	if err != nil || len(data) > 4096 || common.Unmarshal(data, &record) != nil ||
		record.Ticket.UserID != user || record.Ticket.ReferencePrepareRequest != input ||
		record.Bucket != s.config.Bucket || record.Origin != s.config.DownloadURL ||
		record.Until <= time.Now().Add(time.Hour).Unix() {
		return ReferenceUploadResult{}, false
	}
	now := time.Now()
	if os.Chtimes(path, now, now) != nil {
		return ReferenceUploadResult{}, false
	}
	return ReferenceUploadResult{Status: "ready", AssetID: input.SHA256, ExpiresAt: min(now.Add(ReferenceRetention()).Unix(), record.Until),
		DownloadURL: s.signedObjectURL(record.Ticket.Key, record.Until)}, true
}

// completeURL only reads headers from the administrator-configured OSS origin.
// The client computes Qiniu's content ETag from the exact uploaded bytes. Size,
// detected MIME and strong ETag must match before a URL is returned. No binary
// cache entry or image conversion is created, and redirects remain disabled.
func (s *ReferenceUploadStore) completeURL(ctx context.Context, ticket referenceTicket) (ReferenceUploadResult, error) {
	if result, ok := s.preparedURL(ticket.UserID, ticket.ReferencePrepareRequest); ok {
		return result, nil
	}
	until := ticket.Expires + int64((11 * time.Hour).Seconds())
	if until <= time.Now().Add(time.Hour).Unix() {
		return ReferenceUploadResult{}, ErrReferenceMissing
	}
	address := s.signedObjectURL(ticket.Key, until)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, address, nil)
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceDownload
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReferenceUploadResult{}, ErrReferenceDownload
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	etag := resp.Header.Get("ETag")
	if err != nil || contentType != ticket.MIME || resp.ContentLength != ticket.Size ||
		(etag != ticket.ETag && etag != `"`+ticket.ETag+`"`) {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	record, err := common.Marshal(referenceURLRecord{
		Ticket: ticket, Until: until, Bucket: s.config.Bucket, Origin: s.config.DownloadURL,
	})
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used, count := s.cleanupLocked()
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	userFiles := 0
	var userBytes int64
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), strconv.Itoa(ticket.UserID)+"-") {
			if info, err := entry.Info(); err == nil {
				userFiles++
				userBytes += info.Size()
			}
		}
	}
	if count >= 10000 || userFiles >= 1000 || used+s.reserved+int64(len(record)) > s.config.MaxCacheBytes ||
		userBytes+s.userReserved[ticket.UserID]+int64(len(record)) > s.config.MaxUserCacheBytes {
		return ReferenceUploadResult{}, ErrReferenceBusy
	}
	path := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.url", ticket.UserID, ticket.SHA256))
	tmp, err := os.CreateTemp(s.config.Directory, ".reference-url-*")
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(record)
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil || os.Rename(tmp.Name(), path) != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	return ReferenceUploadResult{Status: "ready", AssetID: ticket.SHA256, ExpiresAt: min(time.Now().Add(ReferenceRetention()).Unix(), until), DownloadURL: address}, nil
}
