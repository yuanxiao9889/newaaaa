package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestParseTaskResultAcceptsStringErrorAndRunningStatus(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"grok-deb7f9869d32",
		"task_id":"grok-deb7f9869d32",
		"status":"running",
		"progress":0,
		"error":""
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusInProgress {
		t.Fatalf("expected status %q, got %q", model.TaskStatusInProgress, result.Status)
	}
	if result.Reason != "" {
		t.Fatalf("expected empty reason, got %q", result.Reason)
	}
}

func TestParseTaskResultAcceptsCompletedWithoutURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"video_33",
		"status":"completed",
		"progress":100
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusSuccess {
		t.Fatalf("expected status %q, got %q", model.TaskStatusSuccess, result.Status)
	}
	if result.Url != "" {
		t.Fatalf("expected empty url, got %q", result.Url)
	}
}

func TestParseTaskResultAcceptsSucceededAndExtractsVideoURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"grok-58b24b68819e",
		"status":"succeeded",
		"progress":100,
		"error":"",
		"data":{
			"video_url":"https://dl.example.test/result.mp4",
			"video_urls":["https://dl.example.test/result.mp4"]
		},
		"result":{
			"final_url":"https://dl.example.test/result-from-result.mp4"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusSuccess {
		t.Fatalf("expected status %q, got %q", model.TaskStatusSuccess, result.Status)
	}
	if result.Url != "https://dl.example.test/result.mp4" {
		t.Fatalf("expected data video url, got %q", result.Url)
	}
}
