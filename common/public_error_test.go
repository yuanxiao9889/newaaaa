package common

import (
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPublicError(t *testing.T) {
	tests := []struct {
		name, code, message, wantCode, wantMessage, wantHint string
		status, wantStatus                                   int
		local, retryable                                     bool
	}{
		{name: "production unsafe image wording", message: "The generated images appear to be unsafe. Try modifying the prompts or the seeds.", wantCode: "content_policy_violation", wantHint: "安全审核提示", wantStatus: 400},
		{name: "production unsafe prompt wording", message: "The provided prompt is considered unsafe and it cannot be used to generate content.", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production prompt or image unsafe", message: "prompt or image unsafe", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production nested unsafe image", message: `poll failed: 451 {"error_code":"image_unsafe","message":"The generated images appear to be unsafe. Try modifying the prompts or the seeds."}`, status: 400, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "image unsafe code", code: "image_unsafe", message: "Task failed", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "prompt unsafe code", code: "prompt_unsafe", message: "Task failed", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "structured safety reason", code: "SAFETY", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "nsfw code", code: "NSFW", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "generated image review code", code: "generated_image_review_failed", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production content safety rejection", message: "The request was rejected by the content safety policy. Please revise the prompt or reference images.", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production prohibited use policy", message: "The image was filtered out because it violated Google's [Generative AI Prohibited Use policy](https://private.example/policy). You will not be charged for blocked images.", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "dotted policy code in text", message: "content.policy_violation: 内容不符合平台规范，请修改后重试", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production Chinese content review", message: "生成图片未通过内容审核，请调整提示词或参考图片后重试", wantCode: "content_policy_violation", wantStatus: 400},
		{name: "local generic failure retains safety reason", code: "generation_failed", message: "prompt is considered unsafe", local: true, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "bare safe is not definite rejection", code: "safe", message: "Task failed", wantCode: "content_safety_unknown", wantHint: "尚不能确认内容被拦截", wantStatus: 502},
		{name: "bare safety message", message: "Error: safety", wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "bare safe message", message: "Error: safe", wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "bare safe forbidden", code: "safe", message: "Task failed", status: 403, wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "generic request safety message", code: "invalid_request", message: "safety check failed", status: 400, local: true, wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "explicit permission error takes precedence", code: "permission_denied", message: "safety service access failed", status: 403, wantCode: "service_access_error", wantStatus: 502},
		{name: "ambiguous moderation failure", message: "safety check failed", wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "production ambiguous provider explanation", message: "image generation failed or was rejected by content moderation", wantCode: "content_safety_unknown", wantStatus: 502},
		{name: "safety service timeout is not rejection", message: "safety service timed out", wantCode: "request_timeout", wantStatus: 504, retryable: true},
		{name: "safety service down is not rejection", message: "moderation service unavailable", status: 503, wantCode: "service_unavailable", wantStatus: 503, retryable: true},
		{name: "safety passed", message: "safety check passed; Task failed", wantCode: "generation_failed", wantStatus: 502},
		{name: "Chinese review passed", message: "安全审核已通过，任务失败", wantCode: "generation_failed", wantStatus: 502},
		{name: "no unsafe content", message: "no unsafe content detected; Task failed", wantCode: "generation_failed", wantStatus: 502},
		{name: "content is safe", message: "content is safe; Task failed", wantCode: "generation_failed", wantStatus: 502},
		{name: "safety URL is not evidence", message: "Task failed: https://safety.example/unsafe-image", wantCode: "generation_failed", wantStatus: 502},
		{name: "safe word fragment is not evidence", message: "safeguard operation failed", wantCode: "generation_failed", wantStatus: 502},
		{name: "safe retry instruction is not evidence", message: "Task failed; safe retry is permitted", wantCode: "generation_failed", wantStatus: 502},
		{name: "safety in echoed prompt is not evidence", message: `{"error":{"message":"Task failed"},"prompt":"unsafe image safety NSFW","metadata":{"code":"SAFETY"}}`, wantCode: "generation_failed", wantStatus: 502},
		{name: "missing model", code: "invalid_request", message: "field model is required", local: true, wantCode: "missing_model", wantMessage: "缺少模型 ID（model）", wantStatus: 400},
		{name: "video missing model", code: "missing_model", message: "model field is required", local: true, wantCode: "missing_model", wantStatus: 400},
		{name: "missing prompt", code: "invalid_parameter", message: "prompt is required for image generation", local: true, wantCode: "missing_prompt", wantMessage: "缺少提示词（prompt）", wantStatus: 400},
		{name: "missing messages", message: "field messages is required", wantCode: "missing_input", wantStatus: 400},
		{name: "empty documents", message: "documents is empty", wantCode: "missing_input", wantStatus: 400},
		{name: "nested missing input", message: `{"error":{"code":"invalid_request_error","message":"input is required"}}`, wantCode: "missing_input", wantStatus: 400},
		{name: "missing field in prompt is not evidence", message: `{"error":{"message":"Task failed"},"prompt":"model is required"}`, wantCode: "generation_failed", wantStatus: 502},
		{name: "image count", code: "invalid_request", message: "invalid image parameters: parameters.n must be an integer between 1 and 128", local: true, wantCode: "invalid_image_count", wantMessage: "图片生成数量（n）无效", wantStatus: 400},
		{name: "async image count", message: "async image tasks only support n=1", wantCode: "async_image_count_unsupported", wantHint: "设置为 1", wantStatus: 400},
		{name: "async candidate count", message: "async gemini image tasks only support candidateCount=1", wantCode: "async_image_count_unsupported", wantStatus: 400},
		{name: "image size separator", code: "invalid_request", message: "size an unexpected error occurred in the parameter, please use 'x' instead of the multiplication sign '×'", local: true, wantCode: "invalid_size_separator", wantMessage: "尺寸格式错误：使用了乘号 ×", wantStatus: 400},
		{name: "image size is not corrupt image", message: "invalid image size for private-model", wantCode: "invalid_size", wantStatus: 400},
		{name: "video resolution", code: "invalid_resolution", message: "resolution and resolution_name must match", local: true, wantCode: "invalid_size", wantStatus: 400},
		{name: "video seconds", code: "invalid_seconds", message: "seconds must be an integer", local: true, wantCode: "invalid_duration", wantMessage: "视频时长参数无效", wantStatus: 400},
		{name: "video duration mismatch", code: "invalid_duration", message: "duration and seconds must match when both are provided", local: true, wantCode: "invalid_duration", wantHint: "数值必须一致", wantStatus: 400},
		{name: "output token limit", code: "invalid_request", message: "maxOutputTokens is invalid", local: true, wantCode: "invalid_token_limit", wantMessage: "输出长度限制参数无效", wantStatus: 400},
		{name: "malformed client JSON", code: "invalid_request", message: "invalid character 'x' looking for beginning of value", local: true, wantCode: "invalid_request_body", wantStatus: 400},
		{name: "wrong client field type", code: "invalid_request", message: "json: cannot unmarshal string into Go struct field ImageRequest.n of type int", local: true, wantCode: "invalid_request_body", wantStatus: 400},
		{name: "invalid provider JSON is not client fault", code: "bad_response_body", message: "invalid character 'x' looking for beginning of value", status: 502, local: true, wantCode: "generation_failed", wantStatus: 502},
		{name: "full task queue", code: "invalid_request", message: "async image task queue is full", status: 429, local: true, wantCode: "request_queue_full", wantMessage: "异步任务队列已满，暂时无法接收新任务", wantStatus: 429, retryable: true},
		{name: "service connection", code: "do_request_failed", message: "dial tcp secret.example:443: connection refused", status: 500, local: true, wantCode: "service_connection_failed", wantMessage: "暂时无法连接生成服务", wantStatus: 502, retryable: true},
		{name: "connection timeout stays specific", code: "do_request_failed", message: "context deadline exceeded", local: true, wantCode: "request_timeout", wantStatus: 504, retryable: true},
		{name: "no generated image", code: "bad_response_body", message: "image generation failed: Gemini returned no image result", wantCode: "generation_result_empty", wantMessage: "生成服务未返回有效结果", wantStatus: 502},
		{name: "production invalid image", code: "invalid_image_input", message: "The image request is invalid. Check the input and try again.", status: 400, wantCode: "invalid_image_input", wantMessage: "输入图片无效或无法解码", wantHint: "PNG、JPEG 或 WebP", wantStatus: 400},
		{name: "historical image task without structured code", message: "The image request is invalid. Check the input and try again.", wantCode: "invalid_image_input", wantStatus: 400},
		{name: "image decoding", message: "could not decode image", wantCode: "invalid_image_input", wantStatus: 400},
		{name: "image link", message: "Failed to download image at https://secret.example/image?key=private", wantCode: "image_download_failed", wantHint: "过期", wantStatus: 400},
		{name: "transparent background", code: "invalid_value", message: "Transparent background is not supported for internal-real-model. Supported values are opaque and auto.", status: 400, wantCode: "unsupported_background", wantHint: "background 改为 opaque 或 auto", wantStatus: 400},
		{name: "invalid parameter", code: "invalid_parameter", status: 400, wantCode: "invalid_parameter", wantStatus: 400},
		{name: "provider parameter alias", code: "InvalidParameter", status: 400, wantCode: "invalid_parameter", wantStatus: 400},
		{name: "provider moderation alias", code: "DataInspectionFailed", status: 400, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "provider image alias", code: "InvalidImage", status: 400, wantCode: "invalid_image_input", wantStatus: 400},
		{name: "unsupported parameter", code: "unsupported_parameter", status: 400, wantCode: "unsupported_parameter", wantStatus: 400},
		{name: "policy status", status: 451, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "production policy overrides generic error", code: "generation_failed", message: "图片审核失败，请重试", status: 502, wantCode: "content_policy_violation", wantHint: "提示词或参考图片", wantStatus: 400},
		{name: "policy overrides provider access status", code: "content_policy_violation", status: 403, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "size", status: 413, wantCode: "input_too_large", wantStatus: 413},
		{name: "rate", status: 429, wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "throttled", message: "throttled", status: 502, wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "timeout", status: 504, wantCode: "request_timeout", wantStatus: 504, retryable: true},
		{name: "deadline", message: "deadline exceeded", status: 502, wantCode: "request_timeout", wantStatus: 504, retryable: true},
		{name: "unsafe prompt", message: "unsafe prompt", status: 502, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "unsafe image", code: "unsafe_image", status: 502, wantCode: "content_policy_violation", wantStatus: 400},
		{name: "uncertain submission must not resubmit", code: "submission_unknown", status: 502, wantCode: "result_unknown", wantHint: "不要重复提交", wantStatus: 502},
		{name: "async wait timeout is not failure", code: "task_timeout", message: "task internal-task still running", status: 504, local: true, wantCode: "task_timeout", wantMessage: "任务仍在处理中，等待结果超时", wantHint: "不要重复提交", wantStatus: 504, retryable: true},
		{name: "provider task timeout does not imply still running", code: "task_timeout", status: 502, wantCode: "request_timeout", wantMessage: "请求处理超时，暂未取得结果", wantStatus: 504, retryable: true},
		{name: "nested provider task timeout is not local wait", code: "bad_response_status_code", message: `{"error":{"code":"task_timeout"}}`, status: 502, local: true, wantCode: "request_timeout", wantStatus: 504, retryable: true},
		{name: "model unavailable", code: "model_not_found", status: 404, local: true, wantCode: "model_unavailable", wantStatus: 503, retryable: true},
		{name: "production Chinese unavailable channel", message: "当前分组无可用渠道", status: 500, wantCode: "model_unavailable", wantMessage: "status_code=500, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "production English unavailable channel", message: "No available channel", status: 500, wantCode: "model_unavailable", wantMessage: "status_code=500, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "production model temporarily unavailable", message: "Model gpt-image-2.5-sunburst is currently unavailable", status: 503, wantCode: "model_unavailable", wantMessage: "status_code=503, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "model does not exist", message: "The model 'private-model' does not exist", status: 404, wantCode: "model_unavailable", wantStatus: 503, retryable: true},
		{name: "Chinese model does not exist", message: "模型 private-model 不存在", status: 404, wantCode: "model_unavailable", wantStatus: 503, retryable: true},
		{name: "Chinese model unavailable", message: "模型暂时不可用", status: 500, wantCode: "model_unavailable", wantStatus: 503, retryable: true},
		{name: "resource exhausted with rate status", message: "No available resources", status: 429, wantCode: "model_unavailable", wantMessage: "status_code=429, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "unsupported model code", code: "unsupported_model", status: 400, wantCode: "model_unavailable", wantStatus: 503, retryable: true},
		{name: "prompt is not availability evidence", message: `{"error":{"message":"Task failed"},"prompt":"No available channel; model does not exist"}`, status: 500, wantCode: "generation_failed", wantMessage: "status_code=500, 生成失败，服务未提供可公开确认的具体原因", wantStatus: 502},
		{name: "model URL is not availability evidence", message: "Task failed https://private.invalid/模型不存在", status: 500, wantCode: "generation_failed", wantStatus: 502},
		{name: "model capability is not availability", message: "Model private-model does not support audio", status: 400, wantCode: "generation_failed", wantStatus: 502},
		{name: "service down", status: 503, wantCode: "service_unavailable", wantStatus: 503, retryable: true},
		{name: "provider auth", code: "invalid_token", message: "API key invalid", status: 401, wantCode: "service_auth_error", wantHint: "无需更换您的令牌", wantStatus: 502},
		{name: "provider access", code: "access_denied", status: 403, wantCode: "service_access_error", wantStatus: 502},
		{name: "provider quota is not user quota", code: "insufficient_user_quota", message: "upstream insufficient balance", status: 429, wantCode: "service_quota_exceeded", wantStatus: 503},
		{name: "provider payment is not user balance", status: 402, wantCode: "service_quota_exceeded", wantStatus: 503},
		{name: "local token quota", code: "insufficient_token_quota", local: true, wantCode: "insufficient_token_quota", wantMessage: "令牌配额不足，无法支付本次请求", wantStatus: 402},
		{name: "local token quota reservation", code: "pre_consume_token_quota_failed", message: "token quota is not enough, token remain quota: 1, need quota: 2", local: true, wantCode: "insufficient_token_quota", wantStatus: 402},
		{name: "local token quota Chinese reservation", code: "pre_consume_token_quota_failed", message: "令牌额度不足", local: true, wantCode: "insufficient_token_quota", wantStatus: 402},
		{name: "local token reservation storage error is not quota", code: "pre_consume_token_quota_failed", message: "database unavailable", status: 403, local: true, wantCode: "billing_failed", wantStatus: 500},
		{name: "local wallet", code: "insufficient_user_quota", status: 403, local: true, wantCode: "insufficient_user_quota", wantMessage: "账户余额不足，无法支付本次请求", wantHint: "充值后重试，无需更换 API Key", wantStatus: 402},
		{name: "local token invalid", code: "invalid_token", local: true, wantCode: "invalid_token", wantStatus: 401},
		{name: "local token expired", code: "token_expired", local: true, wantCode: "token_expired", wantStatus: 401},
		{name: "local permission", code: "access_denied", local: true, wantCode: "access_denied", wantStatus: 403},
		{name: "local task ownership", code: "task_not_exist", local: true, wantCode: "task_not_found", wantStatus: 404},
		{name: "local task expired", code: "task_expired", local: true, wantCode: "task_expired", wantStatus: 410},
		{name: "local task pending", code: "task_not_ready", local: true, wantCode: "task_not_ready", wantStatus: 409, retryable: true},
		{name: "local empty code does not trust auth text", message: "Invalid token", status: 401, local: true, wantCode: "service_auth_error", wantStatus: 502},
		{name: "local wrapper does not trust nested user quota", code: "bad_response_status_code", message: `{"error":{"code":"insufficient_user_quota","message":"quota exceeded"}}`, local: true, wantCode: "service_quota_exceeded", wantStatus: 503},
		{name: "JSON code and escaped message", message: `{"error":{"code":"invalid_image_input","message":"invalid \u0069mage"},"status":400}`, wantCode: "invalid_image_input", wantStatus: 400},
		{name: "JSON numeric HTTP status", message: `{"error":{"code":401,"message":"private provider auth"}}`, wantCode: "service_auth_error", wantStatus: 502},
		{name: "wrapper status does not hide auth", message: `{"status":502,"error":{"status_code":401,"message":"private provider auth"}}`, status: 502, wantCode: "service_auth_error", wantStatus: 502},
		{name: "wrapper status does not hide rate limit", message: `{"error":{"status_code":429}}`, status: 502, wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "JSON metadata is not error evidence", message: `{"error":{"message":"Task failed"},"prompt":"unsafe prompt; insufficient quota"}`, status: 502, wantCode: "generation_failed", wantStatus: 502},
		{name: "JSON prompt status is not error evidence", message: `{"error":{"message":"Task failed"},"prompt":"status_code=401"}`, status: 502, wantCode: "generation_failed", wantStatus: 502},
		{name: "JSON metadata status cannot override diagnostic status", message: `{"metadata":{"note":"status_code=401"},"error":{"status_code":429}}`, status: 502, wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "JSON diagnostic message status remains useful", message: `{"error":{"message":"status_code=429, request throttled"},"prompt":"status_code=401"}`, status: 502, wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "JSON prefix status remains useful", message: `status_code=401, body: {"error":{"message":"Task failed"},"prompt":"status_code=429"}`, status: 502, wantCode: "service_auth_error", wantStatus: 502},
		{name: "embedded HTTP status", message: "status_code=429, private provider busy", wantCode: "rate_limit_exceeded", wantStatus: 429, retryable: true},
		{name: "upstream channel unavailable", message: "status_code=500, 分组 default 下模型 all-image-2.5-sunburst-1k 的可用渠道不存在（retry）; retry: 124", wantCode: "model_unavailable", wantMessage: "status_code=500, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "upstream channel unavailable JSON", message: `{"error":{"message":"分组 default 下模型 all-image-2.5-sunburst-1k 的可用渠道不存在（retry）","type":"new_api_error"}}`, status: 500, wantCode: "model_unavailable", wantMessage: "status_code=500, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "local upstream response wrapper", code: "bad_response_status_code", message: "status_code=500, 可用渠道不存在", status: 502, local: true, wantCode: "model_unavailable", wantMessage: "status_code=500, 当前资源紧张，暂时无可用资源", wantStatus: 503, retryable: true},
		{name: "unknown does not invent reason", code: "unknown_private_code", message: "Task failed Task failed", status: 500, wantCode: "generation_failed", wantMessage: "生成失败，服务未提供可公开确认的具体原因", wantHint: "请求 ID 或任务 ID", wantStatus: 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPublicError(tt.code, tt.message, tt.status, tt.local)
			require.Equal(t, tt.wantCode, got.Code)
			assert.Equal(t, tt.wantStatus, got.StatusCode)
			assert.Equal(t, tt.retryable, got.Retryable)
			if tt.wantMessage != "" {
				message := got.Message
				if !strings.HasPrefix(tt.wantMessage, "status_code=") && strings.HasPrefix(message, "status_code=") {
					_, message, _ = strings.Cut(message, ", ")
				}
				assert.Equal(t, tt.wantMessage, message)
			}
			assert.Contains(t, got.Hint, tt.wantHint)
			assert.Contains(t, got.Summary(), "["+tt.wantCode+"]")
			assert.Contains(t, got.Summary(), got.Hint)
			assert.True(t, strings.ContainsFunc(got.Message, func(r rune) bool { return unicode.Is(unicode.Han, r) }), "public messages must include Chinese explanations")
		})
	}
}

func TestPublicErrorProjectionIsIdempotent(t *testing.T) {
	for code, original := range publicErrorCatalog {
		t.Run(code, func(t *testing.T) {
			got := ClassifyPublicError(original.Code, original.Summary(), original.StatusCode, true)
			assert.Equal(t, original, got)
			assert.True(t, strings.ContainsFunc(got.Message, func(r rune) bool { return unicode.Is(unicode.Han, r) }))
		})
	}
	for _, code := range []string{"generation_failed", "model_unavailable", "invalid_image_input"} {
		t.Run("upstream status "+code, func(t *testing.T) {
			original := ClassifyPublicError(code, "", 500, false)
			got := ClassifyPublicError(original.Code, original.Summary(), original.StatusCode, true)
			assert.Equal(t, original, got)
			assert.Equal(t, 1, strings.Count(got.Summary(), "status_code=500"))
		})
	}
}

func TestMidjourneyResourceUnavailableUsesSharedMessage(t *testing.T) {
	public := MidjourneyPublicError("", "No available channel for private-model", 500, false)
	assert.Equal(t, "model_unavailable", public.Code)
	assert.Equal(t, "status_code=500, 当前资源紧张，暂时无可用资源", public.Message)
	assert.Equal(t, "请稍后重试", public.Hint)
	assert.NotContains(t, public.Summary(), "private-model")
}

func TestPublicErrorNeverReturnsProviderDiagnostics(t *testing.T) {
	const secret = "sk-private-key host=secret.example IP=192.0.2.87 channel=153 plugin=secret-plugin model=internal-real-model"
	for _, raw := range []string{
		secret,
		"could not decode image: " + secret,
		"Transparent background is not supported: " + secret,
		"insufficient quota: " + secret,
		"invalid image size: " + secret,
		"field prompt is required: " + secret,
		"duration must be between 4 and 15 seconds: " + secret,
		"no images generated: " + secret,
		"The provided prompt is considered unsafe: " + secret,
		"safety check failed: " + secret,
		`{"error":{"code":"private_error_code","message":"` + secret + `"}}`,
	} {
		got := ClassifyPublicError("private_error_code", raw, 500, false)
		encoded, err := Marshal(got)
		require.NoError(t, err)
		for _, text := range []string{string(encoded), got.Summary()} {
			for _, forbidden := range []string{"sk-private-key", "secret.example", "192.0.2.87", "153", "secret-plugin", "internal-real-model", "private_error_code", "Task failed"} {
				assert.NotContains(t, text, forbidden)
			}
		}
		assert.NotContains(t, string(encoded), "StatusCode")
		assert.Contains(t, got.Summary(), "status_code=500")
	}
}
