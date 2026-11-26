package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
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
	// Older plugin snapshots saved their executor format as the client format.
	// Both executors consume the same retained public Images request.
	if retry.RelayFormat == relay.PluginAsyncImageFormat {
		retry.RelayFormat = string(types.RelayFormatOpenAIImage)
	}
	if retry.Submitting {
		return nil, task.ChannelId, "", nil, fmt.Errorf("async image submission outcome is unknown; automatic resubmission stopped")
	}
	if retry.StartedAtMS == 0 {
		retry.StartedAtMS = time.Now().UnixMilli()
		if len(task.PrivateData.ChannelRetryPath) > 0 && task.SubmitTime > 0 {
			retry.StartedAtMS = task.SubmitTime * 1000
		}
	}
	budget := service.MediaRetryBudget(task.PrivateData.MediaRetry, time.UnixMilli(retry.StartedAtMS))
	cycle := &service.ChannelRetryCycle{Enabled: task.PrivateData.MediaRetry != nil || model.CurrentRequestPolicy().RetryCycleEnabled, Start: retry.CycleStart, Budget: budget}
	var attemptContext *gin.Context
	defer func() {
		if attemptContext != nil {
			service.ReleaseAutoSwitchAttempt(attemptContext)
		}
	}()
	for {
		if attemptContext != nil {
			service.ReleaseAutoSwitchAttempt(attemptContext)
			attemptContext = nil
		}
		if err := ctx.Err(); err != nil {
			return nil, task.ChannelId, "", nil, err
		}
		var c *gin.Context
		var channel *model.Channel
		var apiErr *types.NewAPIError
		var proxy string
		var upstreamErrorType string
		var finishedAt time.Time
		confirmedTaskFailure := false
		if task.PrivateData.RequestRelayFormat == relay.NativeAsyncImageFormat {
			body, channelID, usedProxy, err := relay.ResumeNativeAsyncImageTask(ctx, task)
			finishedAt = time.Now()
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
			if failure.ConfirmedFailure {
				confirmedTaskFailure = true
				apiErr = service.NewUpstreamTaskFailure(failure, types.ErrorCode(code), failure.Type, status)
			} else {
				apiErr = types.NewErrorWithStatusCode(failure, types.ErrorCode(code), status, types.ErrOptionWithSkipRetry())
			}
			// Return to the retained client snapshot only after a terminal failure.
			task.PrivateData.RequestRelayFormat = retry.RelayFormat
		} else {
			if task.PrivateData.PluginImage == nil && len(task.PrivateData.ChannelRetryPath) > 0 && budget.Remaining(common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1, len(task.PrivateData.ChannelRetryPath), time.Now()) <= 0 {
				return nil, task.ChannelId, "", nil, fmt.Errorf("async image retry budget exhausted")
			}
			body, err := os.ReadFile(retry.BodyPath)
			if err != nil {
				return nil, task.ChannelId, "", nil, fmt.Errorf("read original image request: %w", err)
			}
			var recorder *httptest.ResponseRecorder
			var info *relaycommon.RelayInfo
			if isCompatibleImageFormat(retry.RelayFormat) {
				c, recorder, info, err = buildInternalAsyncCompatibleImageContext(ctx, task, body)
			} else if retry.RelayFormat == string(types.RelayFormatGemini) {
				c, recorder, _, info, err = buildInternalAsyncGeminiImageContext(ctx, task, body)
			} else {
				c, recorder, _, info, err = buildInternalAsyncImageContext(ctx, task, body)
			}
			if err != nil {
				return nil, task.ChannelId, "", nil, err
			}
			service.BindMediaRetry(c, task.PrivateData.MediaRetry)
			if retry.UserGroup != "" {
				common.SetContextKey(c, constant.ContextKeyUserGroup, retry.UserGroup)
			}
			service.PersistMediaRetryBeforeDispatch(c, func() error { return persistAsyncImageAttempt(task) })
			attemptContext = c
			constraints := service.GetChannelConstraints(c)
			constraints.Pins = append(constraints.Pins, retry.Constraints.Pins...)
			constraints.Filters = append(constraints.Filters, retry.Constraints.Filters...)
			if execution := task.PrivateData.Execution; execution != nil && execution.TaskPlugin != nil {
				// Resume a checkpoint with its own plugin. Older plugin tasks that
				// only recorded the selected plugin can also safely use that key.
				if task.PrivateData.PluginImage != nil || !slices.ContainsFunc(retry.Constraints.Filters, func(f dto.ChannelFilter) bool {
					return f.Kind == dto.FilterTaskPluginIdentity && f.TaskPluginKey != ""
				}) {
					constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: execution.TaskPlugin.Key})
				}
			}
			var endpoint *pluginruntime.PinnedEndpoint
			if retry.RelayFormat == string(types.RelayFormatOpenAIImage) {
				endpoint = middleware.PrepareImageRetryEndpoint(c, task.Properties.OriginModelName)
			}
			identity := dto.ChannelFilter{Kind: dto.FilterChannelIdentity}
			excluded := task.PrivateData.ChannelRetryPath
			if cycle.Enabled {
				excluded = nil // The cycle selector owns this round's exclusions.
			}
			for _, used := range excluded {
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
			cycle.History = task.PrivateData.ChannelRetryPath
			if task.PrivateData.PluginImage != nil {
				channel, err = model.GetChannelById(task.ChannelId, true)
				if err != nil {
					common.CleanupBodyStorage(c)
					return nil, task.ChannelId, "", nil, err
				}
				apiErr = middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
			} else {
				channel, apiErr = getChannel(c, info, &service.RetryParam{Cycle: cycle, Ctx: c, TokenGroup: task.Group, ModelName: task.Properties.OriginModelName, RequestPath: c.Request.URL.Path})
			}
			if apiErr != nil {
				common.CleanupBodyStorage(c)
				recordInternalAsyncImageFinalErrorLog(c, task, nil, apiErr, task.PrivateData.ChannelRetryPath, retry.Details)
				return nil, task.ChannelId, "", nil, apiErr
			}
			if endpoint != nil {
				for _, candidate := range endpoint.Candidates {
					filter := dto.ChannelFilter{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: candidate.Plugin.Meta.Key, TaskPluginChannelTypes: candidate.Plugin.Meta.ChannelTypes}
					if matches, _ := model.ChannelSatisfiesFilters(channel, info.OriginModelName, []dto.ChannelFilter{filter}); !matches {
						continue
					}
					endpoint.Plugin, endpoint.Protocol, endpoint.Operation = candidate.Plugin, candidate.Protocol, candidate.Operation
					endpoint.Candidates = []pluginruntime.ProtocolBinding{candidate}
					c.Set(pluginruntime.ContextKeyPinnedEndpoint, *endpoint)
					c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: endpoint.Generation, Plugin: candidate.Plugin})
					middleware.PrepareTaskPluginEndpoint()(c)
					if c.IsAborted() {
						common.CleanupBodyStorage(c)
						return nil, channel.Id, "", nil, fmt.Errorf("queued image plugin request can no longer be decoded")
					}
					if setupErr := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName); setupErr != nil {
						common.CleanupBodyStorage(c)
						return nil, channel.Id, "", nil, setupErr
					}
					break
				}
			}
			if _, plugin := c.Get(pluginruntime.ContextKeyPinnedEndpoint); plugin {
				result, usedProxy, again, pluginErr := runQueuedTaskPluginImageAttempt(ctx, task, cycle, c, channel)
				common.CleanupBodyStorage(c)
				if again {
					continue
				}
				return result, task.ChannelId, usedProxy, nil, pluginErr
			}
			if task.PrivateData.PluginImage != nil {
				common.CleanupBodyStorage(c)
				return nil, channel.Id, "", nil, fmt.Errorf("accepted image plugin is unavailable")
			}
			if len(task.PrivateData.ChannelRetryPath) > 0 && budget.Remaining(1, len(task.PrivateData.ChannelRetryPath), time.Now()) == 0 {
				common.CleanupBodyStorage(c)
				return nil, task.ChannelId, "", nil, fmt.Errorf("async image retry time budget exhausted")
			}
			retry.CycleStart = cycle.Start
			proxy = channel.GetSetting().Proxy
			task.ChannelId = channel.Id
			task.PrivateData.ChannelRetryPath = append(task.PrivateData.ChannelRetryPath, strconv.Itoa(channel.Id))
			task.PrivateData.UpstreamTaskID = ""
			task.PrivateData.AutoSwitch = service.AutoSwitchAttempt(c)
			retry.Submitting = true
			if task.PrivateData.RequestRelayFormat == relay.PluginAsyncImageFormat {
				// The provider's u() expression cannot price an ordinary relay.
				// Re-estimate from the original request before reserving a delta.
				info.TieredBillingSnapshot = nil
				info.ImageTokenFallbackQuota = nil
				info.ImageTokenReserveUSD = 0
				info.InitChannelMeta(c)
				if priceErr := relay.EstimateRequestBilling(c, info); priceErr != nil {
					common.CleanupBodyStorage(c)
					return nil, channel.Id, proxy, nil, priceErr
				}
				info.Billing, err = service.NewImageTaskRetryBilling(ctx, task, info)
				if err != nil {
					common.CleanupBodyStorage(c)
					return nil, channel.Id, proxy, nil, err
				}
				// Persist the submitting claim before the reservation's CAS.
				if err := persistAsyncImageAttempt(task); err != nil {
					common.CleanupBodyStorage(c)
					return nil, channel.Id, proxy, nil, err
				}
				if err := info.Billing.Reserve(info.PriceData.QuotaToPreConsume); err != nil {
					common.CleanupBodyStorage(c)
					return nil, channel.Id, proxy, nil, err
				}
				task.PrivateData.BillingContext = &model.TaskBillingContext{
					ModelPrice: info.PriceData.ModelPrice, FixedPriceUnit: info.PriceData.FixedPriceUnit,
					GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio, ModelRatio: info.PriceData.ModelRatio,
					OtherRatios: info.PriceData.OtherRatios(), OriginModelName: info.OriginModelName,
					PerCallBilling: info.PriceData.UsePrice, TieredSnapshot: info.TieredBillingSnapshot,
					ImageTokenFallbackQuota: info.ImageTokenFallbackQuota, ImageTokenReserveUSD: info.ImageTokenReserveUSD,
				}
				retry.SettlementQuota = common.GetPointer(info.PriceData.QuotaToPreConsume)
				if input := info.BillingRequestInput; input != nil {
					// Image pricing contains only validated scalar parameters. Keep
					// those frozen for settlement; never persist request credentials.
					retry.SettlementInput = &billingexpr.RequestInput{Body: append([]byte(nil), input.Body...), ImageCount: input.ImageCount}
				}
				if info.ImageTokenFallbackQuota != nil {
					retry.SettlementQuota = common.GetPointer(*info.ImageTokenFallbackQuota)
				}
				task.PrivateData.RequestRelayFormat = retry.RelayFormat
				if task.PrivateData.Execution != nil {
					task.PrivateData.Execution.TaskPlugin = nil
				}
			}
			if err := persistAsyncImageAttempt(task); err != nil {
				common.CleanupBodyStorage(c)
				return nil, channel.Id, proxy, nil, err
			}
			info.RetryIndex = len(task.PrivateData.ChannelRetryPath) - 1
			addUsedChannel(c, channel.Id)
			retry.AttemptStartedAtMS = time.Now().UnixMilli()
			c.Request = c.Request.WithContext(service.BeginUpstreamDelivery(c.Request.Context(), channel.Id, len(task.PrivateData.ChannelRetryPath)))
			if isCompatibleImageFormat(retry.RelayFormat) {
				apiErr = runCompatibleImageHelper(c, info, retry.RelayFormat)
			} else if retry.RelayFormat == string(types.RelayFormatGemini) {
				apiErr = geminiRelayHandler(c, info)
			} else {
				apiErr = relay.ImageHelper(c, info)
			}
			finishedAt = time.Now()
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
		detail := buildInternalAsyncChannelRetryDetail(len(task.PrivateData.ChannelRetryPath), channel, apiErr, retry.AttemptStartedAtMS, finishedAt)
		if upstreamErrorType != "" {
			detail.ErrorType = upstreamErrorType
		}
		retry.Details = append(retry.Details, detail)
		if key := common.GetContextKeyString(c, constant.ContextKeyChannelKey); key != "" {
			c.Set("async_channel_retry_path", task.PrivateData.ChannelRetryPath)
			processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, key, channel.GetAutoBan()), apiErr, nil)
		}
		service.BindMediaRetry(c, task.PrivateData.MediaRetry)
		c.Set("channel_id", channel.Id)
		common.SetContextKey(c, constant.ContextKeyChannelConstraints, &retry.Constraints)
		remaining := budget.Remaining(common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1, len(task.PrivateData.ChannelRetryPath), time.Now())
		_, pinned, _ := retry.Constraints.ResolvedPin()
		canRetry := remaining > 0 && !pinned && !retry.SkipRetry && shouldRetry(c, apiErr, remaining)
		// A broken connection after POST may hide an accepted job. A status
		// code synthesized for a transport error is not an upstream rejection.
		if !confirmedTaskFailure && (apiErr.GetErrorCode() == types.ErrorCodeDoRequestFailed || apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed || apiErr.GetErrorCode() == types.ErrorCodeBadResponseBody) {
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
