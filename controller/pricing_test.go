package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicRelayHeadersPreserveProtocolAndHideDiagnostics(t *testing.T) {
	for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		for _, apiToken := range []bool{false, true} {
			t.Run(strconv.Itoa(role)+"/api_token="+strconv.FormatBool(apiToken), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("role", role)
				if apiToken {
					c.Set("token_id", 1)
				}
				c.Header(common.RequestIdKey, "gateway-request")
				headers := http.Header{
					"Content-Type": {"application/json"}, "Retry-After": {"3"},
					"X-Ratelimit-Remaining-Requests": {"9"}, "X-Codex-Turn-State": {"opaque-state"},
					"X-Reasoning-Included": {"true"}, "Content-Disposition": {`attachment; filename="audio.wav"`},
					"Accept-Ranges": {"bytes"}, "Content-Range": {"bytes 0-10/100"},
					"X-Upstream-Provider": {"private-provider"}, "X-Channel-Id": {"287"}, "X-Plugin-Key": {"private-plugin"},
					"Server": {"private-server"}, "Set-Cookie": {"upstream_session=secret"}, "Connection": {"close"},
					"Cache-Control": {"public, max-age=86400"}, common.RequestIdKey: {"upstream-request"}, "Empty": {},
				}
				service.IOCopyBytesGracefully(c, &http.Response{StatusCode: 206, Header: headers}, []byte(`{"ok":true}`))
				assert.Equal(t, 206, recorder.Code)
				assert.Equal(t, `{"ok":true}`, recorder.Body.String())
				assert.Equal(t, "11", recorder.Header().Get("Content-Length"))
				for _, key := range []string{"Content-Type", "Retry-After", "X-Ratelimit-Remaining-Requests", "X-Codex-Turn-State", "X-Reasoning-Included", "Content-Disposition", "Accept-Ranges", "Content-Range"} {
					assert.Equal(t, headers.Get(key), recorder.Header().Get(key), key)
				}
				for _, key := range []string{"X-Upstream-Provider", "X-Channel-Id", "X-Plugin-Key", "Server"} {
					if role >= common.RoleAdminUser && !apiToken {
						assert.Equal(t, headers.Get(key), recorder.Header().Get(key))
					} else {
						assert.Empty(t, recorder.Header().Get(key))
					}
				}
				assert.Empty(t, recorder.Header().Get("Set-Cookie"))
				assert.Empty(t, recorder.Header().Get("Connection"))
				assert.NotContains(t, recorder.Header().Get("Cache-Control"), "public")
				assert.Equal(t, "gateway-request", recorder.Header().Get(common.RequestIdKey))
				assert.Equal(t, "upstream-request", c.GetString(common.UpstreamRequestIdKey))
			})
		}
	}
	assert.False(t, service.ShouldCopyUpstreamHeader(nil, "x-PLUGIN-key", []string{"private"}))
	assert.True(t, service.ShouldCopyUpstreamHeader(nil, "content-type", []string{"audio/mpeg"}))
}

func TestPerformanceMetricsRespectViewerGroups(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			db := modelManagementDB(t, dialect.kind, dsn)
			require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
			previousRatio, previousUsable := ratio_setting.GroupRatio2JSONString(), setting.UserUsableGroups2JSONString()
			t.Cleanup(func() {
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatio))
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
			})
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"privacy-test-public":1,"privacy-test-member":1,"privacy-test-private":1}`))
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"privacy-test-public":"Public"}`))
			bucket := time.Now().Unix() / 3600 * 3600
			for _, group := range []string{"privacy-test-public", "privacy-test-member", "privacy-test-private", "auto"} {
				require.NoError(t, db.Create(&model.PerfMetric{ModelName: "model-" + group, Group: group, BucketTs: bucket, RequestCount: 1, SuccessCount: 1, TotalLatencyMs: 100}).Error)
			}
			for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
				for _, path := range []string{"/api/perf-metrics?model=model-privacy-test-private", "/api/perf-metrics?model=model-privacy-test-private&group=privacy-test-private", "/api/perf-metrics/summary?recent_model=model-privacy-test-private"} {
					r := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(r)
					c.Request = httptest.NewRequest(http.MethodGet, path, nil)
					c.Set("role", role)
					if role != 0 {
						c.Set("group", "privacy-test-member")
					}
					if c.Request.URL.Path == "/api/perf-metrics/summary" {
						GetPerfMetricsSummary(c)
					} else {
						GetPerfMetrics(c)
					}
					require.Equal(t, http.StatusOK, r.Code, r.Body.String())
					assert.Equal(t, "private, no-store", r.Header().Get("Cache-Control"))
					var response struct {
						Data struct {
							Groups []struct{ Group string }
							Models []struct {
								ModelName string `json:"model_name"`
							}
							Recent map[string][]any `json:"recent_success_series"`
						}
					}
					require.NoError(t, common.Unmarshal(r.Body.Bytes(), &response))
					if c.Request.URL.Path == "/api/perf-metrics/summary" {
						want := 1
						if role == common.RoleCommonUser {
							want = 2
						}
						if role >= common.RoleAdminUser {
							want = 4
						}
						assert.Len(t, response.Data.Models, want, "role=%d", role)
						if role < common.RoleAdminUser {
							assert.Empty(t, response.Data.Recent["model-privacy-test-private"])
						} else {
							assert.Len(t, response.Data.Recent["model-privacy-test-private"], 1)
						}
					} else if role >= common.RoleAdminUser {
						require.Len(t, response.Data.Groups, 1)
						assert.Equal(t, "privacy-test-private", response.Data.Groups[0].Group)
					} else {
						assert.Empty(t, response.Data.Groups)
					}
				}
			}
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{}`))
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics/summary", nil)
			GetPerfMetricsSummary(c)
			require.Equal(t, http.StatusOK, r.Code)
			assert.NotContains(t, r.Body.String(), "model-privacy-test-public")
			assert.NotContains(t, r.Body.String(), "privacy-test-private")
		})
	}
}

func TestPricingViewerPrivacyPreservesPricesAndSharedCache(t *testing.T) {
	variants := []model.PricingPluginVariant{
		{PluginKey: "private-alpha", PluginName: "Private Alpha", Icon: "https://private-alpha.example/icon.svg",
			BillingExpr: `tier("standard", u("seconds") * 0.4)`, BillingMode: "tiered_expr",
			BillingUsageSchema:   map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}},
			BillingUsageExamples: []jsplugin.UsageExample{{Label: "5 seconds", Facts: map[string]any{"seconds": 5}}}},
		{PluginKey: "private-beta", PluginName: "Private Beta", BillingMode: "ratio"},
	}
	source := []model.Pricing{
		{ModelName: "video", EnableGroup: []string{"default", "private-provider"}, ModelPrice: 0.25, BillingPluginVariants: variants},
		{ModelName: "global", EnableGroup: []string{"all"}, BillingPluginVariants: variants},
		{ModelName: "restricted", EnableGroup: []string{"private"}, BillingPluginVariants: variants},
	}
	before, err := common.Marshal(source)
	require.NoError(t, err)
	for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(strconv.Itoa(role), func(t *testing.T) {
			result := filterPricingForViewer(source, map[string]string{"default": ""}, role)
			require.Len(t, result, 2)
			assert.Equal(t, 0.25, result[0].ModelPrice)
			if role < common.RoleAdminUser {
				assert.Equal(t, []string{"default"}, result[0].EnableGroup)
			} else {
				assert.Equal(t, source[0].EnableGroup, result[0].EnableGroup)
			}
			assert.Equal(t, []string{"all"}, result[1].EnableGroup)
			for _, item := range result {
				require.Len(t, item.BillingPluginVariants, 2)
				for i, variant := range item.BillingPluginVariants {
					expected := variants[i]
					expected.OptionID = strconv.Itoa(i + 1)
					if role < common.RoleAdminUser {
						expected.PluginKey, expected.PluginName, expected.Icon = "", "", ""
					}
					assert.Equal(t, expected, variant)
				}
			}
			payload, err := common.Marshal(result)
			require.NoError(t, err)
			if role < common.RoleAdminUser {
				for _, secret := range []string{"plugin_key", "plugin_name", "icon", "private-alpha", "private-beta", "Private Alpha", "Private Beta"} {
					assert.NotContains(t, string(payload), secret)
				}
			} else {
				assert.Contains(t, string(payload), "Private Alpha")
			}
		})
	}
	after, err := common.Marshal(source)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "public responses must not modify the shared pricing cache")
	assert.Empty(t, filterPricingForViewer(source, nil, common.RoleAdminUser))
}

func TestPricingMetadataOnlyIncludesVisibleModels(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	previousUsable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() { require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable)) })
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Public"}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"public-model":0.25,"private-model":0.8}`))
	for i, visibility := range []string{"public", "private"} {
		vendor := model.Vendor{Name: visibility + "-vendor"}
		require.NoError(t, db.Create(&vendor).Error)
		channel := model.Channel{Name: visibility + "-channel", Type: 1}
		require.NoError(t, db.Create(&channel).Error)
		group := "default"
		if i == 1 {
			group = "private-provider"
		}
		require.NoError(t, db.Create(&model.Ability{Group: group, Model: visibility + "-model", ChannelId: channel.Id, Enabled: true}).Error)
		require.NoError(t, db.Create(&model.Model{ModelName: visibility + "-model", VendorID: vendor.Id, Endpoints: `{"` + visibility + `-endpoint":"/` + visibility + `/generate"}`}).Error)
	}
	for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Set("role", role)
		GetPricing(c)
		require.Equal(t, http.StatusOK, r.Code)
		var response struct {
			Data      []model.Pricing
			Vendors   []model.PricingVendor
			Endpoints map[string]common.EndpointInfo `json:"supported_endpoint"`
		}
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &response))
		require.Len(t, response.Data, 1)
		assert.Equal(t, 0.25, response.Data[0].ModelPrice)
		assert.Equal(t, "/public/generate", response.Endpoints["public-endpoint"].Path)
		if role < common.RoleAdminUser {
			require.Len(t, response.Vendors, 1)
			assert.Equal(t, "public-vendor", response.Vendors[0].Name)
			assert.NotContains(t, r.Body.String(), "private-")
		} else {
			assert.Len(t, response.Vendors, 2)
			assert.Contains(t, response.Endpoints, "private-endpoint")
		}
	}
}
