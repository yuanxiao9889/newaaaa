package common

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const AsyncImageRetentionHoursOptionKey = "AsyncImageRetentionHours"
const AsyncImageInternalTaskEnabledOptionKey = "AsyncImageInternalTaskEnabled"
const AsyncImageWorkerConcurrencyOptionKey = "AsyncImageWorkerConcurrency"
const AsyncImageMaxUnfinishedTasksOptionKey = "AsyncImageMaxUnfinishedTasks"

var AsyncImageRetentionAllowedHours = []int{2, 6, 12, 18, 24}

func DefaultAsyncImageRetentionHours() int {
	return NormalizeAsyncImageRetentionHours(GetEnvOrDefault("ASYNC_IMAGE_RETENTION_HOURS", 24))
}

func DefaultAsyncImageWorkerConcurrency() int {
	return NormalizeAsyncImagePositiveInt(GetEnvOrDefault("ASYNC_IMAGE_WORKER_CONCURRENCY", 4), 1, 256, 4)
}

func DefaultAsyncImageMaxUnfinishedTasks() int {
	return NormalizeAsyncImagePositiveInt(GetEnvOrDefault("ASYNC_IMAGE_MAX_UNFINISHED_TASKS", 500), 1, 100000, 500)
}

func DefaultAsyncImageTaskTimeoutSeconds() int {
	value := strings.TrimSpace(GetEnvOrDefaultString("ASYNC_IMAGE_TASK_TIMEOUT_SECONDS", "1600"))
	seconds, err := strconv.Atoi(value)
	if err != nil {
		SysError(fmt.Sprintf("invalid ASYNC_IMAGE_TASK_TIMEOUT_SECONDS=%q, using default value 1600", value))
		return 1600
	}
	if seconds == 0 {
		return 0
	}
	return NormalizeAsyncImagePositiveInt(seconds, 1, 86400, 1600)
}

func NormalizeAsyncImageRetentionHours(hours int) int {
	for _, allowed := range AsyncImageRetentionAllowedHours {
		if hours == allowed {
			return hours
		}
	}
	return 24
}

func NormalizeAsyncImagePositiveInt(value int, min int, max int, fallback int) int {
	if value < min || value > max {
		return fallback
	}
	return value
}

func ParseAsyncImageRetentionHours(value string) (int, error) {
	hours, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("async image retention hours must be one of: 2, 6, 12, 18, 24")
	}
	for _, allowed := range AsyncImageRetentionAllowedHours {
		if hours == allowed {
			return hours, nil
		}
	}
	return 0, fmt.Errorf("async image retention hours must be one of: 2, 6, 12, 18, 24")
}

func ParseAsyncImageWorkerConcurrency(value string) (int, error) {
	return parseAsyncImagePositiveInt(value, 1, 256, "async image worker concurrency must be between 1 and 256")
}

func ParseAsyncImageMaxUnfinishedTasks(value string) (int, error) {
	return parseAsyncImagePositiveInt(value, 1, 100000, "async image max unfinished tasks must be between 1 and 100000")
}

func parseAsyncImagePositiveInt(value string, min int, max int, message string) (int, error) {
	num, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || num < min || num > max {
		return 0, errors.New(message)
	}
	return num, nil
}

func GetAsyncImageRetentionHours() int {
	OptionMapRWMutex.RLock()
	value := OptionMap[AsyncImageRetentionHoursOptionKey]
	OptionMapRWMutex.RUnlock()
	if value == "" {
		return DefaultAsyncImageRetentionHours()
	}
	hours, err := ParseAsyncImageRetentionHours(value)
	if err != nil {
		SysError(fmt.Sprintf("invalid %s=%q, using default value", AsyncImageRetentionHoursOptionKey, value))
		return DefaultAsyncImageRetentionHours()
	}
	return hours
}

func GetAsyncImageInternalTaskEnabled() bool {
	OptionMapRWMutex.RLock()
	value := strings.TrimSpace(OptionMap[AsyncImageInternalTaskEnabledOptionKey])
	OptionMapRWMutex.RUnlock()
	if value == "" {
		return AsyncImageInternalTaskEnabled
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return enabled
}

func GetAsyncImageWorkerConcurrency() int {
	OptionMapRWMutex.RLock()
	value := OptionMap[AsyncImageWorkerConcurrencyOptionKey]
	OptionMapRWMutex.RUnlock()
	if value == "" {
		return DefaultAsyncImageWorkerConcurrency()
	}
	concurrency, err := ParseAsyncImageWorkerConcurrency(value)
	if err != nil {
		SysError(fmt.Sprintf("invalid %s=%q, using default value", AsyncImageWorkerConcurrencyOptionKey, value))
		return DefaultAsyncImageWorkerConcurrency()
	}
	return concurrency
}

func GetAsyncImageMaxUnfinishedTasks() int {
	OptionMapRWMutex.RLock()
	value := OptionMap[AsyncImageMaxUnfinishedTasksOptionKey]
	OptionMapRWMutex.RUnlock()
	if value == "" {
		return DefaultAsyncImageMaxUnfinishedTasks()
	}
	maxTasks, err := ParseAsyncImageMaxUnfinishedTasks(value)
	if err != nil {
		SysError(fmt.Sprintf("invalid %s=%q, using default value", AsyncImageMaxUnfinishedTasksOptionKey, value))
		return DefaultAsyncImageMaxUnfinishedTasks()
	}
	return maxTasks
}

func GetAsyncImageRequestStoragePath() string {
	return GetEnvOrDefaultString("ASYNC_IMAGE_REQUEST_STORAGE_PATH", "./data/async-image-requests")
}

func GetAsyncImageWorkerStaleMinutes() int {
	value := GetEnvOrDefault("ASYNC_IMAGE_WORKER_STALE_MINUTES", 30)
	if value <= 0 {
		return 30
	}
	return value
}

func GetAsyncImageTaskTimeoutSeconds() int {
	return DefaultAsyncImageTaskTimeoutSeconds()
}
