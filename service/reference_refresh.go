package service

import (
	"bytes"
	"crypto/hmac"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// RenewReferenceURL renews only this authenticated owner's retained reference.
// An expired download signature is not authorization: ownership and the original
// signature must both match. Public downloads still enforce the original expiry.
// External URLs are never fetched or signed here.
func RenewReferenceURL(user int, address string) (string, error) {
	if !strings.Contains(address, "/v1/uploads/references/content/") {
		return address, nil
	}
	parsed, err := url.Parse(address)
	if err != nil || !strings.Contains(parsed.Path, "/v1/uploads/references/content/") {
		return address, nil
	}
	origin, err := url.Parse(strings.TrimRight(system_setting.ServerAddress, "/"))
	if err != nil || origin.Host == "" || !strings.EqualFold(parsed.Host, origin.Host) {
		return address, nil
	}
	prefix := strings.TrimRight(origin.Path, "/") + "/v1/uploads/references/content/"
	identity, ok := strings.CutPrefix(parsed.Path, prefix)
	owner, id, found := strings.Cut(identity, "/")
	if !ok || !found || user <= 0 || owner != strconv.Itoa(user) || !validReferenceID(id) ||
		parsed.Scheme != "https" || origin.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", ErrReferenceInvalid
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 2 || len(query["e"]) != 1 || len(query["token"]) != 1 {
		return "", ErrReferenceInvalid
	}
	expires := query.Get("e")
	if strings.HasPrefix(expires, "task:") {
		if ReferenceUploads == nil || !ReferenceUploads.VerifyContent(user, id, expires, query.Get("token")) {
			return "", ErrReferenceMissing
		}
		return address, nil
	}
	if expiry, err := strconv.ParseInt(expires, 10, 64); err != nil || expiry <= 0 {
		return "", ErrReferenceInvalid
	}
	store := ReferenceUploads
	if store == nil {
		return "", ErrReferenceUnavailable
	}
	// PublicURL signs the API path independently of a deployment base path.
	path := "/v1/uploads/references/content/" + owner + "/" + id
	if !hmac.Equal([]byte(query.Get("token")), []byte(store.sign("content:"+path+":"+expires))) {
		return "", ErrReferenceInvalid
	}
	return store.PublicURL(user, id)
}

// RefreshReferenceBody is shared by initial submission, saved-input replay and
// worker attempts. It preserves unrelated bodies and literal prompt text, and
// refuses missing assets before another upstream generation can be submitted.
func RefreshReferenceBody(user int, body []byte, contentType string) ([]byte, string, error) {
	refresh := referenceURLRefresh{user: user, renewed: make(map[string]string)}
	return refresh.body(body, contentType)
}

func RefreshTaskReferenceBody(task *model.Task, body []byte) ([]byte, string, error) {
	refresh := referenceURLRefresh{user: task.UserId, renewed: make(map[string]string), task: task}
	return refresh.body(body, task.PrivateData.RequestContentType)
}

func (refresh *referenceURLRefresh) body(body []byte, contentType string) ([]byte, string, error) {
	if len(body) == 0 {
		return body, contentType, nil
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return body, contentType, nil // The endpoint's validator owns malformed input.
	}
	switch mediaType {
	case "application/json":
		body, _, err = refresh.json(body, 0)
		return body, contentType, err
	case "multipart/form-data":
		if !bytes.Contains(body, []byte("/v1/uploads/references/content/")) {
			return body, contentType, nil
		}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		var output bytes.Buffer
		writer := multipart.NewWriter(&output)
		changed := false
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, contentType, ErrReferenceInvalid
			}
			value, err := io.ReadAll(part)
			if err != nil {
				return nil, contentType, err
			}
			name := part.FormName()
			if part.FileName() == "" && name != "prompt" && name != "negative_prompt" && name != "text" {
				original := string(value)
				if strings.HasPrefix(original, "https://") || strings.HasPrefix(original, "http://") {
					renewed, err := refresh.url(original)
					if err != nil {
						return nil, contentType, err
					}
					value = []byte(renewed)
				} else if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) || bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
					value, _, err = refresh.json(value, 0)
					if err != nil {
						return nil, contentType, err
					}
				}
				changed = changed || string(value) != original
			}
			field, err := writer.CreatePart(part.Header)
			if err != nil {
				return nil, contentType, err
			}
			if _, err := field.Write(value); err != nil {
				return nil, contentType, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, contentType, err
		}
		if changed {
			return output.Bytes(), writer.FormDataContentType(), nil
		}
	}
	return body, contentType, nil
}

type referenceURLRefresh struct {
	task    *model.Task
	user    int
	renewed map[string]string
}

func (r *referenceURLRefresh) url(value string) (string, error) {
	if renewed, ok := r.renewed[value]; ok {
		return renewed, nil
	}
	if len(r.renewed) >= 16 && strings.Contains(value, "/v1/uploads/references/content/") {
		return "", ErrReferenceInvalid
	}
	renewed, err := RenewReferenceURL(r.user, value)
	if err == nil && r.task != nil && ReferenceUploads != nil {
		parsed, parseErr := url.Parse(renewed)
		origin, _ := url.Parse(system_setting.ServerAddress)
		if parseErr == nil && origin != nil && strings.EqualFold(parsed.Host, origin.Host) {
			for _, id := range r.task.PrivateData.ReferenceAssetIDs {
				if strings.HasSuffix(parsed.Path, "/v1/uploads/references/content/"+strconv.Itoa(r.user)+"/"+id) {
					expires := "task:" + r.task.TaskID
					path := "/v1/uploads/references/content/" + strconv.Itoa(r.user) + "/" + id
					query := url.Values{"e": {expires}, "token": {ReferenceUploads.sign("content:" + path + ":" + expires)}}
					parsed.RawQuery = query.Encode()
					renewed = parsed.String()
					break
				}
			}
		}
	}
	if err == nil && strings.Contains(value, "/v1/uploads/references/content/") {
		r.renewed[value] = renewed
	}
	return renewed, err
}

func (r *referenceURLRefresh) json(raw json.RawMessage, depth int) (json.RawMessage, bool, error) {
	if depth > 64 {
		return nil, false, ErrReferenceInvalid
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false, nil
	}
	changed := false
	switch trimmed[0] {
	case '"':
		var value string
		if common.Unmarshal(raw, &value) != nil {
			return raw, false, nil
		}
		renewed, err := r.url(value)
		if err != nil || renewed == value {
			return raw, false, err
		}
		encoded, err := common.Marshal(renewed)
		return encoded, true, err
	case '{':
		var fields map[string]json.RawMessage
		if common.Unmarshal(raw, &fields) != nil {
			return raw, false, nil
		}
		for key, value := range fields {
			if key == "prompt" || key == "negative_prompt" || key == "text" {
				continue
			}
			renewed, updated, err := r.json(value, depth+1)
			if err != nil {
				return nil, false, err
			}
			fields[key], changed = renewed, changed || updated
		}
		if changed {
			encoded, err := common.Marshal(fields)
			return encoded, true, err
		}
	case '[':
		var values []json.RawMessage
		if common.Unmarshal(raw, &values) != nil {
			return raw, false, nil
		}
		for i, value := range values {
			renewed, updated, err := r.json(value, depth+1)
			if err != nil {
				return nil, false, err
			}
			values[i], changed = renewed, changed || updated
		}
		if changed {
			encoded, err := common.Marshal(values)
			return encoded, true, err
		}
	}
	return raw, false, nil
}
