package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

var domesticFilename = regexp.MustCompile(`^storyboard-(?:resumable-[a-f0-9]{32}|seedance-[a-f0-9-]{36})\.[a-z0-9]{2,5}$`)
var ErrDomesticReferenceMissing = errors.New("domestic reference missing")

func DomesticTaskKey() []byte {
	value := os.Getenv("DOMESTIC_TASK_SECRET")
	if value == "" {
		data, _ := os.ReadFile("/data/domestic-task-secret")
		value = strings.TrimSpace(string(data))
	}
	key, err := hex.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}

func DomesticTaskSignature(path, timestamp string, body []byte) string {
	key := DomesticTaskKey()
	if len(key) == 0 {
		return ""
	}
	hash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "domestic-task-v1\nPOST\n"+path+"\n"+timestamp+"\n"+hex.EncodeToString(hash[:]))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyDomesticTaskRequest(r *http.Request, body []byte) bool {
	stamp := r.Header.Get("X-Domestic-Time")
	when, err := strconv.ParseInt(stamp, 10, 64)
	now := time.Now().Unix()
	if err != nil || when < now-60 || when > now+60 {
		return false
	}
	expected := DomesticTaskSignature(r.URL.Path, stamp, body)
	return expected != "" && hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Domestic-Signature")))
}

// Only exact managed public URLs are eligible. Arbitrary text/hosts never cause a fetch.
func DomesticReferenceFiles(body []byte) []string {
	var value any
	if common.Unmarshal(body, &value) != nil {
		return nil
	}
	files := make([]string, 0)
	pending := []any{value}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		switch v := current.(type) {
		case map[string]any:
			for _, item := range v {
				pending = append(pending, item)
			}
		case []any:
			pending = append(pending, v...)
		case string:
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || !slices.Contains([]string{"www.oopii.cn", "oopii.cn"}, u.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				continue
			}
			name, ok := strings.CutPrefix(u.Path, "/seedance-reference/")
			if ok && domesticFilename.MatchString(name) && !slices.Contains(files, name) {
				files = append(files, name)
			}
		}
	}
	slices.Sort(files)
	return files
}

// The domestic store commits its hold before any upstream POST or quota reservation.
func HoldDomesticReferences(ctx context.Context, executionID string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	if len(files) > 32 || len(DomesticTaskKey()) == 0 {
		return errors.New("domestic task retention unavailable")
	}
	body, err := common.Marshal(map[string]any{"execution_id": executionID, "files": files})
	if err != nil {
		return err
	}
	const path = "/api/seedance/reference-media/resumable/task-hold"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.oopii.cn"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Domestic-Time", stamp)
	req.Header.Set("X-Domestic-Signature", DomesticTaskSignature(path, stamp, body))
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("domestic task retention unavailable")
	}
	defer response.Body.Close()
	var result struct {
		Held  bool   `json:"held"`
		Error string `json:"error"`
	}
	if common.DecodeJson(io.LimitReader(response.Body, 4096), &result) != nil {
		return errors.New("invalid domestic retention response")
	}
	if response.StatusCode == 410 && result.Error == "reference_expired" {
		return ErrDomesticReferenceMissing
	}
	if response.StatusCode != 200 || !result.Held {
		return errors.New("domestic task retention unavailable")
	}
	return nil
}
