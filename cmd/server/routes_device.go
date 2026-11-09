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
	pub.POST("/device/start", handlers.DeviceStart(deps.deviceFlow))
	pub.POST("/device/token", handlers.DeviceToken(deps.deviceFlow, deps.authStore, deps.tokenMgr))
	pub.POST("/device/session", handlers.DeviceSession(deps.authStore, deps.tokenMgr))
	pub.GET("/device/profiles", handlers.DeviceProfiles(deps.authStore))
}

// registerDeviceRoutes wires the phone/web side (JWT): approve a pairing and
// manage paired devices. DeviceRestrict keeps device tokens out of these.
func registerDeviceRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.deviceFlow == nil {
		return
	}
	api.GET("/auth/device/pending", handlers.DevicePending(deps.deviceFlow, deps.deviceLockout))
	api.POST("/auth/device/approve", handlers.DeviceApprove(deps.deviceFlow, deps.deviceLockout))
	api.GET("/auth/devices", handlers.ListDevices(deps.authStore))
	api.DELETE("/auth/devices/:id", handlers.RevokeDevice(deps.authStore))
	api.POST("/auth/devices/:id/pin", handlers.SetDevicePIN(deps.authStore))
}
