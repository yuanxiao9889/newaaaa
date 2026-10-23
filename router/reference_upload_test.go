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
	"image"
	"image/png"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type domesticHoldTransport struct {
	missing atomic.Bool
	calls   atomic.Int32
}

func (f *domesticHoldTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if r.URL.Host != "www.oopii.cn" || !service.VerifyDomesticTaskRequest(r, body) {
		return nil, fmt.Errorf("invalid machine hold")
	}
	f.calls.Add(1)
	status, response := 200, `{"held":true}`
	if f.missing.Load() {
		status, response = 410, `{"error":"reference_expired"}`
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
}

func TestDomesticTaskRetentionAndRecovery(t *testing.T) {
	setupBridgeSecurityDB(t)
	var version string
	queryVersion := "SELECT version()"
	if model.DB.Dialector.Name() == "sqlite" {
		queryVersion = "SELECT sqlite_version()"
	}
	require.NoError(t, model.DB.Raw(queryVersion).Scan(&version).Error)
	t.Logf("database=%s version=%s", model.DB.Dialector.Name(), version)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}, &model.TransferRequest{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Task{}, &model.TransferRequest{})) })
	t.Setenv("DOMESTIC_TASK_SECRET", strings.Repeat("ab", 32))
	old := http.DefaultTransport
	transport := &domesticHoldTransport{}
	transport.missing.Store(true)
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = old })
	router := gin.New()
	var generations atomic.Int32
	var task model.Task
	router.POST("/v1/videos", func(c *gin.Context) { c.Set("id", 7); c.Next() }, middleware.TransferRequestGuard, func(c *gin.Context) {
		generations.Add(1)
		task = model.Task{UserId: 7, TaskID: uuid.NewString(), Status: model.TaskStatusInProgress}
		task.PrivateData.TransferExecutionID = c.GetString("transfer_execution_id")
		require.NoError(t, model.DB.Create(&task).Error)
		c.JSON(200, gin.H{"id": task.TaskID})
	})
	router.POST("/internal/domestic/task-states", controller.DomesticReferenceTaskStates)
	key := uuid.NewString()
	body := `{"model":"fixture","reference_videos":["https://www.oopii.cn/seedance-reference/storyboard-resumable-11111111111111111111111111111111.mp4"]}`
	post := func(recovery bool, input string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(input))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if recovery {
			req.Header.Set("X-New-Api-Reference-Recovery", "1")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := post(false, body)
	require.Equal(t, 410, first.Code)
	assert.Contains(t, first.Body.String(), `"reference_service":"domestic"`)
	assert.Zero(t, generations.Load())
	assert.Equal(t, 410, post(false, body).Code)
	transport.missing.Store(false)
	assert.Equal(t, 409, post(true, strings.Replace(body, "fixture", "changed", 1)).Code)
	require.Equal(t, 200, post(true, body).Code)
	assert.Equal(t, 200, post(true, body).Code)
	assert.EqualValues(t, 1, generations.Load())
	assert.EqualValues(t, 2, transport.calls.Load())
	legacyID := "legacy:7:"+task.TaskID
	stateBody, err := common.Marshal(map[string]any{"execution_ids": []string{task.PrivateData.TransferExecutionID, legacyID}})
	require.NoError(t, err)
	query := func(stamp string, tamper bool) *httptest.ResponseRecorder {
		path := "/internal/domestic/task-states"
		req := httptest.NewRequest("POST", path, bytes.NewReader(stateBody))
		req.Header.Set("X-Domestic-Time", stamp)
		signature := service.DomesticTaskSignature(path, stamp, stateBody)
		if tamper {
			signature += "a"
		}
		req.Header.Set("X-Domestic-Signature", signature)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	assert.Equal(t, 401, query(stamp, true).Code)
	assert.Equal(t, 401, query("1", false).Code)
	active := query(stamp, false)
	require.Equal(t, 200, active.Code)
	assert.Contains(t, active.Body.String(), `"`+task.PrivateData.TransferExecutionID+`":0`)
	assert.Contains(t, active.Body.String(), `"`+legacyID+`":0`)
	now := time.Now().Unix()
	require.NoError(t, model.DB.Model(&task).Updates(map[string]any{"status": model.TaskStatusSuccess, "finish_time": now}).Error)
	finished := query(stamp, false)
	require.Equal(t, 200, finished.Code)
	assert.Contains(t, finished.Body.String(), strconv.FormatInt(now, 10))
	assert.Contains(t, finished.Body.String(), `"`+legacyID+`":`+strconv.FormatInt(now,10))
	assert.Empty(t, service.DomesticReferenceFiles([]byte(`{"url":"https://evil.test/seedance-reference/storyboard-resumable-11111111111111111111111111111111.mp4"}`)))
}

func TestReferenceURLRenewalBeforeSubmissionAndRetry(t *testing.T) {
	setupBridgeSecurityDB(t)
	var databaseVersion string
	versionQuery := "SELECT version()"
	if model.DB.Dialector.Name() == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	require.NoError(t, model.DB.Raw(versionQuery).Scan(&databaseVersion).Error)
	t.Logf("database=%s version=%s", model.DB.Dialector.Name(), databaseVersion)
	require.NoError(t, model.DB.AutoMigrate(&model.ReferenceAsset{}, &model.ReferencePreparation{}, &model.ReferenceAdmission{}, &model.Task{}, &model.TransferRequest{}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Migrator().DropTable(&model.ReferencePreparation{}, &model.ReferenceAsset{}, &model.ReferenceAdmission{}, &model.Task{}, &model.TransferRequest{}))
	})
	t.Setenv("ASYNC_IMAGE_REQUEST_STORAGE_PATH", t.TempDir())
	var encodedImage bytes.Buffer
	require.NoError(t, png.Encode(&encodedImage, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	data := encodedImage.Bytes()
	hash := sha256.Sum256(data)
	id := hex.EncodeToString(hash[:])
	oldStore, oldAddress := service.ReferenceUploads, system_setting.ServerAddress
	system_setting.ServerAddress = "https://relay.example"
	t.Cleanup(func() { service.ReferenceUploads, system_setting.ServerAddress = oldStore, oldAddress })
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			directory := t.TempDir()
			store, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "ak", SecretKey: "test-secret", SigningSecret: strings.Repeat("s", 32), Bucket: "bucket",
				UploadURL: "https://upload.example", DownloadURL: "https://refs.example", Directory: directory, DirectEnabled: direct, MaxCacheBytes: 32 << 20, MinFreePercent: 1})
			require.NoError(t, err)
			service.ReferenceUploads = store
			assetPath := filepath.Join(directory, "7-"+id+".asset")
			if direct {
				prepared, err := store.PrepareDirect(7, service.ReferencePrepareRequest{SHA256: id, Size: int64(len(data)), MIME: "image/png", RequestID: uuid.NewString()})
				require.NoError(t, err)
				_, err = store.UploadDirect(7, prepared.SessionID, prepared.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
				require.NoError(t, err)
				require.NoError(t, model.DB.Model(&model.ReferenceAsset{}).Where("user_id = ? AND sha256 = ?", 7, id).Update("idle_expires", time.Now().Add(time.Minute).Unix()).Error)
			} else {
				require.NoError(t, os.WriteFile(assetPath, data, 0600))
				past := time.Now().Add(-service.ReferenceRetention() + time.Minute)
				require.NoError(t, os.Chtimes(assetPath, past, past))
			}
			path := "/v1/uploads/references/content/7/" + id
			expires := strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
			mac := hmac.New(sha256.New, []byte("test-secret"))
			_, _ = mac.Write([]byte("reference-v1:content:" + path + ":" + expires))
			expired := system_setting.ServerAddress + path + "?e=" + expires + "&token=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
			body, err := common.Marshal(map[string]any{"image_urls": []string{expired, expired}, "prompt": expired, "n": 1})
			require.NoError(t, err)
			engine := gin.New()
			engine.Use(func(c *gin.Context) { owner, _ := strconv.Atoi(c.GetHeader("Test-User")); c.Set("id", owner) })
			attempts := 0
			engine.POST("/submit", middleware.ResolveReferenceAssets(), func(c *gin.Context) {
				attempts++
				value, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				assert.Equal(t, int64(len(value)), c.Request.ContentLength)
				c.Data(200, c.GetHeader("Content-Type"), value)
			})
			engine.GET("/v1/uploads/references/content/:user/:asset", controller.GetReferenceContent)
			call := func(body []byte, contentType, retry, user string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", "/submit", bytes.NewReader(body))
				req.Header.Set("Content-Type", contentType)
				req.Header.Set("Test-User", user)
				req.Header.Set(service.ImageRetryTaskHeader, retry)
				out := httptest.NewRecorder()
				engine.ServeHTTP(out, req)
				return out
			}
			before := time.Now()
			response := call(body, "application/json", "", "7")
			require.Equal(t, 200, response.Code, response.Body.String())
			var renewed struct {
				Images []string `json:"image_urls"`
				Prompt string   `json:"prompt"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &renewed))
			assert.Equal(t, expired, renewed.Prompt)
			require.Len(t, renewed.Images, 2)
			assert.NotEqual(t, expired, renewed.Images[0])
			assert.Equal(t, renewed.Images[0], renewed.Images[1])
			for _, tc := range []struct {
				address string
				status  int
			}{{expired, 404}, {renewed.Images[0], 200}} {
				u, err := url.Parse(tc.address)
				require.NoError(t, err)
				out := httptest.NewRecorder()
				engine.ServeHTTP(out, httptest.NewRequest("GET", u.RequestURI(), nil))
				assert.Equal(t, tc.status, out.Code)
			}
			if direct {
				var asset model.ReferenceAsset
				require.NoError(t, model.DB.Where("user_id = ? AND sha256 = ?", 7, id).First(&asset).Error)
				assert.WithinDuration(t, before.Add(service.ReferenceRetention()), time.Unix(asset.IdleExpires, 0), 5*time.Second)
			} else {
				stat, err := os.Stat(assetPath)
				require.NoError(t, err)
				assert.WithinDuration(t, before, stat.ModTime(), 5*time.Second)
			}
			// Manual retry replays an old body but must send newly signed URLs.
			source := filepath.Join(t.TempDir(), "body.json")
			require.NoError(t, os.WriteFile(source, body, 0600))
			taskID := "reference-renewal-" + strconv.FormatBool(direct)
			require.NoError(t, service.RetainImageTaskInput(&service.ImageTaskInput{UserID: 7, TaskID: taskID, Path: "/submit", ContentType: "application/json", BodyPath: source, ExpiresAt: time.Now().Add(time.Minute).Unix()}))
			response = call([]byte(`{}`), "application/json", taskID, "7")
			require.Equal(t, 200, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &renewed))
			assert.NotEqual(t, expired, renewed.Images[0])
			// Adapter dispatch also refreshes a DTO retained across attempts.
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("id", 7)
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			request := &dto.ImageRequest{}
			require.NoError(t, common.Unmarshal(body, request))
			urls, err := service.PrepareImageAdapterReferences(c, request, &dto.ImageAdapterReferenceConfig{MaxImages: 2})
			require.NoError(t, err)
			require.Len(t, urls, 2)
			assert.NotEqual(t, expired, urls[0])
			// Multipart files and literal prompts survive URL renewal unchanged.
			var form bytes.Buffer
			writer := multipart.NewWriter(&form)
			require.NoError(t, writer.WriteField("image_url", expired))
			require.NoError(t, writer.WriteField("prompt", expired))
			file, err := writer.CreateFormFile("mask", "mask.png")
			require.NoError(t, err)
			_, err = file.Write(data)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			response = call(form.Bytes(), writer.FormDataContentType(), "", "7")
			require.Equal(t, 200, response.Code, response.Body.String())
			req := httptest.NewRequest("POST", "/submit", bytes.NewReader(response.Body.Bytes()))
			req.Header.Set("Content-Type", response.Header().Get("Content-Type"))
			require.NoError(t, req.ParseMultipartForm(1<<20))
			defer req.MultipartForm.RemoveAll()
			assert.NotEqual(t, expired, req.FormValue("image_url"))
			assert.Equal(t, expired, req.FormValue("prompt"))
			mask, err := req.MultipartForm.File["mask"][0].Open()
			require.NoError(t, err)
			got, err := io.ReadAll(mask)
			require.NoError(t, err)
			require.NoError(t, mask.Close())
			assert.Equal(t, data, got)
			prior := attempts
			assert.Equal(t, 400, call(body, "application/json", "", "8").Code)
			assert.Equal(t, 400, call(bytes.ReplaceAll(body, []byte("token="), []byte("token=invalid")), "application/json", "", "7").Code)
			assert.Equal(t, prior, attempts, "unauthorized references never reach generation")
			external := strings.ReplaceAll(expired, "relay.example", "external.example")
			unchanged, err := service.RenewReferenceURL(7, external)
			require.NoError(t, err)
			assert.Equal(t, external, unchanged)
			t.Run("durable task lease and exact identity recovery", func(t *testing.T) {
				task := &model.Task{TaskID: "lease-" + uuid.NewString(), UserId: 7, Status: model.TaskStatusQueued,
					PrivateData: model.TaskPrivateData{RequestContentType: "application/json", ImageRetry: &model.AsyncImageRetryState{BodyPath: source}}}
				require.NoError(t, service.InsertImageTaskWithReferences(task))
				assert.Equal(t, []string{id}, task.PrivateData.ReferenceAssetIDs)
				taskBody, _, err := service.RefreshTaskReferenceBody(task, body)
				require.NoError(t, err)
				var taskInput struct {
					Images []string `json:"image_urls"`
				}
				require.NoError(t, common.Unmarshal(taskBody, &taskInput))
				taskURL, err := url.Parse(taskInput.Images[0])
				require.NoError(t, err)
				assert.Equal(t, "task:"+task.TaskID, taskURL.Query().Get("e"))
				assert.False(t, store.VerifyContent(8, id, taskURL.Query().Get("e"), taskURL.Query().Get("token")))
				assert.False(t, store.VerifyContent(7, id, taskURL.Query().Get("e"), "tampered"))
				past := time.Now().Add(-2 * service.ReferenceRetention())
				if direct {
					require.NoError(t, model.DB.Model(&model.ReferenceAsset{}).Where("user_id = ? AND sha256 = ?", 7, id).Update("idle_expires", past.Unix()).Error)
				} else {
					require.NoError(t, os.Chtimes(assetPath, past, past))
				}
				for _, state := range []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusUnknown, model.TaskStatusSuccess, model.TaskStatusFailure} {
					require.NoError(t, model.DB.Model(task).Updates(map[string]any{"status": state, "finish_time": time.Now().Unix()}).Error)
					store.Cleanup()
					require.NoError(t, store.CleanupDirect())
					got, _, err := store.Read(7, id, false)
					require.NoError(t, err, "state=%s", state)
					assert.Equal(t, data, got)
					assert.True(t, store.VerifyContent(7, id, taskURL.Query().Get("e"), taskURL.Query().Get("token")), "task URL remains usable while its lease is live")
					_, _, err = store.Read(8, id, false)
					assert.Error(t, err, "a lease must never grant another user's access")
				}
				// A separate task continues to protect a shared reference.
				other := *task
				other.ID, other.TaskID, other.Status = 0, "lease-"+uuid.NewString(), model.TaskStatusQueued
				require.NoError(t, other.Insert())
				require.NoError(t, model.DB.Model(task).Update("finish_time", past.Unix()).Error)
				assert.False(t, store.VerifyContent(7, id, taskURL.Query().Get("e"), taskURL.Query().Get("token")), "another task must not extend this task's signed capability")
				store.Cleanup()
				require.NoError(t, store.CleanupDirect())
				_, _, err = store.Read(7, id, false)
				require.NoError(t, err)
				require.NoError(t, model.DB.Model(&other).Updates(map[string]any{"status": model.TaskStatusFailure, "finish_time": past.Unix()}).Error)
				store.Cleanup()
				require.NoError(t, store.CleanupDirect())
				_, _, err = store.Read(7, id, false)
				assert.ErrorIs(t, err, service.ErrReferenceMissing)

				guarded := gin.New()
				guarded.Use(func(c *gin.Context) { c.Set("id", 7) })
				var generated atomic.Int32
				guarded.POST("/v1/images/generations", middleware.TransferRequestGuard, middleware.ResolveReferenceAssets(), func(c *gin.Context) {
					generated.Add(1)
					c.JSON(200, gin.H{"task_id": "single-task"})
				})
				key := uuid.NewString()
				submit := func(input []byte, repair bool) *httptest.ResponseRecorder {
					req := httptest.NewRequest("POST", "/v1/images/generations", bytes.NewReader(input))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Idempotency-Key", key)
					if repair {
						req.Header.Set("X-New-Api-Reference-Recovery", "1")
					}
					out := httptest.NewRecorder()
					guarded.ServeHTTP(out, req)
					return out
				}
				rejected := submit(body, false)
				require.Equal(t, 410, rejected.Code)
				assert.Contains(t, rejected.Body.String(), `"reference_recovery":true`)
				assert.Zero(t, generated.Load())
				assert.Equal(t, 409, submit([]byte(`{"prompt":"changed"}`), true).Code)
				// Simulate the client's upload restoring the exact original pixels.
				require.NoError(t, os.WriteFile(assetPath, data, 0600))
				assert.Equal(t, 410, submit(body, false).Code, "ordinary replay still returns the original rejection")
				statuses := make(chan int, 4)
				var wg sync.WaitGroup
				for range 4 {
					wg.Go(func() { statuses <- submit(body, true).Code })
				}
				wg.Wait()
				close(statuses)
				accepted := 0
				for status := range statuses {
					assert.Contains(t, []int{200, 409}, status)
					if status == 200 {
						accepted++
					}
				}
				assert.Positive(t, accepted)
				for range 3 {
					assert.Equal(t, 200, submit(body, true).Code)
				}
				assert.EqualValues(t, 1, generated.Load())
				record, err := model.FindTransferRequest(7, key)
				require.NoError(t, err)
				assert.Equal(t, "succeeded", record.State)
				resumed, err := model.ResumeReferenceSubmission(record)
				require.NoError(t, err)
				assert.False(t, resumed, "accepted generation cannot be reopened")
				require.NoError(t, os.Remove(assetPath))
			})
			if direct {
				require.NoError(t, model.DB.Model(&model.ReferenceAsset{}).Where("user_id = ? AND sha256 = ?", 7, id).Update("idle_expires", time.Now().Add(-time.Second).Unix()).Error)
			} else {
				_ = os.Remove(assetPath)
			}
			assert.Equal(t, 410, call(body, "application/json", "", "7").Code)
			assert.Equal(t, prior, attempts, "deleted or expired assets never reach generation")
		})
	}
}

func TestResumableReferenceHTTPAuthorizationAndRangeContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupRelayRouterTestDB(t)
	users := []model.User{
		{Username: "resume-owner", Status: common.UserStatusEnabled, Group: "default", AffCode: "resume-owner"},
		{Username: "resume-other", Status: common.UserStatusEnabled, Group: "default", AffCode: "resume-other"},
	}
	require.NoError(t, model.DB.Create(&users).Error)
	tokens := []model.Token{
		{UserId: users[0].Id, Key: "resumehttpowner", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
		{UserId: users[1].Id, Key: "resumehttpother", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
	}
	require.NoError(t, model.DB.Create(&tokens).Error)
	store, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "test", SecretKey: "test-secret",
		Bucket: "test", UploadURL: "https://upload.example.test", DownloadURL: "https://refs.example.test",
		Directory: t.TempDir(), MaxCacheBytes: 32 << 20})
	require.NoError(t, err)
	oldStore, oldAddress := service.ReferenceUploads, system_setting.ServerAddress
	service.ReferenceUploads, system_setting.ServerAddress = store, "https://www.oopii.cc"
	t.Cleanup(func() { service.ReferenceUploads, system_setting.ServerAddress = oldStore, oldAddress })
	engine := gin.New()
	SetRelayRouter(engine)
	call := func(method, path string, body []byte, key, digest string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
		request.Header.Set("X-Chunk-SHA256", digest)
		request.Header.Set("Idempotency-Key", "upload_not_generation_123")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	capabilities := call("GET", "/v1/uploads/references/capabilities", nil, tokens[0].Key, "")
	require.Equal(t, 200, capabilities.Code, capabilities.Body.String())
	assert.Contains(t, capabilities.Body.String(), `"resumable_reference_uploads":true`)
	assert.Equal(t, 401, call("POST", "/v1/uploads/references/resumable/prepare", []byte(`{}`), "", "").Code)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	data := encoded.Bytes()
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	prepareBody, err := common.Marshal(service.ReferencePrepareRequest{SHA256: digest, Size: int64(len(data)), MIME: "image/png"})
	require.NoError(t, err)
	response := call("POST", "/v1/uploads/references/resumable/prepare", prepareBody, tokens[0].Key, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var prepared service.ReferenceResumableResult
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &prepared))
	path := "/v1/uploads/references/resumable/" + prepared.UploadID
	assert.Equal(t, 410, call("GET", path, nil, tokens[1].Key, "").Code)
	assert.Equal(t, 409, call("PUT", path+"/chunks/0", data, tokens[0].Key, strings.Repeat("a", 64)).Code)
	response = call("PUT", path+"/chunks/0", data, tokens[0].Key, digest)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = call("POST", path+"/complete", []byte(`{}`), tokens[0].Key, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"ready"`)
	address, err := store.PublicURL(users[0].Id, digest)
	require.NoError(t, err)
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	request := httptest.NewRequest("GET", parsed.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-7")
	request.Header.Set("If-Range", `"`+digest+`"`)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, 206, response.Code)
	assert.Equal(t, data[:8], response.Body.Bytes())
	assert.Equal(t, `"`+digest+`"`, response.Header().Get("ETag"))
	request = httptest.NewRequest("GET", parsed.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-7")
	request.Header.Set("If-Range", `"different-version"`)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, 200, response.Code)
	assert.Equal(t, data, response.Body.Bytes())
	for _, token := range tokens {
		var stored model.Token
		require.NoError(t, model.DB.First(&stored, token.Id).Error)
		assert.Zero(t, stored.UsedQuota, "upload transport does not bill generation")
	}
}

func TestResumableReferencePersistsAcknowledgementsAndValidatesPublication(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	var encoded bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 700, 700))))
	data := encoded.Bytes()
	require.Greater(t, len(data), service.ReferenceChunkBytes)
	digest := sha256.Sum256(data)
	input := service.ReferencePrepareRequest{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), MIME: "image/png"}
	config := service.ReferenceUploadConfig{AccessKey: "ak", SecretKey: "secret", Bucket: "bucket",
		UploadURL: "https://upload.example.test", DownloadURL: "https://refs.example.test",
		Directory: t.TempDir(), MaxCacheBytes: 32 << 20}
	store, err := service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	prepared, err := store.PrepareResumable(7, input)
	require.NoError(t, err)
	again, err := store.PrepareResumable(7, input)
	require.NoError(t, err)
	assert.Equal(t, prepared.UploadID, again.UploadID)
	other, err := store.PrepareResumable(8, input)
	require.NoError(t, err)
	assert.NotEqual(t, prepared.UploadID, other.UploadID)
	_, err = store.ResumableStatus(8, prepared.UploadID)
	assert.ErrorIs(t, err, service.ErrReferenceMissing)

	tail := data[service.ReferenceChunkBytes:]
	tailDigest := sha256.Sum256(tail)
	ack, err := store.WriteResumableChunk(7, prepared.UploadID, 1, hex.EncodeToString(tailDigest[:]), bytes.NewReader(tail))
	require.NoError(t, err)
	assert.Equal(t, []int{1}, ack.ReceivedChunks)
	_, _, err = store.Read(7, input.SHA256, false)
	assert.ErrorIs(t, err, service.ErrReferenceMissing, "a partial upload is never a reusable reference")
	_, err = store.CompleteResumable(7, prepared.UploadID)
	assert.ErrorIs(t, err, service.ErrReferenceConflict)

	// Constructing another instance simulates losing all process memory.
	store, err = service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	status, err := store.ResumableStatus(7, prepared.UploadID)
	require.NoError(t, err)
	assert.Equal(t, []int{1}, status.ReceivedChunks)
	ack, err = store.WriteResumableChunk(7, prepared.UploadID, 1, hex.EncodeToString(tailDigest[:]), bytes.NewReader(tail))
	require.NoError(t, err, "lost ACK replay is idempotent")
	assert.Equal(t, []int{1}, ack.ReceivedChunks)
	badTail := append([]byte{}, tail...)
	badTail[0] ^= 1
	badDigest := sha256.Sum256(badTail)
	_, err = store.WriteResumableChunk(7, prepared.UploadID, 1, hex.EncodeToString(badDigest[:]), bytes.NewReader(badTail))
	assert.ErrorIs(t, err, service.ErrReferenceConflict)
	first := data[:service.ReferenceChunkBytes]
	firstDigest := sha256.Sum256(first)
	_, err = store.WriteResumableChunk(7, prepared.UploadID, 0, hex.EncodeToString(firstDigest[:]), bytes.NewReader(first[:100]))
	assert.ErrorIs(t, err, service.ErrReferenceConflict)
	status, err = store.ResumableStatus(7, prepared.UploadID)
	require.NoError(t, err)
	assert.Equal(t, []int{1}, status.ReceivedChunks, "incomplete body cannot be acknowledged")
	_, err = store.WriteResumableChunk(7, prepared.UploadID, 0, hex.EncodeToString(firstDigest[:]), bytes.NewReader(first))
	require.NoError(t, err)
	var complete sync.WaitGroup
	for range 2 {
		complete.Go(func() {
			result, completeErr := store.CompleteResumable(7, prepared.UploadID)
			assert.NoError(t, completeErr)
			assert.Equal(t, "ready", result.Status)
		})
	}
	complete.Wait()
	read, mime, err := store.Read(7, input.SHA256, false)
	require.NoError(t, err)
	assert.Equal(t, data, read)
	assert.Equal(t, "image/png", mime)
	store, err = service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	again, err = store.PrepareResumable(7, input)
	require.NoError(t, err)
	assert.Equal(t, "ready", again.Status, "completed reuse survives process restart")
	expired := time.Now().Add(-service.ReferenceRetention() - time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(config.Directory, "7-"+input.SHA256+".asset"), expired, expired))
	again, err = store.PrepareResumable(7, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", again.Status)
	assert.Empty(t, again.ReceivedChunks, "a receipt cannot claim lost or expired file bytes")
	_, err = store.PrepareResumable(7, service.ReferencePrepareRequest{
		SHA256: input.SHA256, Size: input.Size, MIME: "image/png", Mode: "url"})
	assert.ErrorIs(t, err, service.ErrReferenceInvalid, "direct URL uploads retain their own protocol")

	// A correct caller-provided digest does not make arbitrary bytes an image.
	invalid := []byte("not an image")
	invalidDigest := sha256.Sum256(invalid)
	bad, err := store.PrepareResumable(7, service.ReferencePrepareRequest{
		SHA256: hex.EncodeToString(invalidDigest[:]), Size: int64(len(invalid)), MIME: "image/png"})
	require.NoError(t, err)
	_, err = store.WriteResumableChunk(7, bad.UploadID, 0, hex.EncodeToString(invalidDigest[:]), bytes.NewReader(invalid))
	require.NoError(t, err)
	_, err = store.CompleteResumable(7, bad.UploadID)
	assert.ErrorIs(t, err, service.ErrReferenceConflict)
}

func TestResumableReferenceReservesQuotaAndCleansExpiredSessions(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	directory := t.TempDir()
	store, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{
		AccessKey: "ak", SecretKey: "secret", Bucket: "bucket",
		UploadURL: "https://upload.example.test", DownloadURL: "https://refs.example.test",
		Directory: directory, MaxCacheBytes: 8 << 20, MaxUserCacheBytes: 8 << 20})
	require.NoError(t, err)
	first, err := store.PrepareResumable(7, service.ReferencePrepareRequest{
		SHA256: strings.Repeat("a", 64), Size: 4 << 20, MIME: "image/png"})
	require.NoError(t, err)
	_, err = store.PrepareResumable(7, service.ReferencePrepareRequest{
		SHA256: strings.Repeat("b", 64), Size: 4 << 20, MIME: "image/png"})
	assert.ErrorIs(t, err, service.ErrReferenceBusy, "unreceived bytes are reserved")
	path := filepath.Join(directory, "7-"+first.UploadID+".upload")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, common.Unmarshal(raw, &manifest))
	manifest["expires_at"] = time.Now().Add(-time.Minute).Unix()
	raw, err = common.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	store.Cleanup()
	_, err = store.ResumableStatus(7, first.UploadID)
	assert.ErrorIs(t, err, service.ErrReferenceMissing)
	_, err = store.PrepareResumable(7, service.ReferencePrepareRequest{
		SHA256: strings.Repeat("b", 64), Size: 4 << 20, MIME: "image/png"})
	require.NoError(t, err, "expired reservation was reclaimed")
}

func TestReferenceUploadDirectURL(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	image := []byte("fixture: exact uploaded bytes")
	digest := sha256.Sum256(image)
	contentDigest := sha1.Sum(image)
	etag := base64.URLEncoding.EncodeToString(append([]byte{0x16}, contentDigest[:]...))
	input := service.ReferencePrepareRequest{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(image)),
		MIME: "image/png", Mode: "url", ETag: etag}
	var methods []string
	status, size, mimeType, returnedETag := 200, len(image), "image/png", `"`+etag+`"`
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.NotEmpty(t, r.URL.Query().Get("token"))
		assert.Greater(t, r.URL.Query().Get("e"), strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("ETag", returnedETag)
		w.Header().Set("Location", "https://example.invalid/not-followed")
		w.WriteHeader(status)
	}))
	defer origin.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = origin.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	config := service.ReferenceUploadConfig{AccessKey: "test-ak", SecretKey: "test-secret", Bucket: "test",
		UploadURL: origin.URL, DownloadURL: origin.URL, Directory: t.TempDir(), MaxCacheBytes: 32 << 20}
	directory := config.Directory
	store, err := service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	prepared, err := store.Prepare(7, input)
	require.NoError(t, err)
	result, err := store.Complete(context.Background(), 7, prepared.Ticket)
	require.NoError(t, err)
	assert.Equal(t, []string{"HEAD"}, methods, "no image GET to the relay")
	address, err := url.Parse(result.DownloadURL)
	require.NoError(t, err)
	assert.Equal(t, "/"+prepared.ObjectKey, address.Path)
	assert.Equal(t, "ready", result.Status)
	var imageRequest dto.ImageRequest
	// The gateway correctly rejects loopback URLs; substitute only the test
	// origin to exercise its public-URL passthrough with the exact signed query.
	forwardURL := strings.Replace(result.DownloadURL, origin.URL, "https://refs.example.test", 1)
	requestBody, err := common.Marshal(map[string]any{"image": forwardURL})
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(requestBody, &imageRequest))
	upstreamURLs, err := service.PrepareImageAdapterReferences(nil, &imageRequest,
		&dto.ImageAdapterReferenceConfig{MaxImages: 1, UploadLocal: true})
	require.NoError(t, err)
	assert.Equal(t, []string{forwardURL}, upstreamURLs, "signed query must reach upstream unchanged")
	assert.Equal(t, []string{"HEAD"}, methods, "image adapter must not fetch or rehost the URL")
	assert.InDelta(t, service.ReferenceRetention().Seconds(), float64(result.ExpiresAt-time.Now().Unix()), 2)
	_, _, err = store.Read(7, input.SHA256, false)
	assert.ErrorIs(t, err, service.ErrReferenceMissing, "URL metadata is not verified binary content")
	// Reuse survives a relay restart; neither a new upload nor a download is required.
	store, err = service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	reused, err := store.Prepare(7, input)
	require.NoError(t, err)
	assert.Equal(t, result.DownloadURL, reused.DownloadURL)
	assert.Equal(t, result.ExpiresAt, reused.ExpiresAt, "reuse must not extend OSS lifetime")
	assert.Len(t, methods, 1)
	changedConfig := config
	changedConfig.Bucket = "another-bucket"
	changedStore, err := service.NewReferenceUploadStore(changedConfig)
	require.NoError(t, err)
	changedResult, err := changedStore.Prepare(7, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", changedResult.Status, "changed OSS configuration cannot reuse old metadata")
	binary := input
	binary.Mode, binary.ETag = "", ""
	binaryResult, err := store.Prepare(7, binary)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", binaryResult.Status)
	other, err := store.Prepare(8, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", other.Status)
	_, err = store.Complete(context.Background(), 8, prepared.Ticket)
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	_, err = store.Complete(context.Background(), 7, prepared.Ticket+"x")
	assert.ErrorIs(t, err, service.ErrReferenceInvalid)
	assert.Len(t, methods, 1, "wrong account and altered tickets never contact OSS")

	for _, tc := range []struct {
		name, mime, etag string
		status, size     int
	}{
		{"redirect", "image/png", etag, 302, len(image)},
		{"missing", "image/png", etag, 404, len(image)},
		{"short", "image/png", etag, 200, len(image) - 1},
		{"wrong mime", "image/jpeg", etag, 200, len(image)},
		{"wrong hash", "image/png", "different", 200, len(image)},
		{"weak hash", "image/png", `W/"` + etag + `"`, 200, len(image)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.Directory = t.TempDir()
			fresh, err := service.NewReferenceUploadStore(config)
			require.NoError(t, err)
			p, err := fresh.Prepare(7, input)
			require.NoError(t, err)
			status, size, mimeType, returnedETag = tc.status, tc.size, tc.mime, tc.etag
			_, err = fresh.Complete(context.Background(), 7, p.Ticket)
			require.Error(t, err)
			entries, err := os.ReadDir(config.Directory)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
	status, size, mimeType, returnedETag = 200, len(image), "image/png", etag
	// Near-expiry objects cannot be reused for a new queued job.
	recordPath := filepath.Join(directory, "7-"+input.SHA256+".url")
	raw, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, common.Unmarshal(raw, &record))
	record["until"] = time.Now().Add(59 * time.Minute).Unix()
	raw, err = common.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(recordPath, raw, 0600))
	expired, err := store.Prepare(7, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", expired.Status)
	old := time.Now().Add(-13 * time.Hour)
	require.NoError(t, os.Chtimes(recordPath, old, old))
	store.Cleanup()
	_, err = os.Stat(recordPath)
	assert.True(t, os.IsNotExist(err))
}

func TestReferenceUploadProtocol(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
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
	t.Run("reuse renews configured idle lifetime", func(t *testing.T) {
		path := filepath.Join(directory, "7-"+id+".asset")
		past := time.Now().Add(-service.ReferenceRetention() + time.Minute)
		require.NoError(t, os.Chtimes(path, past, past))
		before := time.Now()
		ready, err := store.Prepare(7, input)
		require.NoError(t, err)
		assert.Equal(t, "ready", ready.Status)
		assert.Empty(t, ready.UploadToken)
		assert.WithinDuration(t, before.Add(service.ReferenceRetention()), time.Unix(ready.ExpiresAt, 0), 5*time.Second)
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
				assert.Equal(t, int(service.ReferenceRetention().Seconds()), capability.RetentionSeconds)
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

	t.Run("failed submission preserves reference for another task", func(t *testing.T) {
		path := filepath.Join(directory, "7-"+id+".asset")
		past := time.Now().Add(-service.ReferenceRetention() + time.Minute)
		require.NoError(t, os.Chtimes(path, past, past))
		engine := gin.New()
		engine.Use(func(c *gin.Context) { c.Set("id", 7) })
		attempts := 0
		engine.POST("/submit", middleware.ResolveReferenceAssets(), func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			var request map[string]string
			require.NoError(t, common.Unmarshal(body, &request))
			assert.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image), request["image"])
			attempts++
			if attempts == 1 {
				c.Status(http.StatusServiceUnavailable)
				return
			}
			c.Status(http.StatusAccepted)
		})
		before := time.Now()
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusAccepted} {
			body := []byte(`{"image":"newapi-asset://` + id + `"}`)
			req := httptest.NewRequest(http.MethodPost, "/submit", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(middleware.ReferenceAssetsHeader, "1")
			reply := httptest.NewRecorder()
			engine.ServeHTTP(reply, req)
			require.Equal(t, status, reply.Code, reply.Body.String())
			store.Cleanup()
			data, _, err := store.Read(7, id, false)
			require.NoError(t, err)
			assert.Equal(t, image, data)
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.WithinDuration(t, before, info.ModTime(), 5*time.Second)
			assert.True(t, info.ModTime().Add(service.ReferenceRetention()).After(before.Add(10*time.Minute)))
		}
		assert.Equal(t, 2, attempts)
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
		past := time.Now().Add(-service.ReferenceRetention() - time.Second)
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
