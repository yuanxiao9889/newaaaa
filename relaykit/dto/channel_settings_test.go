package dto

import (
	"encoding/json"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"regexp"
	"testing"
)

func TestAdvancedCustomValidateResponsesToChatConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
		},
	}
	require.NoError(t, valid.Validate())

	validGemini := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}
	require.NoError(t, validGemini.Validate())

	tests := []struct {
		name         string
		incomingPath string
	}{
		{name: "chat completions", incomingPath: "/v1/chat/completions"},
		{name: "responses compact", incomingPath: "/v1/responses/compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: tt.incomingPath,
						UpstreamPath: "/v1/chat/completions",
						Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestAdvancedCustomValidateModelListRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "https://upstream.example/custom/models",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Models:       []string{"gpt-4o"},
				},
			},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
				},
			},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models/{model}",
				},
			},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/v1/models"},
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/provider/models"},
			},
			want: "duplicates the /v1/models route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomModelListRouteRequiresExactIncomingPath(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/{model}",
				UpstreamPath: "/generic/{model}",
			},
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.ModelListRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/models", route.UpstreamPath)
}

func TestAdvancedCustomValidateBalanceRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{{
			IncomingPath: AdvancedCustomBalancePath,
			UpstreamPath: "/provider/balance",
			Converter:    advancedCustomConverterNone,
		}},
	}
	require.NoError(t, valid.Validate())

	route, ok := valid.BalanceRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/balance", route.UpstreamPath)

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Models:       []string{"gpt-4o"},
			}},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
			}},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/{model}/balance",
			}},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/balance"},
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/credits"},
			},
			want: "duplicates the /v1/dashboard/billing/credit_grants route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathWithDisjointModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	require.NoError(t, config.Validate())
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsOverlappingModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"shared-model"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"shared-model"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsMultipleCatchAllRoutes(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all already exists")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRequiresCatchAllLast(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all route must be last")
}

func TestAdvancedCustomMatchPathForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "unknown-model")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomMatchPathForModelRegexRules(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:(?i)^OAI-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "oai-test")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomRouteModelRegexRulesAreCachedCompiled(t *testing.T) {
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-model"))

	cached, ok := advancedCustomModelRegexCache.Load("^cache-probe-")
	require.True(t, ok)
	require.NotNil(t, cached)
	_, isRegexp := cached.(*regexp.Regexp)
	require.True(t, isRegexp)

	// Invalid patterns never match and are cached as nil so they are not recompiled.
	require.False(t, matchAdvancedCustomRouteModelRule("re:(", "anything"))
	cached, ok = advancedCustomModelRegexCache.Load("(")
	require.True(t, ok)
	re, _ := cached.(*regexp.Regexp)
	require.Nil(t, re)

	// Cached entries keep matching correctly on subsequent calls.
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-other"))
	require.False(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "other-model"))
}

func TestAdvancedCustomMatchPathForModelExactRuleDoesNotMatchPrefix(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsInvalidRegexModels(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		want   string
	}{
		{name: "empty regex", models: []string{"re:"}, want: "regex is empty"},
		{name: "invalid regex", models: []string{"re:["}, want: "regex is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/responses",
						UpstreamPath: "/v1beta/models/{model}:generateContent",
						Converter:    advancedCustomConverterOpenAIResponsesToGemini,
						Models:       tt.models,
					},
				},
			}

			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsDuplicateRegexModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:^gemini-"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomMatchPathForModelUsesFirstMatchingRegexRoute(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, route.Converter)
}

func TestAdvancedCustomSupportedEndpointTypesForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:generateContent",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:streamGenerateContent",
				UpstreamPath: "/v1beta/models/{model}:streamGenerateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/messages",
			},
			{
				IncomingPath: "/custom/endpoint",
				UpstreamPath: "/custom/endpoint",
			},
		},
	}
	require.NoError(t, config.Validate())

	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIResponse,
		types.EndpointTypeGemini,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gemini-2.5-flash"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAI,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gpt-4o"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("other-model"))
}

func TestAdvancedCustomValidateAlphaSearchConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/alpha/search",
				UpstreamPath: "/v1/alpha/search",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIAlphaSearch,
	}, valid.SupportedEndpointTypesForModel("gpt-5.1"))

	nonNoneConverters := []string{
		advancedCustomConverterClaudeMessagesToOpenAIChat,
		advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIResponsesToOpenAIChat,
		advancedCustomConverterOpenAIResponsesToGemini,
		advancedCustomConverterGeminiContentToOpenAIChat,
		advancedCustomConverterOpenAIChatToGeminiContent,
	}
	for _, converter := range nonNoneConverters {
		t.Run(converter, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/alpha/search",
						UpstreamPath: "/v1/alpha/search",
						Converter:    converter,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestAdvancedCustomValidateRoutePassThroughBody(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath:           "/v1/chat/completions",
				UpstreamPath:           "/v1/chat/completions",
				PassThroughBodyEnabled: true,
			},
			{
				IncomingPath:           "/v1/rerank",
				UpstreamPath:           "/v1/rerank",
				Converter:              AdvancedCustomConverterSGLangRerank,
				PassThroughBodyEnabled: true,
			},
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterClaudeMessagesToOpenAIChat,
			},
		},
	}
	require.NoError(t, valid.Validate())

	route, ok := valid.MatchPathForModel("/v1/chat/completions", "gpt-4o")
	require.True(t, ok)
	assert.True(t, route.PassThroughBodyEnabled)
	route, ok = valid.MatchPathForModel("/v1/messages", "gpt-4o")
	require.True(t, ok)
	assert.False(t, route.PassThroughBodyEnabled)

	encoded, err := json.Marshal(valid.Routes[2])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "pass_through_body_enabled")

	tests := []struct {
		name  string
		route AdvancedCustomRoute
		want  string
	}{
		{
			name: "converter route",
			route: AdvancedCustomRoute{
				IncomingPath:           "/v1/messages",
				UpstreamPath:           "/v1/chat/completions",
				Converter:              advancedCustomConverterClaudeMessagesToOpenAIChat,
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled requires converter none",
		},
		{
			name: "model list route",
			route: AdvancedCustomRoute{
				IncomingPath:           AdvancedCustomModelListPath,
				UpstreamPath:           "/v1/models",
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled must be false for /v1/models",
		},
		{
			name: "balance route",
			route: AdvancedCustomRoute{
				IncomingPath:           AdvancedCustomBalancePath,
				UpstreamPath:           "/provider/balance",
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled must be false for /v1/dashboard/billing/credit_grants",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: []AdvancedCustomRoute{tt.route}}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestChannelSettingsHTTPTransportJSONRoundTrip(t *testing.T) {
	legacy := `{"proxy":"http://127.0.0.1:8080","force_format":true}`
	var settings ChannelSettings
	require.NoError(t, json.Unmarshal([]byte(legacy), &settings))
	assert.Equal(t, "http://127.0.0.1:8080", settings.Proxy)
	assert.True(t, settings.ForceFormat)
	assert.Empty(t, settings.HTTPProtocol)
	assert.Zero(t, settings.HTTP2ConnectionShards)

	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "http_protocol")
	assert.NotContains(t, string(encoded), "http2_connection_shards")

	explicit := ChannelSettings{
		Proxy:                 "socks5://127.0.0.1:1080",
		HTTPProtocol:          HTTPProtocolHTTP1,
		HTTP2ConnectionShards: 1,
	}
	encoded, err = json.Marshal(explicit)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http_protocol":"http1"`)

	var decoded ChannelSettings
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, explicit.HTTPProtocol, decoded.HTTPProtocol)
	assert.Equal(t, 1, decoded.HTTP2ConnectionShards)

	sharded := ChannelSettings{HTTP2ConnectionShards: 4}
	encoded, err = json.Marshal(sharded)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http2_connection_shards":4`)
	assert.NotContains(t, string(encoded), "http_protocol")
}

func TestChannelSettingsValidateHTTPTransport(t *testing.T) {
	require.NoError(t, (&ChannelSettings{}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "AUTO"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "http1"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTP2ConnectionShards: 8}).ValidateHTTPTransport())

	err := (&ChannelSettings{HTTPProtocol: "http2"}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http_protocol")

	err = (&ChannelSettings{HTTP2ConnectionShards: -1}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTP2ConnectionShards: 9}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTPProtocol: "http1", HTTP2ConnectionShards: 2}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")
}

func TestChannelOtherSettingsValidateToolLossPolicy(t *testing.T) {
	require.NoError(t, (*ChannelOtherSettings)(nil).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "allow"}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "safe"}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "strict"}).ValidateToolLossPolicy())

	err := (&ChannelOtherSettings{ToolLossPolicy: "drop"}).ValidateToolLossPolicy()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool_loss_policy")
}

func TestChannelSettingsMultiImageReferenceJSONContract(t *testing.T) {
	enabled := ChannelSettings{MultiImageReferenceEnabled: true}
	encoded, err := json.Marshal(enabled)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"multi_image_reference_enabled":true`)

	var decoded ChannelSettings
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.True(t, decoded.MultiImageReferenceEnabled)

	encoded, err = json.Marshal(ChannelSettings{})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "multi_image_reference_enabled")
}

func imageAdapterTestConfig() *ImageAdapterConfig {
	return &ImageAdapterConfig{
		Enabled: true,
		Models:  []string{"upstream-image"},
		Request: ImageAdapterRequestConfig{
			GenerationsPath: "/v1/images/generations",
			EditsPath:       "/v1/images/generations",
			FieldMap:        map[string]string{"quality": "detail_level"},
			ValueMap:        map[string]map[string]string{"quality": {"high": "High"}},
			SizeMap:         map[string]map[string]string{"2048x2048": {"quality": "2K", "aspect_ratio": "1:1"}},
			DropFields:      []string{"size", "response_format"},
			RejectFields:    []string{"mask", "background"},
		},
		References: &ImageAdapterReferenceConfig{Field: "input_reference", Encoding: "url_array", MaxImages: 16, UploadLocal: true},
		Task: &ImageAdapterTaskConfig{
			IDPaths:            []string{"id", "task_id"},
			StatusPath:         "status",
			ResultURLPath:      "data.0.url",
			ErrorPath:          "error.message",
			PollURLTemplate:    "/v1/images/generations/{task_id}",
			ContentURLTemplate: "/v1/images/{task_id}/content",
			PendingStates:      []string{"queued", "in_progress"},
			SuccessStates:      []string{"completed"},
			FailureStates:      []string{"failed"},
		},
	}
}

func TestImageAdapterConfigurationRoundTripAndModelSelection(t *testing.T) {
	settings := ChannelSettings{ImageAdapter: imageAdapterTestConfig()}
	require.NoError(t, settings.ValidateImageAdapter())
	encoded, err := kitutil.Marshal(settings)
	require.NoError(t, err)
	var decoded ChannelSettings
	require.NoError(t, kitutil.Unmarshal(encoded, &decoded))
	assert.Equal(t, settings.ImageAdapter, decoded.ImageAdapter)
	assert.True(t, decoded.ImageAdapter.Matches(" upstream-image "))
	assert.False(t, decoded.ImageAdapter.Matches("public-image-alias"))
	assert.False(t, decoded.ImageAdapter.Matches("upstream-image-extra"))
	decoded.ImageAdapter.Models = nil
	assert.True(t, decoded.ImageAdapter.Matches("another-image"))
	decoded.ImageAdapter.Enabled = false
	assert.False(t, decoded.ImageAdapter.Matches("upstream-image"))
	assert.False(t, (*ImageAdapterConfig)(nil).Matches("upstream-image"))

	encoded, err = kitutil.Marshal(ChannelSettings{})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "image_adapter")
	require.NoError(t, (&ChannelSettings{}).ValidateImageAdapter())
	require.NoError(t, (&ChannelSettings{PassThroughBodyEnabled: true, ImageAdapter: &ImageAdapterConfig{}}).ValidateImageAdapter())
}

func TestImageAdapterRejectsInvalidAndBillingUnsafeConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ImageAdapterConfig)
	}{
		{"empty enabled configuration", func(c *ImageAdapterConfig) { *c = ImageAdapterConfig{Enabled: true} }},
		{"model aliases are not patterns", func(c *ImageAdapterConfig) { c.Models = []string{" ", "upstream-image"} }},
		{"duplicate model", func(c *ImageAdapterConfig) { c.Models = []string{"image", "image"} }},
		{"field path nesting", func(c *ImageAdapterConfig) { c.Request.FieldMap["quality"] = "config.detail" }},
		{"duplicate output target", func(c *ImageAdapterConfig) { c.Request.FieldMap["style"] = "detail_level" }},
		{"rename image count", func(c *ImageAdapterConfig) { c.Request.FieldMap["n"] = "copies" }},
		{"overwrite image count", func(c *ImageAdapterConfig) { c.Request.FieldMap["style"] = "n" }},
		{"modify image count", func(c *ImageAdapterConfig) { c.Request.ValueMap["n"] = map[string]string{"1": "2"} }},
		{"drop image count", func(c *ImageAdapterConfig) { c.Request.DropFields = append(c.Request.DropFields, "n") }},
		{"size writes count", func(c *ImageAdapterConfig) { c.Request.SizeMap["2048x2048"]["n"] = "2" }},
		{"prototype field", func(c *ImageAdapterConfig) { c.Request.FieldMap["style"] = "__proto__" }},
		{"invalid dimensions", func(c *ImageAdapterConfig) { c.Request.SizeMap["2048*2048"] = map[string]string{"quality": "2K"} }},
		{"empty value mapping", func(c *ImageAdapterConfig) { c.Request.ValueMap["quality"] = map[string]string{} }},
		{"invalid reference encoding", func(c *ImageAdapterConfig) { c.References.Encoding = "multipart" }},
		{"too many references", func(c *ImageAdapterConfig) { c.References.MaxImages = 17 }},
		{"reference overwrites count", func(c *ImageAdapterConfig) { c.References.Field = "n" }},
		{"json path modifier", func(c *ImageAdapterConfig) { c.Task.ResultURLPath = "data.@reverse.0.url" }},
		{"json path wildcard", func(c *ImageAdapterConfig) { c.Task.StatusPath = "data.*.status" }},
		{"missing task id path", func(c *ImageAdapterConfig) { c.Task.IDPaths = nil }},
		{"no result source", func(c *ImageAdapterConfig) { c.Task.ResultURLPath, c.Task.ContentURLTemplate = "", "" }},
		{"overlapping terminal states", func(c *ImageAdapterConfig) { c.Task.FailureStates = []string{"COMPLETED"} }},
		{"missing success states", func(c *ImageAdapterConfig) { c.Task.SuccessStates = nil }},
		{"poll interval below minimum", func(c *ImageAdapterConfig) { c.Task.PollIntervalSeconds = 2 }},
		{"poll interval above maximum", func(c *ImageAdapterConfig) { c.Task.PollIntervalSeconds = 61 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := imageAdapterTestConfig()
			test.change(config)
			require.Error(t, config.Validate())
		})
	}
	settings := ChannelSettings{ImageAdapter: imageAdapterTestConfig(), PassThroughBodyEnabled: true}
	require.ErrorContains(t, settings.ValidateImageAdapter(), "pass_through_body_enabled")
}

func TestImageAdapterURLTemplatesStayOnConfiguredOrigin(t *testing.T) {
	for _, template := range []string{
		"https://other.example/{task_id}", "//other.example/{task_id}",
		"/task/{task_id}#fragment", "/task/{other_id}", "/task/no-placeholder",
		"/task/../{task_id}", "/task/%2e%2e/{task_id}", "/task/%252e%252e/{task_id}",
		"/task\\{task_id}", "/task/%5c/{task_id}", "/%2fother.example/{task_id}",
		"/task/{task_id}\r\nheader", "/task/%00/{task_id}",
	} {
		t.Run(template, func(t *testing.T) {
			config := imageAdapterTestConfig()
			config.Task.PollURLTemplate = template
			require.Error(t, config.Validate())
		})
	}
	config := imageAdapterTestConfig()
	config.Task.PollURLTemplate = "/v2/task?task_id={task_id}"
	config.Task.PollIntervalSeconds = 60
	require.NoError(t, config.Validate())
	config.Request.GenerationsPath = "/generate/{task_id}"
	require.Error(t, config.Validate())
}
