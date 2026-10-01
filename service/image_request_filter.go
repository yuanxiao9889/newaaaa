package service

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// AppendImageRequestFilter snapshots client parameters before channel overrides.
// It is idempotent across retries. Invalid or missing image parameters exclude
// only channels whose enabled rules match the requested or mapped model.
func AppendImageRequestFilter(c *gin.Context) {
	if c == nil {
		return
	}
	constraints := GetChannelConstraints(c)
	for _, filter := range constraints.Filters {
		if filter.Kind == dto.FilterImageRequest {
			return
		}
	}
	filter := dto.ChannelFilter{Kind: dto.FilterImageRequest}
	defer func() { constraints.AddFilter(filter) }()
	if c.Request == nil || c.Request.URL == nil || c.Request.Method != http.MethodPost {
		return
	}
	if c.Request.URL.Path != "/v1/images/generations" && c.Request.URL.Path != "/v1/images/edits" {
		return
	}
	if c.ContentType() == gin.MIMEMultipartPOSTForm {
		form := c.Request.MultipartForm
		if form == nil {
			var err error
			form, err = common.ParseMultipartFormReusable(c)
			if err != nil {
				return
			}
			c.Request.MultipartForm = form
			c.Request.PostForm = url.Values(form.Value)
		}
		filter.ImageQuality, filter.ImageSize, filter.ImageRequestValid, filter.ImageModelAmbiguous = imageRequestFormParameters(url.Values(form.Value))
		for name, files := range form.File {
			if len(files) == 0 {
				continue
			}
			if strings.EqualFold(name, "model") {
				filter.ImageModelAmbiguous = true
			}
			if strings.EqualFold(name, "quality") || strings.EqualFold(name, "size") {
				filter.ImageRequestValid = false
			}
		}
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return
	}
	c.Request.Body = io.NopCloser(storage)
	if c.ContentType() == gin.MIMEPOSTForm {
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return
		}
		filter.ImageQuality, filter.ImageSize, filter.ImageRequestValid, filter.ImageModelAmbiguous = imageRequestFormParameters(values)
		return
	}
	if c.ContentType() != gin.MIMEJSON || !gjson.ValidBytes(body) {
		return
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return
	}
	modelCount, qualityCount, sizeCount := 0, 0, 0
	valid := true
	root.ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		switch {
		case strings.EqualFold(name, "model"):
			modelCount++
			filter.ImageModelAmbiguous = filter.ImageModelAmbiguous || name != "model" || modelCount > 1 || value.Type != gjson.String
		case strings.EqualFold(name, "quality"):
			qualityCount++
			valid = valid && name == "quality" && value.Type == gjson.String
			filter.ImageQuality = value.String()
		case strings.EqualFold(name, "size"):
			sizeCount++
			valid = valid && name == "size" && value.Type == gjson.String
			filter.ImageSize = value.String()
		}
		return true
	})
	filter.ImageRequestValid = valid && qualityCount == 1 && sizeCount == 1 && filter.ImageQuality != "" && filter.ImageSize != ""
}

func imageRequestFormParameters(values url.Values) (quality, size string, valid, modelAmbiguous bool) {
	valid = true
	for name, items := range values {
		if strings.EqualFold(name, "model") && (name != "model" || len(items) > 1) {
			modelAmbiguous = true
		}
		if strings.EqualFold(name, "quality") && name != "quality" || strings.EqualFold(name, "size") && name != "size" {
			valid = false
		}
	}
	if len(values["quality"]) != 1 || len(values["size"]) != 1 {
		return "", "", false, modelAmbiguous
	}
	quality, size = values.Get("quality"), values.Get("size")
	return quality, size, valid && quality != "" && size != "", modelAmbiguous
}
