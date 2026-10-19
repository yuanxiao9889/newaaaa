package router

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferenceUploadProtocol(t *testing.T) {
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a3WQAAAAASUVORK5CYII=")
	require.NoError(t, err)
	digest := sha256.Sum256(image)
	id := hex.EncodeToString(digest[:])
	var mu sync.Mutex
	var ranges []string
	var badBody, redirect bool
	var pauseAfterPartial bool
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		ranges = append(ranges, r.Header.Get("Range"))
		if redirect {
			w.Header().Set("Location", "https://example.invalid/private")
			w.WriteHeader(302)
			return
		}
		if pauseAfterPartial && r.Header.Get("Range") != "" {
			w.WriteHeader(503)
			return
		}
		data := image
		if badBody {
			data = bytes.Repeat([]byte("x"), len(image))
		}
		offset := 0
		if r.Header.Get("Range") != "" {
			_, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &offset)
			if err != nil || offset >= len(data) {
				w.WriteHeader(416)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(data)-1, len(data)))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)-offset))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		}
		if len(ranges) == 1 {
			_, _ = w.Write(data[:len(data)/2])
			return
		}
		_, _ = w.Write(data[offset:])
	}))
	t.Cleanup(origin.Close)
	oldTransport := http.DefaultTransport
	http.DefaultTransport = origin.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	directory := t.TempDir()
	store, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "test-ak", SecretKey: "test-secret", Bucket: "test-bucket", UploadURL: origin.URL, DownloadURL: origin.URL, Directory: directory, MaxCacheBytes: 32 << 20})
	require.NoError(t, err)
	oldStore, oldAddress := service.ReferenceUploads, system_setting.ServerAddress
	service.ReferenceUploads, system_setting.ServerAddress = store, "https://relay.example"
	t.Cleanup(func() { service.ReferenceUploads, system_setting.ServerAddress = oldStore, oldAddress })
	input := service.ReferencePrepareRequest{SHA256: id, Size: int64(len(image)), MIME: "image/png"}
	prepared, err := store.Prepare(7, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", prepared.Status)
	policyParts := strings.Split(prepared.UploadToken, ":")
	require.Len(t, policyParts, 3)
	policyBytes, err := base64.URLEncoding.DecodeString(policyParts[2])
	require.NoError(t, err)
	var policy map[string]any
	require.NoError(t, common.Unmarshal(policyBytes, &policy))
	assert.Equal(t, "test-bucket:"+prepared.ObjectKey, policy["scope"])
	assert.Equal(t, float64(1), policy["insertOnly"])
	assert.Equal(t, float64(1), policy["deleteAfterDays"])
	assert.Equal(t, float64(len(image)), policy["fsizeLimit"])
	assert.Equal(t, float64(len(image)), policy["fsizeMin"])
	assert.Equal(t, "image/png", policy["mimeLimit"])
	assert.NotContains(t, prepared.UploadToken, "test-secret")
	qiniuMAC := hmac.New(sha1.New, []byte("test-secret"))
	_, _ = qiniuMAC.Write([]byte(policyParts[2]))
	assert.Equal(t, base64.URLEncoding.EncodeToString(qiniuMAC.Sum(nil)), policyParts[1])
	encodedTicket, _, _ := strings.Cut(prepared.Ticket, ".")
	ticketBytes, err := base64.RawURLEncoding.DecodeString(encodedTicket)
	require.NoError(t, err)
	var expiredTicket map[string]any
	require.NoError(t, common.Unmarshal(ticketBytes, &expiredTicket))
	expiredTicket["expires"] = 1
	ticketBytes, err = common.Marshal(expiredTicket)
	require.NoError(t, err)
	encodedTicket = base64.RawURLEncoding.EncodeToString(ticketBytes)
	ticketMAC := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = ticketMAC.Write([]byte("reference-v1:ticket:" + encodedTicket))
	_, err = store.Complete(context.Background(), 7, encodedTicket+"."+base64.RawURLEncoding.EncodeToString(ticketMAC.Sum(nil)))
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	_, err = store.Complete(context.Background(), 8, prepared.Ticket)
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	_, err = store.Complete(context.Background(), 7, prepared.Ticket+"x")
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	completed, err := store.Complete(context.Background(), 7, prepared.Ticket)
	require.NoError(t, err)
	assert.Equal(t, "ready", completed.Status)
	mu.Lock()
	assert.Equal(t, []string{"", fmt.Sprintf("bytes=%d-", len(image)/2)}, ranges)
	mu.Unlock()
	data, mime, err := store.Read(7, id, false)
	require.NoError(t, err)
	assert.Equal(t, image, data)
	assert.Equal(t, "image/png", mime)
	_, _, err = store.Read(8, id, false)
	assert.ErrorIs(t, err, service.ErrReferenceMissing)
	_, _, err = store.Read(7, "../../secret", false)
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	reused, err := store.Prepare(7, input)
	require.NoError(t, err)
	assert.Equal(t, "ready", reused.Status)
	assert.Empty(t, reused.UploadToken)
	t.Run("reuse renews twelve hour idle lifetime", func(t *testing.T) {
		path := filepath.Join(directory, "7-"+id+".asset")
		past := time.Now().Add(-11 * time.Hour)
		require.NoError(t, os.Chtimes(path, past, past))
		before := time.Now()
		ready, err := store.Prepare(7, input)
		require.NoError(t, err)
		assert.Equal(t, "ready", ready.Status)
		assert.Empty(t, ready.UploadToken)
		assert.WithinDuration(t, before.Add(12*time.Hour), time.Unix(ready.ExpiresAt, 0), 5*time.Second)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.WithinDuration(t, before, info.ModTime(), 5*time.Second)
		_, _, err = store.Read(7, id, false)
		require.NoError(t, err)
		afterRead, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, info.ModTime(), afterRead.ModTime(), "download alone must not renew retention")
	})
	t.Run("HTTP prepare and completion contract", func(t *testing.T) {
		engine := gin.New()
		engine.Use(func(c *gin.Context) { c.Set("id", 7) })
		engine.GET("/capabilities", controller.ReferenceUploadCapabilities)
		engine.POST("/prepare", controller.PrepareReferenceUpload)
		engine.POST("/complete", controller.CompleteReferenceUpload)
		for _, tc := range []struct {
			method, path string
			input        any
			status       int
		}{
			{"GET", "/capabilities", nil, 200},
			{"POST", "/prepare", input, 200},
			{"POST", "/complete", map[string]string{"ticket": prepared.Ticket}, 200},
			{"POST", "/complete", map[string]string{"ticket": "invalid"}, 400},
		} {
			body, err := common.Marshal(tc.input)
			require.NoError(t, err)
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(body))
			reply := httptest.NewRecorder()
			engine.ServeHTTP(reply, req)
			assert.Equal(t, tc.status, reply.Code, reply.Body.String())
			assert.Equal(t, "no-store", reply.Header().Get("Cache-Control"))
			if tc.path == "/capabilities" {
				var capability struct {
					RetentionSeconds int `json:"retention_seconds"`
				}
				require.NoError(t, common.Unmarshal(reply.Body.Bytes(), &capability))
				assert.Equal(t, 43200, capability.RetentionSeconds)
			}
		}
	})
	_, err = store.Complete(context.Background(), 7, prepared.Ticket)
	require.NoError(t, err)
	mu.Lock()
	assert.Len(t, ranges, 2)
	mu.Unlock()
	for _, badInput := range []service.ReferencePrepareRequest{{SHA256: id, Size: service.ReferenceMaxBytes + 1, MIME: "image/png"}, {SHA256: id, Size: 1, MIME: "image/svg+xml"}, {SHA256: "bad", Size: 1, MIME: "image/png"}} {
		_, err := store.Prepare(7, badInput)
		assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	}

	t.Run("request materialization and direct compatibility", func(t *testing.T) {
		engine := gin.New()
		engine.Use(func(c *gin.Context) { user, _ := strconv.Atoi(c.GetHeader("Test-User")); c.Set("id", user) })
		engine.POST("/submit", middleware.ResolveReferenceAssets(), func(c *gin.Context) {
			assert.Empty(t, c.GetHeader(middleware.ReferenceAssetsHeader))
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			c.Data(200, c.GetHeader("Content-Type"), body)
		})
		request := func(body []byte, contentType, version string, user int) *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "/submit", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("Test-User", strconv.Itoa(user))
			req.Header.Set(middleware.ReferenceAssetsHeader, version)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)
			return recorder
		}
		body := []byte(`{"prompt":"unchanged","n":1,"big":9007199254740993,"image":"newapi-asset://` + id + `"}`)
		reply := request(body, "application/json", "1", 7)
		require.Equal(t, 200, reply.Code, reply.Body.String())
		assert.Contains(t, reply.Body.String(), `"big":9007199254740993`)
		assert.Contains(t, reply.Body.String(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image))
		assert.Contains(t, reply.Body.String(), `"prompt":"unchanged"`)
		for _, mode := range []string{"base64", "url"} {
			modeReply := request([]byte(`{"image":"newapi-asset://`+id+`/`+mode+`","prompt":"newapi-asset://literal"}`), "application/json", "1", 7)
			require.Equal(t, 200, modeReply.Code, modeReply.Body.String())
			var decoded map[string]string
			require.NoError(t, common.Unmarshal(modeReply.Body.Bytes(), &decoded))
			assert.Equal(t, "newapi-asset://literal", decoded["prompt"])
			if mode == "base64" {
				assert.Equal(t, base64.StdEncoding.EncodeToString(image), decoded["image"])
			} else {
				assert.True(t, strings.HasPrefix(decoded["image"], "https://relay.example/v1/uploads/references/content/7/"))
			}
		}
		direct := request(body, "application/json", "", 7)
		assert.Equal(t, 200, direct.Code)
		assert.Equal(t, body, direct.Body.Bytes())
		assert.Equal(t, 410, request(body, "application/json", "1", 8).Code)
		assert.Equal(t, 400, request(body, "application/json", "2", 7).Code)
		assert.Equal(t, 400, request([]byte(`{"image":"newapi-asset://../../secret"}`), "application/json", "1", 7).Code)
		oversize := []byte(`{"image":[` + strings.Repeat(`"newapi-asset://`+id+`",`, 16) + `"newapi-asset://` + id + `"]}`)
		assert.Equal(t, 400, request(oversize, "application/json", "1", 7).Code)
		var form bytes.Buffer
		writer := multipart.NewWriter(&form)
		require.NoError(t, writer.WriteField("model", "test"))
		require.NoError(t, writer.WriteField("prompt", "hello"))
		require.NoError(t, writer.WriteField("image[]", "newapi-asset://"+id))
		require.NoError(t, writer.Close())
		reply = request(form.Bytes(), writer.FormDataContentType(), "1", 7)
		require.Equal(t, 200, reply.Code, reply.Body.String())
		req := httptest.NewRequest("POST", "/", bytes.NewReader(reply.Body.Bytes()))
		req.Header.Set("Content-Type", reply.Header().Get("Content-Type"))
		require.NoError(t, req.ParseMultipartForm(1<<20))
		defer req.MultipartForm.RemoveAll()
		assert.Equal(t, "hello", req.FormValue("prompt"))
		require.Len(t, req.MultipartForm.File["image[]"], 1)
		file, err := req.MultipartForm.File["image[]"][0].Open()
		require.NoError(t, err)
		defer file.Close()
		got, err := io.ReadAll(file)
		require.NoError(t, err)
		assert.Equal(t, image, got)
		service.ReferenceUploads = nil
		assert.Equal(t, 503, request(body, "application/json", "1", 7).Code)
		assert.Equal(t, 200, request(body, "application/json", "", 7).Code)
		service.ReferenceUploads = store
	})

	t.Run("signed relay content and expiration", func(t *testing.T) {
		address, err := store.PublicURL(7, id)
		require.NoError(t, err)
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		assert.True(t, store.VerifyContent(7, id, parsed.Query().Get("e"), parsed.Query().Get("token")))
		assert.False(t, store.VerifyContent(8, id, parsed.Query().Get("e"), parsed.Query().Get("token")))
		assert.False(t, store.VerifyContent(7, id, "1", parsed.Query().Get("token")))
		engine := gin.New()
		var accessLog bytes.Buffer
		oldWriter := gin.DefaultWriter
		gin.DefaultWriter = &accessLog
		t.Cleanup(func() { gin.DefaultWriter = oldWriter })
		middleware.SetUpLogger(engine)
		engine.GET("/v1/uploads/references/content/:user/:asset", controller.GetReferenceContent)
		req := httptest.NewRequest("GET", parsed.RequestURI(), nil)
		req.Header.Set("Range", "bytes=0-7")
		reply := httptest.NewRecorder()
		engine.ServeHTTP(reply, req)
		assert.Equal(t, 206, reply.Code)
		assert.Equal(t, image[:8], reply.Body.Bytes())
		assert.NotContains(t, accessLog.String(), parsed.Query().Get("token"))
		past := time.Now().Add(-12*time.Hour - time.Second)
		require.NoError(t, os.Chtimes(filepath.Join(directory, "7-"+id+".asset"), past, past))
		_, _, err = store.Read(7, id, false)
		assert.ErrorIs(t, err, service.ErrReferenceMissing)
		missing, err := store.Prepare(7, input)
		require.NoError(t, err)
		assert.Equal(t, "upload_required", missing.Status)
		store.Cleanup()
		_, err = os.Stat(filepath.Join(directory, "7-"+id+".asset"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("reject redirects and wrong bytes", func(t *testing.T) {
		mu.Lock()
		redirect = true
		mu.Unlock()
		_, err := store.Complete(context.Background(), 7, prepared.Ticket)
		assert.ErrorIs(t, err, service.ErrReferenceDownload)
		mu.Lock()
		redirect = false
		badBody = true
		mu.Unlock()
		_, err = store.Complete(context.Background(), 7, prepared.Ticket)
		assert.ErrorIs(t, err, service.ErrReferenceInvalid)
		_, _, err = store.Read(7, id, false)
		assert.ErrorIs(t, err, service.ErrReferenceMissing)
	})
	t.Run("resume across completion requests and process restart", func(t *testing.T) {
		mu.Lock()
		badBody = false
		ranges = nil
		pauseAfterPartial = true
		mu.Unlock()
		_, err := store.Complete(context.Background(), 7, prepared.Ticket)
		require.ErrorIs(t, err, service.ErrReferenceDownload)
		mu.Lock()
		pauseAfterPartial = false
		mu.Unlock()
		_, err = store.Complete(context.Background(), 7, prepared.Ticket)
		require.NoError(t, err)
		mu.Lock()
		assert.Equal(t, []string{"", fmt.Sprintf("bytes=%d-", len(image)/2), fmt.Sprintf("bytes=%d-", len(image)/2)}, ranges)
		mu.Unlock()
		restarted, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "test-ak", SecretKey: "test-secret", Bucket: "test-bucket", UploadURL: origin.URL, DownloadURL: origin.URL, Directory: directory, MaxCacheBytes: 32 << 20})
		require.NoError(t, err)
		data, _, err := restarted.Read(7, id, false)
		require.NoError(t, err)
		assert.Equal(t, image, data)
	})

	t.Run("production authentication rejects anonymous upload", func(t *testing.T) {
		engine := gin.New()
		engine.POST("/prepare", middleware.TokenAuth(), controller.PrepareReferenceUpload)
		reply := httptest.NewRecorder()
		engine.ServeHTTP(reply, httptest.NewRequest("POST", "/prepare", strings.NewReader(`{}`)))
		assert.Equal(t, http.StatusUnauthorized, reply.Code)
	})
	t.Run("per-user cache capacity preserves other users", func(t *testing.T) {
		quotaDir := t.TempDir()
		quotaStore, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "test-ak", SecretKey: "test-secret", Bucket: "test-bucket", UploadURL: origin.URL, DownloadURL: origin.URL, Directory: quotaDir, MaxCacheBytes: 32 << 20, MaxUserCacheBytes: 8 << 20})
		require.NoError(t, err)
		occupied, err := os.Create(filepath.Join(quotaDir, "7-existing.asset"))
		require.NoError(t, err)
		require.NoError(t, occupied.Truncate(8<<20))
		require.NoError(t, occupied.Close())
		_, err = quotaStore.Complete(context.Background(), 7, prepared.Ticket)
		assert.ErrorIs(t, err, service.ErrReferenceBusy)
		other, err := quotaStore.Prepare(8, input)
		require.NoError(t, err)
		_, err = quotaStore.Complete(context.Background(), 8, other.Ticket)
		require.NoError(t, err)
	})
}
