package jackett

import (
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const btihPrefix = "xt=urn:btih:"

// CanonicalInfoHash returns a lowercase 40-char hex btih for a Jackett result,
// or "" if none can be derived. It prefers the dedicated InfoHash field and
// falls back to the magnet's xt=urn:btih: param when the field is absent —
// many indexers ship only the magnet.
//
// Both the hex (40-char) and base32 (32-char) btih encodings are coerced to
// hex so the SAME torrent always yields the SAME key. Without this, indexers
// that disagree on encoding/case (or omit the field) scatter one torrent across
// several group buckets, rendering as duplicate cards that all Play the same
// magnet — the "two cards, same torrent" bug. Exported so the SSE dedup layer
// can re-canonicalize cached rows that predate this normalization.
func CanonicalInfoHash(rawHash, magnet string) string {
	if h := normalizeBTIH(rawHash); h != "" {
		return h
	}
	return normalizeBTIH(btihFromMagnet(magnet))
}

// btihFromMagnet pulls the raw btih value out of a magnet's xt param (case
// insensitive on the prefix), or "" if absent.
func btihFromMagnet(magnet string) string {
	if magnet == "" {
		return ""
	}
	q := magnet
	if i := strings.Index(magnet, "?"); i >= 0 {
		q = magnet[i+1:]
	}
	for _, p := range strings.Split(q, "&") {
		if strings.HasPrefix(strings.ToLower(p), btihPrefix) {
			return p[len(btihPrefix):]
		}
	}
	return ""
}

// normalizeBTIH coerces a raw btih (hex-40 or base32-32) to lowercase hex-40,
// returning "" for anything that isn't a valid btih in either encoding.
func normalizeBTIH(s string) string {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 40:
		lower := strings.ToLower(s)
		if _, err := hex.DecodeString(lower); err != nil {
			return ""
		}
		return lower
	case 32:
		b, err := base32.StdEncoding.DecodeString(strings.ToUpper(s))
		if err != nil {
			return ""
		}
		return hex.EncodeToString(b)
	default:
		return ""
	}
}
