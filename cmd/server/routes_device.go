package main

import (
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers"
)

// registerDevicePublicRoutes wires the TV side of device pairing (#201): no
// JWT — start/token are anonymous (RFC 8628 shape), session/profiles carry
// "Authorization: Device <key>".
func registerDevicePublicRoutes(pub *gin.RouterGroup, deps *appDeps) {
	if deps.deviceFlow == nil {
		return
	}
	// Pending pairings live in this process's memory: on an edge, the TV and the
	// approving user must meet on the primary.
	if deps.edgeRelay != nil {
		pub.POST("/device/start", deps.edgeRelay)
		pub.POST("/device/token", deps.edgeRelay)
	} else {
		pub.POST("/device/start", handlers.DeviceStart(deps.deviceFlow))
		pub.POST("/device/token", handlers.DeviceToken(deps.deviceFlow, deps.authStore, deps.tokenMgr))
	}
	pub.POST("/device/session", handlers.DeviceSession(deps.authStore, deps.tokenMgr))
	pub.GET("/device/profiles", handlers.DeviceProfiles(deps.authStore))
}

// registerDeviceRoutes wires the phone/web side (JWT): approve a pairing and
// manage paired devices. DeviceRestrict keeps device tokens out of these.
func registerDeviceRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.deviceFlow == nil {
		return
	}
	if deps.edgeRelay != nil {
		api.GET("/auth/device/pending", deps.edgeRelay)
		api.POST("/auth/device/approve", deps.edgeRelay)
	} else {
		api.GET("/auth/device/pending", handlers.DevicePending(deps.deviceFlow, deps.deviceLockout))
		api.POST("/auth/device/approve", handlers.DeviceApprove(deps.deviceFlow, deps.deviceLockout))
	}
	api.GET("/auth/devices", handlers.ListDevices(deps.authStore))
	api.DELETE("/auth/devices/:id", handlers.RevokeDevice(deps.authStore))
	api.POST("/auth/devices/:id/pin", handlers.SetDevicePIN(deps.authStore))
}
