package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// runRetryableAsyncImageRelay keeps accepted upstream jobs on their original
// channel until a confirmed, retryable terminal failure. Each new POST starts
// from the client's body and public model, never the previous mapped request.
func runRetryableAsyncImageRelay(ctx context.Context, task *model.Task) ([]byte, int, string, *dto.Usage, error) {
	retry := task.PrivateData.ImageRetry
	if retry.Submitting {
		return nil, task.ChannelId, "", nil, fmt.Errorf("async image submission outcome is unknown; automatic resubmission stopped")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, task.ChannelId, "", nil, err
		}
		var c *gin.Context
		var channel *model.Channel
		var apiErr *types.NewAPIError
		var proxy string
		var upstreamErrorType string
		if task.PrivateData.RequestRelayFormat == relay.NativeAsyncImageFormat {
			body, channelID, usedProxy, err := relay.ResumeNativeAsyncImageTask(ctx, task)
			if err == nil {
				return body, channelID, usedProxy, nil, nil
			}
			var failure *relay.NativeImageTaskError
			if !errors.As(err, &failure) {
				if errors.Is(err, relay.ErrNativeImageResultUnavailable) {
					service.RecordAutoSwitchTaskFailure(ctx, task, 0, "", "image_result_unavailable")
				}
				// Transport uncertainty, timeout and result-download errors cannot
				// establish that upstream generation failed. Never submit again.
				return nil, channelID, usedProxy, nil, err
			}
			service.RecordAutoSwitchTaskFailure(ctx, task, failure.StatusCode, failure.Code+" "+failure.Type, failure.Message)
			proxy = usedProxy
			upstreamErrorType = failure.Type
			channel, err = model.GetChannelById(channelID, true)
			if err != nil {
				return nil, channelID, proxy, nil, err
			}
			c, _, err = buildInternalAsyncBaseContext(ctx, task, nil)
			if err != nil {
				return nil, channelID, proxy, nil, err
			}
			code := failure.Code
			if code == "" {
				code = "upstream_image_task_failed"
			}
			status := failure.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			apiErr = types.NewErrorWithStatusCode(failure, types.ErrorCode(code), status)
			if !failure.Retryable {
				apiErr = types.NewErrorWithStatusCode(failure, types.ErrorCode(code), status, types.ErrOptionWithSkipRetry())
			}
			// Return to the retained client snapshot only after a terminal failure.
			task.PrivateData.RequestRelayFormat = retry.RelayFormat
		} else {
			if len(task.PrivateData.ChannelRetryPath) > common.RetryTimes {
				return nil, task.ChannelId, "", nil, fmt.Errorf("async image channel retry limit reached")
			}
			body, err := os.ReadFile(retry.BodyPath)
			if err != nil {
				return nil, task.ChannelId, "", nil, fmt.Errorf("read original image request: %w", err)
			}
			var recorder *httptest.ResponseRecorder
			var info *relaycommon.RelayInfo
			if retry.RelayFormat == string(types.RelayFormatGemini) {
				c, recorder, _, info, err = buildInternalAsyncGeminiImageContext(ctx, task, body)
			} else {
				c, recorder, _, info, err = buildInternalAsyncImageContext(ctx, task, body)
			}
			if err != nil {
				return nil, task.ChannelId, "", nil, err
			}
			defer service.ReleaseAutoSwitchAttempt(c)
			constraints := service.GetChannelConstraints(c)
			constraints.Pins = append(constraints.Pins, retry.Constraints.Pins...)
			constraints.Filters = append(constraints.Filters, retry.Constraints.Filters...)
			identity := dto.ChannelFilter{Kind: dto.FilterChannelIdentity}
			for _, used := range task.PrivateData.ChannelRetryPath {
				id, err := strconv.Atoi(used)
				if err != nil {
					common.CleanupBodyStorage(c)
					return nil, task.ChannelId, "", nil, fmt.Errorf("invalid stored image retry channel")
				}
				identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, id)
			}
			if pin, pinned, _ := constraints.ResolvedPin(); pinned {
				identity.ChannelID = pin.ChannelId
			}
			constraints.AddFilter(identity)
			constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: c.Request.URL.Path})
			// Exclude attempted channels before selecting the highest remaining
			// priority. Advancing a priority index here would skip valid peers.
			channel, apiErr = getChannel(c, info, &service.RetryParam{Ctx: c, TokenGroup: task.Group, ModelName: task.Properties.OriginModelName, RequestPath: c.Request.URL.Path})
			if apiErr != nil {
				common.CleanupBodyStorage(c)
				recordInternalAsyncImageFinalErrorLog(c, task, nil, apiErr, task.PrivateData.ChannelRetryPath, retry.Details)
				return nil, task.ChannelId, "", nil, apiErr
			}
			proxy = channel.GetSetting().Proxy
			task.ChannelId = channel.Id
			task.PrivateData.ChannelRetryPath = append(task.PrivateData.ChannelRetryPath, strconv.Itoa(channel.Id))
			task.PrivateData.UpstreamTaskID = ""
			task.PrivateData.AutoSwitch = service.AutoSwitchAttempt(c)
			retry.Submitting = true
			if err := persistAsyncImageAttempt(task); err != nil {
				common.CleanupBodyStorage(c)
				return nil, channel.Id, proxy, nil, err
			}
			info.RetryIndex = len(task.PrivateData.ChannelRetryPath) - 1
			addUsedChannel(c, channel.Id)
			c.Request = c.Request.WithContext(service.BeginUpstreamDelivery(c.Request.Context(), channel.Id, len(task.PrivateData.ChannelRetryPath)))
			if retry.RelayFormat == string(types.RelayFormatGemini) {
				apiErr = geminiRelayHandler(c, info)
			} else {
				apiErr = relay.ImageHelper(c, info)
			}
			common.CleanupBodyStorage(c)
			task.Properties.UpstreamModelName = info.UpstreamModelName
			retry.Submitting = false
			if apiErr == nil {
				// A valid synchronous response is checked by the worker; an
				// accepted native job must retain its ticket until polling ends.
				service.DetachAutoSwitchAttempt(c)
				if value, accepted := c.Get(relay.NativeAsyncImageFormat); accepted {
					snapshot, ok := value.(*relay.NativeAsyncImageSnapshot)
					if !ok {
						return nil, channel.Id, proxy, nil, fmt.Errorf("invalid native image snapshot")
					}
					if err := relay.SaveNativeAsyncImageSnapshot(task, snapshot); err != nil {
						return nil, channel.Id, proxy, nil, err
					}
					if err := persistAsyncImageAttempt(task); err != nil {
						return nil, channel.Id, proxy, nil, err
					}
					continue
				}
				var usage *dto.Usage
				if value, ok := c.Get("async_image_usage"); ok {
					usage, _ = value.(*dto.Usage)
				}
				return recorder.Body.Bytes(), channel.Id, proxy, usage, nil
			}
			service.RecordAutoSwitchRequestResult(c, false, apiErr)
		}
		apiErr = service.NormalizeViolationFeeError(apiErr)
		detail := buildInternalAsyncChannelRetryDetail(len(task.PrivateData.ChannelRetryPath), channel, apiErr)
		if upstreamErrorType != "" {
			detail.ErrorType = upstreamErrorType
		}
		retry.Details = append(retry.Details, detail)
		if key := common.GetContextKeyString(c, constant.ContextKeyChannelKey); key != "" {
			c.Set("async_channel_retry_path", task.PrivateData.ChannelRetryPath)
			processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, key, channel.GetAutoBan()), apiErr, nil)
		}
		common.SetContextKey(c, constant.ContextKeyChannelConstraints, &retry.Constraints)
		remaining := common.RetryTimes - len(task.PrivateData.ChannelRetryPath) + 1
		_, pinned, _ := retry.Constraints.ResolvedPin()
		canRetry := remaining > 0 && !pinned && shouldRetry(c, apiErr, remaining)
		// A broken connection after POST may hide an accepted job. A status
		// code synthesized for a transport error is not an upstream rejection.
		if apiErr.GetErrorCode() == types.ErrorCodeDoRequestFailed || apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed || apiErr.GetErrorCode() == types.ErrorCodeBadResponseBody {
			canRetry = false
		}
		if !canRetry {
			recordInternalAsyncImageFinalErrorLog(c, task, channel, apiErr, task.PrivateData.ChannelRetryPath, retry.Details)
			return nil, task.ChannelId, proxy, nil, apiErr
		}
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, task.ChannelId, proxy, nil, err
		}
	}
}

func persistAsyncImageAttempt(task *model.Task) error {
	task.PrivateData.WorkerHeartbeatAt = common.GetTimestamp()
	won, err := task.UpdateInternalAsyncImageTask(model.TaskStatusInProgress, task.PrivateData.WorkerAttempts)
	if err != nil {
		return err
	}
	if !won {
		return fmt.Errorf("async image worker no longer owns the task")
	}
	return nil
}
