package downloads

import "testing"

// TestSeedSource_PrefersBareInfoHashMagnet locks in that a completed download is
// re-seeded via a bare info_hash magnet (resolved from cached metainfo) rather
// than its stored origin URL, which is an ephemeral indexer link that 404s.
func TestSeedSource_PrefersBareInfoHashMagnet(t *testing.T) {
	d := Download{
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Magnet:   "https://jackett.example/dl/abc?token=expired", // would 404
	}
	want := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	if got := d.SeedSource(); got != want {
		t.Errorf("SeedSource() = %q, want bare magnet %q", got, want)
	}
}

// TestSeedSource_FallsBackWhenNoInfoHash falls back to the stored source when no
// info_hash is known, honouring a recovered ActiveMagnet first.
func TestSeedSource_FallsBackWhenNoInfoHash(t *testing.T) {
	d := Download{Magnet: "magnet:?xt=urn:btih:deadbeef"}
	if got := d.SeedSource(); got != "magnet:?xt=urn:btih:deadbeef" {
		t.Errorf("SeedSource() without info_hash = %q, want stored magnet", got)
	}
	d2 := Download{ActiveMagnet: "magnet:active", Magnet: "magnet:orig"}
	if got := d2.SeedSource(); got != "magnet:active" {
		t.Errorf("SeedSource() fallback = %q, want active magnet", got)
	}
}
