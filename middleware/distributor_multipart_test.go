package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetModelFromRequestMultipartReadsTextModelAndRestoresBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "edit this image"))
	require.NoError(t, writer.WriteField("model", "  gpt-image-1  "))
	imagePart, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("fake image"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	originalBody := append([]byte(nil), body.Bytes()...)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(originalBody))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType()+"; charset=utf-8")

	modelRequest, err := getModelFromRequest(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-1", modelRequest.Model)
	require.NotNil(t, c.Request.MultipartForm)
	require.Len(t, c.Request.MultipartForm.File["image"], 1)
	defer c.Request.MultipartForm.RemoveAll()

	replayedBody, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, originalBody, replayedBody)
}

func TestGetModelFromRequestMultipartRejectsMissingEmptyOrFileModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		writeBody func(t *testing.T, writer *multipart.Writer)
		wantError string
	}{
		{
			name: "missing model",
			writeBody: func(t *testing.T, writer *multipart.Writer) {
				require.NoError(t, writer.WriteField("prompt", "edit this image"))
			},
			wantError: "multipart field model is required",
		},
		{
			name: "empty model",
			writeBody: func(t *testing.T, writer *multipart.Writer) {
				require.NoError(t, writer.WriteField("model", "   "))
			},
			wantError: "multipart field model is required",
		},
		{
			name: "model is a file part",
			writeBody: func(t *testing.T, writer *multipart.Writer) {
				part, err := writer.CreateFormFile("model", "model.txt")
				require.NoError(t, err)
				_, err = part.Write([]byte("gpt-image-1"))
				require.NoError(t, err)
			},
			wantError: "multipart field model must be a text field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			tt.writeBody(t, writer)
			require.NoError(t, writer.Close())

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())

			_, err := getModelFromRequest(c)
			require.EqualError(t, err, tt.wantError)
			if c.Request.MultipartForm != nil {
				c.Request.MultipartForm.RemoveAll()
			}
		})
	}
}

func TestGetModelRequestImagesEditsReturnsMultipartParseError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	body := []byte("--actual\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ngpt-image-1\r\n--actual--\r\n")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=wrong")

	_, _, err := getModelRequest(c)
	require.Error(t, err)
}
