package controller

import (
	"maps"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingForViewer(pricing []model.Pricing, usableGroup map[string]string, role int) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		visible := slices.Contains(item.EnableGroup, "all")
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				visible = true
				break
			}
		}
		if !visible {
			continue
		}
		// Pricing is shared in the model cache. Only modify a response-local copy.
		if role < common.RoleAdminUser {
			item.EnableGroup = slices.DeleteFunc(slices.Clone(item.EnableGroup), func(group string) bool {
				_, usable := usableGroup[group]
				return group != "all" && !usable
			})
		}
		item.BillingPluginVariants = slices.Clone(item.BillingPluginVariants)
		for i := range item.BillingPluginVariants {
			item.BillingPluginVariants[i].OptionID = strconv.Itoa(i + 1)
			if role < common.RoleAdminUser {
				item.BillingPluginVariants[i].PluginKey = ""
				item.BillingPluginVariants[i].PluginName = ""
				item.BillingPluginVariants[i].Icon = ""
			}
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingForViewer(pricing, usableGroup, c.GetInt("role"))
	vendors := model.GetVendors()
	endpoints := model.GetSupportedEndpointMap()
	if c.GetInt("role") < common.RoleAdminUser {
		visibleVendors := make(map[int]bool)
		visibleEndpoints := make(map[string]common.EndpointInfo)
		for _, item := range pricing {
			visibleVendors[item.VendorID] = true
			for _, endpoint := range item.SupportedEndpointTypes {
				key := string(endpoint)
				if info, ok := endpoints[key]; ok {
					visibleEndpoints[key] = info
				}
			}
		}
		vendors = slices.DeleteFunc(slices.Clone(vendors), func(vendor model.PricingVendor) bool {
			return !visibleVendors[vendor.ID]
		})
		endpoints = visibleEndpoints
	}
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	// Responses contain viewer-specific prices and, for admins, provider identities.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": endpoints,
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "f784f3deb29a0162ec91db73a0abcf1bcebc774a02f9bc88a96b8d2143267c49",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
