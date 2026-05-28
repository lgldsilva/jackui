package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) implemented on stdlib only — no external dependency. 6-digit
// codes, 30s step, SHA-1 (what Google Authenticator / Authy expect).

const totpStep = 30 * time.Second

// GenerateTOTPSecret returns a fresh base32 secret (no padding) for enrollment.
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20) // 160-bit, standard for TOTP
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="), nil
}

// TOTPURI builds the otpauth:// URI that authenticator apps consume (also used to
// render a QR). issuer/account label the entry in the app.
func TOTPURI(secret, issuer, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s:%s?%s",
		url.PathEscape(issuer), url.PathEscape(account), v.Encode())
}

func totpAt(secret string, counter uint64) string {
	key, err := base32.StdEncoding.DecodeString(padBase32(secret))
	if err != nil {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}

func padBase32(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if m := len(s) % 8; m != 0 {
		s += strings.Repeat("=", 8-m)
	}
	return s
}

// ValidateTOTP checks a code against the secret, allowing ±1 step (clock skew /
// the user typing as the window rolls).
func ValidateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 || secret == "" {
		return false
	}
	now := uint64(time.Now().Unix() / int64(totpStep.Seconds()))
	for _, c := range []uint64{now - 1, now, now + 1} {
		if hmac.Equal([]byte(totpAt(secret, c)), []byte(code)) {
			return true
		}
	}
	return false
}
