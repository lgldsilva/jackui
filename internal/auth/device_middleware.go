package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// deviceDeniedPrefixes are account-security surfaces a paired-device token
// must never reach: credentials, MFA, passkeys, sessions, user admin and the
// device registry itself (a TV can't approve other TVs or revoke keys).
var deviceDeniedPrefixes = []string{
	"/api/auth/password",
	"/api/auth/email",
	"/api/auth/mfa/",
	"/api/auth/passkey",
	"/api/auth/sessions",
	"/api/auth/users",
	"/api/auth/devices",
	"/api/auth/device/",
}

// DeviceRestrict blocks paired-device tokens (amr=device) from the
// account-security routes above. Chain after Required.
func DeviceRestrict() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromCtx(c)
		if ok && claims.AMR == AMRDevice {
			for _, p := range deviceDeniedPrefixes {
				if strings.HasPrefix(c.Request.URL.Path, p) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not allowed from a paired device"})
					return
				}
			}
		}
		c.Next()
	}
}

// DeviceKeyFromHeader extracts the key from "Authorization: Device jkd_…".
func DeviceKeyFromHeader(c *gin.Context) string {
	h := c.GetHeader(HeaderAuthorization)
	if !strings.HasPrefix(h, DevicePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, DevicePrefix))
}
