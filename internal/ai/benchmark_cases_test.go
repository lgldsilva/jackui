package ai

import (
	"path/filepath"
	"strings"
	"testing"
)

// The shipped dataset must stay broad and well-formed: enough cases, unique
// inputs, non-empty parseable expects, and the default origin stamped.
func TestDefaultBenchmarkCasesValid(t *testing.T) {
	if len(DefaultBenchmarkCases) < 50 {
		t.Fatalf("default set should be broad (>=50 cases), got %d", len(DefaultBenchmarkCases))
	}
	seen := map[string]bool{}
	for _, tc := range DefaultBenchmarkCases {
		if strings.TrimSpace(tc.Raw) == "" {
			t.Fatal("case with empty raw input")
		}
		if seen[tc.Raw] {
			t.Fatalf("duplicate raw input: %q", tc.Raw)
		}
		seen[tc.Raw] = true
		if strings.TrimSpace(tc.Expect) == "" {
			t.Fatalf("case %q has empty expect", tc.Raw)
		}
		if parseExpect(tc.Expect).Title == "" {
			t.Fatalf("case %q: expect %q parses to an empty title", tc.Raw, tc.Expect)
		}
		if tc.Origin != OriginDefault {
			t.Fatalf("case %q: origin %q, want %q", tc.Raw, tc.Origin, OriginDefault)
		}
	}
}

// The set must pin every structure kind, otherwise the benchmark silently stops
// measuring season/episode extraction and only scores titles again.
func TestDefaultCasesCoverStructures(t *testing.T) {
	var episodes, packs, movies, bare int
	for _, tc := range DefaultBenchmarkCases {
		ef := parseExpect(tc.Expect)
		switch {
		case ef.Episode > 0:
			episodes++
		case ef.Season > 0:
			packs++
		case ef.Year > 0:
			movies++
		default:
			bare++
		}
	}
	if episodes < 10 || packs < 3 || movies < 25 || bare < 1 {
		t.Fatalf("unbalanced dataset: episodes=%d season-packs=%d movies=%d bare=%d", episodes, packs, movies, bare)
	}
}

// Few-shot examples in the production prompts must never reuse a benchmark
// input — a model would copy the answer straight from its own system prompt and
// the accuracy number would be meaningless.
func TestDefaultCasesNotInPrompts(t *testing.T) {
	for _, tc := range DefaultBenchmarkCases {
		if strings.Contains(renameSystem, tc.Raw) || strings.Contains(identifySystem, tc.Raw) {
			t.Fatalf("benchmark case %q appears in a production prompt (few-shot leak)", tc.Raw)
		}
	}
}

// Every few-shot example output in renameSystem must parse with the REAL parser
// — guards prompt edits against drifting away from the format the code expects.
func TestRenamePromptExamplesParse(t *testing.T) {
	n := 0
	for _, line := range strings.Split(renameSystem, "\n") {
		_, after, found := strings.Cut(line, " → ")
		if !found || !strings.HasPrefix(after, "{") {
			continue
		}
		n++
		res, err := parseRenameJSON(after)
		if err != nil || res.Title == "" {
			t.Fatalf("prompt example doesn't parse (%v): %q", err, line)
		}
	}
	if n < 8 {
		t.Fatalf("expected >=8 few-shot examples in renameSystem, found %d", n)
	}
}

func TestParseExpectSeasonPack(t *testing.T) {
	got := parseExpect("The Wire - S04")
	want := expectFields{Title: "The Wire", Season: 4}
	if got != want {
		t.Fatalf("parseExpect season pack = %+v, want %+v", got, want)
	}
	// Right title + right season = perfect; wrong season loses the 40% structure
	// share; an invented episode number on a pack is NOT penalized (unpinned).
	pack := &RenameMetadata{Title: "The Wire", Kind: "tv", Season: 4, Episode: 3}
	if a := caseAccuracy(pack, "The Wire - S04"); a != 1 {
		t.Fatalf("right season pack should be 1.0, got %v", a)
	}
	wrong := &RenameMetadata{Title: "The Wire", Kind: "tv", Season: 2}
	if a := caseAccuracy(wrong, "The Wire - S04"); a != 0.6 {
		t.Fatalf("wrong season should keep only the 0.6 title share, got %v", a)
	}
}

// The comparison criterion: case-insensitive, accent-insensitive, punctuation
// and separator runs collapsed, non-Latin scripts preserved (not erased).
func TestNormalizeTitleCriterion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Amélie", "amelie"},
		{"  São---Paulo!! ", "sao paulo"},
		{"The.Matrix", "the matrix"},
		{"Spider-Man_Across  the Spider-Verse", "spider man across the spider verse"},
		{"Сталкер", "сталкер"},
		{"千と千尋の神隠し", "千と千尋の神隠し"},
	}
	for _, tc := range cases {
		if got := normalizeTitle(tc.in); got != tc.want {
			t.Fatalf("normalizeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTitleAccuracyAccentAndScript(t *testing.T) {
	if a := titleAccuracy("Amelie", "Amélie"); a != 1 {
		t.Fatalf("accent-only difference should be a perfect match, got %v", a)
	}
	// Non-Latin titles used to normalize to "" and score 0 even on an exact match.
	if a := titleAccuracy("Сталкер", "Сталкер"); a != 1 {
		t.Fatalf("exact non-Latin match should be 1, got %v", a)
	}
}

// Simulated LLM replies through the real parser + scorer: prose+fence wrapping,
// and a season-pack extraction scored end-to-end. No network involved.
func TestParseRenameJSONSimulatedReplies(t *testing.T) {
	reply := "Sure! Here's the metadata:\n```json\n{\"title\":\"True Detective\",\"year\":2014,\"kind\":\"tv\",\"season\":1,\"episode\":0,\"episode_title\":\"\"}\n```"
	res, err := parseRenameJSON(reply)
	if err != nil {
		t.Fatalf("parseRenameJSON: %v", err)
	}
	if res.Title != "True Detective" || res.Season != 1 || res.Episode != 0 || res.Kind != "tv" {
		t.Fatalf("unexpected parse: %+v", res)
	}
	if a := caseAccuracy(res, "True Detective - S01"); a != 1 {
		t.Fatalf("season pack reply should score 1.0, got %v", a)
	}
	trap := `{"title":"Wonder Woman 1984","year":2020,"kind":"movie","season":0,"episode":0,"episode_title":""}`
	res, err = parseRenameJSON(trap)
	if err != nil {
		t.Fatalf("parseRenameJSON trap: %v", err)
	}
	if a := caseAccuracy(res, "Wonder Woman 1984 - 2020"); a != 1 {
		t.Fatalf("year-in-title reply should score 1.0, got %v", a)
	}
}

func TestStoreCasesOriginRoundtrip(t *testing.T) {
	st, err := NewBenchmarkStore(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer st.Close()

	// First read seeds the defaults, origin stamped.
	cases := st.Cases()
	if len(cases) != len(DefaultBenchmarkCases) {
		t.Fatalf("seed: got %d cases, want %d", len(cases), len(DefaultBenchmarkCases))
	}
	if cases[0].Origin != OriginDefault {
		t.Fatalf("seeded case origin = %q, want %q", cases[0].Origin, OriginDefault)
	}

	// A custom set — origin-less, exactly what the UI's textarea PUT sends —
	// replaces the seed and round-trips, preserving any origin that WAS sent.
	custom := []BenchmarkCase{
		{Raw: "My.Movie.2024.1080p.WEB-DL", Expect: "My Movie - 2024"},
		{Raw: "Kept.Case.2010.1080p", Expect: "Kept Case - 2010", Origin: OriginDefault},
	}
	if err := st.SetCases(custom); err != nil {
		t.Fatalf("SetCases: %v", err)
	}
	got := st.Cases()
	if len(got) != 2 || got[0].Origin != "" || got[1].Origin != OriginDefault {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestStoreLegacySeedUpgrade(t *testing.T) {
	st, err := NewBenchmarkStore(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer st.Close()

	// Simulate a store seeded by an older build: the original 7 cases, untouched.
	if err := st.SetCases(legacySeedCases); err != nil {
		t.Fatalf("SetCases legacy: %v", err)
	}
	if got := st.Cases(); len(got) != len(DefaultBenchmarkCases) {
		t.Fatalf("legacy seed should upgrade to the new defaults: got %d, want %d", len(got), len(DefaultBenchmarkCases))
	}

	// But an EDITED legacy set is the user's — it must be preserved.
	edited := append([]BenchmarkCase(nil), legacySeedCases...)
	edited[0].Expect = "Edited - 2010"
	if err := st.SetCases(edited); err != nil {
		t.Fatalf("SetCases edited: %v", err)
	}
	got := st.Cases()
	if len(got) != len(edited) || got[0].Expect != "Edited - 2010" {
		t.Fatalf("edited set must be preserved, got %+v", got)
	}
}
