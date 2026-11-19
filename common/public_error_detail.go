package common

import (
	"net"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var publicErrorSecretPattern = regexp.MustCompile(`(?i)(?:["']?(?:\b(?:upstream[_ -]?)?(?:api[_ -]?key(?:\s+(?:provided|supplied))?|key|access[_ -]?token|refresh[_ -]?token|session[_ -]?token|client[_ -]?secret|secret[_ -]?key|token|authorization|proxy-authorization|password|secret|credential|mj-api-secret)|密钥)["']?\s*[:=：]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|(?:(?:Bearer|Basic|Token|ApiKey)\s+)?[^\s,;，；]+)|\b(?:Bearer|Basic)\s+[a-z0-9._~+/=-]+|\bsk-[a-z0-9_-]+|\beyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+|(?s:-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----)`)
var publicErrorCookiePattern = regexp.MustCompile(`(?im)\b(?:set-cookie|cookie)\s*[:=][^\r\n]*`)
var publicErrorAuthorizationPattern = regexp.MustCompile(`(?im)\b(?:proxy-authorization|authorization)["']?\s*[:=]\s*(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\r\n;；]+)`)
var publicErrorInternalFieldPattern = regexp.MustCompile(`(?i)(?:["']?(?:\b(?:channel(?:[_ -]?(?:id|name|type))?|provider|supplier|plugin(?:[_ -]?(?:key|name|id))?|upstream(?:[_ -]?(?:model|request[_ -]?id|task[_ -]?id|url))?|model|group|host|ip|base[_ -]?url|endpoint)|(?:渠道|管道)(?:名称|名稱|编号|編號|名|ID)?|供[应應]商|上游地址|分[组組])["']?\s*[:=：]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;，；()（）]+)(?:\s*[（(][^()（）\r\n]*[)）])?`)
var publicErrorAddressPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>，；）)]+|\b(?:[a-z0-9-]+\.)+[a-z]{2,63}\b(?::\d+)?(?:/[^\s"'<>，；）)]*)?|\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?|\[[0-9a-f:.%]+\](?::\d+)?|\b(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+|::[0-9a-f:]+`)
var publicErrorParameterPathPattern = regexp.MustCompile(`(?i)^(?:parameters|generationconfig|request|image|video)\.(?:n|size|width|height|duration|seconds|quality|prompt|seed|max_tokens|maxoutputtokens|aspect_ratio|resolution|candidatecount)$`)
var publicErrorNamedModelPattern = regexp.MustCompile(`(?i)(?:\bchannel\s+|渠道\s*)\d+(?:\s*[（(][^()（）\r\n]*[)）])?|(?:\b(?:model|channel|provider|plugin|group)\s+(?:for\s+)?|(?:模型|渠道|分[组組])\s*)(?:"[^"\r\n]+"|'[^'\r\n]+'|[a-z0-9]+[-_][a-z0-9._/-]+)`)
var publicErrorRoutingPattern = regexp.MustCompile(`(?im)^\s*#\d+[,，]\s*|\b(?:error_code|error_type)\s*[:=]\s*[^\s,;，；]+|(?:Async Channel Retry Path|async_channel_retry_path)\s*:[^\r\n]*|(?:^|[\s;；])retry\s*[:=]\s*\d+\s*$`)
var publicErrorPrivatePathPattern = regexp.MustCompile(`(?i)\b[a-z]:[\\/][^\s"'<>，；]+|/(?:home|root|etc|var|tmp|app|usr|opt)/[^\s"'<>，；]+|data:[^\s,]+;base64,[a-z0-9+/=]+`)

// publicErrorDetail extracts diagnostics, never arbitrary response metadata.
// Malformed/oversized envelopes and HTML pages fail closed; their raw bodies
// may contain credentials or echoed requests that cannot be safely separated.
func publicErrorDetail(message string, public PublicError, sensitiveValues ...string) string {
	if len(message) > 64*1024 {
		return ""
	}
	message, _, _ = strings.Cut(message, "\nAsync Channel Retry Path:")
	// Recover the detail from our own string projection before parsing it again.
	// This is formatting only: the recovered text still goes through redaction.
	if _, detail, ok := strings.Cut(message, "；具体原因："); ok && strings.HasPrefix(message, "["+public.Code+"] ") {
		message = strings.TrimSuffix(detail, "；"+public.Hint)
	} else {
		message = strings.TrimSpace(strings.TrimPrefix(message, "["+public.Code+"]"))
		if status := publicErrorHTTPStatusPattern.FindStringIndex(message); status != nil && status[0] == 0 {
			message = message[status[1]:]
		}
		message = strings.Trim(message, " ,，")
		canonical := publicErrorCatalog[public.Code]
		if message == canonical.Message || message == canonical.Message+"；"+canonical.Hint {
			return ""
		}
	}
	parts, secrets := publicErrorDiagnosticParts(message, 0)
	secrets = append(secrets, sensitiveValues...)
	// Redact longer values first so overlapping secrets cannot leave a suffix.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	var details []string
	for _, part := range parts {
		part = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, part)
		for _, secret := range secrets {
			if secret != "" {
				part = strings.ReplaceAll(part, secret, "[已隐藏]")
			}
		}
		part = publicErrorCookiePattern.ReplaceAllString(part, "[已隐藏]")
		part = publicErrorAuthorizationPattern.ReplaceAllString(part, "[已隐藏]")
		part = publicErrorSecretPattern.ReplaceAllString(part, "[已隐藏]")
		part = publicErrorInternalFieldPattern.ReplaceAllString(part, "[已隐藏]")
		part = publicErrorAddressPattern.ReplaceAllStringFunc(part, func(address string) string {
			if publicErrorParameterPathPattern.MatchString(address) {
				return address
			}
			// A time such as 00:00:30 is not an IPv6 address.
			if !strings.ContainsAny(address, "/.[") && strings.Count(address, ":") >= 2 && net.ParseIP(address) == nil {
				return address
			}
			return "[地址已隐藏]"
		})
		part = publicErrorNamedModelPattern.ReplaceAllString(part, "[内部标识已隐藏]")
		part = publicErrorPrivatePathPattern.ReplaceAllString(part, "[已隐藏]")
		part = publicErrorRoutingPattern.ReplaceAllString(part, "")
		part = publicErrorHTTPStatusPattern.ReplaceAllString(part, "")
		part = strings.Trim(part, " \r\n\t,;，；")
		meaning := strings.ToLower(part)
		for _, placeholder := range []string{"[已隐藏]", "[地址已隐藏]", "[内部标识已隐藏]", "task failed", "generation failed"} {
			meaning = strings.ReplaceAll(meaning, placeholder, "")
		}
		if strings.Trim(meaning, " \r\n\t,;:，；：.") == "" || slices.Contains(details, part) {
			continue
		}
		details = append(details, part)
	}
	detail := strings.Join(details, "；")
	// Bound display size only after complete extraction/redaction.
	if runes := []rune(detail); len(runes) > 4000 {
		detail = string(runes[:4000]) + "…"
	}
	return detail
}

func publicErrorDiagnosticParts(value any, depth int) (parts, secrets []string) {
	if depth >= 8 {
		return nil, nil
	}
	switch value := value.(type) {
	case string:
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "<") || strings.Contains(strings.ToLower(value), "<!doctype html") || strings.Contains(strings.ToLower(value), "<html") {
			return nil, nil
		}
		start := strings.IndexByte(value, '{')
		if strings.HasPrefix(value, "[") && len(value) > 1 && strings.ContainsAny(value[1:2], "{[\" \n") {
			start = 0
		}
		if start >= 0 {
			var envelope any
			if UnmarshalJsonStr(value[start:], &envelope) == nil {
				parts, secrets = publicErrorDiagnosticParts(envelope, depth+1)
				if start == 0 || len(parts) > 0 {
					return parts, secrets
				}
			} else if start == 0 || strings.HasSuffix(strings.TrimSpace(strings.ToLower(value[:start])), "body:") {
				return nil, nil
			}
		}
		return []string{value}, secrets
	case map[string]any:
		for _, key := range []string{"message", "msg", "detail", "description", "error", "error_message", "reason", "fail_reason", "hint", "suggestion", "solution", "errors", "data"} {
			if nested, ok := value[key]; ok {
				// data is an envelope, never arbitrary output text.
				if _, text := nested.(string); key == "data" && text {
					continue
				}
				childParts, childSecrets := publicErrorDiagnosticParts(nested, depth+1)
				parts = append(parts, childParts...)
				secrets = append(secrets, childSecrets...)
			}
		}
		secrets = append(secrets, publicErrorSensitiveValues(value, depth)...)
	case []any:
		for _, item := range value[:min(len(value), 32)] {
			childParts, childSecrets := publicErrorDiagnosticParts(item, depth+1)
			parts = append(parts, childParts...)
			secrets = append(secrets, childSecrets...)
		}
	}
	return parts, secrets
}

// Internal identities can be echoed in message text while their labels live in
// metadata. Inspect that metadata only for redaction values, never for display.
func publicErrorSensitiveValues(value any, depth int) (secrets []string) {
	if depth >= 8 {
		return nil
	}
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if publicErrorSecretPattern.MatchString(key+"=value") || publicErrorInternalFieldPattern.MatchString(key+"=value") {
				if secret, ok := item.(string); ok && secret != "" {
					secrets = append(secrets, secret)
				}
			}
			secrets = append(secrets, publicErrorSensitiveValues(item, depth+1)...)
		}
	case []any:
		for _, item := range value[:min(len(value), 32)] {
			secrets = append(secrets, publicErrorSensitiveValues(item, depth+1)...)
		}
	}
	return secrets
}
