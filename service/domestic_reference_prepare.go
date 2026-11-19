package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// The error contains a fixed code, never a signed URL or a remote error body.
type DomesticPreparationError struct{ Code string }

func (e *DomesticPreparationError) Error() string { return e.Code }

type domesticPreparedReference struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MIME      string `json:"mime"`
	ReadyAt   int64  `json:"ready_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// The original body is hashed by TransferRequestGuard before this substitution.
// Only a trusted, authenticated metadata service may supply replacement URLs.
func PrepareDomesticReferenceBody(ctx context.Context, user int, execution string, original []byte, files []string, contentType string) ([]byte, error) {
	if len(files) == 0 {
		return original, nil
	}
	if os.Getenv("DOMESTIC_REFERENCE_PREPARE_ENABLED") == "false" {
		return original, HoldDomesticReferences(ctx, execution, files)
	}
	if len(files) > 32 || len(DomesticTaskKey()) == 0 || user <= 0 {
		return nil, &DomesticPreparationError{Code: "reference_unavailable"}
	}
	for _, name := range files {
		if !domesticFilename.MatchString(name) {
			return nil, &DomesticPreparationError{Code: "reference_invalid"}
		}
	}
	const path = "/api/seedance/reference-media/resumable/task-prepare"
	body, err := common.Marshal(map[string]any{"execution_id": execution, "user_id": user, "files": files})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.oopii.cn"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Domestic-Time", stamp)
	req.Header.Set("X-Domestic-Signature", DomesticTaskSignature(path, stamp, body))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, &DomesticPreparationError{Code: "reference_unavailable"}
	}
	defer response.Body.Close()
	var result struct {
		Ready      bool                                 `json:"ready"`
		Error      string                               `json:"error"`
		References map[string]domesticPreparedReference `json:"references"`
	}
	if common.DecodeJson(io.LimitReader(response.Body, 64<<10), &result) != nil {
		return nil, &DomesticPreparationError{Code: "reference_unavailable"}
	}
	if response.StatusCode == 410 && result.Error == "reference_expired" {
		return nil, ErrDomesticReferenceMissing
	}
	if response.StatusCode != http.StatusOK || !result.Ready {
		code := "reference_unavailable"
		switch result.Error {
		case "reference_preparing", "reference_integrity", "reference_forbidden", "reference_rate_limited", "reference_failed":
			code = result.Error
		}
		return nil, &DomesticPreparationError{Code: code}
	}
	if len(result.References) != len(files) {
		return nil, errors.New("invalid reference receipt")
	}
	replacements := make(map[string]string, len(files)*2)
	for _, name := range files {
		r := result.References[name]
		u, parseErr := url.Parse(r.URL)
		hash, hashErr := hex.DecodeString(r.SHA256)
		if parseErr != nil || u.Scheme != "https" || u.Host != "refs.oopii.cn" || u.User != nil || u.Fragment != "" ||
			!strings.HasPrefix(u.Path, "/video-reference/") || strings.Contains(u.Path, "..") || u.RawPath != "" ||
			u.Query().Get("token") == "" || u.Query().Get("e") != strconv.FormatInt(r.ExpiresAt, 10) ||
			hashErr != nil || len(hash) != 32 || r.Size <= 0 || r.MIME == "" || r.ReadyAt <= 0 || r.ReadyAt > time.Now().Unix()+60 ||
			r.ExpiresAt < time.Now().Add(24*time.Hour).Unix() {
			return nil, &DomesticPreparationError{Code: "reference_integrity"}
		}
		for _, host := range []string{"www.oopii.cn", "oopii.cn"} {
			replacements["https://"+host+"/seedance-reference/"+name] = r.URL
		}
	}
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType != "multipart/form-data" {
		return replaceDomesticReferenceJSON(original, replacements)
	}
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	if err := writer.SetBoundary(params["boundary"]); err != nil {
		return nil, err
	}
	reader := multipart.NewReader(bytes.NewReader(original), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		if part.FileName() == "" && part.FormName() != "prompt" && part.FormName() != "negative_prompt" && part.FormName() != "text" {
			if replacement, ok := replacements[string(data)]; ok {
				data = []byte(replacement)
			} else if common.GetJsonType(data) == "object" || common.GetJsonType(data) == "array" {
				data, err = replaceDomesticReferenceJSON(data, replacements)
				if err != nil {
					return nil, err
				}
			}
		}
		field, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if _, err = field.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// RawMessage preserves number precision, explicit zero, array positions and
// prompt text. No URL query is stripped to infer a content identity.
func replaceDomesticReferenceJSON(raw []byte, replacements map[string]string) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("invalid reference request")
	}
	switch raw[0] {
	case '{':
		var fields map[string]common.RawMessage
		if err := common.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for key, value := range fields {
			if key == "prompt" || key == "negative_prompt" || key == "text" {
				continue
			}
			replaced, err := replaceDomesticReferenceJSON(value, replacements)
			if err != nil {
				return nil, err
			}
			fields[key] = replaced
		}
		return common.Marshal(fields)
	case '[':
		var values []common.RawMessage
		if err := common.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		for i, value := range values {
			replaced, err := replaceDomesticReferenceJSON(value, replacements)
			if err != nil {
				return nil, err
			}
			values[i] = replaced
		}
		return common.Marshal(values)
	case '"':
		var value string
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if replacement, ok := replacements[value]; ok {
			return common.Marshal(replacement)
		}
	}
	return raw, nil
}

// Multipart fields are inspected without interpreting uploaded file bytes.
func DomesticReferenceFilesContent(body []byte, contentType string) []string {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType != "multipart/form-data" {
		return DomesticReferenceFiles(body)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var values []any
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		if part.FileName() != "" || part.FormName() == "prompt" || part.FormName() == "negative_prompt" || part.FormName() == "text" {
			continue
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil
		}
		var value any
		if common.Unmarshal(data, &value) == nil {
			values = append(values, value)
		} else {
			values = append(values, string(data))
		}
	}
	encoded, _ := common.Marshal(values)
	return DomesticReferenceFiles(encoded)
}
