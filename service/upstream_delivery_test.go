package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamDeliveryFailoverEvidence(t *testing.T) {
	var bodies []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		bodies = append(bodies, string(body))
		w.Header().Set("X-Request-Id", "provider-request-133")
		if r.URL.Path == "/first" {
			w.WriteHeader(500)
		}
		_, _ = io.WriteString(w, "result")
	}))
	defer server.Close()
	ctx, finish := TrackAsyncImageDelivery(context.Background(), "task-delivery", 1)
	defer finish()
	for i, path := range []string{"/first", "/second"} {
		channel := 139
		if i == 1 {
			channel = 133
		}
		attemptCtx := BeginUpstreamDelivery(ctx, channel, i+1)
		// The channel selection must never be presented as a successful send.
		selected := AsyncImageDeliverySnapshot("task-delivery", 1)
		require.Len(t, selected, i+1)
		assert.False(t, selected[i].RequestWritten)
		assert.False(t, selected[i].ResponseReceived)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, server.URL+path+"?key=secret-query", strings.NewReader("private-prompt"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer private-key")
		existingHook := false
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { existingHook = true }}))
		req, record := TraceUpstreamDelivery(req)
		resp, err := server.Client().Do(req)
		record(resp, err)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.True(t, existingHook)
	}
	evidence := AsyncImageDeliverySnapshot("task-delivery", 1)
	require.Len(t, evidence, 2)
	assert.Equal(t, []string{"private-prompt", "private-prompt"}, bodies)
	assert.Equal(t, 139, evidence[0].ChannelID)
	assert.Equal(t, 500, evidence[0].StatusCode)
	assert.Equal(t, 133, evidence[1].ChannelID)
	assert.Equal(t, 200, evidence[1].StatusCode)
	assert.True(t, evidence[1].ConnectionReused)
	for _, attempt := range evidence {
		assert.True(t, attempt.ConnectionReady)
		assert.True(t, attempt.RequestWritten)
		assert.True(t, attempt.ResponseReceived)
		assert.True(t, attempt.ResponseComplete)
		assert.Equal(t, "provider-request-133", attempt.UpstreamRequestID)
	}
	raw, err := common.Marshal(evidence)
	require.NoError(t, err)
	for _, secret := range []string{"secret-query", "private-prompt", "private-key", "/first", "/second"} {
		assert.NotContains(t, string(raw), secret)
	}
	// The timeout/refund path reads the same immutable snapshot without the HTTP context.
	task := &model.Task{TaskID: "task-delivery"}
	task.PrivateData.WorkerAttempts = 1
	other := taskBillingOther(task).Snapshot()
	admin, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, evidence[1].StatusCode, admin["upstream_delivery"].([]UpstreamDelivery)[1].StatusCode)
	assert.NotContains(t, other, "upstream_delivery")
	evidence[0].ChannelID = 0
	assert.Equal(t, 139, AsyncImageDeliverySnapshot("task-delivery", 1)[0].ChannelID)
	assert.Nil(t, AsyncImageDeliverySnapshot("task-delivery", 2))
	finish()
	assert.Nil(t, AsyncImageDeliverySnapshot("task-delivery", 1))
}

func TestUpstreamDeliveryCancellationEvidence(t *testing.T) {
	for _, sent := range []bool{false, true} {
		name := "before_send"
		if sent {
			name = "after_write_before_response"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session, finish := TrackAsyncImageDelivery(ctx, name, 1)
			defer finish()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				cancel()
				<-r.Context().Done()
			}))
			defer server.Close()
			req, err := http.NewRequestWithContext(BeginUpstreamDelivery(session, 133, 1), http.MethodPost, server.URL, strings.NewReader("body"))
			require.NoError(t, err)
			if !sent {
				cancel()
			}
			req, record := TraceUpstreamDelivery(req)
			resp, err := server.Client().Do(req)
			record(resp, err)
			require.ErrorIs(t, err, context.Canceled)
			evidence := AsyncImageDeliverySnapshot(name, 1)
			require.Len(t, evidence, 1)
			assert.Equal(t, sent, evidence[0].RequestWritten)
			assert.False(t, evidence[0].ResponseReceived)
			assert.Equal(t, "canceled", evidence[0].ErrorKind)
		})
	}
}

func TestUpstreamDeliveryTruncatedResponseAndOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer server.Close()
	ctx, finishOld := TrackAsyncImageDelivery(context.Background(), "replacement", 1)
	ctx, finish := TrackAsyncImageDelivery(ctx, "replacement", 2)
	defer finish()
	finishOld()
	req, err := http.NewRequestWithContext(BeginUpstreamDelivery(ctx, 133, 1), http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	req, record := TraceUpstreamDelivery(req)
	resp, err := server.Client().Do(req)
	record(resp, err)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.NoError(t, resp.Body.Close())
	evidence := AsyncImageDeliverySnapshot("replacement", 2)
	require.Len(t, evidence, 1)
	assert.True(t, evidence[0].ResponseReceived)
	assert.False(t, evidence[0].ResponseComplete)
	assert.Equal(t, "response_body_failed", evidence[0].Stage)
}
