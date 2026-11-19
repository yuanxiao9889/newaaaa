package common

import (
	"net/http"
	"strings"
)

// MidjourneyPublicError preserves actionable provider diagnostics across MJ
// responses and user log views. Stored diagnostics are never rewritten.
func MidjourneyPublicError(code, reason string, status int, local bool, sensitiveValues ...string) PublicError {
	public := ClassifyPublicError(code, reason, status, local, sensitiveValues...)
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
	if public.Code == "service_auth_error" || public.Code == "service_quota_exceeded" {
		// Include the diagnostic, but clarify that the administrator must fix
		// upstream credentials/credit rather than the user's own token/wallet.
		return public
	}
	reason = publicErrorDetail(reason, public, sensitiveValues...)
	if reason == "" {
		return public
	}
	public.Message = reason
	public.Hint = ""
	return public
}
