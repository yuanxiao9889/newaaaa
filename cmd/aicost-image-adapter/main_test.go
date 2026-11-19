package main

import (
	"bytes"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageAdapter(t *testing.T) {
	var audit bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&audit)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	// A Base64 image larger than 1 MiB must pass through, along with usage.
	response := `{"data":[{"b64_json":"` + strings.Repeat("AAAA", 400000) + `"}],"usage":{"input_tokens":1000,"output_tokens":2000,"total_tokens":3000}}`
	requests := make(chan *http.Request, 1)
	bodies := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- r.Clone(r.Context())
		bodies <- body
		w.Header().Set("X-Request-ID", "upstream-fixture")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fail") == "true" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	proxy := httptest.NewServer(imageAdapter(target))
	t.Cleanup(proxy.Close)
	for _, quality := range []string{"xhigh", "max", "high", "auto", "low", "medium", "standard", "", "absent"} {
		t.Run(quality, func(t *testing.T) {
			body := `{"model":"gpt-image-2.6-sunburst-max","size":"2160x3840","output_format":"gif","frame_count":8,"n":1,"prompt":"private fixture prompt"`
			if quality != "absent" {
				body += `,"quality":"` + quality + `"`
			}
			body += `}`
			req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/images/generations", strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer fixture")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, response, string(got))
			assert.Equal(t, "upstream-fixture", resp.Header.Get("X-Request-ID"))
			request, forwarded := <-requests, <-bodies
			assert.Equal(t, "Bearer fixture", request.Header.Get("Authorization"))
			assert.Equal(t, target.Host, request.Host)
			var fields map[string]any
			require.NoError(t, common.Unmarshal(forwarded, &fields))
			if quality == "xhigh" || quality == "max" {
				assert.Equal(t, "auto", fields["quality"])
			} else {
				assert.Equal(t, body, string(forwarded))
			}
			assert.Equal(t, "2160x3840", fields["size"])
			assert.Equal(t, "gif", fields["output_format"])
			assert.EqualValues(t, 8, fields["frame_count"])
		})
	}
	assert.Contains(t, audit.String(), `"input_quality":"max","upstream_quality":"auto"`)
	assert.Contains(t, audit.String(), `"upstream_request_id":"upstream-fixture","status":200`)
	assert.NotContains(t, audit.String(), "Bearer fixture")
	assert.NotContains(t, audit.String(), "private fixture prompt")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"model", "fixture-model"}, {"quality", "xhigh"}, {"quality", "high"}, {"extra", "one"}, {"extra", "two"}, {"size", "2160x3840"}} {
		require.NoError(t, writer.WriteField(field[0], field[1]))
	}
	fileData := []byte("\x89PNG\x00\xffquality=max\r\n")
	for _, field := range []string{"image[]", "image", "mask"} {
		file, err := writer.CreateFormFile(field, "original.png")
		require.NoError(t, err)
		_, err = file.Write(fileData)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	resp, err := http.Post(proxy.URL+"/v1/images/edits?fail=true", writer.FormDataContentType(), &body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 429, resp.StatusCode)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"error":{"message":"rate limited"}}`, string(got))
	request, forwarded := <-requests, <-bodies
	request.Body = io.NopCloser(bytes.NewReader(forwarded))
	require.NoError(t, request.ParseMultipartForm(1<<20))
	defer request.MultipartForm.RemoveAll()
	assert.Equal(t, []string{"auto", "high"}, request.MultipartForm.Value["quality"])
	assert.Equal(t, []string{"one", "two"}, request.MultipartForm.Value["extra"])
	for _, field := range []string{"image[]", "image", "mask"} {
		require.Len(t, request.MultipartForm.File[field], 1)
		file, err := request.MultipartForm.File[field][0].Open()
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		assert.Equal(t, fileData, data)
	}
}
