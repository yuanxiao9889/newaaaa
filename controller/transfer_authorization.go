package controller

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func AuthorizeBridgeDownload(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	key := service.BridgeDownloadAuthorizationKey()
	if key == "" || len(c.Request.Header.Values("Authorization")) != 1 ||
		subtle.ConstantTimeCompare([]byte(c.GetHeader("Authorization")), []byte("Bearer "+key)) != 1 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if c.Request.URL.RawQuery != "" || c.Request.ContentLength != 0 || len(c.Request.TransferEncoding) > 0 ||
		len(c.Request.Header.Values("X-Opics-Download-Ticket")) != 1 ||
		service.AuthorizeTransferDownload(c.GetHeader("X-Opics-Download-Ticket"), time.Now()) != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	c.Status(http.StatusNoContent)
}
