package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// A presented API key always takes precedence: an invalid key must not silently
// fall back to a signed URL. The handler verifies the capability before fetching.
func MidjourneyImageAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != "" || c.GetHeader("mj-api-secret") != "" {
			TokenAuth()(c)
			return
		}
		query := c.Request.URL.Query()
		owner, err := strconv.Atoi(query.Get("uid"))
		if err != nil || owner <= 0 || len(query["uid"]) != 1 || len(query["access"]) != 1 || len(query.Get("access")) != 43 || len(query["expires"]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "image_authorization_required"})
			return
		}
		c.Next()
	}
}
