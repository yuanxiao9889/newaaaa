package common

import (
	"net/http"
	"regexp"
	"strings"
)

var midjourneyCredentialPattern = regexp.MustCompile(`(?i)(?:["']?(?:mj-api-secret|api[_-]?key|access[_-]?token|authorization|password)["']?\s*[:=]\s*["']?)(?:Bearer\s+)?[^\s"',;]+|\bBearer\s+[^\s"',;]+|\bsk-[a-z0-9_-]+`)

// MidjourneyPublicError preserves actionable provider diagnostics across MJ
// responses and user log views. Stored diagnostics are never rewritten.
func MidjourneyPublicError(code, reason string, status int, local bool) PublicError {
	public := ClassifyPublicError(code, reason, status, local)
	// Keep host failures and resource shortages in the shared public wording.
	if local || public.Code == "model_unavailable" {
		return public
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return public
	}
	text := strings.ToLower(reason)
	if strings.Contains(text, "提示词过长") || strings.Contains(text, "提示词长度") || strings.Contains(text, "prompt too long") || strings.Contains(text, "prompt is too long") {
		public.Code = "input_too_large"
		public.StatusCode = http.StatusRequestEntityTooLarge
		public.Retryable = false
	}
	// URLs can include signed credentials. Redact them as a whole before the
	// existing host/IP masker, retaining the surrounding diagnostic verbatim.
	reason = publicErrorSafetyURLPattern.ReplaceAllString(reason, "[URL REDACTED]")
	reason = midjourneyCredentialPattern.ReplaceAllString(reason, "[REDACTED]")
	reason = MaskSensitiveInfo(reason)
	if public.Code == "service_auth_error" || public.Code == "service_quota_exceeded" {
		// Include the diagnostic, but clarify that the administrator must fix
		// upstream credentials/credit rather than the user's own token/wallet.
		public.Message += "：" + reason
		return public
	}
	public.Message = reason
	public.Hint = ""
	return public
}
