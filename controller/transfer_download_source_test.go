package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransferOutputSelectionCannotTurnSecondImageIntoFirst(t *testing.T) {
	task := &model.Task{TaskID: "task_multi", Platform: constant.TaskPlatformInternalImage}
	artifacts := []relaychannel.TaskArtifact{{Key: "image"}, {Key: "image-2"}}
	zero, one := 0, 1
	source := model.TransferOrigin + "/v1/images/tasks/task_multi/signed-content?token=expired&index=1"
	key, err := transferTaskArtifactKey(task, artifacts, transferDownloadRequest{SourceURL: source})
	require.NoError(t, err)
	assert.Equal(t, "image-2", key, "source identifies its result even when the client omitted an index")
	_, err = transferTaskArtifactKey(task, artifacts, transferDownloadRequest{SourceURL: source, ResultIndex: &zero})
	require.Error(t, err, "contradicting client metadata cannot silently return the first image")
	key, err = transferTaskArtifactKey(task, artifacts, transferDownloadRequest{SourceURL: source, ResultIndex: &one})
	require.NoError(t, err)
	assert.Equal(t, "image-2", key)
	_, err = transferTaskArtifactKey(task, artifacts, transferDownloadRequest{SourceURL: "https://example.com/input.png"})
	require.Error(t, err)
	_, err = transferTaskArtifactKey(task, artifacts, transferDownloadRequest{})
	require.Error(t, err, "multiple outputs need a selection")
	_, err = transferTaskArtifactKey(task, artifacts, transferDownloadRequest{
		SourceURL: model.TransferOrigin + "/v1/tasks/task_multi/artifacts/image-2/content", ArtifactKey: "image",
	})
	require.Error(t, err)
	for _, value := range []string{
		model.TransferOrigin + "/v1/tasks/other_task/artifacts/image/content",
		"https://www.oopii.cc.evil.test/v1/videos/task_multi/content",
		"https://user@www.oopii.cc/v1/videos/task_multi/content",
		model.TransferOrigin + "/v1/tasks/task_multi/artifacts/image/other/content",
	} {
		assert.False(t, isOwnedTransferContentURL(task.TaskID, value), value)
	}
}

func TestTransferReceiptOnlyAuthorizesKnownOutputFields(t *testing.T) {
	urls, err := transferReceiptURLs([]byte(`{
		"url":"https://example.com/video.mp4",
		"input":{"url":"http://127.0.0.1/private"},
		"reference_urls":["https://example.com/reference.png"],
		"data":{"video_url":"https://example.com/second.mp4","download_url":"https://example.com/fallback.mp4"}
	}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"https://example.com/video.mp4", "https://example.com/second.mp4", "https://example.com/fallback.mp4"}, urls)
	urls, err = transferReceiptURLs([]byte(`{"data":[{"url":"https://example.com/a.png"},{"url":"https://example.com/b.png"}]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"https://example.com/a.png", "https://example.com/b.png"}, urls)
}

func TestTransferOwnedResultAliasDoesNotAuthorizeArbitraryOrAnotherOutput(t *testing.T) {
	task := &model.Task{TaskID: "video-owned",
		PrivateData: model.TaskPrivateData{ResultURL: "https://cdn.example.test/result.mp4"}}
	artifacts := []relaychannel.TaskArtifact{{Key: "video", Type: "video"}}
	descriptor := "https://api.example.test/videos/id/content"
	assert.True(t, transferSourceMatchesTask(task, artifacts, "video", descriptor, task.GetResultURL()))
	assert.True(t, transferSourceMatchesTask(task, artifacts, "video", descriptor, descriptor))
	assert.False(t, transferSourceMatchesTask(task, artifacts, "video", descriptor, "https://evil.example.test/secret"))
	assert.False(t, transferSourceMatchesTask(task, artifacts, "image", descriptor, task.GetResultURL()))
	artifacts = append(artifacts, relaychannel.TaskArtifact{Key: "video-2", Type: "video"})
	assert.False(t, transferSourceMatchesTask(task, artifacts, "video-2", descriptor, task.GetResultURL()))
	assert.False(t, transferSourceMatchesTask(task, artifacts, "video", descriptor, task.GetResultURL()),
		"ambiguous multi-output aliases must retain explicit output selection")
}
