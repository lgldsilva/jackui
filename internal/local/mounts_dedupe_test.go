package local

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestDedupePreferRestricted(t *testing.T) {
	restricted := config.ExternalMount{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}}
	open := config.ExternalMount{Name: "downloads", Path: "/downloads/"} // case + slash twin

	for name, in := range map[string][]config.ExternalMount{
		"restricted-first": {restricted, open},
		"open-first":       {open, restricted},
	} {
		t.Run(name, func(t *testing.T) {
			out := dedupePreferRestricted(in)
			if len(out) != 1 {
				t.Fatalf("want 1 mount after dedupe, got %+v", out)
			}
			if len(out[0].AllowedUsers) != 1 || out[0].AllowedUsers[0] != "luiz" {
				t.Fatalf("restricted entry must win: %+v", out[0])
			}
		})
	}
}

func TestDedupePreferRestricted_KeepsDistinctMounts(t *testing.T) {
	in := []config.ExternalMount{
		{Name: "A", Path: "/a"},
		{Name: "B", Path: "/b", AllowedUsers: []string{"x"}},
	}
	if out := dedupePreferRestricted(in); len(out) != 2 {
		t.Fatalf("distinct mounts must be preserved: %+v", out)
	}
}

func TestMountsFor_DuplicateNeverWidensVisibility(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Downloads", Path: "/downloads/"},
		{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}},
	})
	if anon := b.MountsFor(""); len(anon) != 0 {
		t.Errorf("anonymous must not see the duplicated restricted mount: %+v", anon)
	}
	luiz := b.MountsFor("luiz")
	if len(luiz) != 1 || !luiz[0].Restricted {
		t.Errorf("luiz should see one restricted mount: %+v", luiz)
	}
}
