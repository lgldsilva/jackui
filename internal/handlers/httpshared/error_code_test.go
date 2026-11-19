package httpshared

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// Every error envelope carries a stable machine-readable `code` alongside the
// human message (issue #201): native clients branch on `code`, never on the
// prose or on stringified statuses.

func TestErrorEnvelopeCarriesDefaultStatusCode(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "bad_request"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.StatusConflict, "conflict"},
		{http.StatusGone, "gone"},
		{http.StatusTooManyRequests, "rate_limited"},
		{StatusClientClosedRequest, "client_closed_request"},
		{http.StatusInternalServerError, "internal_error"},
		{http.StatusBadGateway, "bad_gateway"},
		{http.StatusServiceUnavailable, "service_unavailable"},
		{http.StatusGatewayTimeout, "gateway_timeout"},
	}
	for _, tc := range cases {
		c, w := testCtx("")
		RespondErrorMessage(c, tc.status, "boom")
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("status %d: bad JSON: %v", tc.status, err)
		}
		if body["error"] != "boom" {
			t.Errorf("status %d: error=%v, want boom", tc.status, body["error"])
		}
		if body[CodeField] != tc.want {
			t.Errorf("status %d: code=%v, want %q", tc.status, body[CodeField], tc.want)
		}
	}
}

func TestErrorCodeDefaultsForUnknownStatuses(t *testing.T) {
	if got := ErrorCodeForStatus(http.StatusTeapot); got != "error" {
		t.Errorf("418 code = %q, want error", got)
	}
	if got := ErrorCodeForStatus(599); got != "internal_error" {
		t.Errorf("599 code = %q, want internal_error", got)
	}
}

func TestErrorCodeOverrideViaFields(t *testing.T) {
	c, w := testCtx("")
	RespondErrorMessageFields(c, http.StatusUnauthorized, "invalid device key", map[string]any{CodeField: "invalid_device_key"})
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if body[CodeField] != "invalid_device_key" || body["error"] != "invalid device key" {
		t.Fatalf("override lost: %v", body)
	}
}

// RespondErrorFields must keep its optional members AND gain the code.
func TestRespondErrorFieldsKeepsExtraFieldsAndCode(t *testing.T) {
	c, w := testCtx("")
	RespondErrorFields(c, http.StatusConflict, errors.New("busy"), map[string]any{"retryAfter": 30})
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if body["retryAfter"] != float64(30) {
		t.Errorf("retryAfter=%v, want 30", body["retryAfter"])
	}
	if body[CodeField] != "conflict" {
		t.Errorf("code=%v, want conflict", body[CodeField])
	}
}
