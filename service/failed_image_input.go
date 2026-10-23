package service

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const ImageRetryTaskHeader = "X-New-Api-Retry-Task"

var ErrImageTaskInputMissing = errors.New("image input is unavailable or expired")
var imageTaskInputMu sync.Mutex

// ImageTaskInput retains the client request independently of execution outcome.
// Terminal tasks expire relative to completion; active tasks use their durable
// request snapshot. The legacy cache directory and wire header remain compatible.
type ImageTaskInput struct {
	UserID      int    `json:"user_id"`
	TaskID      string `json:"task_id"`
	Path        string `json:"path"`
	Query       string `json:"query,omitempty"`
	BodyPath    string `json:"-"`
	ContentType string `json:"content_type"`
	ExpiresAt   int64  `json:"expires_at"`
	Size        int64  `json:"size"`
}

func imageTaskInputPath(userID int, taskID string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", userID, taskID)))
	return filepath.Join(common.GetAsyncImageRequestStoragePath(), "failed-inputs", fmt.Sprintf("%x.retry", digest))
}

// CaptureImageTaskInput must run before terminal transitions clear private fields.
func CaptureImageTaskInput(task *model.Task) *ImageTaskInput {
	if task == nil || task.UserId <= 0 || !task.PrivateData.InternalAsync || !IsImageTaskAction(task.Action) {
		return nil
	}
	bodyPath := task.PrivateData.RequestBodyPath
	if replay := task.PrivateData.ImageRetry; replay != nil {
		bodyPath = replay.BodyPath
	} else if task.PrivateData.RequestRelayFormat == "native_async_image" {
		return nil // Never replay an upstream polling snapshot as a client request.
	}
	expires := time.Now().Add(time.Minute).Unix()
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		if task.FinishTime <= 0 {
			return nil
		}
		expires = task.FinishTime + int64(ReferenceRetention().Seconds())
	}
	query, err := url.ParseQuery(task.PrivateData.RequestQuery)
	if err != nil {
		return nil
	}
	// Authenticate each new request normally; never replay saved API credentials.
	query.Del("key")
	query.Del("api_key")
	return &ImageTaskInput{UserID: task.UserId, TaskID: task.TaskID, Path: task.PrivateData.RequestPath,
		Query: query.Encode(), ContentType: task.PrivateData.RequestContentType, BodyPath: bodyPath, ExpiresAt: expires}
}

func RetainImageTaskInput(input *ImageTaskInput) error {
	if input == nil || input.UserID <= 0 || input.BodyPath == "" || input.Path == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(input.ContentType)
	if err != nil || (mediaType != "application/json" && mediaType != "multipart/form-data") {
		return nil
	}
	if input.ExpiresAt <= time.Now().Unix() {
		return nil
	}
	source, err := os.Open(input.BodyPath)
	if err != nil {
		return err
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > 128<<20 {
		return errors.New("failed image input exceeds retention limit")
	}
	input.Size = stat.Size()
	header, err := common.Marshal(input)
	if err != nil || len(header) >= 4095 {
		return errors.New("invalid failed image input metadata")
	}
	imageTaskInputMu.Lock()
	defer imageTaskInputMu.Unlock()
	path := imageTaskInputPath(input.UserID, input.TaskID)
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Bound extra disk use without evicting another user's unexpired input.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var total, userBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".retry") {
			continue
		}
		name := filepath.Join(dir, entry.Name())
		info, _, readErr := openImageTaskInput(name)
		if readErr != nil {
			continue
		}
		if info.ExpiresAt <= time.Now().Unix() {
			if err := os.Remove(name); err == nil {
				continue
			}
		}
		total += info.Size
		if info.UserID == input.UserID {
			userBytes += info.Size
		}
	}
	if total+input.Size > 4<<30 || userBytes+input.Size > 512<<20 || len(entries) >= 10000 {
		return errors.New("failed image input cache is full")
	}
	tmp, err := os.CreateTemp(dir, ".failed-input-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append(header, '\n'))
	if err == nil {
		_, err = io.CopyN(tmp, source, input.Size)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	lastUse := time.Unix(input.ExpiresAt, 0).Add(-ReferenceRetention())
	if err := os.Chtimes(tmp.Name(), lastUse, lastUse); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// openImageTaskInput checks the envelope without reading image bytes. It does
// not leave a file descriptor open; callers serialize reads against cleanup.
func openImageTaskInput(path string) (*ImageTaskInput, int64, error) {
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return nil, 0, ErrImageTaskInputMissing
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	header, err := bufio.NewReaderSize(f, 4096).ReadSlice('\n')
	var input ImageTaskInput
	if err != nil || common.Unmarshal(header, &input) != nil || input.Size <= 0 || input.Size > 128<<20 || stat.Size() != int64(len(header))+input.Size {
		return nil, 0, ErrImageTaskInputMissing
	}
	// mtime is the durable last use; queries never touch it. Renewing metadata
	// does not recopy large image bodies, and uses the current policy duration.
	input.ExpiresAt = stat.ModTime().Add(ReferenceRetention()).Unix()
	return &input, int64(len(header)), nil
}

// RenewImageTaskInput is called only after a fresh submission is accepted.
// Idempotent transport replays exit before materialization and never renew it.
func RenewImageTaskInput(userID int, taskID string) error {
	imageTaskInputMu.Lock()
	defer imageTaskInputMu.Unlock()
	path := imageTaskInputPath(userID, taskID)
	input, _, err := openImageTaskInput(path)
	if os.IsNotExist(err) || errors.Is(err, ErrImageTaskInputMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if input.UserID != userID || input.TaskID != taskID {
		return ErrImageTaskInputMissing
	}
	now := time.Now()
	return os.Chtimes(path, now, now)
}

func ReadImageTaskInput(userID int, taskID string, includeBody bool) (*ImageTaskInput, []byte, error) {
	if userID <= 0 || taskID == "" || len(taskID) > 191 {
		return nil, nil, ErrImageTaskInputMissing
	}
	imageTaskInputMu.Lock()
	defer imageTaskInputMu.Unlock()
	path := imageTaskInputPath(userID, taskID)
	input, offset, err := openImageTaskInput(path)
	if err != nil || input.UserID != userID || input.TaskID != taskID || input.ExpiresAt <= time.Now().Unix() {
		// Active tasks already own a durable original request. Read it directly;
		// completion copies it to the bounded terminal cache before file cleanup.
		task, found, taskErr := model.GetByTaskId(userID, taskID)
		if taskErr != nil || !found || task == nil || (task.Status != model.TaskStatusSubmitted && task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusInProgress) {
			return nil, nil, ErrImageTaskInputMissing
		}
		input = CaptureImageTaskInput(task)
		if input == nil || input.BodyPath == "" || input.Path == "" {
			return nil, nil, ErrImageTaskInputMissing
		}
		mediaType, _, parseErr := mime.ParseMediaType(input.ContentType)
		if parseErr != nil || (mediaType != "application/json" && mediaType != "multipart/form-data") {
			return nil, nil, ErrImageTaskInputMissing
		}
		stat, statErr := os.Lstat(input.BodyPath)
		if statErr != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > 128<<20 {
			return nil, nil, ErrImageTaskInputMissing
		}
		input.Size, path, offset = stat.Size(), input.BodyPath, 0
	}
	if !includeBody {
		return input, nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, nil, err
	}
	body := make([]byte, input.Size)
	if _, err = io.ReadFull(f, body); err != nil {
		return nil, nil, err
	}
	return input, body, nil
}

func CleanupImageTaskInputs() error {
	imageTaskInputMu.Lock()
	defer imageTaskInputMu.Unlock()
	dir := filepath.Dir(imageTaskInputPath(0, ""))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		expired := false
		if strings.HasSuffix(entry.Name(), ".retry") {
			input, _, err := openImageTaskInput(path)
			expired = err != nil || input.ExpiresAt <= time.Now().Unix()
		} else if strings.HasPrefix(entry.Name(), ".failed-input-") {
			info, err := entry.Info()
			expired = err == nil && time.Since(info.ModTime()) >= ReferenceRetention()
		}
		if expired {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
