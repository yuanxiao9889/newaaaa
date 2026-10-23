package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/disk"
	"gorm.io/gorm"
)

const referenceSessionLifetime = 15 * time.Minute
const referenceUploadDeadline = 90 * time.Second

func (s *ReferenceUploadStore) DirectEnabled() bool {
	return s != nil && s.config.DirectEnabled
}

func (s *ReferenceUploadStore) LegacyEnabled() bool {
	return s != nil && s.config.AccessKey != ""
}

func (s *ReferenceUploadStore) initDirectReferences() error {
	s.directDir = filepath.Join(s.config.Directory, "direct-v2")
	if err := os.MkdirAll(s.directDir, 0700); err != nil {
		return ErrReferenceUnavailable
	}
	marker := filepath.Join(s.directDir, ".storage-id")
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := file.WriteString(uuid.NewString())
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return ErrReferenceUnavailable
		}
	} else if !errors.Is(err, os.ErrExist) {
		return ErrReferenceUnavailable
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		return ErrReferenceUnavailable
	}
	if _, err = uuid.Parse(string(raw)); err != nil {
		return ErrReferenceUnavailable
	}
	s.storageID = string(raw)
	return nil
}

func (s *ReferenceUploadStore) directUploadTicket(asset *model.ReferenceAsset) string {
	mac := hmac.New(sha256.New, []byte(s.config.SigningSecret))
	_, _ = fmt.Fprintf(mac, "reference-upload-v2:%d:%s:%s:%d", asset.UserID, asset.SessionID, asset.SHA256, asset.SessionExpires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *ReferenceUploadStore) directResult(asset *model.ReferenceAsset) ReferenceUploadResult {
	result := ReferenceUploadResult{Status: asset.State, AssetID: asset.SHA256, SessionID: asset.SessionID,
		ExpiresAt: asset.IdleExpires, UploadExpiresAt: asset.SessionExpires}
	if asset.State == "uploading" && asset.LeaseUntil <= time.Now().Unix() {
		result.Status = "upload_required"
	}
	if result.Status == "upload_required" {
		result.UploadMethod = "PUT"
		result.UploadURL = "/v1/uploads/references/sessions/" + asset.SessionID
		result.UploadHeaders = map[string]string{"X-Reference-Upload-Ticket": s.directUploadTicket(asset)}
	}
	return result
}

// PrepareDirect reserves capacity before accepting binary input. A preparation
// ID is scoped to the authenticated account, and replay never renews retention.
func (s *ReferenceUploadStore) PrepareDirect(user int, input ReferencePrepareRequest) (ReferenceUploadResult, error) {
	if !s.DirectEnabled() {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	if input.Mode != "" || input.ETag != "" || !model.ValidTransferRequestKey(input.RequestID) ||
		validateReferenceInput(user, input) != nil {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	key := sha256.Sum256([]byte(input.RequestID))
	keyHash := hex.EncodeToString(key[:])
	var result ReferenceUploadResult
	err := model.WithReferenceAdmission(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		if err := s.cleanupDirect(tx, now); err != nil {
			return err
		}
		var preparation model.ReferencePreparation
		err := tx.Where("user_id = ? AND key_hash = ?", user, keyHash).First(&preparation).Error
		replay := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var asset model.ReferenceAsset
		err = tx.Where("user_id = ? AND sha256 = ?", user, input.SHA256).First(&asset).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if replay && (asset.ID == 0 || preparation.AssetID != asset.ID || preparation.SessionID != asset.SessionID) {
			return ErrReferenceInvalid
		}
		if asset.ID != 0 {
			if asset.StorageID != s.storageID {
				return ErrReferenceUnavailable // Never duplicate a different node's authoritative file.
			}
			if asset.Size != input.Size || asset.MIME != input.MIME {
				return ErrReferenceInvalid
			}
			if asset.State == "ready" {
				if _, _, err := s.directBytes(&asset); err != nil {
					if !errors.Is(err, ErrReferenceMissing) || replay {
						return err
					}
					// Only an explicit new preparation may replace a lost file.
					// Old upload tickets cannot write to the new session.
					asset.State, asset.SessionID = "upload_required", uuid.NewString()
					asset.Generation, asset.FileName = "", ""
					asset.LeaseUntil, asset.IdleExpires = 0, 0
					asset.SessionExpires = time.Now().Add(referenceSessionLifetime).Unix()
					if err := tx.Where("asset_id = ?", asset.ID).Delete(&model.ReferencePreparation{}).Error; err != nil {
						return err
					}
					if err := tx.Save(&asset).Error; err != nil {
						return err
					}
					if err := s.cleanupDirect(tx, now); err != nil {
						return err
					}
				}
				if !replay && asset.State == "ready" {
					asset.IdleExpires = time.Now().Add(ReferenceRetention()).Unix()
					if err := tx.Save(&asset).Error; err != nil {
						return err
					}
				}
			}
		} else {
			var assets []model.ReferenceAsset
			if err := tx.Where("storage_id = ?", s.storageID).Find(&assets).Error; err != nil {
				return err
			}
			var total, owned int64
			var userCount int
			for _, existing := range assets {
				total += existing.Size
				if existing.UserID == user {
					owned += existing.Size
					userCount++
				}
			}
			space, err := disk.Usage(s.directDir)
			if err != nil {
				return ErrReferenceUnavailable
			}
			// Reservations include uploads not written yet. Leave the configured
			// filesystem reserve free; never evict live assets.
			var reserved int64
			for _, existing := range assets {
				if existing.State != "ready" {
					reserved += existing.Size
				}
			}
			if len(assets) >= 10000 || userCount >= 1000 ||
				total+input.Size > s.config.MaxCacheBytes || owned+input.Size > s.config.MaxUserCacheBytes ||
				space.Free < max(uint64(2<<30), space.Total*uint64(s.config.MinFreePercent)/100)+uint64(reserved+input.Size) {
				return ErrReferenceBusy
			}
			asset = model.ReferenceAsset{UserID: user, SHA256: input.SHA256, StorageID: s.storageID,
				SessionID: uuid.NewString(), State: "upload_required", Size: input.Size, MIME: input.MIME,
				SessionExpires: time.Now().Add(referenceSessionLifetime).Unix()}
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
		}
		if !replay {
			var count int64
			if err := tx.Model(&model.ReferencePreparation{}).Where("user_id = ?", user).Count(&count).Error; err != nil {
				return err
			}
			if count >= 10000 {
				return ErrReferenceBusy
			}
			if err := tx.Create(&model.ReferencePreparation{UserID: user, KeyHash: keyHash,
				AssetID: asset.ID, SessionID: asset.SessionID}).Error; err != nil {
				return err
			}
		}
		result = s.directResult(&asset)
		return nil
	})
	if err != nil || result.Status != "upload_required" {
		return result, err
	}
	// A legacy verified .asset can be adopted without another client upload.
	// A URL record is only an import source, never proof of local readiness.
	var source io.Reader
	data, mime, readErr := s.readLegacyReference(user, input.SHA256, false)
	if readErr == nil && int64(len(data)) == input.Size && mime == input.MIME {
		source = bytes.NewReader(data)
	} else if s.config.AccessKey != "" {
		recordPath := filepath.Join(s.config.Directory, fmt.Sprintf("%d-%s.url", user, input.SHA256))
		file, openErr := os.Open(recordPath)
		if openErr == nil {
			raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
			_ = file.Close()
			var record referenceURLRecord
			if readErr == nil && len(raw) <= 4096 && common.Unmarshal(raw, &record) == nil &&
				record.Ticket.UserID == user && record.Ticket.SHA256 == input.SHA256 &&
				record.Ticket.Size == input.Size && record.Ticket.MIME == input.MIME &&
				record.Bucket == s.config.Bucket && record.Origin == s.config.DownloadURL &&
				record.Until > time.Now().Unix() &&
				strings.HasPrefix(record.Ticket.Key, fmt.Sprintf("references/%d/%s/", user, input.SHA256)) {
				lazy := &legacyReferenceImport{store: s, address: s.signedObjectURL(record.Ticket.Key, record.Until)}
				defer lazy.Close()
				source = lazy
			}
		}
	}
	if source == nil {
		return result, nil
	}
	imported, importErr := s.UploadDirect(user, result.SessionID, result.UploadHeaders["X-Reference-Upload-Ticket"], source)
	if importErr == nil {
		return imported, nil
	}
	// Failed migration never declares ready. The same session remains available
	// for an authenticated direct upload or observation of the winning writer.
	if errors.Is(importErr, ErrReferenceBusy) || errors.Is(importErr, ErrReferenceInvalid) {
		return s.DirectSession(user, result.SessionID)
	}
	return ReferenceUploadResult{}, importErr
}

// Opening the old OSS source is delayed until UploadDirect owns the database
// lease, so different processes cannot trigger duplicate migration downloads.
type legacyReferenceImport struct {
	store   *ReferenceUploadStore
	address string
	body    io.ReadCloser
}

func (r *legacyReferenceImport) Read(data []byte) (int, error) {
	if r.body == nil {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, r.address, nil)
		if err != nil {
			return 0, ErrReferenceDownload
		}
		response, err := r.store.client.Do(request)
		if err != nil {
			return 0, ErrReferenceDownload
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return 0, ErrReferenceDownload
		}
		r.body = response.Body
	}
	return r.body.Read(data)
}

func (r *legacyReferenceImport) Close() error {
	if r.body == nil {
		return nil
	}
	return r.body.Close()
}

func (s *ReferenceUploadStore) DirectSession(user int, session string) (ReferenceUploadResult, error) {
	if !s.DirectEnabled() {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	if _, err := uuid.Parse(session); err != nil || user <= 0 {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	var result ReferenceUploadResult
	err := model.WithReferenceAdmission(func(tx *gorm.DB) error {
		var asset model.ReferenceAsset
		if err := tx.Where("user_id = ? AND session_id = ?", user, session).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReferenceMissing
			}
			return err
		}
		if asset.StorageID != s.storageID {
			return ErrReferenceUnavailable
		}
		if asset.State == "ready" {
			if asset.IdleExpires <= time.Now().Unix() {
				return ErrReferenceMissing
			}
			if _, _, err := s.directBytes(&asset); err != nil {
				return err
			}
		} else if asset.SessionExpires <= time.Now().Unix() {
			return ErrReferenceMissing
		}
		result = s.directResult(&asset)
		return nil
	})
	return result, err
}

// UploadDirect uses a fixed lease and a fresh generation for every writer. The
// request body is bounded in memory; a stale writer cannot publish any file.
func (s *ReferenceUploadStore) UploadDirect(user int, session, ticket string, body io.Reader) (ReferenceUploadResult, error) {
	if !s.DirectEnabled() {
		return ReferenceUploadResult{}, ErrReferenceUnavailable
	}
	if _, err := uuid.Parse(session); err != nil || user <= 0 || len(ticket) != 64 {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ReferenceUploadResult{}, ErrReferenceBusy
	}
	var asset model.ReferenceAsset
	err := model.WithReferenceAdmission(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND session_id = ?", user, session).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReferenceMissing
			}
			return err
		}
		if asset.StorageID != s.storageID {
			return ErrReferenceUnavailable
		}
		if !hmac.Equal([]byte(ticket), []byte(s.directUploadTicket(&asset))) {
			return ErrReferenceInvalid
		}
		now := time.Now().Unix()
		if asset.SessionExpires <= now {
			return ErrReferenceMissing
		}
		if asset.State == "ready" {
			_, _, err := s.directBytes(&asset)
			return err
		}
		if asset.State == "uploading" && asset.LeaseUntil > now {
			return ErrReferenceBusy
		}
		var active int64
		if err := tx.Model(&model.ReferenceAsset{}).Where("state = ? AND lease_until > ?", "uploading", now).Count(&active).Error; err != nil {
			return err
		}
		if active >= 8 {
			return ErrReferenceBusy
		}
		asset.State, asset.Generation = "uploading", uuid.NewString()
		asset.LeaseUntil = min(asset.SessionExpires, time.Now().Add(referenceUploadDeadline).Unix())
		return tx.Save(&asset).Error
	})
	if err != nil {
		return ReferenceUploadResult{}, err
	}
	if asset.State == "ready" {
		return s.directResult(&asset), nil
	}
	data, readErr := io.ReadAll(io.LimitReader(body, asset.Size+1))
	hash := sha256.Sum256(data)
	imageConfig, format, decodeErr := decodeImageConfig(data)
	valid := readErr == nil && int64(len(data)) == asset.Size && hex.EncodeToString(hash[:]) == asset.SHA256 &&
		decodeErr == nil && "image/"+format == asset.MIME && imageConfig.Width > 0 && imageConfig.Height > 0 &&
		imageConfig.Width <= 16384 && imageConfig.Height <= 16384 && int64(imageConfig.Width)*int64(imageConfig.Height) <= 64_000_000
	err = model.WithReferenceAdmission(func(tx *gorm.DB) error {
		var current model.ReferenceAsset
		if err := tx.First(&current, asset.ID).Error; err != nil {
			return ErrReferenceMissing
		}
		if current.Generation != asset.Generation || current.State != "uploading" || current.LeaseUntil <= time.Now().Unix() {
			return ErrReferenceBusy
		}
		if !valid {
			// Commit the released lease so a corrected upload can use the same session.
			return tx.Model(&current).Updates(map[string]any{"state": "upload_required", "lease_until": 0}).Error
		}
		space, err := disk.Usage(s.directDir)
		if err != nil || space.Free < max(uint64(2<<30), space.Total*uint64(s.config.MinFreePercent)/100)+uint64(len(data)) {
			return ErrReferenceBusy
		}
		name := asset.Generation + ".asset"
		part := filepath.Join(s.directDir, asset.Generation+".part")
		file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return ErrReferenceUnavailable
		}
		defer os.Remove(part)
		_, writeErr := file.Write(data)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || os.Rename(part, filepath.Join(s.directDir, name)) != nil {
			return ErrReferenceUnavailable
		}
		current.State, current.FileName, current.LeaseUntil = "ready", name, 0
		if asset.LeaseUntil <= time.Now().Unix() {
			return ErrReferenceBusy
		}
		current.IdleExpires = time.Now().Add(ReferenceRetention()).Unix()
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		asset = current
		return nil
	})
	if err != nil {
		return ReferenceUploadResult{}, err
	}
	if !valid {
		return ReferenceUploadResult{}, ErrReferenceInvalid
	}
	return s.directResult(&asset), nil
}

func (s *ReferenceUploadStore) directBytes(asset *model.ReferenceAsset) ([]byte, string, error) {
	if _, err := uuid.Parse(strings.TrimSuffix(asset.FileName, ".asset")); err != nil || !strings.HasSuffix(asset.FileName, ".asset") {
		return nil, "", ErrReferenceMissing
	}
	file, err := os.Open(filepath.Join(s.directDir, asset.FileName))
	if err != nil {
		return nil, "", ErrReferenceMissing
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Size || info.Size() > ReferenceMaxBytes {
		return nil, "", ErrReferenceMissing
	}
	data, err := io.ReadAll(io.LimitReader(file, ReferenceMaxBytes+1))
	hash := sha256.Sum256(data)
	if err != nil || int64(len(data)) != asset.Size || hex.EncodeToString(hash[:]) != asset.SHA256 {
		return nil, "", ErrReferenceMissing
	}
	return data, asset.MIME, nil
}

func (s *ReferenceUploadStore) ReadDirect(user int, id string, touch bool) ([]byte, string, error) {
	var data []byte
	var mime string
	err := model.WithReferenceAdmission(func(tx *gorm.DB) error {
		var asset model.ReferenceAsset
		if err := tx.Where("user_id = ? AND sha256 = ?", user, id).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReferenceMissing
			}
			return err
		}
		if asset.StorageID != s.storageID {
			return ErrReferenceUnavailable
		}
		if asset.State != "ready" {
			return ErrReferenceMissing
		}
		if asset.IdleExpires <= time.Now().Unix() {
			protected, err := protectedTaskReferences(tx, user)
			if err != nil {
				return err
			}
			if !protected[fmt.Sprintf("%d-%s", user, id)] {
				return ErrReferenceMissing
			}
		}
		var err error
		data, mime, err = s.directBytes(&asset)
		if err != nil {
			return err
		}
		if touch {
			return tx.Model(&asset).Update("idle_expires", time.Now().Add(ReferenceRetention()).Unix()).Error
		}
		return nil
	})
	return data, mime, err
}

func (s *ReferenceUploadStore) CleanupDirect() error {
	if !s.DirectEnabled() {
		return nil
	}
	return model.WithReferenceAdmission(func(tx *gorm.DB) error {
		return s.cleanupDirect(tx, time.Now().Unix())
	})
}

func (s *ReferenceUploadStore) cleanupDirect(tx *gorm.DB, now int64) error {
	protected, err := protectedTaskReferences(tx, 0)
	if err != nil {
		return err
	}
	var assets []model.ReferenceAsset
	if err := tx.Where("storage_id = ?", s.storageID).Find(&assets).Error; err != nil {
		return err
	}
	live := make(map[string]bool)
	for _, asset := range assets {
		expired := asset.State == "ready" && asset.IdleExpires <= now && !protected[fmt.Sprintf("%d-%s", asset.UserID, asset.SHA256)] ||
			asset.State != "ready" && asset.SessionExpires <= now
		if !expired {
			live[asset.FileName] = true
			continue
		}
		if err := tx.Where("asset_id = ?", asset.ID).Delete(&model.ReferencePreparation{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&asset).Error; err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(s.directDir)
	if err != nil {
		return ErrReferenceUnavailable
	}
	for _, entry := range entries {
		name := entry.Name()
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".asset"), ".part")
		if entry.IsDir() || live[name] || stem == name {
			continue
		}
		if _, err := uuid.Parse(stem); err != nil {
			continue
		}
		// File creation/publication and cleanup hold the same database lock.
		// Upload workers outside this transaction only own bounded memory.
		if err := os.Remove(filepath.Join(s.directDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrReferenceUnavailable
		}
	}
	return nil
}
