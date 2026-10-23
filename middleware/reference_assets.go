package middleware

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const ReferenceAssetsHeader = "X-New-Api-Reference-Assets"

func ReferenceUploadRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(120, 60, "reference-upload")
}

// ResolveReferenceAssets runs after authentication and before model routing,
// quota reservation or async persistence. Owned signed reference URLs are also
// renewed without the opt-in header, including restored retry inputs.
func ResolveReferenceAssets() gin.HandlerFunc {
	return func(c *gin.Context) {
		reuseTaskID := c.GetHeader(service.ImageRetryTaskHeader)
		if !restoreImageTaskInput(c) {
			return
		}
		if reuseTaskID != "" {
			defer func() {
				if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
					if err := service.RenewImageTaskInput(c.GetInt("id"), reuseTaskID); err != nil {
						common.SysError("image input retention renewal failed: " + err.Error())
					}
				}
			}()
		}
		version := c.GetHeader(ReferenceAssetsHeader)
		if version == "" {
			contentType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
			if c.Request.Method == http.MethodPost && (contentType == "application/json" || contentType == "multipart/form-data") {
				storage, err := common.GetBodyStorage(c)
				if err != nil {
					status := http.StatusBadRequest
					if common.IsRequestBodyTooLargeError(err) {
						status = http.StatusRequestEntityTooLarge
					}
					c.AbortWithStatus(status)
					return
				}
				defer common.CleanupBodyStorage(c)
				body, err := storage.Bytes()
				if err != nil {
					c.AbortWithStatus(http.StatusServiceUnavailable)
					return
				}
				body, renewedType, err := service.RefreshReferenceBody(c.GetInt("id"), body, c.GetHeader("Content-Type"))
				if err != nil {
					status, code := http.StatusBadRequest, "reference_invalid"
					if errors.Is(err, service.ErrReferenceMissing) {
						status, code = http.StatusGone, "reference_expired"
						c.Set("reference_recovery_required", true)
					} else if errors.Is(err, service.ErrReferenceUnavailable) {
						status, code = http.StatusServiceUnavailable, "reference_unavailable"
					}
					c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "reference_recovery": c.GetBool("reference_recovery_required"), "message": "reference image is unavailable; prepare or upload it again before generation"}})
					return
				}
				common.CleanupBodyStorage(c)
				c.Set(common.KeyRequestBody, body)
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				c.Request.ContentLength = int64(len(body))
				c.Request.TransferEncoding = nil
				c.Request.Header.Set("Content-Type", renewedType)
				c.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
				c.Request.MultipartForm, c.Request.PostForm, c.Request.Form = nil, nil, nil
			}
			c.Next()
			return
		}
		if version != "1" || c.GetInt("id") <= 0 || c.Request.Method != http.MethodPost {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "reference_invalid", "message": "invalid reference protocol"}})
			return
		}
		if service.ReferenceUploads == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "reference_unavailable", "message": "reference upload acceleration is unavailable"}})
			return
		}
		contentType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		resolver := referenceRequestResolver{user: c.GetInt("id"), store: service.ReferenceUploads}
		var expanded []byte
		switch contentType {
		case "application/json":
			expanded, err = resolver.expandJSON(body, 0)
		case "multipart/form-data":
			var output bytes.Buffer
			writer := multipart.NewWriter(&output)
			reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			for parts := 0; ; parts++ {
				part, partErr := reader.NextPart()
				if partErr == io.EOF {
					break
				}
				if partErr != nil || parts >= 128 {
					err = service.ErrReferenceInvalid
					break
				}
				value, readErr := io.ReadAll(part)
				if readErr != nil || part.FormName() == "" || part.FileName() != "" {
					err = service.ErrReferenceInvalid
					break
				}
				if part.FormName() != "prompt" && part.FormName() != "negative_prompt" && part.FormName() != "text" && strings.HasPrefix(string(value), "newapi-asset://") {
					data, imageMime, _, resolveErr := resolver.resolve(string(value), true)
					if resolveErr != nil {
						err = resolveErr
						break
					}
					header := make(textproto.MIMEHeader)
					header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": part.FormName(), "filename": "reference." + strings.TrimPrefix(imageMime, "image/")}))
					header.Set("Content-Type", imageMime)
					field, createErr := writer.CreatePart(header)
					if createErr != nil {
						err = createErr
						break
					}
					_, err = field.Write(data)
				} else {
					err = writer.WriteField(part.FormName(), string(value))
				}
				if err != nil {
					break
				}
			}
			_ = writer.Close()
			expanded = output.Bytes()
			contentType = writer.FormDataContentType()
		default:
			err = service.ErrReferenceInvalid
		}
		if err != nil {
			status, code := http.StatusBadRequest, "reference_invalid"
			if errors.Is(err, service.ErrReferenceMissing) {
				status, code = http.StatusGone, "reference_expired"
				c.Set("reference_recovery_required", true)
			}
			if errors.Is(err, service.ErrReferenceUnavailable) {
				status, code = http.StatusServiceUnavailable, "reference_unavailable"
			}
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "reference_recovery": c.GetBool("reference_recovery_required"), "message": "reference materialization failed before task submission"}})
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 128
		}
		if int64(len(expanded)) > int64(maxMB)<<20 {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		storage, err := common.CreateBodyStorage(expanded)
		if err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		common.CleanupBodyStorage(c)
		_ = c.Request.Body.Close()
		c.Set(common.KeyRequestBody, nil)
		c.Set(common.KeyBodyStorage, storage)
		defer common.CleanupBodyStorage(c)
		c.Request.Body = io.NopCloser(storage)
		c.Request.ContentLength = int64(len(expanded))
		c.Request.TransferEncoding = nil
		c.Request.Header.Set("Content-Length", strconv.Itoa(len(expanded)))
		c.Request.Header.Set("Content-Type", contentType)
		c.Request.Header.Del(ReferenceAssetsHeader)
		c.Request.MultipartForm, c.Request.PostForm, c.Request.Form = nil, nil, nil
		c.Next()
	}
}

type referenceRequestResolver struct {
	user  int
	store *service.ReferenceUploadStore
	count int
	bytes int
}

func (r *referenceRequestResolver) resolve(value string, file bool) ([]byte, string, string, error) {
	reference := strings.TrimPrefix(value, "newapi-asset://")
	id, mode, _ := strings.Cut(reference, "/")
	if mode == "" {
		mode = "data-url"
	}
	if (file && mode != "data-url") || (mode != "data-url" && mode != "base64" && mode != "url") || r.count >= 16 {
		return nil, "", "", service.ErrReferenceInvalid
	}
	data, imageMime, err := r.store.Read(r.user, id, true)
	if err != nil {
		return nil, "", "", err
	}
	r.count++
	r.bytes += len(data)
	if r.bytes > 64<<20 {
		return nil, "", "", service.ErrReferenceInvalid
	}
	if file {
		return data, imageMime, "", nil
	}
	if mode == "url" {
		address, err := r.store.PublicURL(r.user, id)
		return nil, imageMime, address, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	if mode == "data-url" {
		encoded = "data:" + imageMime + ";base64," + encoded
	}
	return nil, imageMime, encoded, nil
}

func (r *referenceRequestResolver) expandJSON(raw json.RawMessage, depth int) (json.RawMessage, error) {
	if depth > 32 {
		return nil, service.ErrReferenceInvalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, service.ErrReferenceInvalid
	}
	switch raw[0] {
	case '"':
		var value string
		if common.Unmarshal(raw, &value) != nil {
			return nil, service.ErrReferenceInvalid
		}
		if !strings.HasPrefix(value, "newapi-asset://") {
			return raw, nil
		}
		_, _, expanded, err := r.resolve(value, false)
		if err != nil {
			return nil, err
		}
		return common.Marshal(expanded)
	case '{':
		var fields map[string]json.RawMessage
		if common.Unmarshal(raw, &fields) != nil {
			return nil, service.ErrReferenceInvalid
		}
		for key, value := range fields {
			// Literal prompt text is never interpreted as an asset reference.
			if key == "prompt" || key == "negative_prompt" || key == "text" {
				continue
			}
			expanded, err := r.expandJSON(value, depth+1)
			if err != nil {
				return nil, err
			}
			fields[key] = expanded
		}
		return common.Marshal(fields)
	case '[':
		var values []json.RawMessage
		if common.Unmarshal(raw, &values) != nil {
			return nil, service.ErrReferenceInvalid
		}
		for i, value := range values {
			expanded, err := r.expandJSON(value, depth+1)
			if err != nil {
				return nil, err
			}
			values[i] = expanded
		}
		return common.Marshal(values)
	default:
		var scalar json.RawMessage
		if common.Unmarshal(raw, &scalar) != nil {
			return nil, service.ErrReferenceInvalid
		}
		return raw, nil
	}
}
