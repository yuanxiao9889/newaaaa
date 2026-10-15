package common

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const moderationPromptLimit = 32 * 1024
const moderationJSONLimit = 16 * 1024 * 1024

// ModerationPrompt is an administrator-only text snapshot, never a raw request.
// Status makes missing/oversized input distinguishable from an empty prompt.
type ModerationPrompt struct {
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
	Status    string `json:"status"`
}

var moderationCredentialPattern = regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{12,}|\bBearer\s+[a-z0-9._~+/=-]+|\b(?:api[_-]?key|access[_-]?token|authorization|password)\s*[=:]\s*["']?[^\s"',;]+|(?s:-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----)|data:[^\s,]+;base64,[a-z0-9+/=]+`)

// CaptureModerationPrompt reads only an already-cached request, with an
// independent reader so error logging cannot consume or alter a retry body.
// Callers must gate this on a confirmed safety rejection and store it in an
// administrator-only scope. Headers, media, tools and metadata are not copied.
func CaptureModerationPrompt(c *gin.Context) *ModerationPrompt {
	result := &ModerationPrompt{Status: "body_unavailable"}
	if c == nil || c.Request == nil {
		return result
	}
	var reader io.ReadCloser
	var size int64
	if value, exists := c.Get(KeyBodyStorage); exists && value != nil {
		storage, ok := value.(BodyStorage)
		if !ok {
			return result
		}
		var err error
		reader, err = storage.NewReader()
		if err != nil {
			return result
		}
		size = storage.Size()
	} else if value, exists := c.Get(KeyRequestBody); exists {
		if body, ok := value.([]byte); ok {
			reader = io.NopCloser(bytes.NewReader(body))
			size = int64(len(body))
		}
	}
	if reader == nil {
		return result
	}
	defer reader.Close()

	mediaType, params, _ := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		// Skip files without reading them into memory. The input has already
		// passed the gateway's request body limit before reaching this point.
		parts := multipart.NewReader(reader, params["boundary"])
		for {
			part, err := parts.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				result.Status = "invalid_body"
				return result
			}
			if part.FileName() == "" && slices.Contains([]string{"prompt", "negative_prompt"}, part.FormName()) {
				text, err := io.ReadAll(io.LimitReader(part, moderationPromptLimit+1))
				if err != nil {
					result.Status = "invalid_body"
					return result
				}
				if len(text) > moderationPromptLimit {
					result.Truncated = true
					text = text[:moderationPromptLimit]
				}
				result.appendText(c, part.FormName(), strings.ToValidUTF8(string(text), ""))
			}
			_ = part.Close()
		}
	} else {
		// Do not allocate another unbounded copy of a multimodal JSON body.
		if size > moderationJSONLimit {
			result.Status = "body_too_large"
			return result
		}
		var body map[string]any
		if err := DecodeJson(io.LimitReader(reader, moderationJSONLimit+1), &body); err != nil {
			result.Status = "invalid_body"
			return result
		}
		for _, key := range []string{"prompt", "negative_prompt", "instructions", "system"} {
			result.appendContent(c, key, body[key])
		}
		if input, ok := body["input"].(string); ok {
			result.appendText(c, "input", input)
		}
		for _, key := range []string{"messages", "contents", "input"} {
			items, _ := body[key].([]any)
			for index, item := range items {
				message, ok := item.(map[string]any)
				if !ok {
					continue
				}
				// Responses tool calls/results may contain credentials; retain
				// only message items, not arbitrary input objects.
				kind, _ := message["type"].(string)
				role, _ := message["role"].(string)
				if (kind != "" && kind != "message") || !slices.Contains([]string{"", "system", "developer", "user", "assistant", "model"}, role) {
					continue
				}
				label := fmt.Sprintf("%s[%d]", key, index)
				if role != "" {
					label += " (" + role + ")"
				}
				result.appendContent(c, label, message["content"])
				result.appendContent(c, label, message["parts"])
			}
		}
		for _, key := range []string{"systemInstruction", "system_instruction"} {
			if instruction, ok := body[key].(map[string]any); ok {
				result.appendContent(c, key, instruction["parts"])
			}
		}
	}
	result.Status = "captured"
	if result.Text == "" {
		result.Status = "no_text"
	}
	return result
}

func (p *ModerationPrompt) appendContent(c *gin.Context, label string, content any) {
	if text, ok := content.(string); ok {
		p.appendText(c, label, text)
		return
	}
	parts, _ := content.([]any)
	for _, part := range parts {
		if item, ok := part.(map[string]any); ok {
			kind, _ := item["type"].(string)
			if kind == "" || kind == "text" || kind == "input_text" || kind == "output_text" {
				if text, ok := item["text"].(string); ok {
					p.appendText(c, label, text)
				}
			}
		}
	}
}

func (p *ModerationPrompt) appendText(c *gin.Context, label, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if len(p.Text) >= moderationPromptLimit {
		p.Truncated = true
		return
	}
	// Remove known request credentials even if pasted into a text prompt.
	for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		secret := strings.TrimSpace(c.Request.Header.Get(header))
		if header == "Authorization" {
			_, secret, _ = strings.Cut(secret, " ")
		}
		if len(secret) >= 8 && strings.Contains(text, secret) {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
			p.Redacted = true
		}
	}
	redacted := moderationCredentialPattern.ReplaceAllString(text, "[REDACTED]")
	p.Redacted = p.Redacted || redacted != text
	text = "[" + label + "]\n" + redacted + "\n\n"
	remaining := moderationPromptLimit - len(p.Text)
	if len(text) > remaining {
		text = text[:remaining]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		p.Truncated = true
	}
	p.Text += text
}
