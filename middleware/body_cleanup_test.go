package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyStorageCleanupRemovesMultipartTemporaryFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("multipart content larger than the memory limit"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(1))
	require.NotNil(t, c.Request.MultipartForm)
	fileHeader := c.Request.MultipartForm.File["image"][0]

	file, err := fileHeader.Open()
	require.NoError(t, err)
	require.NoError(t, file.Close())

	BodyStorageCleanup()(c)

	_, err = fileHeader.Open()
	require.Error(t, err)
	require.Nil(t, c.Request.MultipartForm)
	require.Nil(t, c.Request.PostForm)
}
