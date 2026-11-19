package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError(common.LogText("failed to close response body: %s", err.Error()))
	}
}

// ShouldCopyUpstreamHeader keeps protocol headers public, while upstream
// diagnostics require a dashboard-authenticated administrator. API tokens do
// not inherit their owner's dashboard privileges.
func ShouldCopyUpstreamHeader(c *gin.Context, k string, v []string) bool {
	if len(v) == 0 {
		return false
	}
	if strings.EqualFold(k, common.RequestIdKey) {
		if c != nil {
			c.Set(common.UpstreamRequestIdKey, v[0])
		}
		return false
	}
	switch strings.ToLower(k) {
	case "content-length", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "set-cookie", "set-cookie2",
		"authorization", "cookie", "auth-version", "cache-control", "expires", "pragma":
		return false
	case "content-type", "content-disposition", "content-encoding", "content-range", "accept-ranges",
		"retry-after", "x-should-retry", "ratelimit", "ratelimit-policy",
		"ratelimit-limit", "ratelimit-remaining", "ratelimit-reset",
		"x-ratelimit-limit", "x-ratelimit-remaining", "x-ratelimit-reset",
		"x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests",
		"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens",
		"x-reasoning-included", "x-codex-turn-state":
		return true
	default:
		if c != nil && c.GetInt("token_id") == 0 && c.GetInt("role") >= common.RoleAdminUser {
			c.Header("Cache-Control", "private, no-store")
			return true
		}
		return false
	}
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			if !ShouldCopyUpstreamHeader(c, k, v) {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, common.LogText("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
