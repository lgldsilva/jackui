package transcode

import "testing"

func TestParseVODMode(t *testing.T) {
	cases := map[string]VODMode{
		"":        VODOff,
		"off":     VODOff,
		"garbage": VODOff,
		"hlsjs":   VODHLSJS,
		"hls.js":  VODHLSJS,
		"HLSJS":   VODHLSJS,
		"all":     VODAll,
		"ALL":     VODAll,
		"on":      VODAll,
		"true":    VODAll,
		"1":       VODAll,
		"  all  ": VODAll,
	}
	for in, want := range cases {
		if got := ParseVODMode(in); got != want {
			t.Errorf("ParseVODMode(%q)=%v want %v", in, got, want)
		}
	}
}

func TestVODModeAllows(t *testing.T) {
	type row struct {
		mode      VODMode
		nativeHLS bool
		want      bool
	}
	rows := []row{
		{VODOff, false, false},
		{VODOff, true, false},
		{VODHLSJS, false, true}, // hls.js client (Chrome) → eligible
		{VODHLSJS, true, false}, // Safari native → stays EVENT
		{VODAll, false, true},
		{VODAll, true, true},
	}
	for _, r := range rows {
		if got := r.mode.allows(r.nativeHLS); got != r.want {
			t.Errorf("%v.allows(native=%v)=%v want %v", r.mode, r.nativeHLS, got, r.want)
		}
	}
}

func TestShouldVOD(t *testing.T) {
	type row struct {
		name      string
		dur       float64
		forceVOD  bool
		mode      VODMode
		nativeHLS bool
		want      bool
	}
	rows := []row{
		// Unknown duration → ALWAYS EVENT, even when forcing VOD.
		{"dur=0 never VOD even forced", 0, true, VODAll, false, false},
		// Local files (forceVOD) → VOD with known duration, on any client and
		// any policy — including native Safari (the reported bug).
		{"local Safari hlsjs → VOD", 2763, true, VODHLSJS, true, true},
		{"local Safari off → VOD", 100, true, VODOff, true, true},
		{"local Chrome → VOD", 100, true, VODHLSJS, false, true},
		// Torrents (forceVOD=false) → the global policy rules; Safari stays EVENT
		// under hlsjs (#61 guard), but Chrome gets VOD.
		{"torrent Safari hlsjs → EVENT", 100, false, VODHLSJS, true, false},
		{"torrent Chrome hlsjs → VOD", 100, false, VODHLSJS, false, true},
		{"torrent Safari off → EVENT", 100, false, VODOff, true, false},
		// mode=all (new default): an IN-PROGRESS torrent (forceVOD=false) with a
		// known duration enters VOD for both native Safari and hls.js; unknown
		// duration still falls back to EVENT (the correct last resort).
		{"torrent Safari all → VOD", 100, false, VODAll, true, true},
		{"torrent Chrome all → VOD", 100, false, VODAll, false, true},
		{"torrent Safari all dur=0 → EVENT", 0, false, VODAll, true, false},
	}
	for _, r := range rows {
		if got := shouldVOD(r.dur, r.forceVOD, r.mode, r.nativeHLS); got != r.want {
			t.Errorf("%s: shouldVOD(%v,%v,%v,native=%v)=%v want %v",
				r.name, r.dur, r.forceVOD, r.mode, r.nativeHLS, got, r.want)
		}
	}
}

func TestVODReason(t *testing.T) {
	type row struct {
		name      string
		dur       float64
		forceVOD  bool
		mode      VODMode
		nativeHLS bool
		want      string
	}
	rows := []row{
		// The three reasons visible in the EVENT session log:
		{"probe failed", 0, false, VODAll, true, "no-duration"},
		{"probe failed even forced", 0, true, VODAll, false, "no-duration"},
		{"policy off", 100, false, VODOff, false, "mode-off"},
		{"hlsjs excludes native Safari", 100, false, VODHLSJS, true, "mode-hlsjs-native"},
		// Combinations where shouldVOD is true → empty reason (not logged).
		{"all Safari → VOD", 100, false, VODAll, true, ""},
		{"hlsjs Chrome → VOD", 100, false, VODHLSJS, false, ""},
		{"forced → VOD", 100, true, VODOff, true, ""},
	}
	for _, r := range rows {
		got := vodReason(r.dur, r.forceVOD, r.mode, r.nativeHLS)
		if got != r.want {
			t.Errorf("%s: vodReason(%v,%v,%v,native=%v)=%q want %q",
				r.name, r.dur, r.forceVOD, r.mode, r.nativeHLS, got, r.want)
		}
		// Invariant: non-empty reason ⇔ shouldVOD false (same conditions).
		if sv := shouldVOD(r.dur, r.forceVOD, r.mode, r.nativeHLS); sv == (got != "") {
			t.Errorf("%s: shouldVOD=%v contradiz vodReason=%q", r.name, sv, got)
		}
	}
}

func TestEffectiveKey(t *testing.T) {
	m := &HLSSessionManager{}

	// Off → key unchanged regardless of client (single shared session).
	m.SetVODMode(VODOff)
	if got := m.EffectiveKey("abc-0", false); got != "abc-0" {
		t.Fatalf("off/chrome key=%q want abc-0", got)
	}
	if got := m.EffectiveKey("abc-0", true); got != "abc-0" {
		t.Fatalf("off/safari key=%q want abc-0", got)
	}

	// hlsjs → Chrome eligible (-vod), Safari not (-evt): distinct sessions.
	m.SetVODMode(VODHLSJS)
	chrome := m.EffectiveKey("abc-0", false)
	safari := m.EffectiveKey("abc-0", true)
	if chrome != "abc-0-vod" {
		t.Fatalf("hlsjs/chrome key=%q want abc-0-vod", chrome)
	}
	if safari != "abc-0-evt" {
		t.Fatalf("hlsjs/safari key=%q want abc-0-evt", safari)
	}
	if chrome == safari {
		t.Fatal("hlsjs must split Chrome and Safari into different sessions")
	}

	// all → everyone -vod (Safari included).
	m.SetVODMode(VODAll)
	if got := m.EffectiveKey("abc-0", true); got != "abc-0-vod" {
		t.Fatalf("all/safari key=%q want abc-0-vod", got)
	}
}

func TestDurationCache(t *testing.T) {
	m := &HLSSessionManager{}
	if got := m.cachedDuration("k"); got != 0 {
		t.Fatalf("empty cache should return 0, got %v", got)
	}
	m.cacheDuration("k", 0)  // non-positive ignored
	m.cacheDuration("k", -5) // ignored
	if got := m.cachedDuration("k"); got != 0 {
		t.Fatalf("non-positive durations must not be cached, got %v", got)
	}
	m.cacheDuration("k", 123.5)
	if got := m.cachedDuration("k"); got != 123.5 {
		t.Fatalf("cachedDuration=%v want 123.5", got)
	}
	// The cache is shared across the -vod/-evt variants because it keys on the
	// raw content key, so a re-created session reuses it (no re-probe).
	if got := m.cachedDuration("other"); got != 0 {
		t.Fatalf("unrelated key should be 0, got %v", got)
	}
}
