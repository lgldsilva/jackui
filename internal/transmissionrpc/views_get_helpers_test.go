package transmissionrpc

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/downloads"
)

func TestTorrentFieldSet(t *testing.T) {
	t.Run("explicit list keeps strings and drops other types", func(t *testing.T) {
		got := torrentFieldSet(map[string]interface{}{"fields": []interface{}{"id", 42, "name", nil}})
		if len(got) != 2 || !got["id"] || !got["name"] {
			t.Fatalf("fields = %v, want {id,name}", got)
		}
	})
	t.Run("missing or empty list falls back to the default set", func(t *testing.T) {
		for _, raw := range []interface{}{nil, []interface{}{}, []interface{}{7}, "id"} {
			got := torrentFieldSet(map[string]interface{}{"fields": raw})
			if len(got) != len(defaultTorrentFields) {
				t.Fatalf("raw=%v: %d fields, want the %d defaults", raw, len(got), len(defaultTorrentFields))
			}
			for _, f := range defaultTorrentFields {
				if !got[f] {
					t.Fatalf("raw=%v: default field %q missing", raw, f)
				}
			}
		}
	})
}

func TestScopeRowsForCaller(t *testing.T) {
	all := []downloads.Download{
		{ID: 1, UserID: 1},
		{ID: 2, UserID: 2},
		{ID: 3, UserID: 1},
	}
	if got := scopeRowsForCaller(all, rpcIdentity{userID: 9, admin: true}); len(got) != 3 {
		t.Fatalf("admin sees %d rows, want all 3", len(got))
	}
	got := scopeRowsForCaller(all, rpcIdentity{userID: 1})
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("user 1 sees %+v, want ids 1 and 3", got)
	}
	if got := scopeRowsForCaller(all, rpcIdentity{userID: 5}); len(got) != 0 {
		t.Fatalf("unknown user sees %d rows, want none", len(got))
	}
}
