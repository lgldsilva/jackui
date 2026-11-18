package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

const backupCodeCount = 10

// MFAEnrollStart handles POST /api/auth/mfa/enroll — generates a fresh TOTP
// secret (stored as not-yet-enabled) and returns the otpauth URI + secret for
// the user to add to their authenticator app. Confirmed via MFAEnrollVerify.
func MFAEnrollStart(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if err := store.SetTOTPSecret(c.Request.Context(), claims.UserID, secret); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "code required")
			return
		}
		secret, _, _ := store.GetTOTPSecret(c.Request.Context(), claims.UserID)
		// ValidateTOTPForUser (not plain ValidateTOTP): records the matched step
		// so the same code cannot be replayed within its window.
		if !store.ValidateTOTPForUser(claims.UserID, secret, req.Code) {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid code")
			return
		}
		// Enable + issue recovery codes immediately so the user can't lock
		// themselves out by losing the authenticator. Shown exactly once.
		codes, err := enableMFAWithBackupCodes(c.Request.Context(), store, claims.UserID)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA enabled", "backupCodes": codes})
	}
}

// MFABackupCodesStatus handles GET /api/auth/mfa/backup-codes — how many unused
// recovery codes remain.
func MFABackupCodesStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		c.JSON(http.StatusOK, gin.H{"remaining": store.CountBackupCodes(c.Request.Context(), claims.UserID)})
	}
}

// MFABackupCodesRegenerate handles POST /api/auth/mfa/backup-codes/regenerate —
// replaces all codes with a fresh set (invalidating the old ones). Requires the
// current password so a hijacked session can't silently rotate recovery codes.
func MFABackupCodesRegenerate(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		if _, err := store.VerifyPassword(c.Request.Context(), claims.Username, req.Password); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "incorrect password")
			return
		}
		codes, err := store.GenerateBackupCodes(c.Request.Context(), claims.UserID, backupCodeCount)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		// Re-verify the password by username (we have it in claims).
		if _, err := store.VerifyPassword(c.Request.Context(), claims.Username, req.Password); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "incorrect password")
			return
		}
		if err := store.DisableTOTP(c.Request.Context(), claims.UserID); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA disabled"})
	}
}

// enableMFAWithBackupCodes turns MFA on and mints the recovery codes as one
// action, detached from the request: a dropped client must not leave MFA
// enabled with no codes (the user would be locked out of recovery).
func enableMFAWithBackupCodes(parent context.Context, store *auth.Store, userID int) ([]string, error) {
	ctx, cancel := httpshared.DetachedCtx(parent)
	defer cancel()
	if err := store.EnableTOTP(ctx, userID); err != nil {
		return nil, err
	}
	return store.GenerateBackupCodes(ctx, userID, backupCodeCount)
}
