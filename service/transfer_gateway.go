package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type BridgeGatewayConfig struct {
	Capacity               *BridgeCapacityConfig `json:"capacity,omitempty"`
	Revision               int64                 `json:"revision"`
	ImageCacheEnabled      bool                  `json:"image_cache_enabled"`
	ImageCacheMaxBytes     int64                 `json:"image_cache_max_bytes"`
	ImageCacheMaxFileBytes int64                 `json:"image_cache_max_file_bytes"`
	MaxResumeAttempts      int                   `json:"max_resume_attempts"`
}

type BridgeGatewayStatus struct {
	Capacity             *BridgeCapacityStatus `json:"capacity,omitempty"`
	Version              int                   `json:"version"`
	AppliedRevision      int64                 `json:"applied_revision"`
	Config               BridgeGatewayConfig   `json:"config"`
	ImageCacheTTLSeconds int64                 `json:"image_cache_ttl_seconds"`
	VideoDiskCache       bool                  `json:"video_disk_cache"`
	Cache                struct {
		Entries       int64 `json:"entries"`
		Bytes         int64 `json:"bytes"`
		ReservedBytes int64 `json:"reserved_bytes"`
		ActiveReaders int64 `json:"active_readers"`
		OrphanBytes   int64 `json:"orphan_bytes"`
	} `json:"cache"`
	ActiveDownloads int64 `json:"active_downloads"`
	Metrics         struct {
		Requests             int64 `json:"requests"`
		UpstreamRequests     int64 `json:"upstream_requests"`
		RelayRequests        int64 `json:"relay_requests"`
		UnknownRouteRequests int64 `json:"unknown_route_requests"`
		UnknownBytes         int64 `json:"unknown_bytes"`
		BytesSent            int64 `json:"bytes_sent"`
		UpstreamBytes        int64 `json:"upstream_bytes"`
		RelayBytes           int64 `json:"relay_bytes"`
		CacheBytes           int64 `json:"cache_bytes"`
		CacheHits            int64 `json:"cache_hits"`
		CacheMisses          int64 `json:"cache_misses"`
		CacheWrites          int64 `json:"cache_writes"`
		CacheErrors          int64 `json:"cache_errors"`
		ResumeAttempts       int64 `json:"resume_attempts"`
		ResumeSuccesses      int64 `json:"resume_successes"`
		ResumeRejected       int64 `json:"resume_rejected"`
		StreamFailures       int64 `json:"stream_failures"`
		Cancellations        int64 `json:"cancellations"`
	} `json:"metrics"`
}

var ErrBridgeGatewayConflict = errors.New("bridge configuration changed; refresh before applying")
var ErrBridgeGatewayUnavailable = errors.New("bridge status is unavailable; runtime support is unknown")

func ValidateBridgeGatewayConfig(config BridgeGatewayConfig) error {
	if (config.Capacity != nil && !config.Capacity.valid()) || config.Revision < 0 || config.ImageCacheMaxBytes < 64<<20 || config.ImageCacheMaxBytes > 4<<30 ||
		config.ImageCacheMaxFileBytes < 1<<20 || config.ImageCacheMaxFileBytes > 64<<20 ||
		config.ImageCacheMaxFileBytes > config.ImageCacheMaxBytes || config.MaxResumeAttempts < 0 || config.MaxResumeAttempts > 3 {
		return errors.New("invalid bridge capacity, cache or recovery limits")
	}
	return nil
}

// Bridge management never accepts a caller-supplied target, forwards browser
// credentials or follows redirects. Only aggregate typed fields are returned.
func bridgeGatewayRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("OPICS_BRIDGE_TICKET_SECRET")))
	if err != nil || len(key) != 32 {
		return nil, ErrBridgeGatewayUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("opics-bridge-management-v1"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{Timeout: 4 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, method, model.TransferBridge+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrBridgeGatewayUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, ErrBridgeGatewayUnavailable
	}
	return response, nil
}

func validBridgeGatewayStatus(status BridgeGatewayStatus) bool {
	return (status.Version == 2 || status.Version == 3) &&
		(status.Version != 3 || (status.Config.Capacity != nil && status.Capacity != nil)) &&
		(status.Version != 2 || (status.Config.Capacity == nil && status.Capacity == nil)) &&
		status.ImageCacheTTLSeconds == 14400 && !status.VideoDiskCache &&
		status.AppliedRevision == status.Config.Revision && ValidateBridgeGatewayConfig(status.Config) == nil
}

func ReadBridgeGatewayStatus(ctx context.Context) (*BridgeGatewayStatus, error) {
	response, err := bridgeGatewayRequest(ctx, http.MethodGet, "/internal/transfer/status", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrBridgeGatewayUnavailable
	}
	var status BridgeGatewayStatus
	if common.DecodeJson(io.LimitReader(response.Body, 64<<10), &status) != nil || !validBridgeGatewayStatus(status) {
		return nil, ErrBridgeGatewayUnavailable
	}
	return &status, nil
}

func ApplyBridgeGatewayConfig(ctx context.Context, config BridgeGatewayConfig) error {
	if config.Revision < 1 {
		return errors.New("bridge update revision must be positive")
	}
	if err := ValidateBridgeGatewayConfig(config); err != nil {
		return err
	}
	body, err := common.Marshal(config)
	if err != nil {
		return err
	}
	response, err := bridgeGatewayRequest(ctx, http.MethodPost, "/internal/transfer/config", body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return ErrBridgeGatewayConflict
	}
	if response.StatusCode != http.StatusOK {
		return ErrBridgeGatewayUnavailable
	}
	var applied BridgeGatewayStatus
	if common.DecodeJson(io.LimitReader(response.Body, 64<<10), &applied) != nil ||
		!validBridgeGatewayStatus(applied) || !reflect.DeepEqual(applied.Config, config) {
		return ErrBridgeGatewayUnavailable
	}

	return nil
}
