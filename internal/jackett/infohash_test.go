package jackett

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"strings"
	"testing"
)

// TestSearch_CanonicalizesInfoHash guards the "two cards, same torrent" dedup
// bug. Jackett indexers are wildly inconsistent about the InfoHash field:
//   - some omit it entirely and only ship the magnet,
//   - some return it UPPER/mixed case,
//   - some encode the btih in base32 (32 chars) instead of hex (40).
//
// Without canonicalization the SAME torrent lands in different group buckets
// (the Map key is the raw hash) and renders as duplicate cards that both Play
// the same magnet — exactly what the user saw searching "mandalorian".
//
// Search must always return a canonical, lowercase, 40-hex-char infoHash,
// deriving it from the magnet when the dedicated field is absent.
func TestSearch_CanonicalizesInfoHash(t *testing.T) {
	const canonical = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a" // 40 hex chars
	raw, err := hex.DecodeString(canonical)
	if err != nil {
		t.Fatalf("bad test fixture: %v", err)
	}
	b32 := base32.StdEncoding.EncodeToString(raw) // 32-char base32 form of the same hash

	cases := []struct {
		name   string
		result jackettResult
	}{
		{
			name: "field omitted, derived from magnet",
			result: jackettResult{
				Title:     "The Mandalorian S01",
				MagnetUri: "magnet:?xt=urn:btih:" + canonical + "&tr=udp://x",
			},
		},
		{
			name: "uppercase hex field",
			result: jackettResult{
				Title:     "The.Mandalorian.S01",
				InfoHash:  strings.ToUpper(canonical),
				MagnetUri: "magnet:?xt=urn:btih:" + strings.ToUpper(canonical),
			},
		},
		{
			name: "base32 btih in magnet",
			result: jackettResult{
				Title:     "Mandalorian base32",
				MagnetUri: "magnet:?xt=urn:btih:" + b32,
			},
		},
		{
			name: "whitespace padded field",
			result: jackettResult{
				Title:     "Mandalorian padded",
				InfoHash:  "  " + canonical + "\n",
				MagnetUri: "magnet:?xt=urn:btih:" + canonical,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, client := makeJackettServer(t, []jackettResult{tc.result})
			results, err := client.Search("mandalorian", "", nil)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("len(results) = %d, want 1", len(results))
			}
			if results[0].InfoHash != canonical {
				t.Errorf("InfoHash = %q, want canonical %q", results[0].InfoHash, canonical)
			}
		})
	}
}

// TestSearch_LeavesUnresolvableInfoHashEmpty ensures we don't fabricate garbage:
// a result with no usable hash anywhere stays empty (so the name|size fallback
// bucket still applies) rather than getting a malformed value.
func TestSearch_LeavesUnresolvableInfoHashEmpty(t *testing.T) {
	_, client := makeJackettServer(t, []jackettResult{
		{Title: "No hash here", Link: "http://x/y.torrent"},
	})
	results, err := client.Search("x", "", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].InfoHash != "" {
		t.Errorf("InfoHash = %q, want empty", results[0].InfoHash)
	}
}

func TestNormalizeBTIH(t *testing.T) {
	const canonical = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	raw, _ := hex.DecodeString(canonical)
	b32 := base32.StdEncoding.EncodeToString(raw)

	cases := []struct {
		in, want string
	}{
		{canonical, canonical},                  // already canonical
		{strings.ToUpper(canonical), canonical}, // upper hex
		{"  " + canonical + "\t", canonical},    // padded
		{b32, canonical},                        // base32 → hex
		{strings.ToLower(b32), canonical},       // lower base32 (upcased internally)
		{"", ""},                                // empty
		{"deadbeef", ""},                        // too short
		{strings.Repeat("z", 40), ""},           // 40 chars, not hex
		{strings.Repeat("1", 32), ""},           // 32 chars, not valid base32
		{strings.Repeat("a", 50), ""},           // wrong length
	}
	for _, tc := range cases {
		if got := normalizeBTIH(tc.in); got != tc.want {
			t.Errorf("normalizeBTIH(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBtihFromMagnet(t *testing.T) {
	const canonical = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"magnet:?xt=urn:btih:" + canonical, canonical},
		{"magnet:?xt=urn:btih:" + canonical + "&dn=x&tr=udp://y", canonical},
		{"magnet:?dn=x&xt=urn:btih:" + canonical, canonical},
		{"magnet:?dn=onlyname&tr=udp://y", ""},  // no btih param
		{"not-a-magnet-no-question-mark", ""},   // no '?' → whole string scanned, no btih
		{"xt=urn:btih:" + canonical, canonical}, // bare query, no '?'
	}
	for _, tc := range cases {
		if got := btihFromMagnet(tc.in); got != tc.want {
			t.Errorf("btihFromMagnet(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSearchOnIndexer_CanonicalizesInfoHash mirrors the guard for the per-indexer
// (SSE / StreamSearch) path, which has its own result-mapping block.
func TestSearchOnIndexer_CanonicalizesInfoHash(t *testing.T) {
	const canonical = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	_, client := makeJackettServer(t, []jackettResult{
		{Title: "Mando", MagnetUri: "magnet:?xt=urn:btih:" + strings.ToUpper(canonical)},
	})
	results, err := client.SearchOnIndexer(context.Background(), "1337x", "mando", "")
	if err != nil {
		t.Fatalf("SearchOnIndexer failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].InfoHash != canonical {
		t.Errorf("InfoHash = %q, want canonical %q", results[0].InfoHash, canonical)
	}
}
