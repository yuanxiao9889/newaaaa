package controller

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type bridgeHealthCheck struct {
	CheckedAt int64  `json:"checked_at"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

var bridgeCheckState struct {
	sync.Mutex
	last    *bridgeHealthCheck
	started time.Time
}

func GetBridgeManagement(c *gin.Context) {
	snapshot, err := model.LoadTransferPolicy()
	if err != nil {
		c.JSON(503, gin.H{"success": false, "message": "Bridge policy storage is unavailable"})
		return
	}
	bridgeCheckState.Lock()
	last := bridgeCheckState.last
	bridgeCheckState.Unlock()
	runtime := readBridgeRuntime(c.Request.Context(), false)
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"success": true, "data": gin.H{
		"version": snapshot.Version, "policy": snapshot.Policy,
		"fields": model.TransferPolicyFields(), "history": snapshot.History, "last_check": last, "runtime": runtime,
		"reference_storage":   service.ReferenceUploads.DirectStatus(),
		"client_observations": service.TransferObservations.Snapshot(),
		"supported": gin.H{"request_recovery": true, "download_routing": true,
			"direct_reference_upload": service.ReferenceUploads.DirectEnabled(),
			"download_tickets":        bridgeTicketsConfigured(), "bridge_storage": runtime.Available,
			"resumable_reference_uploads": service.ReferenceUploads != nil, "domestic_asset_retention": len(service.DomesticTaskKey()) == 32},
		"application": gin.H{"scope": "new_client_operations", "node_applied_version": nil,
			"client_applied_version": nil},
	}})
}

func UpdateBridgeManagement(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var request struct {
		BaseVersion int64                `json:"base_version"`
		Policy      model.TransferPolicy `json:"policy"`
		Reason      string               `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(400, gin.H{"success": false, "message": "Invalid bridge policy"})
		return
	}
	_, err := model.SaveTransferPolicy(request.BaseVersion, request.Policy, c.GetInt("id"), request.Reason)
	if errors.Is(err, model.ErrTransferPolicyConflict) {
		c.JSON(409, gin.H{"success": false, "message": "Bridge policy changed; reload it before saving"})
		return
	}
	if err != nil {
		c.JSON(400, gin.H{"success": false, "message": err.Error()})
		return
	}
	GetBridgeManagement(c)
}

func CheckBridgeConnection(c *gin.Context) {
	bridgeCheckState.Lock()
	if time.Since(bridgeCheckState.started) < 15*time.Second {
		bridgeCheckState.Unlock()
		c.Header("Retry-After", "15")
		c.JSON(429, gin.H{"success": false, "message": "Wait before checking the bridge again"})
		return
	}
	bridgeCheckState.started = time.Now()
	bridgeCheckState.Unlock()
	start := time.Now()
	check := &bridgeHealthCheck{CheckedAt: start.Unix()}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, model.TransferBridge+"/healthz", nil)
	if err == nil {
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			defer response.Body.Close()
			var status struct {
				Service string `json:"service"`
				Status  string `json:"status"`
			}
			err = common.DecodeJson(io.LimitReader(response.Body, 4096), &status)
			check.OK = err == nil && response.StatusCode == 200 &&
				status.Service == "opics-bridge" && status.Status == "tls_proxy"
		}
	}
	if !check.OK {
		// Do not put a potentially signed URL, raw network error or credentials in
		// administrator-visible health evidence.
		check.Error = "Bridge HTTPS health check failed; this check covers relay to bridge only"
	}
	check.LatencyMS = time.Since(start).Milliseconds()
	bridgeCheckState.Lock()
	bridgeCheckState.last = check
	bridgeCheckState.Unlock()
	GetBridgeManagement(c)
}

func bridgeTicketsConfigured() bool {
	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("OPICS_BRIDGE_TICKET_SECRET")))
	return err == nil && len(key) == 32
}

func TransferCapabilities(c *gin.Context) {
	snapshot, err := model.LoadTransferPolicy()
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "transfer_policy_unavailable"}})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{
		"protocol_version": 1, "minimum_client_protocol": 1, "relay_id": "oopii",
		"admission_user_id": c.GetInt("id"),
		"policy_version":    snapshot.Version, "expires_at": time.Now().Add(5 * time.Minute).Unix(),
		"request_recovery": true, "download_tickets": bridgeTicketsConfigured(), "policy": snapshot.Policy,
		"recovery_limits": gin.H{
			"response_max_bytes":           model.TransferResponseLimit,
			"response_retention_seconds":   int64(model.TransferResponseRetention.Seconds()),
			"complete_stream_recovery":     false,
			"automatic_unknown_redispatch": false,
			"domestic_asset_pins":          len(service.DomesticTaskKey()) == 32,
		},
		"relay_direct_upload":         service.ReferenceUploads.DirectEnabled(),
		"resumable_reference_uploads": service.ReferenceUploads != nil,
	})
}

func GetTransferRequest(c *gin.Context) {
	key := c.Param("key")
	if !model.ValidTransferRequestKey(key) {
		c.JSON(400, gin.H{"error": gin.H{"code": "transfer_invalid_request_id"}})
		return
	}
	record, err := model.FindTransferRequest(c.GetInt("id"), key)
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "transfer_registration_unavailable"}})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	if record == nil {
		// This is an authenticated primary-database answer, never a 404 inferred
		// from a legacy server, replica, unsupported route or network error.
		c.JSON(200, gin.H{"protocol_version": 1, "state": "not_registered", "request_id": key})
		return
	}
	state := record.State
	if state == "input_required" {
		state = "failed" // Recoverable preprocessing error; payload retains its explicit marker.
	}
	if state == "running" && record.UpdatedAt < time.Now().Add(-15*time.Minute).Unix() {
		state = "unknown"
	}
	if record.FinishedAt > 0 && record.FinishedAt < time.Now().Add(-model.TransferResponseRetention).Unix() &&
		(state == "succeeded" || state == "failed") {
		state = "expired"
	}
	reply := gin.H{"protocol_version": 1, "state": state, "request_id": key, "execution_id": record.ExecutionID}
	if (state == "succeeded" || state == "failed") && len(record.ResponseBody) > 0 {
		var headers map[string]string
		_ = common.UnmarshalJsonStr(record.ResponseHeaders, &headers)
		reply["response_status"] = record.ResponseStatus
		reply["response_headers"] = headers
		reply["response_body_base64"] = record.ResponseBody
	}
	c.JSON(200, reply)
}
