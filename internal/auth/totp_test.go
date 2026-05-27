package auth

import (
	"testing"
	"time"
)

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	// The code computed for the current step must validate.
	now := uint64(time.Now().Unix() / int64(totpStep.Seconds()))
	code := totpAt(secret, now)
	if len(code) != 6 {
		t.Fatalf("code not 6 digits: %q", code)
	}
	if !ValidateTOTP(secret, code) {
		t.Fatal("current code should validate")
	}
	// A clearly-wrong code must fail; so must a malformed one.
	if ValidateTOTP(secret, "000000") && code != "000000" {
		t.Fatal("a fixed wrong code validated")
	}
	if ValidateTOTP(secret, "12345") {
		t.Fatal("short code should fail")
	}
	if ValidateTOTP("", code) {
		t.Fatal("empty secret should fail")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("ABC234", "JackUI", "bob")
	if uri == "" || uri[:10] != "otpauth://" {
		t.Fatalf("bad uri: %q", uri)
	}
}
