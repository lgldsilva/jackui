package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is what we encode inside the JWT access token.
type Claims struct {
	UserID   int    `json:"uid"`
	Username string `json:"u"`
	Role     Role   `json:"r"`
	jwt.RegisteredClaims
}

// TokenManager signs and validates access tokens with HMAC-SHA256.
type TokenManager struct {
	secret    []byte
	accessTTL time.Duration
}

// NewTokenManager — secret must be at least 32 random bytes for HS256 to be safe.
// accessTTL controls how often the frontend must hit /refresh.
func NewTokenManager(secret []byte, accessTTL time.Duration) *TokenManager {
	if accessTTL == 0 {
		accessTTL = 15 * time.Minute
	}
	return &TokenManager{secret: secret, accessTTL: accessTTL}
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
