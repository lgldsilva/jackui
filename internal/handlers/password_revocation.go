package handlers

import (
	"context"
	"fmt"

	"github.com/lgldsilva/jackui/internal/auth"
)

// revokeAllAccess drops every credential that outlives a password: refresh
// sessions and paired-device keys. Called whenever the password is set on
// someone's behalf (public reset, admin set-password).
func revokeAllAccess(ctx context.Context, store *auth.Store, userID int) error {
	if err := store.RevokeAllSessions(ctx, userID); err != nil {
		return fmt.Errorf("password set but sessions were not revoked: %w", err)
	}
	if err := store.RevokeAllDeviceKeys(ctx, userID); err != nil {
		return fmt.Errorf("password set but paired devices were not revoked: %w", err)
	}
	return nil
}

// revokeAccessAfterPasswordChange is the self-service variant: the session
// identified by currentRefresh survives so the caller stays logged in. An
// empty or unknown token matches no session, so every one is revoked — failing
// closed instead of leaving them all alive. Returns how many sessions dropped.
func revokeAccessAfterPasswordChange(ctx context.Context, store *auth.Store, userID int, currentRefresh string) (int, error) {
	revoked, err := store.RevokeOtherSessions(ctx, userID, currentRefresh)
	if err != nil {
		return 0, fmt.Errorf("password changed but sessions were not revoked: %w", err)
	}
	if err := store.RevokeAllDeviceKeys(ctx, userID); err != nil {
		return revoked, fmt.Errorf("password changed but paired devices were not revoked: %w", err)
	}
	return revoked, nil
}
