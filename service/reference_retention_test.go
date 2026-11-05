package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/disk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupReferenceRetentionStore(t *testing.T) *ReferenceUploadStore {
	t.Helper()
	var dialect gorm.Dialector = sqlite.Open(":memory:")
	if dsn := os.Getenv("REFERENCE_RETENTION_TEST_DSN"); dsn != "" {
		require.Contains(t, dsn, "bridge_security_test_refcleanup")
		if strings.HasPrefix(dsn, "postgres://") {
			dialect = postgres.Open(dsn)
		} else {
			dialect = mysql.Open(dsn)
		}
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	previous := model.DB
	model.DB = db
	tables := []any{&model.Task{}, &model.ReferenceAsset{}, &model.ReferencePreparation{}, &model.ReferenceAdmission{}}
	require.NoError(t, db.AutoMigrate(tables...))
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(tables...))
		model.DB = previous
		require.NoError(t, conn.Close())
	})
	var version string
	query := "SELECT version()"
	if db.Dialector.Name() == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("database=%s version=%s", db.Dialector.Name(), version)
	store, err := NewReferenceUploadStore(ReferenceUploadConfig{AccessKey: "test", SecretKey: "test",
		Bucket: "test", UploadURL: "https://upload.example", DownloadURL: "https://refs.example",
		Directory: t.TempDir(), DirectEnabled: true, SigningSecret: strings.Repeat("s", 32),
		MaxCacheBytes: 8 << 20, MaxUserCacheBytes: 8 << 20})
	require.NoError(t, err)
	store.diskUsage = func(string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Free: 100 << 30, Total: 200 << 30}, nil
	}
	return store
}

func TestReferenceRetentionReclaimsOldCompletedReceipts(t *testing.T) {
	store := setupReferenceRetentionStore(t)
	for _, remainingAsset := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_asset_exists=%t", remainingAsset), func(t *testing.T) {
			input := ReferencePrepareRequest{SHA256: strings.Repeat("a", 64), Size: 4 << 20, MIME: "image/png"}
			prepared, err := store.PrepareResumable(7, input)
			require.NoError(t, err)
			path, _ := store.uploadPaths(7, prepared.UploadID)
			manifest, err := store.loadUpload(7, prepared.UploadID)
			require.NoError(t, err)
			manifest.Complete = true
			manifest.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
			raw, err := common.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			asset := filepath.Join(store.config.Directory, "7-"+input.SHA256+".asset")
			if remainingAsset {
				require.NoError(t, os.WriteFile(asset, []byte("expired image"), 0600))
				past := time.Now().Add(-ReferenceRetention() - time.Minute)
				require.NoError(t, os.Chtimes(asset, past, past))
			}
			store.Cleanup()
			assert.NoFileExists(t, path, "old completed receipts must not reserve absent bytes")
			assert.NoFileExists(t, asset)
			_, err = store.PrepareResumable(7, input)
			require.NoError(t, err, "new upload remains possible without manually clearing quota")
			require.NoError(t, os.Remove(path))
		})
	}
}

func TestReferenceRetentionSharedExpiryAndRenewal(t *testing.T) {
	store := setupReferenceRetentionStore(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}))
	previousPolicy := model.CurrentRequestPolicy().Options
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = maps.Clone(previousPolicy)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, model.UpdateRequestPolicyOptions(previousPolicy))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, model.DB.Migrator().DropTable(&model.Option{}))
	})
	require.NoError(t, model.UpdateRequestPolicyOptions(map[string]string{"ImageInputRetentionMinutes": "60"}))
	assert.Equal(t, time.Hour, ReferenceRetention())
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	data := buffer.Bytes()
	hash := sha256.Sum256(data)
	input := ReferencePrepareRequest{SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data)), MIME: "image/png"}
	before := time.Now().Add(time.Hour).Unix()
	prepared, err := store.PrepareResumable(7, input)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, prepared.ExpiresAt, before)
	assert.LessOrEqual(t, prepared.ExpiresAt, time.Now().Add(time.Hour).Unix())
	_, err = store.WriteResumableChunk(7, prepared.UploadID, 0, input.SHA256, bytes.NewReader(data))
	require.NoError(t, err)
	completed, err := store.CompleteResumable(7, prepared.UploadID)
	require.NoError(t, err)
	assetPath := filepath.Join(store.config.Directory, "7-"+input.SHA256+".asset")
	lastUse := time.Now().Add(-40 * time.Minute).Truncate(time.Second)
	require.NoError(t, os.Chtimes(assetPath, lastUse, lastUse))
	status, err := store.ResumableStatus(7, prepared.UploadID)
	require.NoError(t, err)
	assert.Equal(t, lastUse.Add(time.Hour).Unix(), status.ExpiresAt, "polling does not renew retention")
	_, _, err = store.Read(7, input.SHA256, false)
	require.NoError(t, err)
	info, err := os.Stat(assetPath)
	require.NoError(t, err)
	assert.Equal(t, lastUse.Unix(), info.ModTime().Unix(), "reading without use does not renew retention")
	before = time.Now().Add(time.Hour).Unix()
	_, _, err = store.Read(7, input.SHA256, true)
	require.NoError(t, err)
	status, err = store.ResumableStatus(7, prepared.UploadID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, status.ExpiresAt, before, "use renews to now plus one hour")
	assert.LessOrEqual(t, status.ExpiresAt, time.Now().Add(time.Hour).Unix())
	completed.ExpiresAt = status.ExpiresAt
	manifest, err := store.loadUpload(7, prepared.UploadID)
	require.NoError(t, err)
	assert.Equal(t, completed.ExpiresAt, manifest.ExpiresAt)
	path, _ := store.uploadPaths(7, prepared.UploadID)
	past := time.Now().Add(-ReferenceRetention() - time.Minute)
	require.NoError(t, os.Chtimes(path, past, past))
	status, err = store.ResumableStatus(7, prepared.UploadID)
	require.NoError(t, err, "renewed asset remains available despite an older receipt")
	assert.Equal(t, "ready", status.Status)
	input.SHA256 = strings.Repeat("b", 64)
	partial, err := store.PrepareResumable(7, input)
	require.NoError(t, err)
	path, _ = store.uploadPaths(7, partial.UploadID)
	require.NoError(t, os.Chtimes(path, past, past))
	_, err = store.ResumableStatus(7, partial.UploadID)
	assert.ErrorIs(t, err, ErrReferenceMissing)
	store.Cleanup()
	assert.NoFileExists(t, path)

	before = time.Now().Add(time.Hour).Unix()
	direct, err := store.PrepareDirect(8, ReferencePrepareRequest{SHA256: input.SHA256, Size: input.Size, MIME: input.MIME, RequestID: uuid.NewString()})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, direct.UploadExpiresAt, before)
	assert.LessOrEqual(t, direct.UploadExpiresAt, time.Now().Add(time.Hour).Unix())
	_, err = store.UploadDirect(9, direct.SessionID, direct.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	assert.ErrorIs(t, err, ErrReferenceMissing, "tickets remain scoped to their owner")
	require.NoError(t, model.DB.Model(&model.ReferenceAsset{}).Where("session_id = ?", direct.SessionID).Update("session_expires", time.Now().Add(-time.Second).Unix()).Error)
	_, err = store.UploadDirect(8, direct.SessionID, direct.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	assert.Error(t, err, "expired tickets cannot upload or renew a session")

	direct, err = store.PrepareDirect(9, ReferencePrepareRequest{SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data)), MIME: "image/png", RequestID: uuid.NewString()})
	require.NoError(t, err)
	_, err = store.UploadDirect(9, direct.SessionID, direct.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	require.NoError(t, err)
	oldExpiry := lastUse.Add(time.Hour).Unix()
	require.NoError(t, model.DB.Model(&model.ReferenceAsset{}).Where("session_id = ?", direct.SessionID).Update("idle_expires", oldExpiry).Error)
	_, _, err = store.ReadDirect(9, hex.EncodeToString(hash[:]), false)
	require.NoError(t, err)
	directStatus, err := store.DirectSession(9, direct.SessionID)
	require.NoError(t, err)
	assert.Equal(t, oldExpiry, directStatus.ExpiresAt, "direct status and passive reads do not renew")
	before = time.Now().Add(time.Hour).Unix()
	_, _, err = store.ReadDirect(9, hex.EncodeToString(hash[:]), true)
	require.NoError(t, err)
	directStatus, err = store.DirectSession(9, direct.SessionID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, directStatus.ExpiresAt, before, "direct use renews from the current time")
	assert.LessOrEqual(t, directStatus.ExpiresAt, time.Now().Add(time.Hour).Unix())
}

func TestReferenceRetentionDiskPressureProtectsTasksAndUploads(t *testing.T) {
	store := setupReferenceRetentionStore(t)
	protectedID, oldID, newID := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for i, id := range []string{protectedID, oldID, newID} {
		path := filepath.Join(store.config.Directory, "7-"+id+".asset")
		require.NoError(t, os.WriteFile(path, []byte("image"), 0600))
		past := time.Now().Add(-time.Duration(3-i) * time.Minute)
		require.NoError(t, os.Chtimes(path, past, past))
	}
	require.NoError(t, model.DB.Create(&model.Task{TaskID: "protected-reference", UserId: 7, Status: model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{ReferenceAssetIDs: []string{protectedID}}}).Error)
	partial, err := store.PrepareResumable(8, ReferencePrepareRequest{SHA256: strings.Repeat("d", 64), Size: 100, MIME: "image/png"})
	require.NoError(t, err)
	partialPath, partPath := store.uploadPaths(8, partial.UploadID)
	require.NoError(t, os.WriteFile(partPath, []byte("partial upload"), 0600))
	oldPath := filepath.Join(store.config.Directory, "7-"+oldID+".asset")
	store.diskUsage = func(string) (*disk.UsageStat, error) {
		free := uint64(20<<30) - 1
		if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
			free += 1 << 20
		}
		return &disk.UsageStat{Free: free, Total: 100 << 30}, nil
	}
	store.Cleanup()
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, filepath.Join(store.config.Directory, "7-"+newID+".asset"), "stop once the reserve is restored")
	assert.FileExists(t, filepath.Join(store.config.Directory, "7-"+protectedID+".asset"))
	assert.FileExists(t, partialPath)
	assert.FileExists(t, partPath)
	store.diskUsage = func(string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Free: 19 << 30, Total: 100 << 30}, nil
	}
	_, err = store.PrepareResumable(9, ReferencePrepareRequest{SHA256: strings.Repeat("e", 64), Size: 100, MIME: "image/png"})
	assert.ErrorIs(t, err, ErrReferenceDiskSpace)
	assert.FileExists(t, partialPath)
	assert.FileExists(t, partPath)
	assert.FileExists(t, filepath.Join(store.config.Directory, "7-"+protectedID+".asset"))
	store.diskUsage = func(string) (*disk.UsageStat, error) { return nil, errors.New("disk measurement failed") }
	_, err = store.PrepareResumable(9, ReferencePrepareRequest{SHA256: strings.Repeat("f", 64), Size: 100, MIME: "image/png"})
	assert.ErrorIs(t, err, ErrReferenceUnavailable)
}

func TestReferenceRetentionDirectRollingCleanup(t *testing.T) {
	store := setupReferenceRetentionStore(t)
	var oldest string
	for i := range 3 {
		id := strings.Repeat(string(rune('a'+i)), 64)
		name := uuid.NewString() + ".asset"
		require.NoError(t, os.WriteFile(filepath.Join(store.directDir, name), []byte("image"), 0600))
		asset := model.ReferenceAsset{UserID: 7, SHA256: id, SessionID: uuid.NewString(), StorageID: store.storageID,
			State: "ready", Size: 5, FileName: name, IdleExpires: time.Now().Add(time.Duration(i+1) * time.Minute).Unix()}
		require.NoError(t, model.DB.Create(&asset).Error)
		if i == 0 {
			require.NoError(t, model.DB.Create(&model.Task{TaskID: "direct-protected", UserId: 7, Status: model.TaskStatusInProgress,
				PrivateData: model.TaskPrivateData{ReferenceAssetIDs: []string{id}}}).Error)
		}
		if i == 1 {
			oldest = name
		}
	}
	store.diskUsage = func(string) (*disk.UsageStat, error) {
		free := uint64(20<<30) - 1
		if _, err := os.Stat(filepath.Join(store.directDir, oldest)); errors.Is(err, os.ErrNotExist) {
			free += 100
		}
		return &disk.UsageStat{Free: free, Total: 100 << 30}, nil
	}
	require.NoError(t, store.CleanupDirect())
	assert.NoFileExists(t, filepath.Join(store.directDir, oldest))
	var assets []model.ReferenceAsset
	require.NoError(t, model.DB.Order("sha256").Find(&assets).Error)
	require.Len(t, assets, 2)
	assert.Equal(t, strings.Repeat("a", 64), assets[0].SHA256)
	assert.Equal(t, strings.Repeat("c", 64), assets[1].SHA256)
	for _, asset := range assets {
		assert.FileExists(t, filepath.Join(store.directDir, asset.FileName))
	}
	require.NoError(t, store.CleanupDirect(), "repeated cleanup preserves surviving records and files")
}
