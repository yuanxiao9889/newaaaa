package controller

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1" // Legacy Qiniu ETags use SHA-1.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// The same authenticated owner / request ledger contract runs against all real
// dialects. Test table names are isolated, including from other matrix runners.
func TestTransferDatabaseAndHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPICS_BRIDGE_TICKET_SECRET", strings.Repeat("12", 32))
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			prefix := fmt.Sprintf("transfer_%d_", time.Now().UnixNano())
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
			require.NoError(t, err)
			conn, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				conn.SetMaxOpenConns(1)
			} else {
				conn.SetMaxOpenConns(8)
			}
			previous := model.DB
			model.DB = db
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&model.User{}, &model.Task{}))
				require.NoError(t, db.Migrator().DropTable(&model.TransferRequest{}, &model.TransferPolicyRecord{},
					&model.ReferencePreparation{}, &model.ReferenceAsset{}, &model.ReferenceAdmission{}))
				model.DB = previous
				require.NoError(t, conn.Close())
			})
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)
			require.NoError(t, db.AutoMigrate(&model.TransferRequest{}, &model.TransferPolicyRecord{}))
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Task{}))
			require.NoError(t, db.Create(&model.User{Id: 1, Username: "transfer-owner", AffCode: "transfer-owner", Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
			// Additive migration over existing relay policy/request tables, followed
			// by repeated startup with live asset metadata below.
			require.NoError(t, db.AutoMigrate(&model.ReferenceAsset{}, &model.ReferencePreparation{}, &model.ReferenceAdmission{}))
			testDirectReferenceContract(t, db)

			first, err := model.LoadTransferPolicy()
			require.NoError(t, err)
			assert.EqualValues(t, 1, first.Version)
			changed := first.Policy
			changed.CooldownSeconds = 90
			updated, err := model.SaveTransferPolicy(first.Version, changed, 1, "Test update")
			require.NoError(t, err)
			require.Len(t, updated.History, 2)
			assert.EqualValues(t, 1, updated.History[0].Version)
			_, err = model.SaveTransferPolicy(first.Version, first.Policy, 1, "Stale update")
			require.ErrorIs(t, err, model.ErrTransferPolicyConflict)
			changed.StallPrepareSeconds = changed.StallFailSeconds
			_, err = model.SaveTransferPolicy(updated.Version, changed, 1, "Invalid limits")
			require.Error(t, err)
			// Repeated startup retains both existing policy and request rows.
			require.NoError(t, db.AutoMigrate(&model.TransferRequest{}, &model.TransferPolicyRecord{}))
			reloaded, err := model.LoadTransferPolicy()
			require.NoError(t, err)
			assert.Equal(t, updated, reloaded)

			var dispatches atomic.Int32
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				owner := 1
				if c.GetHeader("Test-Owner") == "2" {
					owner = 2
				}
				c.Set("id", owner)
				c.Next()
			})
			engine.POST("/v1/images/generations", middleware.TransferRequestGuard, func(c *gin.Context) {
				dispatches.Add(1)
				require.NotEmpty(t, c.GetString("transfer_execution_id"))
				switch c.GetHeader("Test-Outcome") {
				case "unknown":
					c.JSON(503, gin.H{"error": "upstream disconnected"})
				case "stream":
					c.Header("Content-Type", "text/event-stream")
					_, _ = c.Writer.WriteString("data: output\n\n")
				case "large":
					c.Data(200, "application/json", bytes.Repeat([]byte("a"), model.TransferResponseLimit+1))
				default:
					c.JSON(200, gin.H{"data": []gin.H{{"url": "https://example.com/owned.png"}}})
				}
			})
			engine.GET("/v1/transfer/requests/:key", GetTransferRequest)
			engine.GET("/v1/transfer/capabilities", TransferCapabilities)
			engine.POST("/v1/transfer/download-tickets", CreateTransferDownloadTicket)
			engine.POST("/mj/submit/upload-discord-images", middleware.TransferRequestGuard, func(c *gin.Context) {
				assert.Empty(t, c.GetString("transfer_execution_id"))
				c.Status(204)
			})
			send := func(method, path, key, body, owner, outcome string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				r.Header.Set("Test-Owner", owner)
				r.Header.Set("Test-Outcome", outcome)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, r)
				return w
			}
			path := "/v1/images/generations"
			key := "transfer-request-0001"
			body := `{"model":"test","seed":9007199254740993,"flag":false,"n":0}`
			out := send("POST", path, key, body, "1", "")
			require.Equal(t, 200, out.Code, out.Body.String())
			replay := send("POST", path, key, `{"n":0,"flag":false,"seed":9007199254740993,"model":"test"}`, "1", "")
			assert.Equal(t, out.Body.String(), replay.Body.String())
			assert.Equal(t, "true", replay.Header().Get("X-Transfer-Replayed"))
			assert.EqualValues(t, 1, dispatches.Load())
			assert.Equal(t, 204, send("POST", "/mj/submit/upload-discord-images", key, `{"images":[]}`, "1", "").Code,
				"reference upload must not collide with the generation identity")
			conflict := send("POST", path, key, strings.Replace(body, "9007199254740993", "9007199254740992", 1), "1", "")
			assert.Equal(t, 409, conflict.Code)
			assert.Contains(t, conflict.Body.String(), "transfer_request_conflict")
			assert.Equal(t, 200, send("POST", path, key, body, "2", "").Code)
			assert.EqualValues(t, 2, dispatches.Load(), "different account has a different operation")

			unknownKey := "transfer-unknown-0001"
			assert.Equal(t, 503, send("POST", path, unknownKey, body, "1", "unknown").Code)
			assert.Equal(t, 409, send("POST", path, unknownKey, body, "1", "").Code)
			unknown := send("GET", "/v1/transfer/requests/"+unknownKey, "", "", "1", "")
			assert.Contains(t, unknown.Body.String(), `"state":"unknown"`)
			assert.NotContains(t, unknown.Body.String(), "response_body_base64")
			missing := send("GET", "/v1/transfer/requests/transfer-missing-0001", "", "", "1", "")
			assert.Equal(t, 200, missing.Code)
			assert.Contains(t, missing.Body.String(), `"state":"not_registered"`)
			assert.EqualValues(t, 3, dispatches.Load())
			streamKey := "transfer-stream-0001"
			assert.Equal(t, 200, send("POST", path, streamKey, body, "1", "stream").Code)
			assert.Equal(t, 409, send("POST", path, streamKey, body, "1", "").Code)
			assert.NotContains(t, send("GET", "/v1/transfer/requests/"+streamKey, "", "", "1", "").Body.String(), "response_body_base64")
			largeKey := "transfer-large-0001"
			assert.Equal(t, 200, send("POST", path, largeKey, body, "1", "large").Code)
			assert.Equal(t, 409, send("POST", path, largeKey, body, "1", "").Code)
			largeResult := send("GET", "/v1/transfer/requests/"+largeKey, "", "", "1", "").Body.String()
			assert.Contains(t, largeResult, `"state":"succeeded"`)
			assert.NotContains(t, largeResult, "response_body_base64")

			before := dispatches.Load()
			var group sync.WaitGroup
			for range 12 {
				group.Go(func() {
					response := send("POST", path, "transfer-concurrent-0001", body, "1", "")
					assert.Contains(t, []int{200, 409}, response.Code, response.Body.String())
				})
			}
			group.Wait()
			assert.Equal(t, before+1, dispatches.Load(), "only one handler may dispatch")

			// Case-sensitive identity remains correct with default MySQL collation.
			for _, k := range []string{"Transfer-Case-0001", "transfer-case-0001"} {
				assert.Equal(t, 200, send("POST", path, k, body, "1", "").Code)
			}
			assert.Equal(t, before+3, dispatches.Load())

			ticketBody := `{"request_id":"transfer-request-0001","source_url":"https://example.com/owned.png"}`
			ticket := send("POST", "/v1/transfer/download-tickets", "", ticketBody, "1", "")
			require.Equal(t, 200, ticket.Code, ticket.Body.String())
			var response struct {
				BridgeURL     string            `json:"bridge_url"`
				OriginURL     string            `json:"origin_url"`
				BridgeHeaders map[string]string `json:"bridge_headers"`
			}
			require.NoError(t, common.Unmarshal(ticket.Body.Bytes(), &response))
			assert.Equal(t, model.TransferBridge+"/v1/transfer/download", response.BridgeURL)
			assert.Equal(t, "https://example.com/owned.png", response.OriginURL)
			sealed, err := base64.RawURLEncoding.DecodeString(response.BridgeHeaders["X-Opics-Download-Ticket"])
			require.NoError(t, err)
			secret, _ := hex.DecodeString(os.Getenv("OPICS_BRIDGE_TICKET_SECRET"))
			block, err := aes.NewCipher(secret)
			require.NoError(t, err)
			aead, err := cipher.NewGCM(block)
			require.NoError(t, err)
			plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("opics-download-v1"))
			require.NoError(t, err)
			var claim service.TransferDownloadClaim
			require.NoError(t, common.Unmarshal(plain, &claim))
			assert.Equal(t, 1, claim.UserID)
			assert.EqualValues(t, 300, claim.ExpiresAt-claim.IssuedAt)
			assert.Equal(t, response.OriginURL, claim.URL)
			assert.False(t, bytes.Contains(sealed, []byte(claim.URL)))
			arbitrary := strings.Replace(ticketBody, "https://example.com/owned.png", "https://example.com/unowned.png", 1)
			assert.Equal(t, 404, send("POST", "/v1/transfer/download-tickets", "", arbitrary, "1", "").Code)
			privateKey := "transfer-private-0001"
			assert.Equal(t, 200, send("POST", path, privateKey, body, "1", "").Code)
			privateBody := strings.Replace(ticketBody, key, privateKey, 1)
			assert.Equal(t, 404, send("POST", "/v1/transfer/download-tickets", "", privateBody, "2", "").Code)
			assert.Equal(t, 404, send("POST", "/v1/transfer/download-tickets", "", `{"source_url":"https://example.com/owned.png"}`, "1", "").Code)

			// Cleanup cannot authorize a replay, even after content retention ends.
			require.NoError(t, db.Model(&model.TransferRequest{}).Where("request_key = ?", key).
				Update("finished_at", time.Now().Add(-8*24*time.Hour).Unix()).Error)
			require.NoError(t, model.CompactTransferResponses())
			assert.Equal(t, 409, send("POST", path, key, body, "1", "").Code)
			assert.Contains(t, send("GET", "/v1/transfer/requests/"+key, "", "", "1", "").Body.String(), `"state":"expired"`)
			record, err := model.FindTransferRequest(1, key)
			require.NoError(t, err)
			require.NotNil(t, record)
			assert.Empty(t, record.ResponseBody)
			assert.NotEmpty(t, record.Digest)
			unknownRecord, err := model.FindTransferRequest(1, unknownKey)
			require.NoError(t, err)
			assert.Equal(t, "unknown", unknownRecord.State)
			require.NoError(t, db.AutoMigrate(&model.TransferRequest{}, &model.TransferPolicyRecord{}))
			_, owned, err := model.ClaimTransferRequest(1, key, record.Digest)
			require.NoError(t, err)
			assert.False(t, owned, "migration never resets registrations")
			assert.Equal(t, http.StatusOK, send("GET", "/v1/transfer/capabilities", "", "", "1", "").Code)
		})
	}
}

func testDirectReferenceContract(t *testing.T, db *gorm.DB) {
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 3))))
	data := pngBytes.Bytes()
	digest := sha256.Sum256(data)
	input := service.ReferencePrepareRequest{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)),
		MIME: "image/png", Transport: "relay", RequestID: "prepare-reference-0001"}
	config := service.ReferenceUploadConfig{Directory: t.TempDir(), DirectEnabled: true,
		SigningSecret: strings.Repeat("local-reference-secret", 2), MaxCacheBytes: 32 << 20, MaxUserCacheBytes: 16 << 20,
		MinFreePercent: 1}
	first, err := service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	second, err := service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	prepared, err := first.Prepare(101, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", prepared.Status)
	require.NotEmpty(t, prepared.SessionID)
	assert.Empty(t, prepared.UploadToken, "relay direct never issues an OSS credential")
	replayed, err := second.Prepare(101, input)
	require.NoError(t, err)
	assert.Equal(t, prepared, replayed, "separate service instances share one persistent session")
	_, err = second.DirectSession(102, prepared.SessionID)
	require.ErrorIs(t, err, service.ErrReferenceMissing)
	_, err = first.UploadDirect(101, prepared.SessionID, strings.Repeat("0", 64), bytes.NewReader(data))
	require.ErrorIs(t, err, service.ErrReferenceInvalid)
	ticket := prepared.UploadHeaders["X-Reference-Upload-Ticket"]
	_, err = first.UploadDirect(101, prepared.SessionID, ticket, bytes.NewReader([]byte("corrupt")))
	require.ErrorIs(t, err, service.ErrReferenceInvalid)
	retry, err := second.DirectSession(101, prepared.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", retry.Status)
	ready, err := second.UploadDirect(101, prepared.SessionID, ticket, bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "ready", ready.Status)
	again, err := first.UploadDirect(101, prepared.SessionID, ticket, bytes.NewReader(nil))
	require.NoError(t, err)
	assert.Equal(t, ready, again, "lost PUT response does not require another body upload")
	require.NoError(t, db.AutoMigrate(&model.ReferenceAsset{}, &model.ReferencePreparation{}, &model.ReferenceAdmission{}))
	require.NoError(t, db.AutoMigrate(&model.ReferenceAsset{}, &model.ReferencePreparation{}, &model.ReferenceAdmission{}))
	restarted, err := service.NewReferenceUploadStore(config)
	require.NoError(t, err)
	read, mime, err := restarted.Read(101, input.SHA256, false)
	require.NoError(t, err)
	assert.Equal(t, data, read)
	assert.Equal(t, "image/png", mime)
	_, _, err = restarted.Read(102, input.SHA256, false)
	require.ErrorIs(t, err, service.ErrReferenceMissing)
	status := restarted.DirectStatus()
	assert.True(t, status.Enabled)
	assert.True(t, status.Measured)
	assert.Equal(t, 1, status.ReadyAssets)
	assert.EqualValues(t, 0, status.ReservedBytes)
	assert.GreaterOrEqual(t, status.StoredBytes, int64(len(data)))

	// Adopt a verified legacy binary without an OSS request or client upload.
	legacyPath := filepath.Join(config.Directory, "103-"+input.SHA256+".asset")
	require.NoError(t, os.WriteFile(legacyPath, data, 0600))
	adopted, err := restarted.Prepare(103, input)
	require.NoError(t, err)
	assert.Equal(t, "ready", adopted.Status)
	legacyHash := sha1.Sum(data)
	etag := base64.URLEncoding.EncodeToString(append([]byte{0x16}, legacyHash[:]...))
	var migrations atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("ETag", `"`+etag+`"`)
		if r.Method == "GET" {
			migrations.Add(1)
			_, _ = w.Write(data)
		}
	}))
	defer origin.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = origin.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	migrationConfig := config
	migrationConfig.AccessKey, migrationConfig.SecretKey, migrationConfig.Bucket = "fixture-access", "fixture-secret", "fixture"
	migrationConfig.UploadURL, migrationConfig.DownloadURL = origin.URL, origin.URL
	migrating, err := service.NewReferenceUploadStore(migrationConfig)
	require.NoError(t, err)
	oldInput := input
	oldInput.Transport, oldInput.RequestID = "", ""
	oldInput.Mode, oldInput.ETag = "url", etag
	oldPrepared, err := migrating.Prepare(105, oldInput)
	require.NoError(t, err)
	_, err = migrating.Complete(context.Background(), 105, oldPrepared.Ticket)
	require.NoError(t, err)
	assert.Zero(t, migrations.Load(), "legacy URL metadata did not download the object")
	migrated, err := migrating.Prepare(105, input)
	require.NoError(t, err)
	assert.Equal(t, "ready", migrated.Status)
	assert.EqualValues(t, 1, migrations.Load())
	_, err = migrating.Prepare(105, input)
	require.NoError(t, err)
	assert.EqualValues(t, 1, migrations.Load(), "verified local migration is reused")
	// Capacity is reserved for the entire body at prepare, even with no bytes
	// received yet. A second account cannot exceed the storage-wide budget.
	limitedConfig := config
	limitedConfig.MaxCacheBytes, limitedConfig.MaxUserCacheBytes = 8<<20, 8<<20
	limited, err := service.NewReferenceUploadStore(limitedConfig)
	require.NoError(t, err)
	oversized := input
	oversized.SHA256 = strings.Repeat("a", 64)
	oversized.Size = 8 << 20
	_, err = limited.Prepare(104, oversized)
	require.ErrorIs(t, err, service.ErrReferenceBusy)
	oversized.Size++
	_, err = limited.Prepare(104, oversized)
	require.ErrorIs(t, err, service.ErrReferenceInvalid)

	// Polling and preparation replay preserve the already promised deadline;
	// a distinct preparation extends it exactly once.
	until := time.Now().Add(time.Minute).Unix()
	require.NoError(t, db.Model(&model.ReferenceAsset{}).Where("session_id = ?", ready.SessionID).Update("idle_expires", until).Error)
	poll, err := restarted.DirectSession(101, ready.SessionID)
	require.NoError(t, err)
	assert.Equal(t, until, poll.ExpiresAt)
	replayed, err = restarted.Prepare(101, input)
	require.NoError(t, err)
	assert.Equal(t, until, replayed.ExpiresAt)
	input.RequestID = "prepare-reference-0002"
	renewed, err := restarted.Prepare(101, input)
	require.NoError(t, err)
	assert.Greater(t, renewed.ExpiresAt, until)
	assert.InDelta(t, service.ReferenceRetention().Seconds(), float64(renewed.ExpiresAt-time.Now().Unix()), 2)
	assert.Equal(t, ready.SessionID, renewed.SessionID)
	// A real use adopts the new policy even for entries issued under the old
	// twelve-hour setting; polling above never changes an issued deadline.
	require.NoError(t, db.Model(&model.ReferenceAsset{}).Where("session_id = ?", ready.SessionID).
		Update("idle_expires", time.Now().Add(12*time.Hour).Unix()).Error)
	_, _, err = restarted.Read(101, input.SHA256, true)
	require.NoError(t, err)
	used, err := restarted.DirectSession(101, ready.SessionID)
	require.NoError(t, err)
	assert.InDelta(t, service.ReferenceRetention().Seconds(), float64(used.ExpiresAt-time.Now().Unix()), 2)
	// Another directory on the same database must not masquerade as a cache hit
	// or create a second authoritative copy.
	otherConfig := config
	otherConfig.Directory = t.TempDir()
	wrongNode, err := service.NewReferenceUploadStore(otherConfig)
	require.NoError(t, err)
	_, err = wrongNode.Prepare(101, input)
	require.ErrorIs(t, err, service.ErrReferenceUnavailable)

	// A late upload worker cannot publish after its lease is replaced.
	other, err := first.Prepare(102, input)
	require.NoError(t, err)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := first.UploadDirect(102, other.SessionID, other.UploadHeaders["X-Reference-Upload-Ticket"], reader)
		done <- err
	}()
	// The first byte is consumed only after the durable writer lease is claimed.
	_, err = writer.Write(data[:1])
	require.NoError(t, err)
	uploading, err := second.DirectSession(102, other.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "uploading", uploading.Status)
	_, err = second.UploadDirect(102, other.SessionID, other.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	require.ErrorIs(t, err, service.ErrReferenceBusy)
	require.NoError(t, db.Model(&model.ReferenceAsset{}).Where("session_id = ?", other.SessionID).Update("lease_until", 0).Error)
	recovered, err := second.UploadDirect(102, other.SessionID, other.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "ready", recovered.Status)
	_, err = writer.Write(data[1:])
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.ErrorIs(t, <-done, service.ErrReferenceBusy)
	require.NoError(t, reader.Close())

	// Cleanup protects valid material, removes only expired registered files,
	// and never changes generation request tombstones.
	require.NoError(t, restarted.CleanupDirect())
	require.NoError(t, db.Model(&model.ReferenceAsset{}).Where("session_id = ?", ready.SessionID).
		Update("idle_expires", time.Now().Add(-time.Second).Unix()).Error)
	require.NoError(t, restarted.CleanupDirect())
	_, _, err = restarted.Read(101, input.SHA256, false)
	require.ErrorIs(t, err, service.ErrReferenceMissing)
	var saved model.ReferenceAsset
	require.NoError(t, db.Where("session_id = ?", other.SessionID).First(&saved).Error)
	require.NoError(t, os.Remove(filepath.Join(config.Directory, "direct-v2", saved.FileName)))
	_, err = second.DirectSession(102, other.SessionID)
	require.ErrorIs(t, err, service.ErrReferenceMissing, "metadata alone never reports ready")
	input.RequestID = "prepare-reference-0003"
	replacement, err := second.Prepare(102, input)
	require.NoError(t, err)
	assert.Equal(t, "upload_required", replacement.Status)
	assert.NotEqual(t, other.SessionID, replacement.SessionID)
	_, err = second.UploadDirect(102, other.SessionID, other.UploadHeaders["X-Reference-Upload-Ticket"], bytes.NewReader(data))
	require.ErrorIs(t, err, service.ErrReferenceMissing)

	// HTTP sessions return bounded metadata, require the account-scoped ticket,
	// and publish strong validators for resumed content downloads.
	previous := service.ReferenceUploads
	service.ReferenceUploads = restarted
	defer func() { service.ReferenceUploads = previous }()
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", 102); c.Next() })
	engine.GET("/session/:session", GetDirectReferenceSession)
	engine.PUT("/session/:session", PutDirectReferenceSession)
	engine.GET("/capabilities", ReferenceUploadCapabilities)
	engine.GET("/v1/uploads/references/content/:user/:asset", GetReferenceContent)
	missingTicket := httptest.NewRecorder()
	engine.ServeHTTP(missingTicket, httptest.NewRequest("PUT", "/session/"+replacement.SessionID, bytes.NewReader(data)))
	assert.Equal(t, 400, missingTicket.Code)
	r := httptest.NewRequest("PUT", "/session/"+replacement.SessionID, bytes.NewReader(data))
	r.Header.Set("X-Reference-Upload-Ticket", replacement.UploadHeaders["X-Reference-Upload-Ticket"])
	uploaded := httptest.NewRecorder()
	engine.ServeHTTP(uploaded, r)
	assert.Equal(t, 200, uploaded.Code, uploaded.Body.String())
	assert.Contains(t, uploaded.Body.String(), `"status":"ready"`)
	assert.Equal(t, "private, no-store", uploaded.Header().Get("Cache-Control"))
	capabilities := httptest.NewRecorder()
	engine.ServeHTTP(capabilities, httptest.NewRequest("GET", "/capabilities", nil))
	assert.Contains(t, capabilities.Body.String(), `"relay_direct_upload":true`)
	assert.Contains(t, capabilities.Body.String(), `"direct_reference_urls":false`)
	_, err = first.Prepare(102, service.ReferencePrepareRequest{SHA256: input.SHA256, Size: input.Size, MIME: input.MIME})
	require.ErrorIs(t, err, service.ErrReferenceUnavailable)
	incomplete := config
	incomplete.AccessKey = "partial-legacy-configuration"
	_, err = service.NewReferenceUploadStore(incomplete)
	require.ErrorIs(t, err, service.ErrReferenceUnavailable)
	oldAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://relay.example"
	defer func() { system_setting.ServerAddress = oldAddress }()
	contentURL, err := restarted.PublicURL(102, input.SHA256)
	require.NoError(t, err)
	rangeRequest := httptest.NewRequest("GET", contentURL, nil)
	rangeRequest.Header.Set("Range", "bytes=0-2")
	partial := httptest.NewRecorder()
	engine.ServeHTTP(partial, rangeRequest)
	assert.Equal(t, 206, partial.Code)
	assert.Equal(t, data[:3], partial.Body.Bytes())
	assert.Equal(t, `"`+input.SHA256+`"`, partial.Header().Get("ETag"))
	for _, address := range []string{contentURL + "x", strings.Replace(contentURL, "/content/102/", "/content/103/", 1)} {
		denied := httptest.NewRecorder()
		engine.ServeHTTP(denied, httptest.NewRequest("GET", address, nil))
		assert.Equal(t, 404, denied.Code)
	}
}

func TestBridgeCapacitySettingsContract(t *testing.T) {
	var config service.BridgeGatewayConfig
	raw := `{"revision":1,"image_cache_enabled":true,"image_cache_max_bytes":1073741824,"image_cache_max_file_bytes":33554432,"max_resume_attempts":2,"capacity":{"upload":{"global":8,"per_account":2,"queue":16,"per_account_queue":4,"wait_ms":2000},"download":{"global":8,"per_account":2,"queue":16,"per_account_queue":4,"wait_ms":2000},"bandwidth_bytes_per_second":4194304,"api_reserved_bytes_per_second":524288}}`
	require.NoError(t, common.UnmarshalJsonStr(raw, &config))
	require.NoError(t, service.ValidateBridgeGatewayConfig(config))
	for _, tc := range []struct {
		name   string
		change func(*service.BridgeCapacityConfig)
	}{
		{"reserve consumes file budget", func(c *service.BridgeCapacityConfig) { c.APIReservedBytesPerSecond = c.BandwidthBytesPerSecond }},
		{"per account bypasses global", func(c *service.BridgeCapacityConfig) { c.Upload.PerAccount = 9 }},
		{"unbounded wait", func(c *service.BridgeCapacityConfig) { c.Download.WaitMS = 3001 }},
		{"inconsistent disabled queue", func(c *service.BridgeCapacityConfig) { c.Upload.Queue = 0 }},
		{"combined resource ceiling", func(c *service.BridgeCapacityConfig) { c.Upload.Global = 32; c.Download.Global = 32 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyConfig := config
			capacity := *config.Capacity
			copyConfig.Capacity = &capacity
			tc.change(&capacity)
			assert.Error(t, service.ValidateBridgeGatewayConfig(copyConfig))
		})
	}
	config.Capacity.Upload.Queue = 0
	config.Capacity.Upload.PerAccountQueue = 0
	config.Capacity.Upload.WaitMS = 0
	assert.NoError(t, service.ValidateBridgeGatewayConfig(config))
	config.Capacity = nil // v2 runtime keeps existing cache controls, not invented limits.
	assert.NoError(t, service.ValidateBridgeGatewayConfig(config))
}
