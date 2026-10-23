package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // Qiniu's upload/download signing protocol requires HMAC-SHA1.
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const ReferenceMaxBytes = 8 << 20

// Successful preparation or task use renews the cache's idle lifetime.
func ReferenceRetention() time.Duration {
	minutes := model.CurrentRequestPolicy().ImageInputRetentionMinutes
	if minutes != 10 && minutes != 20 && minutes != 30 {
		minutes = 20
	}
	return time.Duration(minutes) * time.Minute
}

var (
	ReferenceUploads        *ReferenceUploadStore // Initialized once before HTTP routes start.
	ErrReferenceInvalid     = errors.New("invalid reference asset")
	ErrReferenceMissing     = errors.New("reference asset is missing or expired")
	ErrReferenceUnavailable = errors.New("reference upload acceleration is unavailable")
	ErrReferenceBusy        = errors.New("reference cache capacity or concurrency limit reached")
	ErrReferenceDownload    = errors.New("reference download failed; retry completion")
)

type ReferenceUploadConfig struct {
	AccessKey, SecretKey, Bucket, UploadURL, DownloadURL, Directory string
	MaxCacheBytes                                                   int64
	MaxUserCacheBytes                                               int64
	DirectEnabled                                                   bool
	SigningSecret                                                   string
	MinFreePercent                                                  int
}

type ReferenceUploadStore struct {
	config       ReferenceUploadConfig
	client       *http.Client
	qiniuFetcher *http.Client
	mu           sync.Mutex
	reserved     int64
	userReserved map[int]int64
	active       map[string]bool
	locks        [64]chan struct{}
	slots        chan struct{}
	directDir    string
	storageID    string
}

type ReferencePrepareRequest struct {
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MIME      string `json:"mime_type"`
	Mode      string `json:"mode,omitempty"`
	ETag      string `json:"etag,omitempty"`
	Transport string `json:"transport,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type ReferenceUploadResult struct {
	Status          string            `json:"status"`
	AssetID         string            `json:"asset_id"`
	ExpiresAt       int64             `json:"expires_at"`
	UploadURL       string            `json:"upload_url,omitempty"`
	UploadToken     string            `json:"upload_token,omitempty"`
	ObjectKey       string            `json:"object_key,omitempty"`
	Ticket          string            `json:"ticket,omitempty"`
	UploadExpiresAt int64             `json:"upload_expires_at,omitempty"`
	DownloadURL     string            `json:"download_url,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	UploadMethod    string            `json:"upload_method,omitempty"`
	UploadHeaders   map[string]string `json:"upload_headers,omitempty"`
}

type referenceTicket struct {
	ReferencePrepareRequest
	UserID  int    `json:"user_id"`
	Key     string `json:"key"`
	Expires int64  `json:"expires"`
}

func InitReferenceUploads() error {
	if os.Getenv("REFERENCE_UPLOAD_ENABLED") != "true" {
		return nil
	}
	store, err := NewReferenceUploadStore(ReferenceUploadConfig{
		AccessKey: os.Getenv("REFERENCE_QINIU_ACCESS_KEY"), SecretKey: os.Getenv("REFERENCE_QINIU_SECRET_KEY"),
		Bucket:            os.Getenv("REFERENCE_QINIU_BUCKET"),
		UploadURL:         common.GetEnvOrDefaultString("REFERENCE_QINIU_UPLOAD_URL", "https://upload-z2.qiniup.com"),
		DownloadURL:       common.GetEnvOrDefaultString("REFERENCE_QINIU_DOWNLOAD_URL", "https://refs.oopii.cn"),
		Directory:         common.GetEnvOrDefaultString("REFERENCE_CACHE_DIR", "/data/reference-assets"),
		MaxCacheBytes:     int64(common.GetEnvOrDefault("REFERENCE_CACHE_MAX_MB", 2048)) << 20,
		MaxUserCacheBytes: int64(common.GetEnvOrDefault("REFERENCE_CACHE_USER_MAX_MB", 256)) << 20,
		DirectEnabled:     os.Getenv("REFERENCE_DIRECT_UPLOAD_ENABLED") == "true",
		SigningSecret:     os.Getenv("REFERENCE_SIGNING_SECRET"),
		MinFreePercent:    common.GetEnvOrDefault("REFERENCE_CACHE_MIN_FREE_PERCENT", 10),
	})
	if err != nil {
		return err
	}
	store.Cleanup()
	if err := store.CleanupDirect(); err != nil {
		return err
	}
	ReferenceUploads = store
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			store.Cleanup()
			if err := store.CleanupDirect(); err != nil {
				common.SysError("reference direct cleanup could not complete")
			}
		}
	}()
	return nil
}

func NewReferenceUploadStore(config ReferenceUploadConfig) (*ReferenceUploadStore, error) {
	if config.MinFreePercent == 0 {
		config.MinFreePercent = 10
	}
	if config.MinFreePercent < 1 || config.MinFreePercent > 50 {
		return nil, ErrReferenceUnavailable
	}
	if config.MaxUserCacheBytes == 0 {
		config.MaxUserCacheBytes = min(256<<20, config.MaxCacheBytes)
	}
	legacy := config.AccessKey != "" && config.SecretKey != "" && config.Bucket != "" && !strings.ContainsAny(config.Bucket, ":/\\")
	if !legacy && (config.AccessKey != "" || config.SecretKey != "" || config.Bucket != "") {
		return nil, ErrReferenceUnavailable
	}
	if (!legacy && !config.DirectEnabled) || (config.DirectEnabled && len(config.SigningSecret) < 32) || config.Directory == "" || config.MaxCacheBytes < ReferenceMaxBytes || config.MaxUserCacheBytes < ReferenceMaxBytes {
		return nil, ErrReferenceUnavailable
	}
	for _, address := range []string{config.UploadURL, config.DownloadURL} {
		if !legacy {
			break
		}
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, ErrReferenceUnavailable
		}
	}
	if err := os.MkdirAll(config.Directory, 0700); err != nil {
		return nil, ErrReferenceUnavailable
	}
	// Only administrator-configured HTTPS origins are contacted. No proxy,
	// redirects, insecure TLS setting or client-supplied fetch URL is accepted.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = false
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	store := &ReferenceUploadStore{config: config, client: client, qiniuFetcher: newQiniuResultClient(), slots: make(chan struct{}, 8), active: make(map[string]bool), userReserved: make(map[int]int64)}
	for i := range store.locks {
		store.locks[i] = make(chan struct{}, 1)
	}
	if config.DirectEnabled {
		if err := store.initDirectReferences(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func validReferenceID(id string) bool {
	if len(id) != 64 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateReferenceInput(user int, input ReferencePrepareRequest) error {
	if user <= 0 || !validReferenceID(input.SHA256) || input.Size < 1 || input.Size > ReferenceMaxBytes {
		return ErrReferenceInvalid
	}
	if input.Mode != "" && input.Mode != "url" {
		return ErrReferenceInvalid
	}
	if input.Mode == "url" {
		digest, err := base64.URLEncoding.DecodeString(input.ETag)
		if err != nil || len(digest) != 21 || (digest[0] != 0x16 && digest[0] != 0x96) {
			return ErrReferenceInvalid
		}
	}
	switch input.MIME {
	case "image/png", "image/jpeg", "image/webp":
		return nil
	}
	return ErrReferenceInvalid
}

func (s *ReferenceUploadStore) sign(value string) string {
	secret := s.config.SecretKey
	if secret == "" {
		secret = s.config.SigningSecret
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("reference-v1:" + value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ReferenceUploadStore) qiniuSign(value string) string {
	mac := hmac.New(sha1.New, []byte(s.config.SecretKey))
	_, _ = mac.Write([]byte(value))
	return s.config.AccessKey + ":" + base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ReferenceUploadStore) Prepare(user int, input ReferencePrepareRequest) (ReferenceUploadResult, error) {
	if input.Transport == "relay" {
		return s.PrepareDirect(user, input)
	}
	if input.Transport != "" || !s.LegacyEnabled() {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	if err := validateReferenceInput(user, input); err != nil {
		return ReferenceUploadResult{}, err
	}
	if input.Mode == "url" {
		if result, ok := s.preparedURL(user, input); ok {
			return result, nil
		}
		// URL-only objects must never masquerade as verified binary cache entries.
	} else {
		data, mime, err := s.Read(user, input.SHA256, true)
		if err == nil {
			if int64(len(data)) != input.Size || mime != input.MIME {
				return ReferenceUploadResult{}, ErrReferenceInvalid
			}
			return ReferenceUploadResult{Status: "ready", AssetID: input.SHA256, ExpiresAt: time.Now().Add(ReferenceRetention()).Unix()}, nil
		}
		if !errors.Is(err, ErrReferenceMissing) {
			return ReferenceUploadResult{}, err
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	now := time.Now()
	key := fmt.Sprintf("references/%d/%s/%s", user, input.SHA256, hex.EncodeToString(nonce[:]))
	if input.Mode == "url" {
		extension := strings.TrimPrefix(input.MIME, "image/")
		if extension == "jpeg" {
			extension = "jpg"
		}
		key += "." + extension
	}
	ticket := referenceTicket{ReferencePrepareRequest: input, UserID: user, Key: key, Expires: now.Add(time.Hour).Unix()}
	payload, err := common.Marshal(ticket)
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	policy, err := common.Marshal(map[string]any{
		"scope": s.config.Bucket + ":" + key, "deadline": now.Add(15 * time.Minute).Unix(),
		"insertOnly": 1, "fsizeMin": input.Size, "fsizeLimit": input.Size,
		"mimeLimit": input.MIME, "detectMime": 1, "fileType": 0, "deleteAfterDays": 1,
	})
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	encodedPolicy := base64.URLEncoding.EncodeToString(policy)
	return ReferenceUploadResult{Status: "upload_required", AssetID: input.SHA256,
		ExpiresAt: ticket.Expires, UploadExpiresAt: now.Add(15 * time.Minute).Unix(),
		UploadURL: s.config.UploadURL, UploadToken: s.qiniuSign(encodedPolicy) + ":" + encodedPolicy,
		ObjectKey: key, Ticket: encoded + "." + s.sign("ticket:"+encoded)}, nil
}

func (s *ReferenceUploadStore) Complete(ctx context.Context, user int, token string) (ReferenceUploadResult, error) {
	if !s.LegacyEnabled() {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok || len(token) > 2048 || !hmac.Equal([]byte(signature), []byte(s.sign("ticket:"+encoded))) {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	var ticket referenceTicket
	if err != nil || common.Unmarshal(payload, &ticket) != nil || ticket.UserID != user || ticket.Expires <= time.Now().Unix() || validateReferenceInput(user, ticket.ReferencePrepareRequest) != nil {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ReferenceUploadResult{}, ErrReferenceBusy
	}
	// Bound both downloads and waiters; cancelled imports do not wait for a lock.
	stripe := s.locks[(int(ticket.SHA256[0])+int(ticket.SHA256[1]))&63]
	select {
	case stripe <- struct{}{}:
		defer func() { <-stripe }()
	case <-ctx.Done():
		return ReferenceUploadResult{}, ErrReferenceDownload
	}
	if ticket.Mode == "url" {
		return s.completeURL(ctx, ticket)
	}
	// Old Qiniu binary imports and native resumable uploads share the quota and
	// destination files. Serialize publication/reservations across workers so
	// a rolling deployment cannot overcommit another process's reserved bytes.
	unlockDisk, err := lockReferenceDirectory(s.config.Directory)
	if err != nil {
		return ReferenceUploadResult{}, err
	}
	defer unlockDisk()
	if _, _, err := s.Read(user, ticket.SHA256, true); err == nil {
		return ReferenceUploadResult{Status: "ready", AssetID: ticket.SHA256, ExpiresAt: time.Now().Add(ReferenceRetention()).Unix()}, nil
	}
	name := fmt.Sprintf("%d-%s", user, ticket.SHA256)
	keyHash := sha256.Sum256([]byte(ticket.Key))
	part := filepath.Join(s.config.Directory, name+"-"+hex.EncodeToString(keyHash[:8])+".part")
	s.mu.Lock()
	used, files := s.cleanupLocked()
	reservation := ticket.Size
	if info, err := os.Stat(part); err == nil {
		reservation -= min(ticket.Size, info.Size())
	}
	userUsed, userFiles := s.referenceUserUsageLocked(user)
	if used+s.reserved+reservation > s.config.MaxCacheBytes || files >= 10000 || userUsed+s.userReserved[user]+reservation > s.config.MaxUserCacheBytes || userFiles >= 1000 {
		s.mu.Unlock()
		return ReferenceUploadResult{}, ErrReferenceBusy
	}
	s.reserved += reservation
	s.userReserved[user] += reservation
	s.active[part] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reserved -= reservation
		s.userReserved[user] -= reservation
		if s.userReserved[user] == 0 {
			delete(s.userReserved, user)
		}
		delete(s.active, part)
	}()
	file, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	defer file.Close()
	for range 3 {
		info, err := file.Stat()
		if err != nil {
			return ReferenceUploadResult{}, ErrReferenceUnavailable
		}
		offset := info.Size()
		if offset == ticket.Size {
			break
		}
		if offset > ticket.Size {
			_ = file.Truncate(0)
			offset = 0
		}
		address := strings.TrimRight(s.config.DownloadURL, "/") + "/" + ticket.Key + "?e=" + strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10)
		address += "&token=" + s.qiniuSign(address)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return ReferenceUploadResult{}, ErrReferenceDownload
		}
		if resp.StatusCode == http.StatusPartialContent {
			expected := fmt.Sprintf("bytes %d-%d/%d", offset, ticket.Size-1, ticket.Size)
			if resp.Header.Get("Content-Range") != expected {
				resp.Body.Close()
				return ReferenceUploadResult{}, ErrReferenceDownload
			}
		} else if offset > 0 {
			_ = file.Truncate(0)
			offset = 0
		}
		_, _ = file.Seek(offset, io.SeekStart)
		_, copyErr := io.Copy(file, io.LimitReader(resp.Body, ticket.Size-offset+1))
		resp.Body.Close()
		info, err = file.Stat()
		if err != nil {
			return ReferenceUploadResult{}, ErrReferenceUnavailable
		}
		if info.Size() > ticket.Size {
			file.Close()
			_ = os.Remove(part)
			return ReferenceUploadResult{}, ErrReferenceInvalid
		}
		if copyErr == nil && info.Size() == ticket.Size {
			break
		}
	}
	_, _ = file.Seek(0, io.SeekStart)
	data, err := io.ReadAll(io.LimitReader(file, ReferenceMaxBytes+1))
	if err != nil || int64(len(data)) != ticket.Size {
		return ReferenceUploadResult{}, ErrReferenceDownload
	}
	hash := sha256.Sum256(data)
	imageConfig, format, decodeErr := decodeImageConfig(data)
	if hex.EncodeToString(hash[:]) != ticket.SHA256 || decodeErr != nil || "image/"+format != ticket.MIME || imageConfig.Width < 1 || imageConfig.Height < 1 || imageConfig.Width > 16384 || imageConfig.Height > 16384 || int64(imageConfig.Width)*int64(imageConfig.Height) > 64_000_000 {
		file.Close()
		_ = os.Remove(part)
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	if err := file.Sync(); err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	if err := file.Close(); err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	now := time.Now()
	if os.Chtimes(part, now, now) != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	s.mu.Lock()
	err = os.Rename(part, filepath.Join(s.config.Directory, name+".asset"))
	s.mu.Unlock()
	if err != nil {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	return ReferenceUploadResult{Status: "ready", AssetID: ticket.SHA256, ExpiresAt: time.Now().Add(ReferenceRetention()).Unix()}, nil
}

// Read returns a private, owned snapshot. Queued requests persist these bytes
// through their existing body storage, so cleanup cannot invalidate active jobs.
func (s *ReferenceUploadStore) Read(user int, id string, touch bool) ([]byte, string, error) {
	if user <= 0 || !validReferenceID(id) {
		return nil, "", ErrReferenceInvalid
	}
	if s.config.DirectEnabled {
		data, mime, err := s.ReadDirect(user, id, touch)
		if !errors.Is(err, ErrReferenceMissing) {
			return data, mime, err
		}
	}
	return s.readLegacyReference(user, id, touch)
}

func (s *ReferenceUploadStore) readLegacyReference(user int, id string, touch bool) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.asset", user, id))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", ErrReferenceMissing
	}
	if time.Since(info.ModTime()) >= ReferenceRetention() {
		protected, err := protectedTaskReferences(model.DB, user)
		if err != nil {
			return nil, "", ErrReferenceUnavailable
		}
		if !protected[fmt.Sprintf("%d-%s", user, id)] {
			return nil, "", ErrReferenceMissing
		}
	}
	if info.Size() <= 0 || info.Size() > ReferenceMaxBytes {
		return nil, "", ErrReferenceInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", ErrReferenceUnavailable
	}
	hash := sha256.Sum256(data)
	_, format, err := decodeImageConfig(data)
	if err != nil || hex.EncodeToString(hash[:]) != id {
		return nil, "", ErrReferenceInvalid
	}
	if touch {
		now := time.Now()
		if os.Chtimes(path, now, now) != nil {
			return nil, "", ErrReferenceUnavailable
		}
	}
	return data, "image/" + format, nil
}

func (s *ReferenceUploadStore) PublicURL(user int, id string) (string, error) {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrReferenceUnavailable
	}
	if _, _, err := s.Read(user, id, true); err != nil {
		return "", err
	}
	expires := strconv.FormatInt(time.Now().Add(ReferenceRetention()-time.Minute).Unix(), 10)
	path := fmt.Sprintf("/v1/uploads/references/content/%d/%s", user, id)
	return base + path + "?e=" + expires + "&token=" + s.sign("content:"+path+":"+expires), nil
}

func (s *ReferenceUploadStore) VerifyContent(user int, id, expires, signature string) bool {
	if taskID, ok := strings.CutPrefix(expires, "task:"); ok {
		if model.DB == nil {
			return false
		}
		if user <= 0 || !validReferenceID(id) || len(taskID) > 191 || taskID == "" {
			return false
		}
		path := fmt.Sprintf("/v1/uploads/references/content/%d/%s", user, id)
		if !hmac.Equal([]byte(signature), []byte(s.sign("content:"+path+":"+expires))) {
			return false
		}
		task, exists, err := model.GetByTaskId(user, taskID)
		if err != nil || !exists || !slices.Contains(task.PrivateData.ReferenceAssetIDs, id) {
			return false
		}
		return task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure ||
			task.FinishTime == 0 || task.FinishTime > time.Now().Add(-ReferenceRetention()).Unix()
	}
	e, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || e <= time.Now().Unix() || e > time.Now().Add(ReferenceRetention()).Unix() || user <= 0 || !validReferenceID(id) {
		return false
	}
	path := fmt.Sprintf("/v1/uploads/references/content/%d/%s", user, id)
	return hmac.Equal([]byte(signature), []byte(s.sign("content:"+path+":"+expires)))
}

func (s *ReferenceUploadStore) Cleanup() {
	unlock, err := s.lockResumable()
	if err != nil {
		return
	}
	defer unlock()
	s.cleanupLocked()
}

func (s *ReferenceUploadStore) cleanupLocked() (int64, int) {
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return s.config.MaxCacheBytes, 10000
	}
	var used int64
	var count int
	protected, protectionErr := protectedTaskReferences(model.DB, 0)
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".asset") && !strings.HasSuffix(entry.Name(), ".part") && !strings.HasSuffix(entry.Name(), ".url")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(s.config.Directory, entry.Name())
		ttl := ReferenceRetention()
		if strings.HasSuffix(entry.Name(), ".part") {
			ttl = time.Hour
		}
		identity := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".asset"), ".url")
		if protectionErr == nil && !protected[identity] && !s.active[path] && time.Since(info.ModTime()) >= ttl {
			if os.Remove(path) == nil {
				continue
			}
		}
		used += info.Size()
		count++
	}
	reservedUploads, uploadCount := s.resumableUsageLocked(0)
	return used + reservedUploads, count + uploadCount
}
