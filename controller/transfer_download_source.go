package controller

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
)

// A source URL may identify an output, but it never grants ownership. Callers
// must load the owned, completed task before using these selectors.
func transferTaskArtifactKey(task *model.Task, artifacts []relaychannel.TaskArtifact, request transferDownloadRequest) (string, error) {
	invalid := errors.New("resource not available")
	key := request.ArtifactKey
	index := 0
	if request.ResultIndex != nil {
		index = *request.ResultIndex
	}
	mapped := ""
	if request.SourceURL != "" && isOwnedTransferContentURL(task.TaskID, request.SourceURL) {
		source, _ := url.Parse(request.SourceURL)
		prefix := "/v1/tasks/" + task.TaskID + "/artifacts/"
		if strings.HasPrefix(source.Path, prefix) {
			mapped = strings.TrimSuffix(strings.TrimPrefix(source.Path, prefix), "/content")
		} else if task.Platform == constant.TaskPlatformInternalImage {
			indexText := source.Query().Get("index")
			mappedIndex := 0
			var err error
			if indexText != "" {
				mappedIndex, err = strconv.Atoi(indexText)
			}
			if err != nil || mappedIndex < 0 || mappedIndex >= len(artifacts) {
				return "", invalid
			}
			mapped = artifacts[mappedIndex].Key
		}
	}
	if mapped != "" {
		if key != "" && key != mapped {
			return "", invalid
		}
		if request.ResultIndex != nil {
			if len(artifacts) == 0 && index != 0 {
				return "", invalid
			}
			if len(artifacts) > 0 && (index >= len(artifacts) || artifacts[index].Key != mapped) {
				return "", invalid
			}
		}
		return mapped, nil
	}
	if task.Platform == constant.TaskPlatformInternalImage && request.SourceURL != "" {
		return "", invalid // An unrecognized URL cannot silently turn into image 0.
	}
	if key != "" {
		return key, nil
	}
	if len(artifacts) > 1 && request.ResultIndex == nil {
		return "", invalid
	}
	if index >= 0 && index < len(artifacts) {
		return artifacts[index].Key, nil
	}
	return "", nil
}

func isOwnedTransferContentURL(taskID, value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" ||
		(u.Host != "www.oopii.cc" && u.Host != "bridge.opics.top") {
		return false
	}
	prefix := "/v1/tasks/" + taskID + "/artifacts/"
	if strings.HasPrefix(u.Path, prefix) && strings.HasSuffix(u.Path, "/content") {
		key := strings.TrimSuffix(strings.TrimPrefix(u.Path, prefix), "/content")
		return taskArtifactKeyPattern.MatchString(key)
	}
	return slices.Contains([]string{
		"/v1/images/tasks/" + taskID + "/content",
		"/v1/images/tasks/" + taskID + "/signed-content",
		"/v1/images/generations/" + taskID + "/content",
		"/v1/videos/" + taskID + "/content",
		"/v1/video/generations/" + taskID + "/content",
	}, u.Path)
}

// A plugin may turn its persisted CDN result URL into an authenticated content
// API descriptor. Both identify the single owned artifact; the client's URL
// remains a selector only and is never used as the fetch target. Multi-output
// tasks require an exact descriptor/content selection to avoid aliasing outputs.
func transferSourceMatchesTask(task *model.Task, artifacts []relaychannel.TaskArtifact, key, descriptorURL, requestedURL string) bool {
	if requestedURL == "" || requestedURL == descriptorURL || isOwnedTransferContentURL(task.TaskID, requestedURL) {
		return true
	}
	return len(artifacts) == 1 && artifacts[0].Key == key &&
		task.GetResultURL() != "" && !isTaskMediaFallbackLoop(task.GetResultURL(), task.TaskID) &&
		requestedURL == task.GetResultURL()
}

// Read only known output fields of a successful receipt. Never recursively scan
// arbitrary URLs: echoed input/reference URLs must not become download rights.
func transferReceiptURLs(raw []byte) ([]string, error) {
	var root map[string]common.RawMessage
	if err := common.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	type output struct {
		URL         string `json:"url"`
		VideoURL    string `json:"video_url"`
		DownloadURL string `json:"download_url"`
	}
	var sources []string
	appendOutput := func(value output) {
		for _, candidate := range []string{value.URL, value.VideoURL, value.DownloadURL} {
			if candidate != "" && !slices.Contains(sources, candidate) {
				sources = append(sources, candidate)
			}
		}
	}
	var direct output
	if err := common.Unmarshal(raw, &direct); err != nil {
		return nil, err
	}
	appendOutput(direct)
	if data, exists := root["data"]; exists {
		var items []output
		if err := common.Unmarshal(data, &items); err == nil {
			for _, item := range items {
				appendOutput(item)
			}
		} else {
			var item output
			if err := common.Unmarshal(data, &item); err == nil {
				appendOutput(item)
			}
		}
	}
	return sources, nil
}
