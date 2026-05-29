package middleware

import (
	"testing"
)

func TestIsTruthy(t *testing.T) {
	if !isTruthy("1") {
		t.Error("expected truthy for '1'")
	}
	if !isTruthy("true") {
		t.Error("expected truthy for 'true'")
	}
	if !isTruthy("yes") {
		t.Error("expected truthy for 'yes'")
	}
	if !isTruthy("on") {
		t.Error("expected truthy for 'on'")
	}
	if isTruthy("0") {
		t.Error("expected falsy for '0'")
	}
	if isTruthy("false") {
		t.Error("expected falsy for 'false'")
	}
	if isTruthy("") {
		t.Error("expected falsy for ''")
	}
	if !isTruthy(" TRUE ") {
		t.Error("isTruthy should handle trimmed space")
	}
}
