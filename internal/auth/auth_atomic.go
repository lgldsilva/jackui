package auth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrInviteInvalid is returned when an invite token cannot be redeemed.
var ErrInviteInvalid = errors.New("invalid or expired invite")

// VerifyEmailWithToken spends an email-verification token and marks the
// address verified in one transaction: either both happen or the token stays
// usable. Validation (existence, purpose, expiry, single use) is unchanged and
// fails closed.
func (s *Store) VerifyEmailWithToken(ctx context.Context, plain string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ti, err := consumeToken(ctx, tx, plain, TokenVerifyEmail)
	if err != nil {
		return err
	}
	if err := setEmailVerified(ctx, tx, ti.UserID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateUserWithInvite redeems an invite and creates the (active) account in
// one transaction, so a failed insert never burns the invite. An invite that
// cannot be redeemed yields ErrInviteInvalid.
func (s *Store) CreateUserWithInvite(ctx context.Context, invite, username, email, password string, role Role) (int, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf(errHashPassword, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := consumeToken(ctx, tx, invite, TokenInvite); err != nil {
		return 0, ErrInviteInvalid
	}
	id, err := insertUserFull(ctx, tx, username, email, string(hash), role, StatusActive)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
