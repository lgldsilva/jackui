package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

const backupCodeCount = 10

// MFAEnrollStart handles POST /api/auth/mfa/enroll — generates a fresh TOTP
// secret (stored as not-yet-enabled) and returns the otpauth URI + secret for
// the user to add to their authenticator app. Confirmed via MFAEnrollVerify.
func MFAEnrollStart(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetTOTPSecret(claims.UserID, secret); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		uri := auth.TOTPURI(secret, "JackUI", claims.Username)
		c.JSON(http.StatusOK, gin.H{"secret": secret, "uri": uri})
	}
}

// MFAEnrollVerify handles POST /api/auth/mfa/verify — confirms a code against
// the pending secret and enables MFA.
func MFAEnrollVerify(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "código obrigatório"})
			return
		}
		secret, _, _ := store.GetTOTPSecret(claims.UserID)
		if !auth.ValidateTOTP(secret, req.Code) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "código inválido"})
			return
		}
		if err := store.EnableTOTP(claims.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Issue recovery codes immediately so the user can't lock themselves out by
		// losing the authenticator. Shown exactly once.
		codes, err := store.GenerateBackupCodes(claims.UserID, backupCodeCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA ativado", "backupCodes": codes})
	}
}

// MFABackupCodesStatus handles GET /api/auth/mfa/backup-codes — how many unused
// recovery codes remain.
func MFABackupCodesStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		c.JSON(http.StatusOK, gin.H{"remaining": store.CountBackupCodes(claims.UserID)})
	}
}

// MFABackupCodesRegenerate handles POST /api/auth/mfa/backup-codes/regenerate —
// replaces all codes with a fresh set (invalidating the old ones). Requires the
// current password so a hijacked session can't silently rotate recovery codes.
func MFABackupCodesRegenerate(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		if _, err := store.VerifyPassword(claims.Username, req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha incorreta"})
			return
		}
		codes, err := store.GenerateBackupCodes(claims.UserID, backupCodeCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"backupCodes": codes})
	}
}

// MFADisable handles POST /api/auth/mfa/disable — turns MFA off (requires the
// current password to prevent a hijacked session from removing it).
func MFADisable(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		// Re-verify the password by username (we have it in claims).
		if _, err := store.VerifyPassword(claims.Username, req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha incorreta"})
			return
		}
		if err := store.DisableTOTP(claims.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA desativado"})
	}
}
