package common

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// PublicError contains only allowlisted diagnostics that are safe for API users.
// Provider details remain in administrator logs, never in these fields.
type PublicError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint,omitempty"`
	Retryable  bool   `json:"retryable"`
	StatusCode int    `json:"-"`
}

// Summary keeps the stable code available to clients that display only a string.
func (e PublicError) Summary() string {
	text := "[" + e.Code + "] " + e.Message
	if e.Hint != "" {
		text += "；" + e.Hint
	}
	return text
}

var publicErrorCatalog = map[string]PublicError{
	"missing_model":                 {"missing_model", "缺少模型 ID（model）", "请填写平台提供的对外模型 ID 后重试", false, http.StatusBadRequest},
	"missing_prompt":                {"missing_prompt", "缺少提示词（prompt）", "请填写生成内容的描述后重试", false, http.StatusBadRequest},
	"missing_input":                 {"missing_input", "缺少必填输入内容", "请按当前接口要求提供 input、messages、contents 或其他必填输入字段", false, http.StatusBadRequest},
	"invalid_image_count":           {"invalid_image_count", "图片生成数量（n）无效", "请将 n（或 parameters.n）设为接口允许范围内的整数", false, http.StatusBadRequest},
	"async_image_count_unsupported": {"async_image_count_unsupported", "异步图片任务每次只支持生成一张图片", "请将 n 或 candidateCount 设置为 1；多张图片请分次提交", false, http.StatusBadRequest},
	"invalid_size":                  {"invalid_size", "图片或视频尺寸参数无效", "请检查 size、分辨率及宽高比是否符合当前模型的接口要求", false, http.StatusBadRequest},
	"invalid_size_separator":        {"invalid_size_separator", "尺寸格式错误：使用了乘号 ×", "请将 size 中的乘号 × 改成小写英文字母 x，例如 1024x1024；具体尺寸须符合当前模型要求", false, http.StatusBadRequest},
	"invalid_duration":              {"invalid_duration", "视频时长参数无效", "请按当前模型要求填写 duration 或 seconds；同时提供两个字段时数值必须一致", false, http.StatusBadRequest},
	"invalid_token_limit":           {"invalid_token_limit", "输出长度限制参数无效", "请检查 max_tokens、max_output_tokens、maxOutputTokens 或 min_tokens 的类型和允许范围", false, http.StatusBadRequest},
	"invalid_request_body":          {"invalid_request_body", "请求正文格式错误或字段类型不正确", "请检查 JSON 格式、字段类型和 Content-Type 后重试", false, http.StatusBadRequest},
	"request_queue_full":            {"request_queue_full", "异步任务队列已满，暂时无法接收新任务", "请降低提交并发并稍后重试", true, http.StatusTooManyRequests},
	"service_connection_failed":     {"service_connection_failed", "暂时无法连接生成服务", "请稍后重试；若持续失败，请联系管理员并提供请求 ID", true, http.StatusBadGateway},
	"generation_result_empty":       {"generation_result_empty", "生成服务未返回有效结果", "请联系管理员并提供请求 ID 或任务 ID，以确认任务结果后再重试", false, http.StatusBadGateway},
	"invalid_image_input":           {"invalid_image_input", "输入图片无效或无法解码", "请确认图片文件完整，并重新上传有效的 PNG、JPEG 或 WebP 图片", false, http.StatusBadRequest},
	"image_download_failed":         {"image_download_failed", "无法读取输入图片链接", "请检查图片链接是否过期、能否直接访问，或改为重新上传图片", false, http.StatusBadRequest},
	"unsupported_background":        {"unsupported_background", "当前图片请求不支持透明背景", "请将 background 改为 opaque 或 auto 后重试", false, http.StatusBadRequest},
	"invalid_parameter":             {"invalid_parameter", "请求参数无效或缺少必填参数", "请检查提示词、图片、尺寸、数量等参数是否符合接口要求", false, http.StatusBadRequest},
	"unsupported_parameter":         {"unsupported_parameter", "请求包含当前功能不支持的参数或参数值", "请按接口要求移除或调整不支持的参数后重试", false, http.StatusBadRequest},
	"unsupported_stream_mode":       {"unsupported_stream_mode", "当前模型不支持流式请求", "请设置 stream=false，并使用模型支持的同步或后台模式", false, http.StatusBadRequest},
	"unsupported_sync_mode":         {"unsupported_sync_mode", "当前模型不支持同步非流式请求", "请使用模型支持的 stream=true 或 background=true 模式", false, http.StatusBadRequest},
	"unsupported_background_mode":   {"unsupported_background_mode", "当前模型不支持后台请求", "请移除 background=true，并使用模型支持的其他模式", false, http.StatusBadRequest},
	"invalid_stream_parameter":      {"invalid_stream_parameter", "stream 参数必须为布尔值", "请传入 true 或 false，不要传入字符串", false, http.StatusBadRequest},
	"content_policy_violation":      {"content_policy_violation", "输入内容或生成结果未通过安全审核", "安全审核提示：请调整提示词或参考图片后重试；如确认内容合规仍失败，请联系管理员并提供请求 ID 或任务 ID", false, http.StatusBadRequest},
	"content_safety_unknown":        {"content_safety_unknown", "生成失败，安全检查的具体原因尚未明确", "安全审核提示：报错涉及安全检查，但尚不能确认内容被拦截；请检查提示词或参考图片，如仍失败请联系管理员并提供请求 ID 或任务 ID", false, http.StatusBadGateway},
	"input_too_large":               {"input_too_large", "输入内容或文件超过大小限制", "请压缩图片、减少文件数量或缩短输入内容后重试", false, http.StatusRequestEntityTooLarge},
	"rate_limit_exceeded":           {"rate_limit_exceeded", "请求过于频繁或当前并发数已达上限", "请降低并发并稍后重试", true, http.StatusTooManyRequests},
	"request_timeout":               {"request_timeout", "请求处理超时，暂未取得结果", "异步任务请先查询原任务状态，避免重复提交；若持续无结果，请联系管理员并提供请求 ID 或任务 ID", true, http.StatusGatewayTimeout},
	"task_timeout":                  {"task_timeout", "任务仍在处理中，等待结果超时", "请继续查询原任务状态，不要重复提交", true, http.StatusGatewayTimeout},
	"model_unavailable":             {"model_unavailable", "所选模型或处理模式暂不可用", "请检查对外模型 ID 和处理模式，或联系管理员确认可用配置", true, http.StatusServiceUnavailable},
	"service_unavailable":           {"service_unavailable", "生成服务暂时不可用", "请稍后重试；若持续失败，请联系管理员并提供请求 ID 或任务 ID", true, http.StatusServiceUnavailable},
	"service_auth_error":            {"service_auth_error", "生成服务认证失败", "请联系管理员并提供请求 ID 或任务 ID；无需更换您的令牌", false, http.StatusBadGateway},
	"service_access_error":          {"service_access_error", "生成服务拒绝了此次请求", "请联系管理员并提供请求 ID 或任务 ID，以检查服务访问配置", false, http.StatusBadGateway},
	"service_quota_exceeded":        {"service_quota_exceeded", "生成服务可用额度不足", "请联系管理员处理服务额度；无需为此调整您的账户或令牌额度", false, http.StatusServiceUnavailable},
	"insufficient_token_quota":      {"insufficient_token_quota", "令牌配额不足，无法支付本次请求", "请提高此令牌的额度上限或改用额度充足的令牌；账户余额不等于令牌可用额度", false, http.StatusPaymentRequired},
	"insufficient_user_quota":       {"insufficient_user_quota", "账户余额不足，无法支付本次请求", "请充值后重试，无需更换 API Key", false, http.StatusPaymentRequired},
	"billing_failed":                {"billing_failed", "额度校验或预扣费未能完成", "请稍后重试；若持续失败，请联系管理员并提供请求 ID", false, http.StatusInternalServerError},
	"invalid_token":                 {"invalid_token", "令牌无效或已被禁用", "请检查客户端配置的令牌是否正确且已启用", false, http.StatusUnauthorized},
	"token_expired":                 {"token_expired", "令牌已过期", "请更新令牌有效期或更换有效令牌", false, http.StatusUnauthorized},
	"access_denied":                 {"access_denied", "当前令牌没有执行此操作的权限", "请检查令牌的模型、分组和访问限制，或联系管理员", false, http.StatusForbidden},
	"task_not_found":                {"task_not_found", "任务不存在或不属于当前用户", "请确认任务 ID 正确，并使用创建该任务的账户查询", false, http.StatusNotFound},
	"task_expired":                  {"task_expired", "任务结果已过期", "请重新提交任务；如需恢复历史结果，请联系管理员并提供任务 ID", false, http.StatusGone},
	"task_not_ready":                {"task_not_ready", "任务尚未完成，结果暂不可用", "请继续查询原任务状态，不要重复提交", true, http.StatusConflict},
	"request_conflict":              {"request_conflict", "请求与任务当前状态或操作条件冲突", "请检查任务状态和操作要求后重试", false, http.StatusConflict},
	"request_cancelled":             {"request_cancelled", "请求等待已取消", "异步任务请先查询原任务状态，避免重复提交", false, http.StatusRequestTimeout},
	"result_unknown":                {"result_unknown", "请求提交结果暂无法确认，任务可能仍在处理中", "请先查询原任务状态，不要重复提交；若无法查询，请联系管理员并提供请求 ID 或任务 ID", false, http.StatusBadGateway},
	"generation_failed":             {"generation_failed", "生成失败，服务未提供可公开确认的具体原因", "请联系管理员并提供请求 ID 或任务 ID，以查询详细错误", false, http.StatusBadGateway},
}

var publicErrorHTTPStatusPattern = regexp.MustCompile(`(?i)(?:status_code|http_status|status code|http status|http)[\s=:"']+(4\d\d|5\d\d)\b`)
var publicErrorMissingFieldPattern = regexp.MustCompile(`(?:^|[\s:;(])(?:field )?(model|prompt|input|messages|contents|documents|query|instruction)(?: field)? (?:is required|is empty|must not be empty)\b`)
var publicErrorImageCountPattern = regexp.MustCompile(`(?:^|[\s:;(])(?:parameters\.)?n must be an integer between\b`)
var publicErrorTokenLimitPattern = regexp.MustCompile(`\b(?:max_tokens|max_output_tokens|maxoutputtokens|min_tokens) is invalid\b`)
var publicErrorSafetyURLPattern = regexp.MustCompile(`https?://[^\s"<>]+`)
var publicErrorSafetyPassedPattern = regexp.MustCompile(`\b(?:no|without)\s+(?:unsafe|nsfw)\s+(?:content|images?|prompts?)\b(?:\s+(?:was\s+)?(?:detected|found))?|\bnot unsafe\b|\b(?:is|was|considered) safe\b|\bno safety violations?\b|\b(?:safety|moderation)(?: check| review)?(?: has| was| is)? (?:passed|approved|successful)\b|(?:已通过|通过了)(?:内容|安全|内容安全)?审核|(?:内容|安全|内容安全)?审核(?:已通过|通过了|通过)`)
var publicErrorSafetyRejectionPattern = regexp.MustCompile(`\b(?:content[._ ]policy[._ ]violation|(?:unsafe|nsfw)[ _](?:prompts?|images?|content|videos?)|(?:prompts?|images?|content|videos?)[ _](?:unsafe|nsfw))\b|\b(?:prompt|images?|content|videos?|outputs?)\b[^.!?\n]{0,80}\b(?:unsafe|not safe|nsfw)\b|\b(?:blocked|rejected|filtered(?: out)?|flagged|violates?|violated)\b[^.!?\n]{0,120}\b(?:safety|moderation|content policy|prohibited use policy|nsfw)\b|\b(?:safety|moderation|content policy)\b[^.!?\n]{0,60}\b(?:blocked|rejected|violation)\b|(?:未通过|没有通过)(?:内容|安全|内容安全)?审核|(?:内容|安全|内容安全)?审核(?:失败|未通过|不通过)|违规内容|内容违规`)
var publicErrorSafetyMentionPattern = regexp.MustCompile(`\b(?:safety|moderation|nsfw)\b|(?:^|[\s:])(?:safe|unsafe)(?:$|[.,;](?:\s|$))|安全检查|安全审核|内容审核`)

// ClassifyPublicError projects untrusted diagnostics into fixed public wording.
// local grants trust only to the explicit code argument, never to codes or
// messages extracted from a provider response wrapped by a local error.
func ClassifyPublicError(code, message string, status int, local bool) PublicError {
	hostCode := strings.ToLower(strings.TrimSpace(code))
	if local {
		// Generic codes may still carry a precise validation or safety reason.
		if projected, ok := publicErrorCatalog[hostCode]; ok && hostCode != "invalid_parameter" && hostCode != "generation_failed" {
			return projected
		}
		switch hostCode {
		case "pre_consume_token_quota_failed":
			text := strings.ToLower(message)
			if strings.Contains(text, "token quota is not enough") || strings.Contains(text, "token quota insufficient") || strings.Contains(text, "令牌额度不足") || strings.Contains(text, "令牌配额不足") {
				return publicErrorCatalog["insufficient_token_quota"]
			}
			return publicErrorCatalog["billing_failed"]
		case "insufficient_token_quota", "token_quota_insufficient", "token_quota_not_enough", "token_quota_exhausted":
			return publicErrorCatalog["insufficient_token_quota"]
		case "insufficient_user_quota", "user_quota_insufficient":
			return publicErrorCatalog["insufficient_user_quota"]
		case "invalid_token", "token_invalid", "token_disabled":
			return publicErrorCatalog["invalid_token"]
		case "token_expired":
			return publicErrorCatalog["token_expired"]
		case "access_denied", "permission_denied", "token_model_not_allowed":
			return publicErrorCatalog["access_denied"]
		case "task_not_found", "task_not_exist", "origin_task_not_found":
			return publicErrorCatalog["task_not_found"]
		case "origin_task_platform_mismatch", "origin_task_channel_conflict":
			return publicErrorCatalog["request_conflict"]
		case "origin_task_channel_disabled":
			return publicErrorCatalog["model_unavailable"]
		case "task_expired", "task_result_expired":
			return publicErrorCatalog["task_expired"]
		case "task_not_ready", "task_not_finished":
			return publicErrorCatalog["task_not_ready"]
		case "request_cancelled", "client_request_canceled":
			return publicErrorCatalog["request_cancelled"]
		case "invalid_request", "bad_request_body", "invalid_parameter":
			text := strings.ToLower(message)
			if strings.Contains(text, "invalid character") || strings.Contains(text, "cannot unmarshal") || strings.Contains(text, "unexpected end of json input") {
				return publicErrorCatalog["invalid_request_body"]
			}
		}
	}
	codes, text, status := publicErrorEvidence(hostCode, message, status)
	safetyRejected, safetyMentioned := publicErrorContentSafety(codes, text)
	missingField := ""
	if match := publicErrorMissingFieldPattern.FindStringSubmatch(text); len(match) == 2 {
		missingField = match[1]
	}
	// Authentication and quota failures from providers must never tell users
	// that their own token or wallet is invalid or exhausted.
	switch {
	case strings.Contains(text, "streaming is not supported for this model"):
		return publicErrorCatalog["unsupported_stream_mode"]
	case strings.Contains(text, "synchronous non-streaming requests are not supported for this model"):
		return publicErrorCatalog["unsupported_sync_mode"]
	case strings.Contains(text, "background mode is not supported for this model"):
		return publicErrorCatalog["unsupported_background_mode"]
	case strings.Contains(text, "stream must be a boolean"):
		return publicErrorCatalog["invalid_stream_parameter"]
	case slices.Contains(codes, "result_unknown"), slices.Contains(codes, "submission_unknown"), slices.Contains(codes, "unknown_outcome"), strings.Contains(text, "submission outcome is unknown"), strings.Contains(text, "提交结果不确定"):
		return publicErrorCatalog["result_unknown"]
	case status == http.StatusUnauthorized, slices.Contains(codes, "invalid_api_key"), slices.Contains(codes, "invalid_token"), slices.Contains(codes, "token_expired"), slices.Contains(codes, "authentication_error"), slices.Contains(codes, "channel:invalid_key"):
		return publicErrorCatalog["service_auth_error"]
	case status == http.StatusPaymentRequired, slices.Contains(codes, "insufficient_quota"), slices.Contains(codes, "insufficient_user_quota"), slices.Contains(codes, "insufficient_token_quota"), slices.Contains(codes, "billing_hard_limit_reached"), strings.Contains(text, "insufficient quota"), strings.Contains(text, "insufficient balance"), strings.Contains(text, "余额不足"), strings.Contains(text, "配额不足"):
		return publicErrorCatalog["service_quota_exceeded"]
	case status == http.StatusUnavailableForLegalReasons, safetyRejected:
		return publicErrorCatalog["content_policy_violation"]
	case status == http.StatusForbidden, slices.Contains(codes, "permission_denied"), slices.Contains(codes, "access_denied"):
		if safetyMentioned && !slices.Contains(codes, "permission_denied") && !slices.Contains(codes, "access_denied") {
			return publicErrorCatalog["content_safety_unknown"]
		}
		return publicErrorCatalog["service_access_error"]
	case strings.Contains(text, "transparent") && (strings.Contains(text, "not supported") || strings.Contains(text, "unsupported") || strings.Contains(text, "opaque")), strings.Contains(text, "不支持透明背景"):
		return publicErrorCatalog["unsupported_background"]
	case slices.Contains(codes, "image_download_failed"), slices.Contains(codes, "invalid_image_url"), strings.Contains(text, "error downloading image"), strings.Contains(text, "failed to download image"), strings.Contains(text, "failed to fetch image"), strings.Contains(text, "image url") && (strings.Contains(text, "unreachable") || strings.Contains(text, "expired")), strings.Contains(text, "图片下载失败"), strings.Contains(text, "图片链接失效"):
		return publicErrorCatalog["image_download_failed"]
	case status == http.StatusRequestEntityTooLarge, slices.Contains(codes, "input_too_large"), slices.Contains(codes, "context_length_exceeded"), strings.Contains(text, "payload too large"), strings.Contains(text, "request entity too large"), strings.Contains(text, "image too large"), strings.Contains(text, "maximum context length"), strings.Contains(text, "文件过大"):
		return publicErrorCatalog["input_too_large"]
	case slices.Contains(codes, "missing_model"), missingField == "model":
		return publicErrorCatalog["missing_model"]
	case slices.Contains(codes, "missing_prompt"), missingField == "prompt":
		return publicErrorCatalog["missing_prompt"]
	case slices.Contains(codes, "missing_input"), missingField != "":
		return publicErrorCatalog["missing_input"]
	case slices.Contains(codes, "async_image_count_unsupported"), strings.Contains(text, "async image tasks only support n=1"), strings.Contains(text, "async gemini image tasks only support candidatecount=1"):
		return publicErrorCatalog["async_image_count_unsupported"]
	case slices.Contains(codes, "invalid_image_count"), publicErrorImageCountPattern.MatchString(text):
		return publicErrorCatalog["invalid_image_count"]
	case slices.Contains(codes, "invalid_size_separator"), strings.Contains(text, "please use 'x' instead of the multiplication sign '×'"):
		return publicErrorCatalog["invalid_size_separator"]
	case slices.Contains(codes, "invalid_size"), slices.Contains(codes, "invalid_resolution"), slices.Contains(codes, "invalid_aspect_ratio"), strings.Contains(text, "invalid image size"), strings.Contains(text, "size is invalid"):
		return publicErrorCatalog["invalid_size"]
	case slices.Contains(codes, "invalid_duration"), slices.Contains(codes, "invalid_seconds"), strings.Contains(text, "seconds must be an integer"), strings.Contains(text, "seconds must be between"), strings.Contains(text, "duration must be an integer"), strings.Contains(text, "duration must be between"), strings.Contains(text, "duration and seconds must match"):
		return publicErrorCatalog["invalid_duration"]
	case slices.Contains(codes, "invalid_token_limit"), publicErrorTokenLimitPattern.MatchString(text):
		return publicErrorCatalog["invalid_token_limit"]
	case slices.Contains(codes, "invalid_request_body"), slices.Contains(codes, "invalid_json"):
		return publicErrorCatalog["invalid_request_body"]
	case slices.Contains(codes, "invalid_image_input"), slices.Contains(codes, "invalid_image"), slices.Contains(codes, "invalidimage"), slices.Contains(codes, "image_parse_error"), strings.Contains(text, "could not decode image"), strings.Contains(text, "unable to decode image"), strings.Contains(text, "failed to decode image"), strings.Contains(text, "invalid image"), strings.Contains(text, "image request is invalid"), strings.Contains(text, "unsupported image format"), strings.Contains(text, "无法解码"), strings.Contains(text, "图片格式不支持"):
		return publicErrorCatalog["invalid_image_input"]
	case slices.Contains(codes, "request_queue_full"), strings.Contains(text, "async image task queue is full"):
		return publicErrorCatalog["request_queue_full"]
	case status == http.StatusTooManyRequests, slices.Contains(codes, "rate_limit_exceeded"), slices.Contains(codes, "rate_limit_error"), slices.Contains(codes, "throttled"), strings.Contains(text, "rate limit"), strings.Contains(text, "too many requests"), strings.Contains(text, "throttled"), strings.Contains(text, "请求过于频繁"), strings.Contains(text, "并发上限"):
		return publicErrorCatalog["rate_limit_exceeded"]
	case status == http.StatusGatewayTimeout, status == http.StatusRequestTimeout, slices.Contains(codes, "timeout"), slices.Contains(codes, "request_timeout"), slices.Contains(codes, "task_timeout"), slices.Contains(codes, "deadline_exceeded"), strings.Contains(text, "timed out"), strings.Contains(text, "deadline exceeded"), strings.Contains(text, "超时"):
		return publicErrorCatalog["request_timeout"]
	case slices.Contains(codes, "service_connection_failed"), local && hostCode == "do_request_failed":
		return publicErrorCatalog["service_connection_failed"]
	case slices.Contains(codes, "generation_result_empty"), slices.Contains(codes, "empty_response"), strings.Contains(text, "returned no image result"), strings.Contains(text, "no images generated"), strings.Contains(text, "no usable image data"):
		return publicErrorCatalog["generation_result_empty"]
	case slices.Contains(codes, "model_not_found"), slices.Contains(codes, "model_unavailable"), slices.Contains(codes, "get_channel_failed"), slices.Contains(codes, "channel_not_found"), slices.Contains(codes, "model_mapping_failed"), slices.Contains(codes, "model_mapping_changed"), strings.Contains(text, "no available channel"), strings.Contains(text, "没有可用渠道"):
		return publicErrorCatalog["model_unavailable"]
	case status == http.StatusServiceUnavailable, slices.Contains(codes, "service_unavailable"), slices.Contains(codes, "overloaded_error"):
		return publicErrorCatalog["service_unavailable"]
	case slices.Contains(codes, "unsupported_parameter"), slices.Contains(codes, "unsupported_value"), strings.Contains(text, "unsupported parameter"), strings.Contains(text, "不支持的参数"):
		return publicErrorCatalog["unsupported_parameter"]
	case safetyMentioned:
		return publicErrorCatalog["content_safety_unknown"]
	case slices.Contains(codes, "invalid_parameter"), slices.Contains(codes, "invalidparameter"), slices.Contains(codes, "invalid_request"), slices.Contains(codes, "invalid_request_error"), slices.Contains(codes, "bad_request_body"), slices.Contains(codes, "missing_required_parameter"), strings.Contains(text, "invalid parameter"), strings.Contains(text, "参数错误"), strings.Contains(text, "缺少必填"):
		return publicErrorCatalog["invalid_parameter"]
	default:
		return publicErrorCatalog["generation_failed"]
	}
}

// publicErrorContentSafety separates explicit rejection from an ambiguous safety
// diagnostic. URLs, successful checks and unrelated JSON fields are not grounds
// for telling a user their content was rejected.
func publicErrorContentSafety(codes []string, text string) (rejected, mentioned bool) {
	for _, code := range codes {
		switch code {
		case "content_policy_violation", "content.policy_violation", "datainspectionfailed", "data_inspection_failed",
			"content_filter", "content_filter_error", "sensitive_words_detected", "prompt_blocked",
			"unsafe_prompt", "unsafe_image", "prompt_unsafe", "image_unsafe", "generated_image_review_failed",
			"safety", "image_safety", "safety_violation", "prohibited_content", "nsfw":
			return true, true
		case "safe", "unsafe", "moderation", "safety_check_failed", "moderation_error", "content_safety_unknown":
			mentioned = true
		}
	}
	text = publicErrorSafetyURLPattern.ReplaceAllString(text, " ")
	text = publicErrorSafetyPassedPattern.ReplaceAllString(text, " ")
	// Some providers explicitly give two possible causes, without confirming
	// which one occurred. Keep that uncertainty in the public explanation.
	if strings.Contains(text, "failed or was rejected by content moderation") {
		return false, true
	}
	if publicErrorSafetyRejectionPattern.MatchString(text) {
		return true, true
	}
	return false, mentioned || publicErrorSafetyMentionPattern.MatchString(text)
}

// publicErrorEvidence understands common error envelopes without trusting their
// contents as local errors. Bounds avoid repeatedly parsing large provider bodies.
func publicErrorEvidence(code, message string, status int) ([]string, string, int) {
	message = message[:min(len(message), 64*1024)]
	codes := []string{code}
	text := strings.ToLower(message)
	if status < 400 || status > 599 {
		status = 0
	}
	payload := strings.TrimSpace(message)
	prefix := ""
	if start := strings.IndexByte(payload, '{'); start >= 0 {
		prefix = payload[:start]
		payload = payload[start:]
	}
	var root map[string]any
	if UnmarshalJsonStr(payload, &root) != nil {
		if status == 0 || status == http.StatusBadGateway {
			if match := publicErrorHTTPStatusPattern.FindStringSubmatch(text); len(match) == 2 {
				status, _ = strconv.Atoi(match[1])
			}
		}
		return codes, text, status
	}
	// Valid envelopes may contain echoed prompts and arbitrary metadata. Only
	// their diagnostic fields and any log prefix are evidence, not the full
	// serialized payload, including when extracting an embedded HTTP status.
	text = strings.ToLower(prefix)
	pending := []map[string]any{root}
	for inspected := 0; inspected < 8 && len(pending) > 0; inspected++ {
		entry := pending[0]
		pending = pending[1:]
		for _, key := range []string{"code", "error_code", "type"} {
			if value, ok := entry[key].(string); ok {
				codes = append(codes, strings.ToLower(strings.TrimSpace(value)))
			}
		}
		for _, key := range []string{"message", "detail", "description", "error"} {
			if value, ok := entry[key].(string); ok {
				text += " " + strings.ToLower(value)
			}
		}
		for _, key := range []string{"status_code", "status", "http_status", "code"} {
			if status != 0 && status != http.StatusBadGateway {
				break
			}
			var candidate int
			switch value := entry[key].(type) {
			case float64:
				if value >= 400 && value <= 599 && value == float64(int(value)) {
					candidate = int(value)
				}
			case string:
				candidate, _ = strconv.Atoi(value)
			}
			if candidate >= 400 && candidate <= 599 {
				status = candidate
			}
		}
		for _, key := range []string{"error", "data", "detail"} {
			if nested, ok := entry[key].(map[string]any); ok {
				pending = append(pending, nested)
			}
		}
	}
	if status == 0 || status == http.StatusBadGateway {
		if match := publicErrorHTTPStatusPattern.FindStringSubmatch(text); len(match) == 2 {
			status, _ = strconv.Atoi(match[1])
		}
	}
	return codes, text, status
}
