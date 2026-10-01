package service

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
)

// TraceUpstreamDelivery is a no-op outside an explicitly tracked async attempt.
// It preserves the request's deadline and any existing httptrace hooks.
func TraceUpstreamDelivery(req *http.Request) (*http.Request, func(*http.Response, error)) {
	attempt, _ := req.Context().Value(deliveryAttemptKey{}).(*deliveryAttempt)
	if attempt == nil {
		return req, func(*http.Response, error) {}
	}
	attempt.record("transport_started", func(d *UpstreamDelivery) { d.Host = req.URL.Hostname(); d.Method = req.Method })
	hooks := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { attempt.record("dns_started", nil) },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			attempt.record("dns_finished", func(d *UpstreamDelivery) { d.ErrorKind = deliveryErrorKind(info.Err) })
		},
		ConnectStart: func(_, _ string) { attempt.record("connecting", nil) },
		ConnectDone: func(_, _ string, err error) {
			attempt.record("connect_finished", func(d *UpstreamDelivery) { d.ErrorKind = deliveryErrorKind(err) })
		},
		TLSHandshakeStart: func() { attempt.record("tls_started", nil) },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			attempt.record("tls_finished", func(d *UpstreamDelivery) { d.ErrorKind = deliveryErrorKind(err) })
		},
		GotConn: func(info httptrace.GotConnInfo) {
			attempt.record("connection_ready", func(d *UpstreamDelivery) {
				if info.Conn != nil && info.Conn.RemoteAddr() != nil {
					d.PeerAddress = info.Conn.RemoteAddr().String()
				}
				d.ConnectionReady = true
				d.ConnectionReused = info.Reused
				d.ErrorKind = ""
			})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			stage := "request_written"
			if info.Err != nil {
				stage = "request_write_failed"
			}
			attempt.record(stage, func(d *UpstreamDelivery) {
				d.WriteCount++
				if info.Err == nil {
					d.RequestWritten = true
				}
				d.ErrorKind = deliveryErrorKind(info.Err)
			})
		},
		GotFirstResponseByte: func() { attempt.record("first_response_byte", nil) },
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), hooks)), func(resp *http.Response, err error) {
		if err != nil {
			attempt.record("transport_failed", func(d *UpstreamDelivery) { d.ErrorKind = deliveryErrorKind(err) })
			return
		}
		if resp == nil {
			return
		}
		attempt.record("response_headers", func(d *UpstreamDelivery) {
			d.ResponseReceived = true
			d.StatusCode = resp.StatusCode
			d.ErrorKind = ""
			for _, key := range []string{"X-Request-Id", "Request-Id", "X-Amzn-Requestid"} {
				value := resp.Header.Get(key)
				if value != "" && len(value) <= 160 && strings.IndexFunc(value, func(r rune) bool { return r < 33 || r > 126 }) == -1 {
					d.UpstreamRequestID = value
					break
				}
			}
		})
		if resp.Body != nil {
			resp.Body = &deliveryResponseBody{ReadCloser: resp.Body, attempt: attempt}
		}
	}
}

type deliveryResponseBody struct {
	io.ReadCloser
	attempt *deliveryAttempt
	done    sync.Once
}

func (b *deliveryResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.done.Do(func() {
			if err == io.EOF {
				b.attempt.record("response_body_complete", func(d *UpstreamDelivery) { d.ResponseComplete = true })
			} else {
				b.attempt.record("response_body_failed", func(d *UpstreamDelivery) { d.ErrorKind = deliveryErrorKind(err) })
			}
		})
	}
	return n, err
}
func (b *deliveryResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.done.Do(func() { b.attempt.record("response_body_closed", nil) })
	return err
}
