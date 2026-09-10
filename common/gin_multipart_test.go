package common

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseMultipartFormReusableReportsTruncatedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("image bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	imageOffset := bytes.Index(completeBody, []byte("image bytes"))
	require.Greater(t, imageOffset, 0)
	truncatedBody := completeBody[:imageOffset]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(truncatedBody))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = ParseMultipartFormReusable(c)
	require.Error(t, err)
	var parseErr *MultipartParseError
	require.True(t, errors.As(err, &parseErr))
	require.Equal(t, "body_truncated", parseErr.Kind)
	require.Equal(t, int64(len(truncatedBody)), parseErr.ReceivedBytes)
	require.False(t, parseErr.ClosingBoundarySeen)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestParseMultipartFormReusableRepairsImageEditMissingClosingBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("complete image payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	closingLength := len("--" + writer.Boundary() + "--\r\n")
	truncatedBody := completeBody[:len(completeBody)-closingLength]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits?async=true", bytes.NewReader(truncatedBody))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.True(t, c.GetBool("multipart_eof_repaired"))
	require.Equal(t, int64(len(completeBody)), c.Request.ContentLength)
	require.Equal(t, strconv.Itoa(len(completeBody)), c.Request.Header.Get("Content-Length"))
	require.Equal(t, "gpt-image-1", form.Value["model"][0])
	require.Len(t, form.File["image"], 1)
	require.Equal(t, int64(len("complete image payload")), form.File["image"][0].Size)

	storage, err := GetBodyStorage(c)
	require.NoError(t, err)
	require.Equal(t, int64(len(completeBody)), storage.Size())
	replayed, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, completeBody, replayed)
}

func TestParseMultipartFormReusableDoesNotRepairDeclaredLengthMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("complete image payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	closingLength := len("--" + writer.Boundary() + "--\r\n")
	truncatedBody := completeBody[:len(completeBody)-closingLength]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(truncatedBody))
	c.Request.ContentLength++
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = ParseMultipartFormReusable(c)
	require.Error(t, err)
	require.False(t, c.GetBool("multipart_eof_repaired"))
}

func TestParseMultipartFormReusableDoesNotRepairUnknownLengthBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("complete image payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	closingLength := len("--" + writer.Boundary() + "--\r\n")
	truncatedBody := completeBody[:len(completeBody)-closingLength]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(truncatedBody))
	c.Request.ContentLength = -1
	c.Request.Header.Set("Content-Length", "")
	c.Request.Header.Set("Transfer-Encoding", "chunked")
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = ParseMultipartFormReusable(c)
	require.Error(t, err)
	require.False(t, c.GetBool("multipart_eof_repaired"))
}

func TestParseMultipartFormReusableDoesNotRepairNonImageMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "whisper-1"))
	part, err := writer.CreateFormFile("file", "input.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte("audio payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	closingLength := len("--" + writer.Boundary() + "--\r\n")
	truncatedBody := completeBody[:len(completeBody)-closingLength]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(truncatedBody))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = ParseMultipartFormReusable(c)
	require.Error(t, err)
	require.False(t, c.GetBool("multipart_eof_repaired"))
}

func TestParseMultipartFormReusableDoesNotRepairEmptyImage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	_, err := writer.CreateFormFile("image", "empty.png")
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	completeBody := body.Bytes()
	closingLength := len("--" + writer.Boundary() + "--\r\n")
	truncatedBody := completeBody[:len(completeBody)-closingLength]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(truncatedBody))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = ParseMultipartFormReusable(c)
	require.Error(t, err)
	require.False(t, c.GetBool("multipart_eof_repaired"))
}

func TestMultipartRequestDiagnosticsExcludesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "test-client/1.0")
	c.Request.Header.Set("Content-Encoding", "")

	err := &MultipartParseError{
		Cause:               io.ErrUnexpectedEOF,
		ReceivedBytes:       42,
		DeclaredBytes:       99,
		BoundaryPresent:     true,
		ClosingBoundarySeen: false,
		Kind:                "body_truncated",
	}
	diagnostics := MultipartRequestDiagnostics(c, err)
	require.Contains(t, diagnostics, "kind=body_truncated")
	require.Contains(t, diagnostics, "received_bytes=42")
	require.Contains(t, diagnostics, "user_agent=\"test-client/1.0\"")
	require.NotContains(t, diagnostics, "Authorization")
	require.NotContains(t, diagnostics, "image bytes")
}
