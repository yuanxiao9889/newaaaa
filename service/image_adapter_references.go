package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/idna"
)

var imageAdapterReferenceFields = []string{
	"image", "images", "image_url", "image_urls", "reference_images", "input_reference", "content",
}

type imageAdapterReference struct {
	value string
	file  *multipart.FileHeader
}

// PrepareImageAdapterReferences preserves reference order and converts local
// images into expiring public URLs. Remote references are validated, never fetched.
func PrepareImageAdapterReferences(c *gin.Context, request *dto.ImageRequest, config *dto.ImageAdapterReferenceConfig) ([]string, error) {
	if config == nil {
		return nil, nil
	}
	if config.MaxImages < 1 || config.MaxImages > 16 {
		return nil, errors.New("image reference limit must be between 1 and 16")
	}
	if request == nil {
		return nil, errors.New("image request is required")
	}
	references, err := collectImageAdapterReferences(c, request, config.MaxImages)
	if err != nil || len(references) == 0 {
		return nil, err
	}
	if len(references) > config.MaxImages {
		return nil, fmt.Errorf("image references exceed the configured limit of %d", config.MaxImages)
	}
	var totalBytes int64
	localIndexes := make([]int, 0, len(references))
	for index, reference := range references {
		if reference.file != nil {
			if reference.file.Size <= 0 || reference.file.Size > midjourneyUploadMaxBytes {
				return nil, fmt.Errorf("image reference %d must contain 1 to %d bytes", index+1, midjourneyUploadMaxBytes)
			}
			totalBytes += reference.file.Size
		}
		if reference.file != nil || strings.HasPrefix(reference.value, "data:") {
			if !config.UploadLocal {
				return nil, errors.New("local image references require upload_local to be enabled")
			}
			localIndexes = append(localIndexes, index)
			continue
		}
		if err := validateImageAdapterPublicURL(reference.value); err != nil {
			return nil, fmt.Errorf("image reference %d must be a public HTTP or HTTPS URL without credentials", index+1)
		}
	}
	if totalBytes > int64(config.MaxImages)*midjourneyUploadMaxBytes {
		return nil, errors.New("total image reference size exceeds the configured limit")
	}
	if len(localIndexes) > 0 {
		base := strings.TrimSpace(system_setting.ServerAddress)
		parsed, parseErr := url.Parse(base)
		if parseErr != nil || validateImageAdapterPublicURL(base) != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return nil, errors.New("local image references require a public HTTP or HTTPS ServerAddress without credentials, query or fragment")
		}
	}
	// Validate every image before storing any image; an invalid later reference
	// must not leave uploads behind. File size is checked before opening files.
	for _, index := range localIndexes {
		reference := &references[index]
		if reference.file != nil {
			file, err := reference.file.Open()
			if err != nil {
				return nil, fmt.Errorf("image reference %d could not be opened", index+1)
			}
			data, readErr := io.ReadAll(io.LimitReader(file, midjourneyUploadMaxBytes+1))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil || len(data) == 0 || len(data) > midjourneyUploadMaxBytes {
				return nil, fmt.Errorf("image reference %d could not be read within the image size limit", index+1)
			}
			_, format, decodeErr := decodeImageConfig(data)
			if decodeErr != nil || !slices.Contains([]string{"png", "jpeg", "webp"}, format) {
				return nil, fmt.Errorf("image reference %d must contain a PNG, JPEG or WebP image", index+1)
			}
			reference.value = "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data)
			// Account for the real content rather than trusting FileHeader.Size.
			totalBytes += int64(len(data)) - reference.file.Size
		} else {
			data, _, err := decodeMidjourneyUpload(reference.value)
			if err != nil {
				return nil, fmt.Errorf("image reference %d must contain a valid PNG, JPEG or WebP data URL within the image size limit", index+1)
			}
			totalBytes += int64(len(data))
		}
		if totalBytes > int64(config.MaxImages)*midjourneyUploadMaxBytes {
			return nil, errors.New("total image reference size exceeds the configured limit")
		}
	}
	urls := make([]string, len(references))
	userID := 0
	if c != nil {
		userID = c.GetInt("id")
	}
	for index, reference := range references {
		// Recheck immediately before dispatch as well as during admission: a
		// synchronous retry may keep its parsed DTO longer than the URL lifetime.
		address, err := RenewReferenceURL(userID, reference.value)
		if err != nil {
			return nil, fmt.Errorf("image reference %d is unavailable; prepare or upload it again: %w", index+1, err)
		}
		urls[index] = address
	}
	// Reuse the existing storage lifecycle without changing its four-image API.
	for start := 0; start < len(localIndexes); start += midjourneyUploadMaxImages {
		batch := localIndexes[start:min(start+midjourneyUploadMaxImages, len(localIndexes))]
		values := make([]string, len(batch))
		for index, referenceIndex := range batch {
			values[index] = references[referenceIndex].value
		}
		stored, err := StoreMidjourneyImages(values)
		if err != nil {
			return nil, errors.New("failed to store image references")
		}
		for index, referenceIndex := range batch {
			urls[referenceIndex] = stored.URLs[index]
		}
	}
	return urls, nil
}

func collectImageAdapterReferences(c *gin.Context, request *dto.ImageRequest, limit int) ([]imageAdapterReference, error) {
	if c != nil && c.Request != nil && (c.Request.MultipartForm != nil || strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data")) {
		form := c.Request.MultipartForm
		if form == nil {
			var err error
			form, err = common.ParseMultipartFormReusable(c)
			if err != nil {
				return nil, errors.New("failed to parse image reference multipart form")
			}
			c.Request.MultipartForm = form
		}
		return collectImageAdapterMultipartReferences(form, limit)
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, errors.New("failed to encode image references")
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(encoded, &fields); err != nil {
		return nil, errors.New("failed to parse image references")
	}
	// ImageRequest deliberately does not marshal Extra; only recover reference
	// fields, leaving unrelated client extensions outside this conversion.
	for _, field := range imageAdapterReferenceFields {
		if _, exists := fields[field]; !exists {
			fields[field] = request.Extra[field]
		}
	}
	var references []imageAdapterReference
	for _, field := range imageAdapterReferenceFields {
		raw := fields[field]
		if len(raw) == 0 {
			continue
		}
		var value any
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, errors.New("invalid JSON image references")
		}
		values, err := parseImageAdapterReferenceValue(value, limit, 0)
		if err != nil {
			return nil, err
		}
		if len(values) > 0 && len(references) > 0 {
			return nil, errors.New("use only one image reference field to keep reference order unambiguous")
		}
		for _, value := range values {
			references = append(references, imageAdapterReference{value: value})
		}
	}
	return references, nil
}

func collectImageAdapterMultipartReferences(form *multipart.Form, limit int) ([]imageAdapterReference, error) {
	keys := make(map[string]bool)
	for key, files := range form.File {
		if len(files) > 0 {
			keys[key] = true
		}
	}
	for key, values := range form.Value {
		if len(values) > 0 {
			keys[key] = true
		}
	}
	style := ""
	ordered := make([]string, 0)
	for key := range keys {
		keyStyle := key
		if strings.HasPrefix(key, "image[") && key != "image[]" {
			digits, ok := strings.CutSuffix(strings.TrimPrefix(key, "image["), "]")
			index, err := strconv.Atoi(digits)
			if !ok || err != nil || index < 0 || strconv.Itoa(index) != digits {
				return nil, errors.New("indexed image fields must use non-negative decimal indexes")
			}
			keyStyle = "image[index]"
		} else if key != "image[]" && !slices.Contains(imageAdapterReferenceFields, key) {
			continue
		}
		if style != "" && style != keyStyle {
			return nil, errors.New("use only one multipart image reference field style to keep reference order unambiguous")
		}
		style = keyStyle
		ordered = append(ordered, key)
	}
	if style == "image[index]" {
		slices.SortFunc(ordered, func(a, b string) int {
			aIndex, _ := strconv.Atoi(a[6 : len(a)-1])
			bIndex, _ := strconv.Atoi(b[6 : len(b)-1])
			if aIndex < bIndex {
				return -1
			}
			if aIndex > bIndex {
				return 1
			}
			return 0
		})
	}
	var references []imageAdapterReference
	for _, key := range ordered {
		files, values := form.File[key], form.Value[key]
		if len(files) > 0 && len(values) > 0 || style == "image[index]" && len(files)+len(values) != 1 {
			return nil, errors.New("multipart image references have ambiguous file and value ordering")
		}
		if len(files) > limit-len(references) {
			return nil, fmt.Errorf("image references exceed the configured limit of %d", limit)
		}
		for _, file := range files {
			references = append(references, imageAdapterReference{file: file})
		}
		for _, value := range values {
			var decoded any = value
			if trimmed := strings.TrimSpace(value); strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "\"") {
				if err := common.UnmarshalJsonStr(trimmed, &decoded); err != nil {
					return nil, errors.New("invalid JSON in multipart image references")
				}
			}
			parsed, err := parseImageAdapterReferenceValue(decoded, limit-len(references), 0)
			if err != nil {
				return nil, err
			}
			for _, value := range parsed {
				references = append(references, imageAdapterReference{value: value})
			}
		}
		if len(references) > limit {
			return nil, fmt.Errorf("image references exceed the configured limit of %d", limit)
		}
	}
	return references, nil
}

func parseImageAdapterReferenceValue(value any, limit, depth int) ([]string, error) {
	if depth > 4 {
		return nil, errors.New("image reference nesting exceeds the supported depth")
	}
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		if limit < 1 {
			return nil, errors.New("image references exceed the configured limit")
		}
		return []string{strings.TrimSpace(typed)}, nil
	case []any:
		var references []string
		for _, item := range typed {
			values, err := parseImageAdapterReferenceValue(item, limit-len(references), depth+1)
			if err != nil {
				return nil, err
			}
			references = append(references, values...)
		}
		return references, nil
	case map[string]any:
		var reference any
		found := false
		for _, key := range []string{"url", "image_url"} {
			if candidate, exists := typed[key]; exists {
				if found {
					return nil, errors.New("image reference object must contain only one URL field")
				}
				reference, found = candidate, true
			}
		}
		if found {
			return parseImageAdapterReferenceValue(reference, limit, depth+1)
		}
	}
	return nil, errors.New("image references must be URLs, data URLs, arrays or image URL objects")
}

func validateImageAdapterPublicURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Opaque != "" {
		return errors.New("invalid public image URL")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if net.ParseIP(host) == nil {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return errors.New("image URL has an invalid host")
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
	}
	if strings.ContainsAny(host, "%\\") || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("image URL must use a public host")
	}
	protection := &common.SSRFProtection{}
	if ip := net.ParseIP(host); ip != nil {
		if !protection.IsIPAccessAllowed(ip) {
			return errors.New("image URL must use a public IP address")
		}
	} else {
		if !strings.Contains(host, ".") {
			return errors.New("image URL must use a public host")
		}
		// Reject noncanonical IPv4 spellings that some upstream downloaders
		// interpret as loopback/private addresses rather than domain names.
		numeric := true
		for label := range strings.SplitSeq(host, ".") {
			if _, err := strconv.ParseUint(label, 0, 32); err != nil {
				if _, decimalErr := strconv.ParseUint(label, 10, 32); decimalErr != nil {
					numeric = false
					break
				}
			}
		}
		if numeric {
			return errors.New("image URL must use a canonical public IP address")
		}
	}
	// No DNS or HTTP request is needed: the gateway does not fetch these URLs.
	// Reuse the shared literal-IP safety rules without configurable private access.
	return protection.ValidateURL(raw)
}
