package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Called after TokenAuth and before reference materialization, quota reservation
// or channel selection. It also surrounds asynchronous-task acceptance.
func TransferRequestGuard(c *gin.Context) {
	key := c.GetHeader("Idempotency-Key")
	if !isTransferGeneration(c.Request) {
		c.Next()
		return
	}
	if key == "" && (!strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") ||
		(c.Request.URL.Path != "/v1/videos" && c.Request.URL.Path != "/v1/video/generations" && !strings.HasPrefix(c.Request.URL.Path, "/v1/tasks/"))) {
		c.Next()
		return
	}
	if key != "" && !model.ValidTransferRequestKey(key) {
		transferRequestError(c, 400, "transfer_invalid_request_id", "Invalid request ID")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (64<<20)+1))
	_ = c.Request.Body.Close()
	if err != nil || len(body) > 64<<20 {
		transferRequestError(c, 413, "transfer_request_too_large", "Request cannot be registered safely")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	domestic := service.DomesticReferenceFiles(body)
	if key == "" {
		if len(domestic) == 0 {
			c.Next()
			return
		}
		key = uuid.NewString()
	}
	digest, err := transferRequestDigest(c.Request, body)
	if err != nil {
		transferRequestError(c, 400, "transfer_invalid_request", "Request could not be normalized")
		return
	}
	record, owner, err := model.ClaimTransferRequest(c.GetInt("id"), key, digest)
	if errors.Is(err, model.ErrTransferRequestConflict) {
		transferRequestError(c, 409, "transfer_request_conflict", err.Error())
		return
	}
	if err != nil {
		transferRequestError(c, 503, "transfer_registration_unavailable", "Request registration is unavailable; do not resubmit with a new ID")
		return
	}
	c.Header("X-Transfer-Execution-Id", record.ExecutionID)
	c.Header("Cache-Control", "no-store")
	if !owner && record.State == "input_required" && c.GetHeader("X-New-Api-Reference-Recovery") == "1" {
		owner, err = model.ResumeReferenceSubmission(record)
		if err != nil || !owner {
			transferRequestError(c, 409, "transfer_submission_unknown", "Reference recovery is already in progress; query the original request")
			return
		}
	}
	if !owner {
		if record.FinishedAt > time.Now().Add(-model.TransferResponseRetention).Unix() &&
			(record.State == "succeeded" || record.State == "failed" || record.State == "input_required") &&
			record.ResponseStatus > 0 && len(record.ResponseBody) > 0 {
			var headers map[string]string
			_ = common.UnmarshalJsonStr(record.ResponseHeaders, &headers)
			for name, value := range headers {
				c.Header(name, value)
			}
			c.Header("X-Transfer-Replayed", "true")
			c.Data(record.ResponseStatus, headers["Content-Type"], record.ResponseBody)
			c.Abort()
			return
		}
		transferRequestError(c, 409, "transfer_submission_unknown", "Original submission is already registered; query its state instead of generating again")
		return
	}

	// Billing and task creation use the execution identity. The original HTTP
	// tracing header remains unique to this transport attempt.
	c.Set("transfer_execution_id", record.ExecutionID)
	c.Set(common.RequestIdKey, record.ExecutionID)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), common.RequestIdKey, record.ExecutionID))
	writer := &transferResponseWriter{ResponseWriter: c.Writer}
	c.Writer = writer
	completed := false
	defer func() {
		state := "unknown"
		status := writer.Status()
		if completed && status >= 200 && status < 500 && status != 408 {
			state = "failed"
			if status < 300 {
				state = "succeeded"
			}
		}
		if completed && status == http.StatusGone && c.GetBool("reference_recovery_required") {
			state = "input_required"
		}
		headers, _ := common.Marshal(map[string]string{"Content-Type": writer.Header().Get("Content-Type")})
		response := writer.body.Bytes()
		if state == "unknown" || writer.streamed || writer.overflow {
			response = nil
		}
		if err := model.FinishTransferRequest(record, state, status, string(headers), response); err != nil {
			common.SysError("transfer request completion could not be persisted: " + record.ExecutionID)
		}
	}()
	if err := service.HoldDomesticReferences(c.Request.Context(), record.ExecutionID, domestic); err != nil {
		completed = true
		if errors.Is(err, service.ErrDomesticReferenceMissing) {
			c.Set("reference_recovery_required", true)
			c.AbortWithStatusJSON(410, gin.H{"error": gin.H{"code": "reference_expired", "message": "Domestic reference material must be restored before generation", "reference_recovery": true, "reference_service": "domestic"}})
		} else {
			transferRequestError(c, 503, "domestic_retention_unavailable", "Domestic reference validation is unavailable; query the original request")
		}
		return
	}
	c.Next()
	completed = true
}

func TransferObservationRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(120, 60, "transfer-download-observation")
}

type transferResponseWriter struct {
	gin.ResponseWriter
	body     bytes.Buffer
	overflow bool
	streamed bool
}

func (w *transferResponseWriter) Write(data []byte) (int, error) {
	w.streamed = w.streamed || strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
	if !w.streamed && !w.overflow {
		if w.body.Len()+len(data) <= model.TransferResponseLimit {
			_, _ = w.body.Write(data)
		} else {
			w.overflow = true
			w.body.Reset()
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *transferResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func transferRequestError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "type": code, "message": message}})
}

func isTransferGeneration(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/mj/submit/upload-discord-images" {
		return false // Reference upload, not a generation operation.
	}
	switch r.URL.Path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/messages",
		"/v1/images/generations", "/v1/images/edits", "/v1/videos", "/v1/video/generations":
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/v1/tasks/") {
		return !strings.Contains(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"), "/")
	}
	return strings.HasPrefix(r.URL.Path, "/mj/submit/") ||
		strings.HasPrefix(r.URL.Path, "/v1beta/models/") &&
			(strings.HasSuffix(r.URL.Path, ":generateContent") || strings.HasSuffix(r.URL.Path, ":streamGenerateContent"))
}

func transferRequestDigest(r *http.Request, body []byte) (string, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	canonical := body
	if mediaType == "application/json" {
		canonical, err = canonicalTransferJSON(body)
	} else if mediaType == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		parts := make([]string, 0)
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return "", nextErr
			}
			hash := sha256.New()
			if _, err = io.Copy(hash, part); err != nil {
				return "", err
			}
			parts = append(parts, part.FormName()+"\x00"+part.FileName()+"\x00"+
				part.Header.Get("Content-Type")+"\x00"+hex.EncodeToString(hash.Sum(nil)))
		}
		// Sort fields, while retaining order for repeated material fields.
		slices.SortStableFunc(parts, func(a, b string) int {
			aName, _, _ := strings.Cut(a, "\x00")
			bName, _, _ := strings.Cut(b, "\x00")
			return strings.Compare(aName, bName)
		})
		canonical, err = common.Marshal(parts)
	}
	if err != nil {
		return "", err
	}
	query := r.URL.Query()
	query.Del("key") // Gemini transport credentials are not business input.
	hash := sha256.New()
	_, _ = io.WriteString(hash, "transfer-digest-v1\x00"+r.Method+"\x00"+r.URL.Path+"\x00"+query.Encode()+"\x00"+mediaType+"\x00")
	_, _ = hash.Write(canonical)
	if retryTask := r.Header.Get(service.ImageRetryTaskHeader); retryTask != "" {
		// Preserve existing digests; retry requests must also bind their source
		// task so the same idempotency key cannot silently reuse another input.
		_, _ = io.WriteString(hash, "\x00image-retry-task\x00"+retryTask)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalTransferJSON(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty JSON")
	}
	switch raw[0] {
	case '{':
		var values map[string]common.RawMessage
		if err := common.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		for key, value := range values {
			encoded, err := canonicalTransferJSON(value)
			if err != nil {
				return nil, err
			}
			values[key] = encoded
		}
		return common.Marshal(values)
	case '[':
		var values []common.RawMessage
		if err := common.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		for i, value := range values {
			encoded, err := canonicalTransferJSON(value)
			if err != nil {
				return nil, err
			}
			values[i] = encoded
		}
		return common.Marshal(values)
	case '"':
		var value string
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return common.Marshal(value)
	default:
		var validated common.RawMessage
		if err := common.Unmarshal(raw, &validated); err != nil {
			return nil, err
		}
		return validated, nil // Preserve integer precision and explicit 0/false.
	}
}
