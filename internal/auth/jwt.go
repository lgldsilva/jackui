package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ScopeMedia marks tokens issued by SignMedia — used as ?token= in
// <video>/<track>/<img> that must survive access-token refreshes during a
// long playback session.
const ScopeMedia = "media"

// Claims is what we encode inside the JWT access token.
type Claims struct {
	UserID   int    `json:"uid"`
	Username string `json:"u"`
	Role     Role   `json:"r"`
	// Scope distinguishes the regular access token ("") from special tokens.
	// Today only "media" — long TTL, valid only on routes served via ?token=
	// (isMediaPath). Middleware Required rejects tokens with scope="media"
	// to prevent their use on sensitive routes via the Authorization header.
	Scope string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

// TokenManager signs and validates access tokens with HMAC-SHA256.
type TokenManager struct {
	secret    []byte
	accessTTL time.Duration
	mediaTTL  time.Duration
}

// NewTokenManager — secret must be at least 32 random bytes for HS256 to be safe.
// accessTTL controls how often the frontend must hit /refresh.
// mediaTTL is the TTL of media tokens (SignMedia); default 6h when zero.
func NewTokenManager(secret []byte, accessTTL time.Duration) *TokenManager {
	if accessTTL == 0 {
		accessTTL = 15 * time.Minute
	}
	return &TokenManager{secret: secret, accessTTL: accessTTL, mediaTTL: 6 * time.Hour}
}

// SetMediaTTL adjusts the TTL of media tokens. 0 = default 6h.
func (t *TokenManager) SetMediaTTL(d time.Duration) {
	if d > 0 {
		t.mediaTTL = d
	}
}

// SignAccess creates a new short-lived access JWT for the user.
func (t *TokenManager) SignAccess(u *User) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(t.accessTTL)
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "jackui",
			Subject:   fmt.Sprintf("%d", u.ID),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	return s, exp, nil
}

// SignMedia issues a scope="media" JWT with a long TTL, meant for media URLs
// (<video src>, <track src>) that survive access-token refreshes during a
// playback session. Carries the same user claims as SignAccess so handlers
// keep identifying the requester. It is NOT accepted on routes that use the
// Authorization header (see middleware Required).
func (t *TokenManager) SignMedia(u *User) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(t.mediaTTL)
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
		Scope:    ScopeMedia,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "jackui",
			Subject:   fmt.Sprintf("%d", u.ID),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	return s, exp, nil
}

// ParseAccess validates the JWT and returns its claims. Returns error if expired or tampered.
func (t *TokenManager) ParseAccess(raw string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid claims")
	}
	return claims, nil
}
