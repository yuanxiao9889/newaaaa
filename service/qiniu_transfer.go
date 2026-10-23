package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// These are separate origins: the upload edge checks the current account before
// reading a body; the CDN checks it before every GET/HEAD/Range, including hits.
// Never advertise ordinary Kodo signed URLs as a revocable download route.
func QiniuTransferOrigin(upload bool) string {
	name := "TRANSFER_QINIU_DOWNLOAD_ORIGIN"
	if upload {
		name = "TRANSFER_QINIU_UPLOAD_ORIGIN"
	}
	raw := strings.TrimRight(os.Getenv(name), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	if !upload && os.Getenv("TRANSFER_QINIU_DOWNLOAD_AUTH_VERIFIED") != "true" {
		return ""
	}
	if len(os.Getenv("TRANSFER_QINIU_CALLBACK_SECRET")) < 32 {
		return ""
	}
	if ReferenceUploads == nil || !ReferenceUploads.LegacyEnabled() {
		return ""
	}
	return raw
}

type QiniuUploadGrant struct {
	URL       string `json:"upload_url"`
	Token     string `json:"upload_token"`
	Key       string `json:"object_key"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	ExpiresAt int64  `json:"upload_expires_at"`
}

// A missing cloud chunk is recoverable; an expired local session is not.
// Keep the two distinct so a stale session can never issue a new upload grant.
var errQiniuChunkMissing = fmt.Errorf("%w: cloud chunk missing", ErrReferenceDownload)

// Native upload needs no edge or CDN. Only advertise the supported Guangdong
// HTTPS endpoint, and keep this optional third route disabled until configured.
func QiniuDirectUploadEnabled() bool {
	if os.Getenv("TRANSFER_QINIU_DIRECT_UPLOAD_ENABLED") != "true" || ReferenceUploads == nil || !ReferenceUploads.LegacyEnabled() {
		return false
	}
	return strings.TrimRight(ReferenceUploads.config.UploadURL, "/") == "https://upload-z2.qiniup.com"
}

func (s *ReferenceUploadStore) qiniuGrant(key string, size int64, digest string) (QiniuUploadGrant, error) {
	expires := time.Now().Add(time.Minute).Unix()
	policy, err := common.Marshal(map[string]any{
		"scope": s.config.Bucket + ":" + key, "deadline": expires,
		"insertOnly": 1, "fsizeMin": size, "fsizeLimit": size, "fileType": 0, "deleteAfterDays": 1,
	})
	if err != nil {
		return QiniuUploadGrant{}, err
	}
	encoded := base64.URLEncoding.EncodeToString(policy)
	return QiniuUploadGrant{URL: s.config.UploadURL, Token: s.qiniuSign(encoded) + ":" + encoded, Key: key, Size: size, SHA256: digest, ExpiresAt: expires}, nil
}

// The object name includes the durable session generation and chunk hash. A lost
// ACK, process restart or route change addresses the same immutable chunk.
func (s *ReferenceUploadStore) QiniuChunk(user int, id string, index int, digest string) (TransferDownloadClaim, ReferenceResumableResult, error) {
	unlock, err := s.lockResumable()
	if err != nil {
		return TransferDownloadClaim{}, ReferenceResumableResult{}, err
	}
	defer unlock()
	m, err := s.loadUpload(user, id)
	if err != nil {
		return TransferDownloadClaim{}, ReferenceResumableResult{}, err
	}
	result := s.uploadResult(m)
	if index < 0 || int64(index) > (m.Input.Size-1)/ReferenceChunkBytes || !validReferenceID(digest) {
		return TransferDownloadClaim{}, result, ErrReferenceInvalid
	}
	if old, ok := m.Chunks[index]; ok && old != digest {
		return TransferDownloadClaim{}, result, ErrReferenceConflict
	}
	claim := TransferDownloadClaim{Route: "qiniu-upload", UserID: user, UploadID: id, ChunkIndex: index, ChunkSHA256: digest,
		ObjectKey: fmt.Sprintf("transfer/chunks/%d/%s/%d/%d-%s", user, id, m.CreatedAt, index, digest),
		Size:      min(ReferenceChunkBytes, m.Input.Size-int64(index)*ReferenceChunkBytes)}
	return claim, result, nil
}

// Recheck the receipt before contacting storage. The server bitmap is the only
// completion authority; a cloud upload response alone never publishes an asset.
func (s *ReferenceUploadStore) ImportQiniuChunk(ctx context.Context, claim TransferDownloadClaim) (ReferenceResumableResult, error) {
	expected, result, err := s.QiniuChunk(claim.UserID, claim.UploadID, claim.ChunkIndex, claim.ChunkSHA256)
	if err != nil {
		return result, err
	}
	if expected.ObjectKey != claim.ObjectKey || expected.Size != claim.Size {
		return result, ErrReferenceInvalid
	}
	if result.Status == "ready" || slices.Contains(result.ReceivedChunks, claim.ChunkIndex) {
		return result, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.signedObjectURL(claim.ObjectKey, time.Now().Add(time.Minute).Unix()), nil)
	if err != nil {
		return result, ErrReferenceInvalid
	}
	response, err := s.client.Do(req)
	if err != nil {
		return result, ErrReferenceDownload
	}
	defer response.Body.Close()
	if response.StatusCode == 404 || response.StatusCode == 612 {
		return result, errQiniuChunkMissing
	}
	if response.StatusCode != 200 {
		return result, ErrReferenceDownload
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, claim.Size+1))
	if err != nil || int64(len(data)) != claim.Size {
		return result, ErrReferenceDownload
	}
	return s.WriteResumableChunk(claim.UserID, claim.UploadID, claim.ChunkIndex, claim.ChunkSHA256, bytes.NewReader(data))
}

func (s *ReferenceUploadStore) AuthorizeQiniuUpload(ctx context.Context, claim TransferDownloadClaim) (QiniuUploadGrant, bool, error) {
	_, err := s.ImportQiniuChunk(ctx, claim)
	if err == nil {
		return QiniuUploadGrant{}, true, nil
	}
	if err != errQiniuChunkMissing {
		return QiniuUploadGrant{}, false, err
	}
	grant, err := s.qiniuGrant(claim.ObjectKey, claim.Size, claim.ChunkSHA256)
	return grant, false, err
}

func newQiniuResultClient() *http.Client {
	protection := &common.SSRFProtection{AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}
	dialer := &protectedFetchDialer{resolver: net.DefaultResolver,
		dialContext:   (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		getProtection: func() (*common.SSRFProtection, bool, error) { return protection, true, nil }}
	// This file-copy path never inherits insecure TLS or proxy bypass settings.
	return &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{
		DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		DisableCompression: true, ForceAttemptHTTP2: true, MaxIdleConns: 16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
	}}
}

// A stable result object is reused across retries and clients. Upload uses an
// immutable object and bounded temporary file, never a second generation job.
func (s *ReferenceUploadStore) PutQiniuResult(ctx context.Context, identity, source, version string, headers map[string]string, redirectHosts []string) (string, error) {
	digest := sha256.Sum256([]byte(identity))
	key := "transfer/results/" + hex.EncodeToString(digest[:])
	stripe := s.locks[int(digest[0])&63]
	select {
	case stripe <- struct{}{}:
		defer func() { <-stripe }()
	case <-ctx.Done():
		return "", ErrReferenceDownload
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return "", ErrReferenceBusy
	}
	req, _ := http.NewRequestWithContext(ctx, "HEAD", s.signedObjectURL(key, time.Now().Add(time.Minute).Unix()), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", ErrReferenceDownload
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return key, nil
	}
	if resp.StatusCode != 404 && resp.StatusCode != 612 {
		return "", ErrReferenceDownload
	}
	protection := &common.SSRFProtection{AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}
	client := *s.qiniuFetcher
	client.Timeout = 2 * time.Minute
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" || r.URL.User != nil {
			return ErrReferenceInvalid
		}
		if r.URL.Host != via[0].URL.Host {
			if !slices.Contains(redirectHosts, r.URL.Hostname()) {
				return ErrReferenceInvalid
			}
			r.Header = http.Header{"Accept-Encoding": []string{"identity"}}
		}
		return protection.ValidateURL(r.URL.String())
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return "", ErrReferenceInvalid
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", source, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err = client.Do(req)
	if err != nil {
		return "", ErrReferenceDownload
	}
	defer resp.Body.Close()
	const maximum = 256 << 20
	if resp.StatusCode != 200 || resp.ContentLength > maximum || (resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity") {
		return "", ErrReferenceDownload
	}
	// Reserve before writing. Crash leftovers use the existing .part cleanup.
	reservation := int64(maximum)
	if resp.ContentLength > 0 {
		reservation = resp.ContentLength
	}
	s.mu.Lock()
	used, files := s.cleanupLocked()
	if used+s.reserved+reservation > s.config.MaxCacheBytes || files >= 10000 {
		s.mu.Unlock()
		return "", ErrReferenceBusy
	}
	s.reserved += reservation
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.reserved -= reservation; s.mu.Unlock() }()
	file, err := os.CreateTemp(s.config.Directory, ".qiniu-result-*.part")
	if err != nil {
		return "", ErrReferenceUnavailable
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maximum+1))
	if err != nil || size <= 0 || size > maximum || (resp.ContentLength >= 0 && resp.ContentLength != size) {
		return "", ErrReferenceDownload
	}
	if strings.HasPrefix(version, "sha256:") && version != "sha256:"+hex.EncodeToString(hash.Sum(nil)) {
		return "", ErrReferenceConflict
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", ErrReferenceUnavailable
	}
	grant, err := s.qiniuGrant(key, size, "")
	if err != nil {
		return "", err
	}
	var prefix bytes.Buffer
	w := multipart.NewWriter(&prefix)
	_ = w.WriteField("token", grant.Token)
	_ = w.WriteField("key", key)
	_, err = w.CreateFormFile("file", "result")
	if err != nil {
		return "", err
	}
	head := bytes.Clone(prefix.Bytes())
	prefix.Reset()
	_ = w.Close()
	tail := bytes.Clone(prefix.Bytes())
	req, err = http.NewRequestWithContext(ctx, "POST", grant.URL, io.MultiReader(bytes.NewReader(head), file, bytes.NewReader(tail)))
	if err != nil {
		return "", ErrReferenceInvalid
	}
	req.ContentLength = int64(len(head)) + size + int64(len(tail))
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err = s.client.Do(req)
	if err != nil {
		return "", ErrReferenceDownload
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 && resp.StatusCode != 614 {
		return "", ErrReferenceDownload
	}
	return key, nil
}

// Only the authenticated origin gateway receives this private-bucket URL.
func (s *ReferenceUploadStore) QiniuOriginReadURL(key string) string {
	return s.signedObjectURL(key, time.Now().Add(time.Minute).Unix())
}
