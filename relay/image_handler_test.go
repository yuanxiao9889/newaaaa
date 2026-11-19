package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageBillingTransport func(*http.Request) (*http.Response, error)

func TestImageAnimationParametersSurviveJSONRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"all-image-2.6-sunburst-H", "all-image-2.6-flare-H", "gpt-image-2.6-sunburst"} {
		for _, mode := range []int{relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits} {
			for _, tc := range []struct{ name, fields string }{
				{"animation", `,"frame_count":12,"frame_duration_ms":150`},
				{"explicit zero", `,"frame_count":0,"frame_duration_ms":0`},
				{"count only", `,"frame_count":9`},
				{"duration only", `,"frame_duration_ms":200`},
				{"omitted", ``},
				{"null", `,"frame_count":null,"frame_duration_ms":null`},
			} {
				t.Run(fmt.Sprintf("%s/%d/%s", name, mode, tc.name), func(t *testing.T) {
					body := fmt.Sprintf(`{"model":%q,"prompt":"animate","quality":"max","output_format":"gif","size":"512x512","unknown_extension":"must not leak"%s}`, name, tc.fields)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")
					parsed, err := helper.GetAndValidOpenAIImageRequest(c, mode)
					require.NoError(t, err)
					request, err := common.DeepCopy(parsed)
					require.NoError(t, err)
					info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: name, RelayMode: mode}
					info.UpstreamModelName = name
					c.Set("model_mapping", `{"all-image-2.6-sunburst-H":"gpt-image-2.6-sunburst","all-image-2.6-flare-H":"gpt-image-2.6-flare"}`)
					require.NoError(t, helper.ModelMappedHelper(c, info, request))
					converted, err := (&openai.Adaptor{}).ConvertImageRequest(c, info, *request)
					require.NoError(t, err)
					out, err := common.Marshal(converted)
					require.NoError(t, err)
					var input, actual map[string]any
					require.NoError(t, common.UnmarshalJsonStr(body, &input))
					require.NoError(t, common.Unmarshal(out, &actual))
					for _, field := range []string{"frame_count", "frame_duration_ms"} {
						if value, exists := input[field]; exists && value != nil {
							assert.Equal(t, value, actual[field], field)
						} else {
							assert.NotContains(t, actual, field)
						}
					}
					assert.Equal(t, info.UpstreamModelName, actual["model"])
					assert.Equal(t, name, parsed.Model, "mapping must not mutate the retry source")
					assert.Equal(t, "max", actual["quality"])
					assert.Equal(t, "gif", actual["output_format"])
					assert.NotContains(t, actual, "unknown_extension")
				})
			}
		}
	}
	for _, invalid := range []string{`{"frame_count":1.5}`, `{"frame_duration_ms":"150"}`, `{"frame_count":18446744073709551615}`} {
		var request dto.ImageRequest
		require.Error(t, common.UnmarshalJsonStr(invalid, &request), "invalid integer controls must not be silently discarded")
	}
}

func (f imageBillingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type imageBillingBody struct {
	io.Reader
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *imageBillingBody) Read(p []byte) (int, error) {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.Reader.Read(p)
}

type disconnectedImageWriter struct{ gin.ResponseWriter }

func (w disconnectedImageWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSynchronousImageDisconnectBilling(t *testing.T) {
	db := setupMidjourneyQuotaDB(t)
	common.LogConsumeEnabled = true
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	client := service.GetHttpClient()
	originalTransport := client.Transport
	t.Cleanup(func() { client.Transport = originalTransport })

	for i, tc := range []struct {
		name, cancelAt         string
		status                 int
		worker, stream, tokens bool
		wantCharge             int
	}{
		{name: "disconnect before response headers", cancelAt: "headers", status: 200, wantCharge: 20000},
		{name: "disconnect while reading response body", cancelAt: "body", status: 200, wantCharge: 20000},
		{name: "token usage settles after disconnect", cancelAt: "body", status: 200, tokens: true, wantCharge: 500},
		{name: "upstream rejection still refunds", cancelAt: "headers", status: 400},
		{name: "canceled before submission", cancelAt: "submission", status: 200},
		{name: "worker cancellation remains effective", cancelAt: "headers", status: 200, worker: true},
		{name: "stream cancellation remains effective", cancelAt: "headers", status: 200, stream: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("sync-image-%d", i)
			user := &model.User{Username: name, AffCode: name, Quota: 1000000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(user).Error)
			token := &model.Token{UserId: user.Id, Key: name, Name: name, RemainQuota: 1000000, Status: common.TokenStatusEnabled, ExpiredTime: -1}
			require.NoError(t, db.Create(token).Error)
			channel := &model.Channel{Name: name, Key: "test", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(channel).Error)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Writer = disconnectedImageWriter{c.Writer}
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"test-image","prompt":"draw","n":2}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://image.test")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "test-image")
			c.Set("async_image_worker", tc.worker)
			request, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			require.NoError(t, err)
			if tc.worker {
				request.N = common.GetPointer(uint(1))
			}
			info := &relaycommon.RelayInfo{
				Request: request, OriginModelName: request.Model, RelayMode: relayconstant.RelayModeImagesGenerations,
				RelayFormat: types.RelayFormatOpenAIImage, RequestURLPath: c.Request.URL.Path,
				UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, ForcePreConsume: true,
				UsingGroup: "default", StartTime: time.Now(), IsStream: tc.stream,
				PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			}
			info.UserSetting.BillingPreference = "wallet_only"
			if tc.tokens {
				info.ImageTokenReserveUSD = 0.04
				expr := `tier("base", p * 2 + c * 4)`
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: common.QuotaPerUnit, EstimatedImageCount: common.GetPointer(2)}
				info.BillingRequestInput = &billingexpr.RequestInput{ImageCount: common.GetPointer(2)}
				info.PriceData.UsePrice = false
			}
			require.Nil(t, service.PreConsumeBilling(c, 20000, info))
			calls := 0
			client.Transport = imageBillingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.cancelAt == "headers" {
					cancel()
				}
				if err := req.Context().Err(); err != nil {
					return nil, err
				}
				body := `{"data":[{"url":"https://image.test/result.png"}],"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300}}`
				if tc.status != http.StatusOK {
					body = `{"error":{"type":"invalid_request_error","message":"generation rejected"}}`
				}
				reader := &imageBillingBody{Reader: strings.NewReader(body), ctx: req.Context()}
				if tc.cancelAt == "body" {
					reader.cancel = cancel
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(reader), Request: req}, nil
			})
			if tc.cancelAt == "submission" {
				cancel()
			}
			apiErr := ImageHelper(c, info)
			if tc.wantCharge > 0 {
				require.Nil(t, apiErr)
			} else {
				require.NotNil(t, apiErr)
			}
			RefundFailedRequestBilling(c, info, apiErr)
			assert.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
			wantCalls := 1
			if tc.cancelAt == "submission" {
				wantCalls = 0
			}
			assert.Equal(t, wantCalls, calls)
			require.NoError(t, db.First(user, user.Id).Error)
			require.NoError(t, db.First(token, token.Id).Error)
			require.NoError(t, db.First(channel, channel.Id).Error)
			assert.Equal(t, 1000000-tc.wantCharge, user.Quota)
			assert.Equal(t, 1000000-tc.wantCharge, token.RemainQuota)
			assert.Equal(t, tc.wantCharge, user.UsedQuota)
			assert.Equal(t, int64(tc.wantCharge), channel.UsedQuota)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			if tc.wantCharge > 0 {
				require.Len(t, logs, 1)
				assert.Equal(t, tc.wantCharge, logs[0].Quota)
				assert.Equal(t, 100, logs[0].PromptTokens)
				assert.Equal(t, 200, logs[0].CompletionTokens)
			} else {
				assert.Empty(t, logs)
			}
		})
	}
}

func TestMappedImageJSONBodyRewritesOnlyModel(t *testing.T) {
	storage, err := common.CreateBodyStorage([]byte(`{"model":"dou-5-0-pro","prompt":"draw","reference_image_upload":true}`))
	require.NoError(t, err)
	defer storage.Close()

	body, err := mappedImageJSONBody(storage, "seedream-5.0-pro")
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, common.DecodeJson(body, &payload))
	var modelName string
	require.NoError(t, common.Unmarshal(payload["model"], &modelName))
	require.Equal(t, "seedream-5.0-pro", modelName)
	var preserved bool
	require.NoError(t, common.Unmarshal(payload["reference_image_upload"], &preserved))
	require.True(t, preserved)
}

func TestImageRequestMarshalPreservesSeedreamReferenceFields(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"dou-5-0-pro",
		"prompt":"change clothing",
		"image_url":"https://relay.example/reference.png",
		"strength":"HIGH"
	}`), &request))

	payload, err := common.Marshal(request)
	require.NoError(t, err)
	var body map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(payload, &body))

	var imageURL, strength string
	require.NoError(t, common.Unmarshal(body["image_url"], &imageURL))
	require.NoError(t, common.Unmarshal(body["strength"], &strength))
	require.Equal(t, "https://relay.example/reference.png", imageURL)
	require.Equal(t, "HIGH", strength)
}

func TestImageHelperConvertImageRequestErrorReturnsBadRequestAndSkipsRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	req := &dto.ImageRequest{
		Model:  "monkey-image-pro",
		Prompt: "draw a cat",
	}
	info := &relaycommon.RelayInfo{
		Request:         req,
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		RelayFormat:     types.RelayFormatOpenAIImage,
		OriginModelName: "monkey-image-pro",
		RequestURLPath:  "/v1/images/generations",
	}

	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "monkey-image-pro")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	c.Set("model_mapping", `{"monkey-image-pro":"gemini-3-pro-image-preview"}`)

	err := ImageHelper(c, info)
	if err == nil {
		t.Fatal("expected ImageHelper to fail")
	}
	if got, want := err.StatusCode, http.StatusBadRequest; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if got, want := err.GetErrorCode(), types.ErrorCodeConvertRequestFailed; got != want {
		t.Fatalf("error code = %s, want %s", got, want)
	}
	if !types.IsSkipRetryError(err) {
		t.Fatal("expected convert request error to skip retry")
	}
}
