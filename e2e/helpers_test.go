//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"testing"
)

// mustJSONBody validates that a table's literal body parses as JSON, so a
// typo fails loudly instead of silently binding as an invalid request.
func mustJSONBody(t *testing.T, raw string) string {
	t.Helper()
	if !json.Valid([]byte(raw)) {
		t.Fatalf("table body is not valid JSON: %s", snippet(raw))
	}
	return raw
}

func jsonUnmarshal(body string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	return dec.Decode(v)
}
