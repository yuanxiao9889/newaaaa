package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const ReferenceChunkBytes = 1 << 20
const referenceUploadLifetime = 24 * time.Hour

var ErrReferenceConflict = errors.New("reference upload content conflicts with the prepared asset")

// The manifest is a durable receipt, not a cache hint: a chunk becomes visible
// only after both its data and the replacement manifest have been synchronized.
type referenceUploadManifest struct {
	Version   int                     `json:"version"`
	UserID    int                     `json:"user_id"`
	UploadID  string                  `json:"upload_id"`
	Input     ReferencePrepareRequest `json:"input"`
	CreatedAt int64                   `json:"created_at"`
	ExpiresAt int64                   `json:"expires_at"`
	Chunks    map[int]string          `json:"chunks"`
	Complete  bool                    `json:"complete"`
}

type ReferenceResumableResult struct {
	Status         string `json:"status"`
	AssetID        string `json:"asset_id"`
	UploadID       string `json:"upload_id"`
	Size           int64  `json:"size"`
	MIME           string `json:"mime_type"`
	ChunkSize      int    `json:"chunk_size"`
	ReceivedChunks []int  `json:"received_chunks"`
	ExpiresAt      int64  `json:"expires_at"`
}

func referenceUploadID(user int, input ReferencePrepareRequest) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("reference-upload-v1:%d:%s:%d:%s", user, input.SHA256, input.Size, input.MIME)))
	return hex.EncodeToString(digest[:])
}

func (s *ReferenceUploadStore) uploadPaths(user int, id string) (string, string) {
	base := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s", user, id))
	return base + ".upload", base + ".upload-part"
}

func (s *ReferenceUploadStore) loadUpload(user int, id string) (*referenceUploadManifest, error) {
	if user <= 0 || !validReferenceID(id) {
		return nil, ErrReferenceInvalid
	}
	manifestPath, _ := s.uploadPaths(user, id)
	raw, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrReferenceMissing
	}
	if err != nil {
		return nil, ErrReferenceUnavailable
	}
	var manifest referenceUploadManifest
	if len(raw) > 8192 || common.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 ||
		manifest.UserID != user || manifest.UploadID != id || manifest.Input.Mode != "" ||
		validateReferenceInput(user, manifest.Input) != nil || referenceUploadID(user, manifest.Input) != id ||
		len(manifest.Chunks) > 8 || manifest.Chunks == nil {
		return nil, ErrReferenceInvalid
	}
	if manifest.ExpiresAt <= time.Now().Unix() {
		return nil, ErrReferenceMissing
	}
	for index, digest := range manifest.Chunks {
		if index < 0 || int64(index) > (manifest.Input.Size-1)/ReferenceChunkBytes || !validReferenceID(digest) {
			return nil, ErrReferenceInvalid
		}
	}
	if len(manifest.Chunks) > 0 && s.uploadResult(&manifest).Status != "ready" {
		_, part := s.uploadPaths(user, id)
		if !fileExistsReference(part) {
			return nil, ErrReferenceMissing
		}
	}
	return &manifest, nil
}

func syncReferenceDirectory(directory string) error {
	// Directory handles cannot be Sync'ed on Windows; atomic replacement is
	// provided by MoveFileEx there. Production Linux synchronizes the directory.
	return syncReferenceDirectoryPlatform(directory)
}

func (s *ReferenceUploadStore) saveUpload(manifest *referenceUploadManifest) error {
	raw, err := common.Marshal(manifest)
	if err != nil {
		return err
	}
	path, _ := s.uploadPaths(manifest.UserID, manifest.UploadID)
	file, err := os.CreateTemp(s.config.Directory, ".upload-manifest-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = replaceReferenceFile(temp, path); err != nil {
		return err
	}
	return syncReferenceDirectory(s.config.Directory)
}

func (s *ReferenceUploadStore) uploadResult(manifest *referenceUploadManifest) ReferenceResumableResult {
	result := ReferenceResumableResult{
		Status: "upload_required", AssetID: manifest.Input.SHA256, UploadID: manifest.UploadID,
		Size: manifest.Input.Size, MIME: manifest.Input.MIME, ChunkSize: ReferenceChunkBytes,
		ReceivedChunks: []int{}, ExpiresAt: manifest.ExpiresAt,
	}
	for index := range int((manifest.Input.Size + ReferenceChunkBytes - 1) / ReferenceChunkBytes) {
		if _, exists := manifest.Chunks[index]; exists {
			result.ReceivedChunks = append(result.ReceivedChunks, index)
		}
	}
	assetPath := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.asset", manifest.UserID, manifest.Input.SHA256))
	if info, err := os.Stat(assetPath); err == nil && info.Mode().IsRegular() &&
		info.Size() == manifest.Input.Size && time.Since(info.ModTime()) < ReferenceRetention() {
		result.Status = "ready"
		result.ExpiresAt = info.ModTime().Add(ReferenceRetention()).Unix()
	}
	return result
}

// A process-independent store lock prevents parallel workers or rolling
// deployments from acknowledging conflicting manifests. No database migration
// is needed: the same persistent cache volume stores both bytes and receipts.
func (s *ReferenceUploadStore) lockResumable() (func(), error) {
	s.mu.Lock()
	unlock, err := lockReferenceDirectory(s.config.Directory)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	return func() { unlock(); s.mu.Unlock() }, nil
}

func (s *ReferenceUploadStore) PrepareResumable(user int, input ReferencePrepareRequest) (ReferenceResumableResult, error) {
	if input.Mode != "" || validateReferenceInput(user, input) != nil {
		return ReferenceResumableResult{}, ErrReferenceInvalid
	}
	unlock, err := s.lockResumable()
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	defer unlock()
	id := referenceUploadID(user, input)
	manifest, err := s.loadUpload(user, id)
	if err == nil {
		result := s.uploadResult(manifest)
		if result.Status == "ready" {
			asset := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.asset", user, input.SHA256))
			now := time.Now()
			if os.Chtimes(asset, now, now) != nil {
				return ReferenceResumableResult{}, ErrReferenceUnavailable
			}
			result.ExpiresAt = now.Add(ReferenceRetention()).Unix()
		}
		return result, nil
	}
	if !errors.Is(err, ErrReferenceMissing) {
		return ReferenceResumableResult{}, err
	}
	manifestPath, stalePart := s.uploadPaths(user, id)
	// A completed object may have reached its independent 12-hour cache
	// lifetime before the upload receipt expires. Restart its empty session.
	if os.Remove(manifestPath) != nil && fileExistsReference(manifestPath) {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	if os.Remove(stalePart) != nil && fileExistsReference(stalePart) {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	now := time.Now()
	manifest = &referenceUploadManifest{Version: 1, UserID: user, UploadID: id, Input: input,
		CreatedAt: now.Unix(), ExpiresAt: now.Add(referenceUploadLifetime).Unix(), Chunks: map[int]string{}}
	result := s.uploadResult(manifest)
	if result.Status == "ready" {
		asset := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.asset", user, input.SHA256))
		if os.Chtimes(asset, now, now) != nil {
			return ReferenceResumableResult{}, ErrReferenceUnavailable
		}
		result.ExpiresAt = now.Add(ReferenceRetention()).Unix()
		return result, nil
	}
	used, files := s.cleanupLocked()
	userUsed, userFiles := s.referenceUserUsageLocked(user)
	// Reserve the full eventual file, including sparse/unreceived bytes. This
	// prevents many cheap prepares from overcommitting available disk capacity.
	if used+s.reserved+input.Size+8192 > s.config.MaxCacheBytes || files >= 10000 ||
		userUsed+s.userReserved[user]+input.Size+8192 > s.config.MaxUserCacheBytes || userFiles >= 1000 {
		return ReferenceResumableResult{}, ErrReferenceBusy
	}
	_, part := s.uploadPaths(user, id)
	if err = os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	if err = s.saveUpload(manifest); err != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	return result, nil
}

func (s *ReferenceUploadStore) ResumableStatus(user int, id string) (ReferenceResumableResult, error) {
	unlock, err := s.lockResumable()
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	defer unlock()
	manifest, err := s.loadUpload(user, id)
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	return s.uploadResult(manifest), nil
}

func (s *ReferenceUploadStore) WriteResumableChunk(user int, id string, index int, expectedHash string, body io.Reader) (ReferenceResumableResult, error) {
	if index < 0 || index >= 8 || !validReferenceID(expectedHash) {
		return ReferenceResumableResult{}, ErrReferenceInvalid
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ReferenceResumableResult{}, ErrReferenceBusy
	}
	// Read before taking the disk lock so a stalled client cannot block uploads
	// or cleanup for other accounts. HTTP supplies an independent read deadline.
	data, err := io.ReadAll(io.LimitReader(body, ReferenceChunkBytes+1))
	if err != nil || len(data) == 0 || len(data) > ReferenceChunkBytes {
		return ReferenceResumableResult{}, ErrReferenceInvalid
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return ReferenceResumableResult{}, ErrReferenceConflict
	}
	unlock, err := s.lockResumable()
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	defer unlock()
	manifest, err := s.loadUpload(user, id)
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	expectedSize := min(int64(ReferenceChunkBytes), manifest.Input.Size-int64(index)*ReferenceChunkBytes)
	if expectedSize <= 0 || int64(len(data)) != expectedSize {
		return ReferenceResumableResult{}, ErrReferenceInvalid
	}
	if prior, exists := manifest.Chunks[index]; exists {
		if prior != expectedHash {
			return ReferenceResumableResult{}, ErrReferenceConflict
		}
		return s.uploadResult(manifest), nil // Retried/lost ACK, never rewrite.
	}
	if s.uploadResult(manifest).Status == "ready" {
		return s.uploadResult(manifest), nil
	}
	_, part := s.uploadPaths(user, id)
	file, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	_, err = file.WriteAt(data, int64(index)*ReferenceChunkBytes)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	manifest.Chunks[index] = expectedHash
	if s.saveUpload(manifest) != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	return s.uploadResult(manifest), nil
}

func (s *ReferenceUploadStore) CompleteResumable(user int, id string) (ReferenceResumableResult, error) {
	unlock, err := s.lockResumable()
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	defer unlock()
	manifest, err := s.loadUpload(user, id)
	if err != nil {
		return ReferenceResumableResult{}, err
	}
	if result := s.uploadResult(manifest); result.Status == "ready" {
		return result, nil
	}
	if len(manifest.Chunks) != int((manifest.Input.Size+ReferenceChunkBytes-1)/ReferenceChunkBytes) {
		return ReferenceResumableResult{}, ErrReferenceConflict
	}
	_, part := s.uploadPaths(user, id)
	file, err := os.Open(part)
	if err != nil {
		return ReferenceResumableResult{}, ErrReferenceMissing
	}
	data, err := io.ReadAll(io.LimitReader(file, ReferenceMaxBytes+1))
	file.Close()
	if err != nil || int64(len(data)) != manifest.Input.Size {
		return ReferenceResumableResult{}, ErrReferenceConflict
	}
	digest := sha256.Sum256(data)
	config, format, imageErr := decodeImageConfig(data)
	if hex.EncodeToString(digest[:]) != manifest.Input.SHA256 || imageErr != nil || "image/"+format != manifest.Input.MIME ||
		config.Width < 1 || config.Height < 1 || config.Width > 16384 || config.Height > 16384 ||
		int64(config.Width)*int64(config.Height) > 64_000_000 {
		return ReferenceResumableResult{}, ErrReferenceConflict
	}
	now := time.Now()
	if os.Chtimes(part, now, now) != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	asset := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.asset", user, manifest.Input.SHA256))
	if replaceReferenceFile(part, asset) != nil || syncReferenceDirectory(s.config.Directory) != nil {
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	manifest.Complete = true
	if s.saveUpload(manifest) != nil {
		// A restart/retry still detects the fully verified, atomically published
		// asset. It is safe to answer a retry without re-uploading or regenerating.
		return ReferenceResumableResult{}, ErrReferenceUnavailable
	}
	return s.uploadResult(manifest), nil
}

// Called with the store mutex held. The full promised size of each incomplete
// session is counted; .upload-part must not be counted again.
func (s *ReferenceUploadStore) resumableUsageLocked(user int) (int64, int) {
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return s.config.MaxCacheBytes, 10000
	}
	var used int64
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".upload-part") && !entry.IsDir() &&
			(user == 0 || strings.HasPrefix(entry.Name(), strconv.Itoa(user)+"-")) {
			path := filepath.Join(s.config.Directory, entry.Name())
			manifestPath := strings.TrimSuffix(path, "-part")
			if !fileExistsReference(manifestPath) {
				if info, err := entry.Info(); err == nil {
					if time.Since(info.ModTime()) >= time.Hour && os.Remove(path) == nil {
						continue
					}
					used += info.Size()
					count++
				}
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), ".upload-manifest-") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(s.config.Directory, entry.Name()))
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".upload") || entry.IsDir() ||
			user > 0 && !strings.HasPrefix(entry.Name(), strconv.Itoa(user)+"-") {
			continue
		}
		name := filepath.Join(s.config.Directory, entry.Name())
		raw, err := os.ReadFile(name)
		var manifest referenceUploadManifest
		if err != nil || len(raw) > 8192 || common.Unmarshal(raw, &manifest) != nil ||
			validateReferenceInput(manifest.UserID, manifest.Input) != nil ||
			!validReferenceID(manifest.UploadID) ||
			entry.Name() != fmt.Sprintf("%d-%s.upload", manifest.UserID, manifest.UploadID) {
			// Corrupt metadata is not silently reusable. Quarantine it by charging
			// a maximum file until an operator or the age-based cleanup removes it.
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > referenceUploadLifetime {
				if os.Remove(name) == nil {
					continue
				}
			}
			used += ReferenceMaxBytes + 8192
			count++
			continue
		}
		if manifest.ExpiresAt <= time.Now().Unix() {
			_, part := s.uploadPaths(manifest.UserID, manifest.UploadID)
			if os.Remove(part) == nil || !fileExistsReference(part) {
				if os.Remove(name) == nil {
					continue
				}
			}
		}
		used += 8192
		if s.uploadResult(&manifest).Status != "ready" {
			used += manifest.Input.Size
		}
		count++
	}
	return used, count
}

func fileExistsReference(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func (s *ReferenceUploadStore) referenceUserUsageLocked(user int) (int64, int) {
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return s.config.MaxCacheBytes, 10000
	}
	used, count := s.resumableUsageLocked(user)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), strconv.Itoa(user)+"-") ||
			(!strings.HasSuffix(entry.Name(), ".asset") && !strings.HasSuffix(entry.Name(), ".url") && !strings.HasSuffix(entry.Name(), ".part")) {
			continue
		}
		if info, err := entry.Info(); err == nil {
			used += info.Size()
			count++
		}
	}
	return used, count
}
