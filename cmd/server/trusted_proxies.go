package main

import (
	"log"

	"github.com/gin-gonic/gin"
)

// applyTrustedProxies restricts which peers may set the client IP through
// X-Forwarded-For / X-Real-IP. gin's default trusts every peer, which lets any
// client forge c.ClientIP() (per-IP pairing cap, session/device IPs, auth
// logs). An empty list trusts none, so ClientIP() is the socket peer.
func applyTrustedProxies(router *gin.Engine, proxies []string) {
	if len(proxies) == 0 {
		_ = router.SetTrustedProxies(nil)
		return
	}
	if err := router.SetTrustedProxies(proxies); err != nil {
		// gin keeps its previous (trust-all) CIDR set when parsing fails, so
		// fall back to trusting none explicitly instead of failing open.
		log.Printf("WARNING: JACKUI_TRUSTED_PROXIES is invalid (%v) — trusting no proxy; client IPs will be the direct peer", err)
		_ = router.SetTrustedProxies(nil)
		return
	}
	log.Printf("Trusted proxies: %v", proxies)
}
