package transcode

import "strings"

// VODMode gates the finite-VOD (seekbar) HLS path, by client class. See
// StreamConfig.HLSVODMode. The zero value is VODOff (current/safe behaviour).
type VODMode int

const (
	VODOff   VODMode = iota // EVENT/live for everyone (no seekbar)
	VODHLSJS                // VOD for hls.js clients (non-Safari); Safari stays EVENT
	VODAll                  // VOD for everyone, including Safari native HLS
)

// ParseVODMode maps the config/env string to a VODMode (default VODOff).
func ParseVODMode(s string) VODMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hlsjs", "hls.js":
		return VODHLSJS
	case "all", "on", "true", "1":
		return VODAll
	default:
		return VODOff
	}
}

// allows reports whether a client (nativeHLS = Safari/iOS native HLS) is
// eligible for the VOD path under this mode.
func (m VODMode) allows(nativeHLS bool) bool {
	switch m {
	case VODAll:
		return true
	case VODHLSJS:
		return !nativeHLS
	default:
		return false
	}
}

// SetVODMode sets the VOD policy (called once at wiring time from config).
func (m *HLSSessionManager) SetVODMode(mode VODMode) { m.vodMode = mode }

// shouldVOD decides whether a session serves the finite-VOD (seekbar) path.
// VOD requires a known duration (>0); EVENT/live is the last resort for
// unknown-duration streams. With a known duration it's VOD when EITHER the
// caller forces it (forceVOD — the local-file path, whose sources are complete
// and seekable, so live is simply wrong) OR the per-client policy allows it.
// Torrents pass forceVOD=false, so the global vodMode still guards the #61
// Safari seek instability on incomplete torrent sources.
func shouldVOD(durationSec float64, forceVOD bool, mode VODMode, nativeHLS bool) bool {
	return durationSec > 0 && (forceVOD || mode.allows(nativeHLS))
}

// vodReason names why a session is NOT entering VOD, for the startup log. The
// recurring production question is "why did this client get EVENT?" and the
// answer used to be invisible — vod=false could mean a failed probe OR the
// policy excluding Safari. Pure mirror of shouldVOD's conditions; only
// meaningful when shouldVOD returned false (returns "" otherwise).
func vodReason(durationSec float64, forceVOD bool, mode VODMode, nativeHLS bool) string {
	switch {
	case durationSec <= 0:
		return "no-duration"
	case forceVOD:
		return "" // forced VOD with a known duration never falls to EVENT
	case mode == VODOff:
		return "mode-off"
	case mode == VODHLSJS && nativeHLS:
		return "mode-hlsjs-native"
	default:
		return ""
	}
}

// EffectiveKey maps a raw content key to the session key actually used. When
// VOD is off the key is unchanged (one shared EVENT session per content, zero
// behaviour change). When VOD is on, VOD-eligible and non-eligible clients are
// split into distinct sessions (-vod/-evt) so a VOD session created by one
// client never serves a VOD playlist to a client that must stay on EVENT
// (the Safari #61 safeguard). Master and segment handlers must agree on this.
func (m *HLSSessionManager) EffectiveKey(rawKey string, nativeHLS bool) string {
	if m.vodMode == VODOff {
		return rawKey
	}
	if m.vodMode.allows(nativeHLS) {
		return rawKey + "-vod"
	}
	return rawKey + "-evt"
}

func (m *HLSSessionManager) cachedDuration(contentKey string) float64 {
	m.durMu.Lock()
	defer m.durMu.Unlock()
	return m.durCache[contentKey]
}

func (m *HLSSessionManager) cacheDuration(contentKey string, dur float64) {
	if dur <= 0 {
		return
	}
	m.durMu.Lock()
	defer m.durMu.Unlock()
	if m.durCache == nil {
		m.durCache = make(map[string]float64)
	}
	m.durCache[contentKey] = dur
}
