package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appI18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBridgeSecurityDB(t *testing.T) {
	t.Helper()
	setupRelayRouterTestDB(t)
	dsn := os.Getenv("BRIDGE_SECURITY_TEST_DSN")
	if dsn == "" {
		return
	}
	require.Contains(t, dsn, "bridge_security_test")
	previous, previousLog := model.DB, model.LOG_DB
	t.Setenv("SQL_DSN", dsn)
	// Initialize the real database dialect, including quoted key/group columns.
	require.NoError(t, model.InitDB())
	db := model.DB
	model.LOG_DB = db
	tables := []any{&model.User{}, &model.Token{}, &model.Ability{}, &model.Midjourney{}, &model.Channel{}}
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(tables...))
		conn, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		model.DB, model.LOG_DB = previous, previousLog
	})
	require.NoError(t, db.AutoMigrate(tables...))
}

func TestBridgeDownloadTicketRevocation(t *testing.T) {
	setupBridgeSecurityDB(t)
	t.Setenv("OPICS_BRIDGE_TICKET_SECRET", strings.Repeat("ba", 32))
	user := model.User{Username: "revocation-owner", AffCode: "revocation-owner", Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "revocationKey", Status: common.TokenStatusExhausted, ExpiredTime: -1}
	require.NoError(t, model.DB.Create(&token).Error)
	digest := sha256.Sum256([]byte(token.Key))
	claim := service.TransferDownloadClaim{Version: 1, UserID: user.Id, TokenID: token.Id, TokenDigest: hex.EncodeToString(digest[:]), AuthVersion: 1,
		ResourceID: "owned-result", URL: "https://example.test/result.png", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	ticket, err := service.SealTransferDownload(claim)
	require.NoError(t, err)
	engine := gin.New()
	SetRelayRouter(engine)
	send := func(key, capability string) int {
		req := httptest.NewRequest("POST", "/internal/bridge/download-authorize", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Opics-Download-Ticket", capability)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out.Code
	}
	key := service.BridgeDownloadAuthorizationKey()
	assert.Equal(t, 401, send("invalid", ticket))
	assert.Equal(t, 401, send(key, "invalid"))
	assert.Equal(t, 204, send(key, ticket), "zero remaining quota must not revoke a paid result")
	assert.Error(t, service.AuthorizeTransferDownload(ticket, time.Now().Add(6*time.Minute)))
	for _, update := range []map[string]any{{"status": common.TokenStatusDisabled}, {"status": common.TokenStatusEnabled, "expired_time": time.Now().Unix() - 1}, {"expired_time": -1, "key": "rotatedKey"}} {
		require.NoError(t, model.DB.Model(&token).Updates(update).Error)
		assert.Equal(t, 401, send(key, ticket))
	}
	require.NoError(t, model.DB.Model(&token).Updates(map[string]any{"key": "revocationKey", "status": common.TokenStatusExhausted}).Error)
	assert.Equal(t, 204, send(key, ticket))
	for _, update := range []map[string]any{{"auth_version": 2}, {"auth_version": 1, "status": common.UserStatusDisabled}} {
		require.NoError(t, model.DB.Model(&user).Updates(update).Error)
		assert.Equal(t, 401, send(key, ticket))
	}
}

func TestMidjourneyImageAuthorizationAndResume(t *testing.T) {
	setupBridgeSecurityDB(t)
	require.NoError(t, appI18n.Init())
	require.NoError(t, model.DB.AutoMigrate(&model.Midjourney{}))
	oldSecret, oldAddress := common.CryptoSecret, system_setting.ServerAddress
	oldFetch := *system_setting.GetFetchSetting()
	common.CryptoSecret, system_setting.ServerAddress = "local-mj-security-test", "https://www.oopii.cc"
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	t.Cleanup(func() {
		common.CryptoSecret, system_setting.ServerAddress = oldSecret, oldAddress
		*system_setting.GetFetchSetting() = oldFetch
	})
	service.InitHttpClient()
	owner := model.User{Username: "mj-owner", Status: common.UserStatusEnabled, Group: "default", AffCode: "mj-owner", AuthVersion: 1}
	other := model.User{Username: "mj-other", Status: common.UserStatusEnabled, Group: "default", AffCode: "mj-other", AuthVersion: 1}
	require.NoError(t, model.DB.Create(&owner).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	tokens := []model.Token{{UserId: owner.Id, Key: "mjOwnerKey", Status: common.TokenStatusExhausted, ExpiredTime: -1},
		{UserId: other.Id, Key: "mjOtherKey", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10},
		{UserId: owner.Id, Key: "mjDisabledKey", Status: common.TokenStatusDisabled, ExpiredTime: -1},
		{UserId: owner.Id, Key: "mjExpiredKey", Status: common.TokenStatusEnabled, ExpiredTime: time.Now().Unix() - 10, RemainQuota: 10}}
	require.NoError(t, model.DB.Create(&tokens).Error)
	var fetches atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"image-v1"`)
		body := "0123456789"
		if r.URL.Path == "/other" {
			body = "other-owner"
		}
		http.ServeContent(w, r, "result.png", time.Unix(1, 0), strings.NewReader(body))
	}))
	t.Cleanup(upstream.Close)
	task := model.Midjourney{UserId: owner.Id, MjId: "mj_owned_result", ImageUrl: upstream.URL, Status: "SUCCESS"}
	require.NoError(t, model.DB.Create(&task).Error)
	engine := gin.New()
	SetRelayRouter(engine)
	signed, err := url.Parse(service.BuildMidjourneyImageURL(&task))
	require.NoError(t, err)
	require.NotEmpty(t, signed.RawQuery)
	for _, tc := range []struct {
		name, method, path, key, byteRange string
		status                             int
	}{
		{"anonymous", "GET", "/mj/image/mj_owned_result", "", "", 401},
		{"owner zero balance", "GET", "/mj/image/mj_owned_result", tokens[0].Key, "", 200},
		{"resume", "GET", "/mj/image/mj_owned_result", tokens[0].Key, "bytes=4-", 206},
		{"head", "HEAD", "/mj/image/mj_owned_result", tokens[0].Key, "", 200},
		{"mode alias", "GET", "/fast/mj/image/mj_owned_result", tokens[0].Key, "", 200},
		{"other account", "GET", "/mj/image/mj_owned_result", tokens[1].Key, "", 404},
		{"disabled key", "GET", signed.RequestURI(), tokens[2].Key, "", 401},
		{"expired key", "GET", signed.RequestURI(), tokens[3].Key, "", 401},
		{"invalid key cannot use signature", "GET", signed.RequestURI(), "invalid", "", 401},
		{"signed preview", "GET", signed.RequestURI(), "", "", 200},
		{"signed resume", "GET", signed.RequestURI(), "", "bytes=4-", 206},
		{"signature wrong resource", "GET", "/mj/image/mj_unknown?" + signed.RawQuery, "", "", 404},
		{"duplicate signature", "GET", signed.RequestURI() + "&access=invalid", "", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := fetches.Load()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer sk-"+tc.key)
			}
			req.Header.Set("Range", tc.byteRange)
			req.Header.Set("If-Range", `"image-v1"`)
			out := httptest.NewRecorder()
			engine.ServeHTTP(out, req)
			require.Equal(t, tc.status, out.Code, out.Body.String())
			if tc.status >= 400 {
				assert.Equal(t, before, fetches.Load())
			} else {
				assert.Equal(t, before+1, fetches.Load())
				assert.Equal(t, "private, no-store", out.Header().Get("Cache-Control"))
			}
			if tc.status == 206 {
				assert.Equal(t, "456789", out.Body.String())
			}
			if tc.method == "HEAD" {
				assert.Empty(t, out.Body.String())
			}
		})
	}
	assert.False(t, service.VerifyMidjourneyImageAccess(&task, signed.Query(), time.Now().Add(6*time.Minute)))
	// Upstreams may reuse an ID across channels/accounts. Scope the lookup by
	// owner, not a globally unique assumption about the external MJ ID.
	duplicate := model.Midjourney{UserId: other.Id, MjId: task.MjId, ImageUrl: upstream.URL + "/other", Status: "SUCCESS"}
	require.NoError(t, model.DB.Create(&duplicate).Error)
	for _, tc := range []struct{ key, body string }{{tokens[0].Key, "0123456789"}, {tokens[1].Key, "other-owner"}} {
		req := httptest.NewRequest("GET", "/mj/image/"+task.MjId, nil)
		req.Header.Set("Authorization", "Bearer sk-"+tc.key)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		assert.Equal(t, 200, out.Code)
		assert.Equal(t, tc.body, out.Body.String())
	}
	tampered := signed.Query()
	tampered.Set("uid", strconv.Itoa(other.Id))
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, httptest.NewRequest("GET", signed.Path+"?"+tampered.Encode(), nil))
	assert.Equal(t, 404, out.Code)
	for _, update := range []map[string]any{{"auth_version": 2}, {"auth_version": 1, "status": common.UserStatusDisabled}} {
		require.NoError(t, model.DB.Model(&owner).Updates(update).Error)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, httptest.NewRequest("GET", signed.RequestURI(), nil))
		assert.Equal(t, 404, out.Code, "revoked owner must invalidate already issued URL")
	}
}

func TestTransferImageResultsRemainReadableAfterLastQuotaIsSpent(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, appI18n.Init())
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	owner := model.User{Username: "paid-image-owner", Status: common.UserStatusEnabled, Group: "default", AffCode: "paid-image-owner"}
	other := model.User{Username: "paid-image-other", Status: common.UserStatusEnabled, Group: "default", AffCode: "paid-image-other"}
	require.NoError(t, model.DB.Create(&owner).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	tokens := []model.Token{
		{UserId: owner.Id, Key: "paidImageOwner", Status: common.TokenStatusExhausted, ExpiredTime: -1, UsedQuota: 35000},
		{UserId: other.Id, Key: "paidImageOther", Status: common.TokenStatusExhausted, ExpiredTime: -1},
	}
	require.NoError(t, model.DB.Create(&tokens).Error)
	content := []byte("\x89PNG\r\n\x1a\npaid-image-range-fixture")
	path := filepath.Join(t.TempDir(), "paid.png")
	require.NoError(t, os.WriteFile(path, content, 0600))
	task := model.Task{TaskID: "task_paid_image_recovery", UserId: owner.Id,
		Action: constant.TaskActionImageGenerate, Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{AssetType: service.AsyncImageAssetType, LocalPath: path,
			MimeType: "image/png", FileSize: int64(len(content)), StoredAt: common.GetTimestamp(),
			ExpiresAt: common.GetTimestamp() + 3600}}
	require.NoError(t, model.DB.Create(&task).Error)
	engine := gin.New()
	SetRelayRouter(engine)
	for _, tc := range []struct {
		name, suffix, key, byteRange string
		status                       int
	}{
		{"owned status", "", tokens[0].Key, "", 200},
		{"owned content", "/content", tokens[0].Key, "", 200},
		{"owned resume", "/content", tokens[0].Key, "bytes=8-", 206},
		{"other status", "", tokens[1].Key, "", 404},
		{"other content", "/content", tokens[1].Key, "", 404},
		{"other resume", "/content", tokens[1].Key, "bytes=8-", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+task.TaskID+tc.suffix, nil)
			request.Header.Set("Authorization", "Bearer sk-"+tc.key)
			request.Header.Set("Range", tc.byteRange)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status == 206 {
				assert.Equal(t, content[8:], response.Body.Bytes())
				assert.NotEmpty(t, response.Header().Get("ETag"))
			} else if tc.status == 200 && tc.suffix == "/content" {
				assert.Equal(t, content, response.Body.Bytes())
			} else if tc.status == 200 {
				assert.Contains(t, response.Body.String(), `"status":"succeeded"`)
			}
		})
	}
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, tokens[0].Id).Error)
	assert.Zero(t, stored.RemainQuota)
	assert.Equal(t, 35000, stored.UsedQuota)
	assert.Equal(t, common.TokenStatusExhausted, stored.Status)
}

func TestTransferRoutesRecoverOwnedResultsAfterQuotaExhaustion(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, appI18n.Init())
	require.NoError(t, model.DB.AutoMigrate(&model.TransferPolicyRecord{}, &model.TransferRequest{}))
	t.Setenv("OPICS_BRIDGE_TICKET_SECRET", strings.Repeat("ab", 32))
	owner := model.User{Username: "transfer-owner", Status: common.UserStatusEnabled, Group: "default", AffCode: "transfer-owner"}
	other := model.User{Username: "transfer-other", Status: common.UserStatusEnabled, Group: "default", AffCode: "transfer-other"}
	require.NoError(t, model.DB.Create(&owner).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	tokens := []model.Token{
		{UserId: owner.Id, Key: "transferRouteOwner", Status: common.TokenStatusExhausted, ExpiredTime: -1, UsedQuota: 37},
		{UserId: other.Id, Key: "transferRouteOther", Status: common.TokenStatusExhausted, ExpiredTime: -1, UsedQuota: 53},
	}
	require.NoError(t, model.DB.Create(&tokens).Error)
	const requestID = "transfer_result_12345"
	record, claimed, err := model.ClaimTransferRequest(owner.Id, requestID, strings.Repeat("a", 64))
	require.NoError(t, err)
	require.True(t, claimed)
	body := []byte(`{"data":[{"url":"https://media.example.test/owned.png"}]}`)
	require.NoError(t, model.FinishTransferRequest(record, "succeeded", 200, `{"Content-Type":"application/json"}`, body))
	previousObservations := service.TransferObservations
	service.TransferObservations = service.NewTransferObservationStore()
	t.Cleanup(func() { service.TransferObservations = previousObservations })
	observation := `{"event_id":"observation_event_12345","request_id":"` + requestID +
		`","route":"origin_upstream","bytes":1024,"resumes":1,"reason":"slow_route","outcome":"completed"}`

	engine := gin.New()
	SetRelayRouter(engine)
	SetTaskPluginProtocolRouter(engine)
	var issuedTicket string
	for _, tc := range []struct {
		name, method, path, body, key, wantText string
		status                                  int
	}{
		{"capabilities", "GET", "/v1/transfer/capabilities", "", tokens[0].Key, `"request_recovery":true`, 200},
		{"owned receipt", "GET", "/v1/transfer/requests/" + requestID, "", tokens[0].Key, `"state":"succeeded"`, 200},
		{"other account receipt", "GET", "/v1/transfer/requests/" + requestID, "", tokens[1].Key, `"state":"not_registered"`, 200},
		{"owned ticket", "POST", "/v1/transfer/download-tickets", `{"request_id":"` + requestID + `"}`, tokens[0].Key, `"X-Opics-Download-Ticket"`, 200},
		{"other account ticket", "POST", "/v1/transfer/download-tickets", `{"request_id":"` + requestID + `"}`, tokens[1].Key, "transfer_resource_not_available", 404},
		{"owned observation", "POST", "/v1/transfer/download-observations", observation, tokens[0].Key, `"duplicate":false`, 200},
		{"duplicate observation", "POST", "/v1/transfer/download-observations", observation, tokens[0].Key, `"duplicate":true`, 200},
		{"changed observation", "POST", "/v1/transfer/download-observations", strings.Replace(observation, `"bytes":1024`, `"bytes":2048`, 1), tokens[0].Key, "transfer_observation_conflict", 409},
		{"other account observation", "POST", "/v1/transfer/download-observations", observation, tokens[1].Key, "transfer_resource_not_available", 404},
		{"new generation", "POST", "/v1/images/generations", `{"model":"gpt-image-1","prompt":"test"}`, tokens[0].Key, "insufficient_token_quota", 402},
		{"new upload", "POST", "/v1/uploads/images", "", tokens[0].Key, "insufficient_token_quota", 402},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+tc.key)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Claimed-User", "999999")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), tc.wantText)
			assert.NotContains(t, response.Body.String(), tc.key)
			if tc.name == "owned ticket" {
				var result struct {
					Headers map[string]string `json:"bridge_headers"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				issuedTicket = result.Headers["X-Opics-Download-Ticket"]
				require.NoError(t, service.AuthorizeTransferDownload(issuedTicket, time.Now()))
			}
			if tc.name == "capabilities" {
				var capabilities map[string]any
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &capabilities))
				assert.Equal(t, float64(owner.Id), capabilities["admission_user_id"])
			}
			if tc.name == "other account receipt" {
				assert.NotContains(t, response.Body.String(), "response_body_base64")
				assert.NotContains(t, response.Body.String(), record.ExecutionID)
			}
		})
	}
	totals := service.TransferObservations.Snapshot()
	assert.Equal(t, "client_reported", totals.Source)
	assert.EqualValues(t, 1, totals.Routes["origin_upstream"].Reports)
	assert.EqualValues(t, 1024, totals.Routes["origin_upstream"].Bytes, "lost ACK must not duplicate byte accounting")

	// Revocation remains effective even after a successful recovery with this key.
	require.NoError(t, model.DB.Model(&tokens[0]).Update("status", common.TokenStatusDisabled).Error)
	assert.Error(t, service.AuthorizeTransferDownload(issuedTicket, time.Now()))
	request := httptest.NewRequest(http.MethodGet, "/v1/transfer/requests/"+requestID, nil)
	request.Header.Set("Authorization", "Bearer "+tokens[0].Key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	var count int64
	require.NoError(t, model.DB.Model(&model.TransferRequest{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "recovery and blocked generation must not register another operation")
	for _, token := range tokens {
		var stored model.Token
		require.NoError(t, model.DB.First(&stored, token.Id).Error)
		assert.Zero(t, stored.RemainQuota)
		assert.Equal(t, token.UsedQuota, stored.UsedQuota)
	}
}

func TestQiniuRevocationBindingAndChunkReuse(t *testing.T) {
	setupBridgeSecurityDB(t)
	require.NoError(t, appI18n.Init())
	t.Setenv("OPICS_BRIDGE_TICKET_SECRET", strings.Repeat("ab", 32))
	t.Setenv("TRANSFER_QINIU_CALLBACK_SECRET", strings.Repeat("q", 32))
	t.Setenv("TRANSFER_QINIU_DOWNLOAD_ORIGIN", "https://cdn.example.test")
	t.Setenv("TRANSFER_QINIU_DOWNLOAD_AUTH_VERIFIED", "true")
	t.Setenv("TRANSFER_QINIU_UPLOAD_ORIGIN", "https://upload.example.test")
	t.Setenv("TRANSFER_QINIU_DIRECT_UPLOAD_ENABLED", "true")
	var content bytes.Buffer
	require.NoError(t, png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	data := content.Bytes()
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	var reads atomic.Int32
	var stored atomic.Bool
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		assert.Empty(t, r.Header.Get("Authorization"))
		if !stored.Load() {
			w.WriteHeader(404)
			return
		}
		w.Write(data)
	}))
	defer storage.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "storage.example.test:443", address)
		dialer := tls.Dialer{Config: storage.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
		return dialer.DialContext(ctx, network, storage.Listener.Addr().String())
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	store, err := service.NewReferenceUploadStore(service.ReferenceUploadConfig{AccessKey: "fixture-ak", SecretKey: "fixture-sk", Bucket: "fixture", UploadURL: "https://upload-z2.qiniup.com", DownloadURL: "https://storage.example.test", Directory: t.TempDir(), MaxCacheBytes: 16 << 20})
	require.NoError(t, err)
	previousStore := service.ReferenceUploads
	service.ReferenceUploads = store
	t.Cleanup(func() { service.ReferenceUploads = previousStore })
	owner := model.User{Username: "qiniu-owner", AffCode: "qiniu-owner", Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&owner).Error)
	token := model.Token{UserId: owner.Id, Key: "qiniufixturetoken", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, model.DB.Create(&token).Error)
	tokenHash := sha256.Sum256([]byte(token.Key))
	claim := service.TransferDownloadClaim{Version: 1, UserID: owner.Id, TokenID: token.Id, TokenDigest: hex.EncodeToString(tokenHash[:]), AuthVersion: 1,
		ResourceID: "owned-result", Route: "qiniu-download", ObjectKey: "transfer/results/immutable", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 300}
	ticket, err := service.SealTransferDownload(claim)
	require.NoError(t, err)
	engine := gin.New()
	SetRelayRouter(engine)
	send := func(secret, path, method, capability string) int {
		req := httptest.NewRequest("POST", "/internal/qiniu/authorize?operation=download&host=cdn.example.test&path="+url.QueryEscape(path)+"&method="+method, nil)
		req.Header.Set("X-Opics-Callback-Key", secret)
		req.Header.Set("X-Opics-Qiniu-Ticket", capability)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		assert.Equal(t, "no-store", out.Header().Get("Cache-Control"))
		return out.Code
	}
	secret := strings.Repeat("q", 32)
	assert.Equal(t, 204, send(secret, "/"+claim.ObjectKey, "GET", ticket))
	assert.Equal(t, 204, send(secret, "/"+claim.ObjectKey, "HEAD", ticket))
	assert.Equal(t, 401, send("bad", "/"+claim.ObjectKey, "GET", ticket))
	assert.Equal(t, 403, send(secret, "/transfer/results/other", "GET", ticket))
	assert.Equal(t, 403, send(secret, "/"+claim.ObjectKey, "PUT", ticket))
	assert.Equal(t, 403, send(secret, "/"+claim.ObjectKey, "GET", ticket+"x"))
	assert.Error(t, service.AuthorizeTransferDownload(ticket, time.Now()), "cloud ticket cannot be replayed against the bridge")
	_, err = service.OpenAuthorizedTransferDownload(ticket, time.Now().Add(6*time.Minute))
	assert.Error(t, err)
	for _, update := range []map[string]any{{"status": common.TokenStatusDisabled}, {"status": common.TokenStatusEnabled, "expired_time": time.Now().Unix() - 1}, {"expired_time": -1, "key": "rotated"}} {
		require.NoError(t, model.DB.Model(&token).Updates(update).Error)
		assert.Equal(t, 403, send(secret, "/"+claim.ObjectKey, "GET", ticket))
	}
	require.NoError(t, model.DB.Model(&token).Updates(map[string]any{"key": "qiniufixturetoken", "status": common.TokenStatusEnabled}).Error)
	require.NoError(t, model.DB.Model(&owner).Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, 403, send(secret, "/"+claim.ObjectKey, "GET", ticket))
	require.NoError(t, model.DB.Model(&owner).Update("status", common.UserStatusEnabled).Error)
	for _, path := range []string{"/" + claim.ObjectKey, "/transfer/results/other"} {
		req := httptest.NewRequest("POST", "/internal/qiniu/authorize?operation=origin-read&path="+url.QueryEscape(path), nil)
		req.Header.Set("X-Opics-Qiniu-Ticket", ticket)
		req.Header.Set("X-Opics-Callback-Key", secret)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		if path == "/"+claim.ObjectKey {
			assert.Equal(t, 200, out.Code)
			assert.Contains(t, out.Body.String(), "https://storage.example.test/")
		} else {
			assert.Equal(t, 403, out.Code)
		}
	}
	t.Setenv("TRANSFER_QINIU_DOWNLOAD_AUTH_VERIFIED", "false")
	assert.Equal(t, 403, send(secret, "/"+claim.ObjectKey, "GET", ticket))
	prepared, err := store.PrepareResumable(owner.Id, service.ReferencePrepareRequest{SHA256: digest, Size: int64(len(data)), MIME: "image/png"})
	require.NoError(t, err)
	chunk, _, err := store.QiniuChunk(owner.Id, prepared.UploadID, 0, digest)
	require.NoError(t, err)
	_, _, err = store.QiniuChunk(owner.Id+1, prepared.UploadID, 0, digest)
	assert.Error(t, err)
	// Native grants work without an upload edge or CDN callback secret.
	t.Setenv("TRANSFER_QINIU_UPLOAD_ORIGIN", "")
	t.Setenv("TRANSFER_QINIU_CALLBACK_SECRET", "")
	call := func(action string, body any, key string) *httptest.ResponseRecorder {
		encoded, err := common.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/v1/transfer/qiniu-chunks/"+action, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	input := gin.H{"upload_id": prepared.UploadID, "index": 0, "sha256": digest}
	for _, index := range []int64{-1, 1, 1 << 44, 1<<63 - 1} {
		out := call("prepare", gin.H{"upload_id": prepared.UploadID, "index": index, "sha256": digest}, token.Key)
		assert.Equal(t, 400, out.Code, out.Body.String())
	}
	assert.Zero(t, reads.Load(), "invalid chunk numbers must not reach storage")
	out := call("prepare", input, token.Key)
	require.Equal(t, 200, out.Code, out.Body.String())
	var grant struct {
		service.QiniuUploadGrant
		Ticket string `json:"ticket"`
		Method string `json:"upload_method"`
	}
	require.NoError(t, common.Unmarshal(out.Body.Bytes(), &grant))
	assert.Equal(t, "https://upload-z2.qiniup.com", grant.URL)
	assert.Equal(t, "POST", grant.Method)
	assert.Equal(t, chunk.ObjectKey, grant.Key)
	assert.NotContains(t, out.Body.String(), "fixture-sk")
	assert.NotContains(t, out.Body.String(), token.Key)
	parts := strings.Split(grant.Token, ":")
	require.Len(t, parts, 3)
	policyBytes, err := base64.URLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	var uploadPolicy map[string]any
	require.NoError(t, common.Unmarshal(policyBytes, &uploadPolicy))
	assert.Equal(t, "fixture:"+chunk.ObjectKey, uploadPolicy["scope"])
	assert.EqualValues(t, len(data), uploadPolicy["fsizeMin"])
	assert.EqualValues(t, len(data), uploadPolicy["fsizeLimit"])
	assert.EqualValues(t, 1, uploadPolicy["insertOnly"])
	assert.EqualValues(t, 1, uploadPolicy["deleteAfterDays"])
	assert.EqualValues(t, grant.ExpiresAt, uploadPolicy["deadline"])
	assert.InDelta(t, time.Now().Add(time.Minute).Unix(), grant.ExpiresAt, 2)
	completion := gin.H{"ticket": grant.Ticket}
	out = call("complete", completion, token.Key)
	assert.Equal(t, 502, out.Code, out.Body.String())
	assert.Contains(t, out.Body.String(), "reference_download_failed", "missing storage bytes must permit route recovery")
	stale := chunk
	stale.UploadID = strings.Repeat("0", 64)
	staleGrant, ready, err := store.AuthorizeQiniuUpload(context.Background(), stale)
	assert.ErrorIs(t, err, service.ErrReferenceMissing)
	assert.False(t, ready)
	assert.Empty(t, staleGrant.Token, "missing session must never issue another native token")
	stored.Store(true) // Kodo accepted the object; its upload ACK was lost.
	before := reads.Load()
	for _, update := range []map[string]any{{"status": common.TokenStatusDisabled}, {"status": common.TokenStatusEnabled, "expired_time": time.Now().Unix() - 1}, {"expired_time": -1, "key": "rotated"}} {
		require.NoError(t, model.DB.Model(&token).Updates(update).Error)
		assert.Contains(t, []int{401, 403}, call("complete", completion, "qiniufixturetoken").Code)
		assert.Contains(t, []int{401, 403}, call("prepare", input, "qiniufixturetoken").Code)
		assert.Equal(t, before, reads.Load(), "revoked request cannot import stored data")
	}
	require.NoError(t, model.DB.Model(&token).Updates(map[string]any{"key": "qiniufixturetoken", "status": common.TokenStatusEnabled}).Error)
	for _, status := range []int{common.UserStatusDisabled, common.UserStatusEnabled} {
		require.NoError(t, model.DB.Model(&owner).Update("status", status).Error)
		out = call("complete", completion, "qiniufixturetoken")
		if status == common.UserStatusDisabled {
			assert.Contains(t, []int{401, 403}, out.Code)
			assert.Equal(t, before, reads.Load())
		} else {
			require.Equal(t, 200, out.Code, out.Body.String())
			assert.Contains(t, out.Body.String(), `"received_chunks":[0]`)
		}
	}
	readsAfterImport := reads.Load()
	assert.Equal(t, before+1, readsAfterImport)
	out = call("prepare", input, "qiniufixturetoken")
	require.Equal(t, 200, out.Code, out.Body.String())
	assert.Contains(t, out.Body.String(), `"status":"received"`)
	assert.NotContains(t, out.Body.String(), `"upload_token"`)
	_, err = store.ImportQiniuChunk(context.Background(), chunk)
	require.NoError(t, err)
	assert.Equal(t, readsAfterImport, reads.Load(), "lost acknowledgement must reuse verified chunk")
	_, ready, err = store.AuthorizeQiniuUpload(context.Background(), chunk)
	require.NoError(t, err)
	assert.True(t, ready)
	complete, err := store.CompleteResumable(owner.Id, prepared.UploadID)
	require.NoError(t, err)
	assert.Equal(t, "ready", complete.Status)
	reused, err := store.PrepareResumable(owner.Id, service.ReferencePrepareRequest{SHA256: digest, Size: int64(len(data)), MIME: "image/png"})
	require.NoError(t, err)
	assert.Equal(t, "ready", reused.Status)
	assert.Equal(t, readsAfterImport, reads.Load())
}
