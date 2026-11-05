package common

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/pkg/errors"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpproxy"
)

const KeyRequestBody = "key_request_body"
const KeyBodyStorage = "key_body_storage"
const keyBodyReadError = "key_body_read_error"

var ErrRequestBodyTooLarge = errors.New("request body too large")

func IsRequestBodyTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRequestBodyTooLarge) {
		return true
	}
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func GetRequestBody(c *gin.Context) (io.Seeker, error) {
	// 首先检查是否有 BodyStorage 缓存
	if storage, exists := c.Get(KeyBodyStorage); exists && storage != nil {
		if bs, ok := storage.(BodyStorage); ok {
			if _, err := bs.Seek(0, io.SeekStart); err != nil {
				return nil, fmt.Errorf("failed to seek body storage: %w", err)
			}
			return bs, nil
		}
	}

	// 检查旧的缓存方式
	cached, exists := c.Get(KeyRequestBody)
	if exists && cached != nil {
		if b, ok := cached.([]byte); ok {
			bs, err := CreateBodyStorage(b)
			if err != nil {
				return nil, err
			}
			c.Set(KeyBodyStorage, bs)
			return bs, nil
		}
	}

	// A failed read has consumed and closed the body. Later readers get the
	// same error instead of "http: invalid Read on closed Body".
	if readErr, failed := c.Get(keyBodyReadError); failed {
		return nil, readErr.(error)
	}

	maxMB := constant.MaxRequestBodyMB
	if maxMB <= 0 {
		maxMB = 128 // 默认 128MB
	}
	maxBytes := int64(maxMB) << 20

	contentLength := c.Request.ContentLength

	// 使用新的存储系统
	storage, err := CreateBodyStorageFromReader(c.Request.Body, contentLength, maxBytes)
	_ = c.Request.Body.Close()

	if err != nil {
		if IsRequestBodyTooLargeError(err) {
			err = errors.Wrap(ErrRequestBodyTooLarge, fmt.Sprintf("request body exceeds %d MB", maxMB))
		}
		c.Set(keyBodyReadError, err)
		return nil, err
	}

	// 缓存存储对象
	c.Set(KeyBodyStorage, storage)

	return storage, nil
}

// GetBodyStorage 获取请求体存储对象（用于需要多次读取的场景）
func GetBodyStorage(c *gin.Context) (BodyStorage, error) {
	seeker, err := GetRequestBody(c)
	if err != nil {
		return nil, err
	}
	bs, ok := seeker.(BodyStorage)
	if !ok {
		return nil, errors.New("unexpected body storage type")
	}
	return bs, nil
}

// CleanupBodyStorage 清理请求体存储（应在请求结束时调用）
func CleanupBodyStorage(c *gin.Context) {
	if storage, exists := c.Get(KeyBodyStorage); exists && storage != nil {
		if bs, ok := storage.(BodyStorage); ok {
			bs.Close()
		}
		c.Set(KeyBodyStorage, nil)
	}
}

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	storage, err := GetBodyStorage(c)
	if err != nil {
		return err
	}
	contentType := c.Request.Header.Get("Content-Type")

	// disk-backed JSON: stream-decode directly from the file to avoid
	// materializing the entire payload back into a transient []byte
	// (diskStorage.Bytes() would ReadFull the whole file into the heap).
	if storage.IsDisk() && strings.HasPrefix(contentType, "application/json") {
		if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
			return seekErr
		}
		if err := DecodeJson(storage, v); err != nil {
			return err
		}
		if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
			return seekErr
		}
		c.Request.Body = io.NopCloser(storage)
		return nil
	}

	requestBody, err := storage.Bytes()
	if err != nil {
		return err
	}
	if strings.HasPrefix(contentType, "application/json") {
		err = Unmarshal(requestBody, v)
	} else if strings.Contains(contentType, gin.MIMEPOSTForm) {
		err = parseFormData(requestBody, v)
	} else if strings.Contains(contentType, gin.MIMEMultipartPOSTForm) {
		err = parseMultipartFormData(c, requestBody, v)
	} else {
		// skip for now
		// TODO: someday non json request have variant model, we will need to implementation this
	}
	if err != nil {
		return err
	}
	// Reset request body
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return seekErr
	}
	c.Request.Body = io.NopCloser(storage)
	return nil
}

func SetContextKey(c *gin.Context, key constant.ContextKey, value any) {
	c.Set(string(key), value)
}

func GetContextKey(c *gin.Context, key constant.ContextKey) (any, bool) {
	return c.Get(string(key))
}

func GetContextKeyString(c *gin.Context, key constant.ContextKey) string {
	return c.GetString(string(key))
}

// presetUpstreamHosts are the hosts, with any port, of the default base URLs
// new-api presets for channel types. They are public endpoints, so errors keep
// them readable. The port keeps a configured localhost upstream apart from the
// Ollama preset.
var presetUpstreamHosts = sync.OnceValue(func() map[string]struct{} {
	hosts := make(map[string]struct{}, len(constant.ChannelBaseURLs))
	for _, baseURL := range constant.ChannelBaseURLs {
		if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
			hosts[strings.ToLower(parsed.Host)] = struct{}{}
		}
	}
	return hosts
})

// isPresetUpstreamHost reports whether address points at one of the preset
// default base URL hosts.
func isPresetUpstreamHost(address string) bool {
	rawURL := strings.TrimSpace(address)
	if !strings.Contains(rawURL, "://") {
		rawURL = "//" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	_, preset := presetUpstreamHosts()[strings.ToLower(parsed.Host)]
	return preset
}

// UpstreamAddresses returns the addresses masked in errors shown to users: the
// upstream the admin configured for the selected channel and the proxies its
// requests may go through. Preset default base URLs are public and stay
// readable. The origin actually requested only counts for a configured base
// URL or an Advanced Custom channel, whose routes may target absolute URLs;
// other adaptors build official endpoints themselves.
func UpstreamAddresses(c *gin.Context) []string {
	// A channel without its own proxy dials through the environment proxy.
	environmentProxy := httpproxy.FromEnvironment()
	candidates := []string{environmentProxy.HTTPProxy, environmentProxy.HTTPSProxy}
	if setting, ok := GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting); ok {
		candidates = append(candidates, setting.Proxy)
	}
	baseURL := strings.TrimSpace(GetContextKeyString(c, constant.ContextKeyChannelBaseUrl))
	candidates = append(candidates, baseURL)
	if GetContextKeyInt(c, constant.ContextKeyChannelType) == constant.ChannelTypeAdvancedCustom || (baseURL != "" && !isPresetUpstreamHost(baseURL)) {
		candidates = append(candidates, GetContextKeyString(c, constant.ContextKeyUpstreamRequestOrigin))
	}
	addresses := make([]string, 0, len(candidates))
	for _, address := range candidates {
		if strings.TrimSpace(address) != "" && !isPresetUpstreamHost(address) {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func GetContextKeyInt(c *gin.Context, key constant.ContextKey) int {
	return c.GetInt(string(key))
}

func GetContextKeyBool(c *gin.Context, key constant.ContextKey) bool {
	return c.GetBool(string(key))
}

func GetContextKeyStringSlice(c *gin.Context, key constant.ContextKey) []string {
	return c.GetStringSlice(string(key))
}

func GetContextKeyStringMap(c *gin.Context, key constant.ContextKey) map[string]any {
	return c.GetStringMap(string(key))
}

func GetContextKeyTime(c *gin.Context, key constant.ContextKey) time.Time {
	return c.GetTime(string(key))
}

func GetContextKeyType[T any](c *gin.Context, key constant.ContextKey) (T, bool) {
	if value, ok := c.Get(string(key)); ok {
		if v, ok := value.(T); ok {
			return v, true
		}
	}
	var t T
	return t, false
}

// ApiError responds with err's text. When err is or wraps a *Message, the
// response also carries message_key and message_params for the web console.
func ApiError(c *gin.Context, err error) {
	ApiErrorStatus(c, http.StatusOK, err)
}

// ApiErrorStatus is ApiError with another HTTP status. fields adds response
// fields such as a stable error code or data.
func ApiErrorStatus(c *gin.Context, status int, err error, fields ...gin.H) {
	body := gin.H{
		"success": false,
		"message": err.Error(),
	}
	var message *Message
	if errors.As(err, &message) {
		maps.Copy(body, message.Fields())
	}
	for _, extra := range fields {
		maps.Copy(body, extra)
	}
	c.JSON(status, body)
}

// ApiErrorT responds with a web console message; key is the English source
// text, which the web console translates with params.
func ApiErrorT(c *gin.Context, key string, params ...map[string]any) {
	ApiError(c, NewMessage(key, params...))
}

// ApiSuccessT is the success counterpart of ApiErrorT.
func ApiSuccessT(c *gin.Context, key string, data any, params ...map[string]any) {
	message := NewMessage(key, params...)
	body := gin.H{
		"success": true,
		"message": message.Error(),
		"data":    data,
	}
	maps.Copy(body, message.Fields())
	c.JSON(http.StatusOK, body)
}

func ApiErrorMsg(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
	})
}

func ApiSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

// ApiErrorI18n returns a translated error message based on the user's language preference
// key is the i18n message key, args is optional template data
func ApiErrorI18n(c *gin.Context, key string, args ...map[string]any) {
	msg := TranslateMessage(c, key, args...)
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
	})
}

// ApiSuccessI18n returns a translated success message based on the user's language preference
func ApiSuccessI18n(c *gin.Context, key string, data any, args ...map[string]any) {
	msg := TranslateMessage(c, key, args...)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": msg,
		"data":    data,
	})
}

// TranslateMessage is a helper function that calls i18n.T
// This function is defined here to avoid circular imports
// The actual implementation will be set during init
var TranslateMessage func(c *gin.Context, key string, args ...map[string]any) string

func init() {
	// Default implementation that returns the key as-is
	// This will be replaced by i18n.T during i18n initialization
	TranslateMessage = func(c *gin.Context, key string, args ...map[string]any) string {
		c.Header("X-Translate-id", "d5e7afdfc7f03414b941f9c1e7096be9966510e7")
		return key
	}
}

func ParseMultipartFormReusable(c *gin.Context) (*multipart.Form, error) {
	storage, err := GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	requestBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}

	// Use the original Content-Type saved on first call to avoid boundary
	// mismatch when callers overwrite c.Request.Header after multipart rebuild.
	var contentType string
	if saved, ok := c.Get("_original_multipart_ct"); ok {
		contentType = saved.(string)
	} else {
		contentType = c.Request.Header.Get("Content-Type")
		c.Set("_original_multipart_ct", contentType)
	}
	boundary, err := parseBoundary(contentType)
	if err != nil {
		return nil, err
	}
	if !c.GetBool("request_body_decompressed") &&
		c.Request.Header.Get("Content-Encoding") == "" &&
		c.Request.ContentLength >= 0 &&
		storage.Size() != c.Request.ContentLength {
		return nil, newMultipartParseError(
			requestBody,
			boundary,
			fmt.Errorf("request body length mismatch: received %d bytes, declared %d bytes", storage.Size(), c.Request.ContentLength),
		)
	}

	reader := multipart.NewReader(bytes.NewReader(requestBody), boundary)
	form, err := reader.ReadForm(multipartMemoryLimit())
	if err != nil {
		if form != nil {
			_ = form.RemoveAll()
		}
		parseErr := newMultipartParseError(requestBody, boundary, err)
		if repairedBody, repairedForm, ok := tryRepairImageEditMultipartEOF(c, requestBody, boundary, parseErr); ok {
			repairedStorage, storageErr := CreateBodyStorage(repairedBody)
			if storageErr != nil {
				_ = repairedForm.RemoveAll()
				return nil, fmt.Errorf("failed to store repaired multipart request: %w", storageErr)
			}
			c.Set("multipart_eof_original_bytes", storage.Size())
			storage.Close()
			c.Set(KeyBodyStorage, repairedStorage)
			c.Request.ContentLength = int64(len(repairedBody))
			c.Request.Header.Set("Content-Length", strconv.FormatInt(int64(len(repairedBody)), 10))
			c.Set("multipart_eof_repaired", true)
			c.Request.Body = io.NopCloser(repairedStorage)
			return repairedForm, nil
		}
		return nil, parseErr
	}

	// Reset request body
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}
	c.Request.Body = io.NopCloser(storage)
	return form, nil
}

func tryRepairImageEditMultipartEOF(c *gin.Context, body []byte, boundary string, parseErr *MultipartParseError) ([]byte, *multipart.Form, bool) {
	if c == nil || c.Request == nil || parseErr == nil ||
		parseErr.Kind != "body_truncated" || !errors.Is(parseErr.Cause, io.ErrUnexpectedEOF) ||
		!isImageEditMultipartPath(c.Request.URL.Path) ||
		c.GetBool("request_body_decompressed") ||
		c.Request.Header.Get("Content-Encoding") != "" ||
		c.Request.ContentLength < 0 ||
		int64(len(body)) != c.Request.ContentLength {
		return nil, nil, false
	}

	closingBoundary := []byte("--" + boundary + "--")
	if bytes.Contains(body, closingBoundary) {
		return nil, nil, false
	}

	repairedBody, ok := appendMultipartClosingBoundary(body, boundary)
	if !ok {
		return nil, nil, false
	}

	repairedReader := multipart.NewReader(bytes.NewReader(repairedBody), boundary)
	form, err := repairedReader.ReadForm(multipartMemoryLimit())
	if err != nil || !hasNonEmptyImageMultipartFile(form) {
		if form != nil {
			_ = form.RemoveAll()
		}
		return nil, nil, false
	}
	return repairedBody, form, true
}

func isImageEditMultipartPath(path string) bool {
	return path == "/v1/edits" || strings.HasSuffix(path, "/images/edits")
}

func appendMultipartClosingBoundary(body []byte, boundary string) ([]byte, bool) {
	marker := []byte("--" + boundary)
	if len(body) == 0 || len(marker) == 0 {
		return nil, false
	}

	lastBoundary := -1
	searchFrom := 0
	for searchFrom < len(body) {
		index := bytes.Index(body[searchFrom:], marker)
		if index < 0 {
			break
		}
		index += searchFrom
		afterMarker := index + len(marker)
		atLineStart := index == 0 || (index >= 2 && bytes.Equal(body[index-2:index], []byte("\r\n")))
		validDelimiter := afterMarker+2 <= len(body) &&
			(bytes.Equal(body[afterMarker:afterMarker+2], []byte("\r\n")) ||
				bytes.Equal(body[afterMarker:afterMarker+2], []byte("--")))
		if atLineStart && validDelimiter && !bytes.HasPrefix(body[afterMarker:], []byte("--")) {
			lastBoundary = index
		}
		searchFrom = index + len(marker)
	}
	if lastBoundary < 0 {
		return nil, false
	}

	headerEndOffset := bytes.Index(body[lastBoundary:], []byte("\r\n\r\n"))
	if headerEndOffset < 0 {
		return nil, false
	}
	contentStart := lastBoundary + headerEndOffset + len("\r\n\r\n")
	if contentStart >= len(body) {
		return nil, false
	}

	closing := []byte("--" + boundary + "--\r\n")
	if !bytes.HasSuffix(body, []byte("\r\n")) {
		closing = append([]byte("\r\n"), closing...)
	}
	repaired := make([]byte, 0, len(body)+len(closing))
	repaired = append(repaired, body...)
	repaired = append(repaired, closing...)
	return repaired, true
}

func hasNonEmptyImageMultipartFile(form *multipart.Form) bool {
	if form == nil {
		return false
	}
	for fieldName, files := range form.File {
		if fieldName != "image" && fieldName != "image[]" && !strings.HasPrefix(fieldName, "image[") {
			continue
		}
		for _, file := range files {
			if file != nil && file.Size > 0 {
				return true
			}
		}
	}
	return false
}

// MultipartParseError preserves the parser error while classifying malformed
// bodies for diagnostics. Image edit requests have a narrowly scoped EOF
// compatibility path when the declared body is complete and only the final
// multipart delimiter is missing.
type MultipartParseError struct {
	Cause               error
	ReceivedBytes       int64
	DeclaredBytes       int64
	BoundaryPresent     bool
	ClosingBoundarySeen bool
	Kind                string
}

func (e *MultipartParseError) Error() string {
	if e == nil {
		return "multipart request parse failed"
	}
	if e.Cause == nil {
		return "multipart request parse failed"
	}
	if e.Kind == "body_truncated" {
		return fmt.Sprintf("multipart request body is incomplete: %v", e.Cause)
	}
	return fmt.Sprintf("multipart request is invalid: %v", e.Cause)
}

func (e *MultipartParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newMultipartParseError(body []byte, boundary string, cause error) *MultipartParseError {
	closingBoundary := []byte("--" + boundary + "--")
	kind := "invalid_multipart"
	if errors.Is(cause, io.ErrUnexpectedEOF) ||
		strings.Contains(cause.Error(), "request body length mismatch") ||
		!bytes.Contains(body, closingBoundary) {
		kind = "body_truncated"
	}
	return &MultipartParseError{
		Cause:               cause,
		ReceivedBytes:       int64(len(body)),
		DeclaredBytes:       -1,
		BoundaryPresent:     bytes.Contains(body, []byte("--"+boundary)),
		ClosingBoundarySeen: bytes.Contains(body, closingBoundary),
		Kind:                kind,
	}
}

// MultipartRequestDiagnostics returns request metadata suitable for server
// logs. It deliberately excludes authorization headers and request contents.
func MultipartRequestDiagnostics(c *gin.Context, err error) string {
	var parseErr *MultipartParseError
	if !errors.As(err, &parseErr) {
		return fmt.Sprintf("kind=invalid_multipart error=%q", err)
	}
	declaredBytes := c.Request.ContentLength
	if parseErr.DeclaredBytes >= 0 {
		declaredBytes = parseErr.DeclaredBytes
	}
	transferEncoding := strings.Join(c.Request.TransferEncoding, ",")
	if transferEncoding == "" {
		transferEncoding = "-"
	}
	userAgent := c.Request.UserAgent()
	if userAgent == "" {
		userAgent = "-"
	}
	return fmt.Sprintf(
		"kind=%s received_bytes=%d declared_bytes=%d boundary_present=%t closing_boundary=%t transfer_encoding=%s content_encoding=%q remote=%q user_agent=%q error=%q",
		parseErr.Kind,
		parseErr.ReceivedBytes,
		declaredBytes,
		parseErr.BoundaryPresent,
		parseErr.ClosingBoundarySeen,
		transferEncoding,
		c.Request.Header.Get("Content-Encoding"),
		c.ClientIP(),
		userAgent,
		err,
	)
}

func processFormMap(formMap map[string]any, v any) error {
	jsonData, err := Marshal(formMap)
	if err != nil {
		return err
	}

	err = Unmarshal(jsonData, v)
	if err != nil {
		return err
	}

	return nil
}

func parseFormData(data []byte, v any) error {
	values, err := url.ParseQuery(string(data))
	if err != nil {
		return err
	}
	formMap := make(map[string]any)
	for key, vals := range values {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}

	return processFormMap(formMap, v)
}

func parseMultipartFormData(c *gin.Context, data []byte, v any) error {
	var contentType string
	if saved, ok := c.Get("_original_multipart_ct"); ok {
		contentType = saved.(string)
	} else {
		contentType = c.Request.Header.Get("Content-Type")
		c.Set("_original_multipart_ct", contentType)
	}
	boundary, err := parseBoundary(contentType)
	if err != nil {
		if errors.Is(err, errBoundaryNotFound) {
			return Unmarshal(data, v) // Fallback to JSON
		}
		return err
	}

	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	form, err := reader.ReadForm(multipartMemoryLimit())
	if err != nil {
		return err
	}
	defer form.RemoveAll()
	formMap := make(map[string]any)
	for key, vals := range form.Value {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}

	return processFormMap(formMap, v)
}

var errBoundaryNotFound = errors.New("multipart boundary not found")

// parseBoundary extracts the multipart boundary from the Content-Type header using mime.ParseMediaType
func parseBoundary(contentType string) (string, error) {
	if contentType == "" {
		return "", errBoundaryNotFound
	}
	// Boundary-UUID / boundary-------xxxxxx
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", err
	}
	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		return "", errBoundaryNotFound
	}
	return boundary, nil
}

// multipartMemoryLimit returns the configured multipart memory limit in bytes
func multipartMemoryLimit() int64 {
	limitMB := constant.MaxFileDownloadMB
	if limitMB <= 0 {
		limitMB = 32
	}
	return int64(limitMB) << 20
}
