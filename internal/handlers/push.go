package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/push"
)

const errPushDisabled = "push notifications unavailable"

// PushVapidKey — GET /api/push/vapid. The VAPID public key the browser needs
// for PushManager.subscribe.
func PushVapidKey(sender *push.Sender) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sender == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errPushDisabled})
			return
		}
		c.JSON(http.StatusOK, gin.H{"key": sender.PublicKey()})
	}
}

type pushSubscribeInput struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushSubscribe — POST /api/push/subscribe. Body mirrors the browser's
// PushSubscription.toJSON().
func PushSubscribe(store *push.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errPushDisabled})
			return
		}
		var in pushSubscribeInput
		if err := c.BindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := store.Subscribe(userID, in.Endpoint, in.Keys.P256dh, in.Keys.Auth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// PushUnsubscribe — POST /api/push/unsubscribe. Removes the browser endpoint.
func PushUnsubscribe(store *push.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errPushDisabled})
			return
		}
		var in struct {
			Endpoint string `json:"endpoint"`
		}
		if err := c.BindJSON(&in); err != nil || in.Endpoint == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint is required"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := store.Unsubscribe(userID, in.Endpoint); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// NotificationsList — GET /api/notifications[?limit=]. The in-app feed plus
// the unread badge count.
func NotificationsList(store *push.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusOK, gin.H{"items": []push.Notification{}, "unread": 0})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		limit, _ := strconv.Atoi(c.Query("limit"))
		items, err := store.Notifications(userID, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		unread, _ := store.UnreadCount(userID)
		c.JSON(http.StatusOK, gin.H{"items": items, "unread": unread})
	}
}

// NotificationsMarkRead — POST /api/notifications/read. Marks the whole feed
// as read (the bell clears when opened).
func NotificationsMarkRead(store *push.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := store.MarkAllRead(userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}
