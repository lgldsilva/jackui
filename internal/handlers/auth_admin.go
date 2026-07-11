package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
)

type createUserReq struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

// SetUserStatus handles PATCH /api/auth/users/:id/status (admin only) — approve
// (active), disable, or re-enable an account.
func SetUserStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req struct {
			Status auth.Status `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status required"})
			return
		}
		if req.Status != auth.StatusActive && req.Status != auth.StatusPending && req.Status != auth.StatusDisabled {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status inválido"})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if claims != nil && claims.UserID == id && req.Status != auth.StatusActive {
			c.JSON(http.StatusBadRequest, gin.H{"error": "não pode desabilitar a si mesmo"})
			return
		}
		if err := store.SetStatus(id, req.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Disabling kills active sessions so the block takes effect within one
		// access-token TTL instead of waiting for the refresh token to expire.
		if req.Status == auth.StatusDisabled {
			_ = store.RevokeAllSessions(id)
		}
		c.JSON(http.StatusOK, gin.H{"message": "status atualizado"})
	}
}

// CreateUser handles POST /api/auth/users (admin only).
func CreateUser(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createUserReq
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username + password required"})
			return
		}
		id, err := store.CreateUser(req.Username, req.Password, req.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id})
	}
}

// ListUsers handles GET /api/auth/users (admin only).
func ListUsers(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := store.ListUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if users == nil {
			users = []auth.User{}
		}
		c.JSON(http.StatusOK, users)
	}
}

// SetNtfyTopic handles POST /api/user/ntfy-topic — updates the logged-in
// user's ntfy.sh notification topic. Body: { topic: string }.
func SetNtfyTopic(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Topic string `json:"topic"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "topic required"})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if err := store.SetNtfyTopic(claims.UserID, req.Topic); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "tópico atualizado"})
	}
}

// NotifyTest handles POST /api/user/notify-test — sends a test push
// notification to the user's ntfy topic (or the global default). Useful for
// verifying that ntfy is configured correctly from the Settings UI.
func NotifyTest(cfg *config.Config, store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		baseURL := ntfyBaseURL(cfg)
		topic := resolveNtfyTopic(cfg, store, c)
		if topic == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nenhum tópico ntfy configurado"})
			return
		}
		if !postNtfyNotification(c, baseURL, topic) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notificação de teste enviada"})
	}
}

func ntfyBaseURL(cfg *config.Config) string {
	if cfg.Notifications.NtfyBaseURL != "" {
		return cfg.Notifications.NtfyBaseURL
	}
	return "https://ntfy.sh"
}

func resolveNtfyTopic(cfg *config.Config, store *auth.Store, c *gin.Context) string {
	topic := cfg.Notifications.NtfyDefaultTopic
	if store == nil {
		return topic
	}
	claims, ok := auth.ClaimsFromCtx(c)
	if !ok {
		return topic
	}
	user, err := store.GetUserByID(claims.UserID)
	if err != nil || user.NtfyTopic == "" {
		return topic
	}
	return user.NtfyTopic
}

func postNtfyNotification(c *gin.Context, baseURL, topic string) bool {
	url := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), topic)
	host, _ := os.Hostname()
	body := fmt.Sprintf("Notificação de teste do JackUI (%s)", host)
	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewBufferString(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	req.Header.Set("Title", "JackUI — Teste de Notificação")
	req.Header.Set("Tags", "test,rocket")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("ntfy retornou %d", resp.StatusCode)})
		return false
	}
	return true
}

// DeleteUser handles DELETE /api/auth/users/:id (admin only).
func DeleteUser(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if claims != nil && claims.UserID == id {
			c.JSON(http.StatusBadRequest, gin.H{"error": "não pode deletar a si mesmo"})
			return
		}
		if err := store.DeleteUser(id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}
