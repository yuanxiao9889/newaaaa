package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/QuantumNous/new-api/common"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridgeRuntimeRequiresExactDurableAcknowledgement(t *testing.T) {
	t.Setenv("OPICS_BRIDGE_TICKET_SECRET", strings.Repeat("ab", 32))
	config := BridgeGatewayConfig{Revision: 3, ImageCacheEnabled: true, ImageCacheMaxBytes: 1 << 30, ImageCacheMaxFileBytes: 32 << 20, MaxResumeAttempts: 2,
		Capacity: &BridgeCapacityConfig{Upload: BridgePoolConfig{8, 3, 16, 4, 3000}, Download: BridgePoolConfig{8, 2, 16, 4, 2000}, BandwidthBytesPerSecond: 10 << 20, APIReservedBytesPerSecond: 1 << 20}}
	var answer BridgeGatewayStatus
	code := 200
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bridge.opics.top", r.Host)
		assert.Equal(t, "/internal/transfer/config", r.URL.Path)
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		assert.Empty(t, r.Header.Get("Cookie"))
		var received BridgeGatewayConfig
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		assert.Equal(t, config, received)
		w.WriteHeader(code)
		require.NoError(t, json.NewEncoder(w).Encode(answer))
	}))
	defer server.Close()
	// Substitute the TLS socket boundary only; production URL, request headers,
	// response decoding and revision checks all execute without public traffic.
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "bridge.opics.top:443", address)
		dialer := tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	for _, tc := range []struct {
		name   string
		change func(*BridgeGatewayStatus)
		ok     bool
	}{
		{"accepted", func(*BridgeGatewayStatus) {}, true},
		{"wrong revision", func(s *BridgeGatewayStatus) { s.AppliedRevision-- }, false},
		{"changed bandwidth", func(s *BridgeGatewayStatus) { s.Config.Capacity.APIReservedBytesPerSecond += 1024 }, false},
		{"missing live queues", func(s *BridgeGatewayStatus) { s.Capacity = nil }, false},
		{"unsupported version", func(s *BridgeGatewayStatus) { s.Version = 2 }, false},
		{"unexpected video cache", func(s *BridgeGatewayStatus) { s.VideoDiskCache = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer = BridgeGatewayStatus{Version: 3, AppliedRevision: config.Revision, Config: config, Capacity: &BridgeCapacityStatus{}, ImageCacheTTLSeconds: 14400}
			copied := *config.Capacity
			answer.Config.Capacity = &copied
			tc.change(&answer)
			err := ApplyBridgeGatewayConfig(context.Background(), config)
			if tc.ok {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrBridgeGatewayUnavailable)
			}
		})
	}
	code = 409
	assert.ErrorIs(t, ApplyBridgeGatewayConfig(context.Background(), config), ErrBridgeGatewayConflict)
}

type qiniuResultTransport func(*http.Request) (*http.Response, error)

func (f qiniuResultTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestQiniuResultsCopyOnceAndVerifyContentVersion(t *testing.T) {
	data := []byte("immutable result bytes")
	hash := sha256.Sum256(data)
	version := "sha256:" + hex.EncodeToString(hash[:])
	store, err := NewReferenceUploadStore(ReferenceUploadConfig{AccessKey: "fixture-ak", SecretKey: "fixture-sk", Bucket: "fixture", UploadURL: "https://upload-z2.qiniup.com", DownloadURL: "https://storage.example.test", Directory: t.TempDir(), MaxCacheBytes: 32 << 20})
	require.NoError(t, err)
	uploads, fetches := 0, 0
	stored := false
	store.client = &http.Client{Transport: qiniuResultTransport(func(r *http.Request) (*http.Response, error) {
		status := 200
		if r.Method == "HEAD" {
			if !stored {
				status = 404
			}
		} else {
			uploads++
			require.Equal(t, "https://upload-z2.qiniup.com", r.URL.String())
			assert.Empty(t, r.Header.Get("Authorization"))
			require.NoError(t, r.ParseMultipartForm(1<<20))
			defer r.MultipartForm.RemoveAll()
			parts := strings.Split(r.FormValue("token"), ":")
			require.Len(t, parts, 3)
			policyBytes, err := base64.URLEncoding.DecodeString(parts[2])
			require.NoError(t, err)
			var policy map[string]any
			require.NoError(t, common.Unmarshal(policyBytes, &policy))
			assert.EqualValues(t, 1, policy["insertOnly"])
			assert.EqualValues(t, 1, policy["deleteAfterDays"])
			assert.Greater(t, policy["deadline"].(float64), float64(time.Now().Unix()))
			file, _, err := r.FormFile("file")
			require.NoError(t, err)
			body, err := io.ReadAll(file)
			file.Close()
			require.NoError(t, err)
			assert.Equal(t, data, body)
			stored = true
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	store.qiniuFetcher = &http.Client{Transport: qiniuResultTransport(func(r *http.Request) (*http.Response, error) {
		fetches++
		assert.Equal(t, "https://source.example.test/result", r.URL.String())
		assert.Equal(t, "Bearer upstream-only", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	headers := map[string]string{"Authorization": "Bearer upstream-only"}
	first, err := store.PutQiniuResult(context.Background(), "user1:resource:v1", "https://source.example.test/result", version, headers, nil)
	require.NoError(t, err)
	second, err := store.PutQiniuResult(context.Background(), "user1:resource:v1", "https://source.example.test/result", version, headers, nil)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, uploads)
	assert.Equal(t, 1, fetches)
	stored = false
	_, err = store.PutQiniuResult(context.Background(), "user1:resource:v2", "https://source.example.test/result", "sha256:"+strings.Repeat("0", 64), headers, nil)
	require.ErrorIs(t, err, ErrReferenceConflict)
	assert.Equal(t, 1, uploads)
	files, err := os.ReadDir(store.config.Directory)
	require.NoError(t, err)
	assert.Empty(t, files, "temporary result copy must be removed")
}
