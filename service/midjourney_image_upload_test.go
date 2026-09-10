package service

import (
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMidjourneyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestStoreMidjourneyImagesValidatesAndServesImages(t *testing.T) {
	t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", t.TempDir())
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test/"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })

	result, err := StoreMidjourneyImages([]string{"data:image/png;base64," + testMidjourneyPNG})
	require.NoError(t, err)
	require.Len(t, result.URLs, 1)
	name := filepath.Base(result.URLs[0])
	require.True(t, isMidjourneyUploadName(name))

	recorder := httptest.NewRecorder()
	require.NoError(t, ServeMidjourneyUpload(recorder, httptest.NewRequest("GET", "/mj/upload/"+name, nil), name))
	assert.Equal(t, "image/png", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "\x89PNG", recorder.Body.String()[:4])
}

func TestGetMidjourneyUploadStoragePathUsesDataMountByDefault(t *testing.T) {
	t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", "")
	assert.Equal(t, filepath.Clean("/data/midjourney-uploads"), GetMidjourneyUploadStoragePath())
}

func TestStoreMidjourneyImagesRejectsInvalidAndUnsafeInputs(t *testing.T) {
	t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", t.TempDir())
	cases := []string{
		testMidjourneyPNG,
		"data:image/gif;base64," + testMidjourneyPNG,
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")),
		"data:image/jpeg;base64," + testMidjourneyPNG,
	}
	for _, value := range cases {
		_, err := StoreMidjourneyImages([]string{value})
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrInvalidMidjourneyUpload), err)
	}
	_, err := StoreMidjourneyImages([]string{})
	require.Error(t, err)
	tooMany := []string{"data:image/png;base64," + testMidjourneyPNG, "data:image/png;base64," + testMidjourneyPNG, "data:image/png;base64," + testMidjourneyPNG, "data:image/png;base64," + testMidjourneyPNG, "data:image/png;base64," + testMidjourneyPNG}
	_, err = StoreMidjourneyImages(tooMany)
	require.Error(t, err)
	_, err = StoreMidjourneyImages([]string{"data:image/png;base64," + strings.Repeat("A", ((midjourneyUploadMaxBytes+2)/3)*4+1)})
	require.Error(t, err)
	require.Error(t, ServeMidjourneyUpload(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "../secret.png"))
}

func TestCleanupExpiredMidjourneyUploads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", dir)
	t.Setenv("MIDJOURNEY_UPLOAD_RETENTION_HOURS", "1")
	oldName := strings.Repeat("a", 32) + ".png"
	oldPath := filepath.Join(dir, oldName)
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0600))
	require.NoError(t, os.Chtimes(oldPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)))
	keepName := strings.Repeat("b", 32) + ".png"
	require.NoError(t, os.WriteFile(filepath.Join(dir, keepName), []byte("keep"), 0600))
	require.NoError(t, CleanupExpiredMidjourneyUploads())
	_, err := os.Stat(oldPath)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, keepName))
	assert.NoError(t, err)
}
