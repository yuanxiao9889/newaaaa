// aicost-image-adapter changes only the unsupported image quality values.
// It streams the original upstream response through the ordinary relay path,
// preserving token accounting, image retention and large Base64 responses.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const maxRequestBytes = 256 << 20

type imageAuditKey struct{}

type imageAudit struct {
	InputQuality      string `json:"input_quality"`
	UpstreamQuality   string `json:"upstream_quality"`
	Model             string `json:"model"`
	Size              string `json:"size"`
	PromptSHA256      string `json:"prompt_sha256"`
	UpstreamRequestID string `json:"upstream_request_id"`
	Status            int    `json:"status"`
}

type auditTransport struct{ base http.RoundTripper }

func (t auditTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	// A response binds the exact transformed request to the upstream request ID.
	// Never include headers, prompt text, response bodies or API credentials.
	if entry, ok := r.Context().Value(imageAuditKey{}).(imageAudit); ok && response != nil {
		entry.UpstreamRequestID = response.Header.Get("X-Request-ID")
		entry.Status = response.StatusCode
		if data, marshalErr := common.Marshal(entry); marshalErr == nil {
			log.Printf("aicost_image_request %s", data)
		}
	}
	return response, err
}

func main() {
	upstream := os.Getenv("AICOST_UPSTREAM_URL")
	if upstream == "" {
		upstream = "https://www.aicost.me"
	}
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" || (target.Scheme != "https" && target.Scheme != "http") || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		log.Fatal("Invalid AICOST_UPSTREAM_URL")
	}
	server := &http.Server{Addr: ":8080", Handler: imageAdapter(target), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("AICost image quality adapter listening on :8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func imageAdapter(target *url.URL) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: auditTransport{base: http.DefaultTransport},
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = target.Host
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Do not log request headers, URLs, prompts or upstream credentials.
			log.Print("AICost upstream transport failed")
			http.Error(w, "AICost upstream transport failed", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		if r.Method != http.MethodPost || (r.URL.Path != "/v1/images/generations" && r.URL.Path != "/v1/images/edits") {
			proxy.ServeHTTP(w, r)
			return
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			http.Error(w, "Invalid image Content-Type", http.StatusBadRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		if mediaType == "application/json" {
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "Image request exceeds the body limit", http.StatusRequestEntityTooLarge)
				return
			}
			var fields map[string]json.RawMessage
			if err := common.Unmarshal(body, &fields); err != nil || fields == nil {
				http.Error(w, "Invalid image JSON body", http.StatusBadRequest)
				return
			}
			var quality string
			_ = common.Unmarshal(fields["quality"], &quality)
			entry := imageAudit{InputQuality: quality, UpstreamQuality: quality}
			_ = common.Unmarshal(fields["model"], &entry.Model)
			_ = common.Unmarshal(fields["size"], &entry.Size)
			var prompt string
			_ = common.Unmarshal(fields["prompt"], &prompt)
			digest := sha256.Sum256([]byte(prompt))
			entry.PromptSHA256 = hex.EncodeToString(digest[:])
			if quality == "max" || quality == "xhigh" {
				entry.UpstreamQuality = "auto"
				fields["quality"] = json.RawMessage(`"auto"`)
				body, err = common.Marshal(fields)
				if err != nil {
					http.Error(w, "Cannot encode image request", http.StatusBadRequest)
					return
				}
			}
			r = r.WithContext(context.WithValue(r.Context(), imageAuditKey{}, entry))
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		} else if mediaType == "multipart/form-data" {
			file, contentType, err := adaptMultipart(r.Body, params["boundary"])
			_ = r.Body.Close()
			if err != nil {
				var limit *http.MaxBytesError
				status := http.StatusBadRequest
				if errors.As(err, &limit) {
					status = http.StatusRequestEntityTooLarge
				}
				http.Error(w, "Invalid or oversized image multipart body", status)
				return
			}
			defer os.Remove(file.Name())
			defer file.Close()
			stat, err := file.Stat()
			if err != nil {
				http.Error(w, "Cannot read image request", http.StatusInternalServerError)
				return
			}
			r.Body, r.ContentLength = file, stat.Size()
			r.Header.Set("Content-Type", contentType)
		}
		r.Header.Del("Content-Length")
		r.TransferEncoding = nil
		proxy.ServeHTTP(w, r)
	})
}

// Spool file parts without decoding/re-encoding image bytes. The temporary file
// is private, bounded by MaxBytesReader, and removed after the relay finishes.
func adaptMultipart(body io.Reader, boundary string) (file *os.File, contentType string, err error) {
	file, err = os.CreateTemp("", "aicost-image-request-*")
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	writer := multipart.NewWriter(file)
	reader := multipart.NewReader(body, boundary)
	for {
		part, readErr := reader.NextRawPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return file, "", readErr
		}
		output, writeErr := writer.CreatePart(part.Header)
		if writeErr != nil {
			return file, "", writeErr
		}
		if part.FormName() == "quality" && part.FileName() == "" {
			value, readErr := io.ReadAll(io.LimitReader(part, 65))
			if readErr != nil {
				return file, "", readErr
			}
			if string(value) == "max" || string(value) == "xhigh" {
				value = []byte("auto")
			}
			if _, err = output.Write(value); err != nil {
				return file, "", err
			}
		}
		if _, err = io.Copy(output, part); err != nil {
			return file, "", err
		}
	}
	if err = writer.Close(); err != nil {
		return file, "", err
	}
	_, err = file.Seek(0, io.SeekStart)
	return file, writer.FormDataContentType(), err
}
