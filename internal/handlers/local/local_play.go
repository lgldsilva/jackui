package local

import (
	"bytes"
	"context"
	// #nosec G505 -- import de sha1 p/ hash de conteudo (dedup/oshash), nao cripto de seguranca
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/library"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
)

// localSessionKey derives a stable, filesystem-safe HLS session key from
// (mount, relPath). sha1 keeps the key short and avoids leaking the path.
func localSessionKey(mount, relPath string) string {
	// #nosec G401 -- sha1/md5 p/ hash de conteudo (dedup/oshash), nao uso criptografico de seguranca
	sum := sha1.Sum([]byte(mount + "|" + relPath))
	return "local-" + hex.EncodeToString(sum[:])
}

// transferKeyDirect / transferKeyHLS key the localstream metering Session for
// the direct-play and HLS-transcode paths respectively. They are kept distinct
// (a file plays via one path or the other) so a solo direct session never
// evicts a shared HLS session from the registry. The transfer-status endpoint
// checks the HLS key first, then the direct key.
func transferKeyDirect(mount, scoped string) string { return localSessionKey(mount, scoped) }
func transferKeyHLS(mount, scoped string) string    { return localSessionKey(mount, scoped) + "-hls" }

func resolveLocalFile(b *lb.Browser, c *gin.Context, mount, path string) (string, bool) {
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return "", false
	}
	if !CheckMountAccess(b, c, mount) {
		return "", false
	}
	abs, err := b.ResolvePath(mount, ScopePath(b, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", false
	}
	stat, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": httpshared.ErrFileNotFound})
			return "", false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", false
	}
	if stat.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": httpshared.ErrPathIsDir})
		return "", false
	}
	return abs, true
}

func localPlayToken(c *gin.Context) string {
	if h := c.Request.Header.Get(httpshared.HeaderAuthorization); strings.HasPrefix(h, auth.BearerPrefix) {
		return strings.TrimPrefix(h, auth.BearerPrefix)
	}
	return c.Query("token")
}

func appendTokenToURL(token, base string) string {
	if token == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "token=" + url.QueryEscape(token)
}

// localPlayVideoResp takes ctx + the already-parsed forceHLS hint (not the
// *gin.Context) so the batch handler can call it from goroutines without touching
// the shared context concurrently. LocalPlay passes c.Request.Context() +
// c.Query("transcode")=="hls".
func localPlayVideoResp(ctx context.Context, forceHLS bool, abs, mount, path, token string) LocalPlayResp {
	// iOS/Safari WebKit trava em MP4 progressive servido por HTTP (estaciona em
	// readyState 2). O cliente iOS pede transcode=hls pra vídeo local; H264/AAC vira
	// só REMUX (sem re-encode, barato). Assim o vídeo local vai pelo MESMO caminho HLS
	// confiável do torrent, em vez do direct/progressive que o iOS não toca.
	if forceHLS {
		return LocalPlayResp{
			Kind:   "hls",
			URL:    appendTokenToURL(token, buildLocalHLSURL(mount, path)),
			Reason: "client_forced",
		}
	}
	probe, perr := probeLocalFile(ctx, abs)
	if perr != nil {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".mp4" || ext == ".m4v" || ext == ".mov" || ext == ".webm" {
			return LocalPlayResp{
				Kind:   "direct",
				URL:    appendTokenToURL(token, buildLocalFileURL(mount, path)),
				Reason: "probe_failed_safe_ext",
			}
		}
		return LocalPlayResp{
			Kind:   "hls",
			URL:    appendTokenToURL(token, buildLocalHLSURL(mount, path)),
			Reason: "probe_failed",
		}
	}
	direct, reason := classifyForBrowser(probe)
	if direct {
		return LocalPlayResp{
			Kind:      "direct",
			URL:       appendTokenToURL(token, buildLocalFileURL(mount, path)),
			VCodec:    probe.VideoCodec,
			ACodec:    probe.AudioCodec,
			Container: probe.Container,
		}
	}
	return LocalPlayResp{
		Kind:      "hls",
		URL:       appendTokenToURL(token, buildLocalHLSURL(mount, path)),
		Reason:    reason,
		VCodec:    probe.VideoCodec,
		ACodec:    probe.AudioCodec,
		Container: probe.Container,
	}
}

// universalAudioCodecs play inline in EVERY target browser (incl. Safari/iOS),
// so they always direct-play regardless of the client's reported capabilities.
var universalAudioCodecs = map[string]bool{"aac": true, "mp3": true}

// normalizeAudioCodec maps an ffprobe codec_name to the capability token the
// frontend reports via canPlayType (see web/src/lib/audioCaps.ts). Returns ""
// for codecs no browser plays inline (ape/wma/dts/ac3/eac3/truehd/…) → those
// always transcode.
func normalizeAudioCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "aac":
		return "aac"
	case "mp3":
		return "mp3"
	case "flac":
		return "flac"
	case "opus":
		return "opus"
	case "vorbis":
		return "vorbis"
	case "alac":
		return "alac"
	}
	if strings.HasPrefix(strings.ToLower(codec), "pcm") {
		return "wav"
	}
	return ""
}

// parseAudioCaps reads the `acaps` query (comma-separated capability tokens the
// client can direct-play, computed from canPlayType) into a set.
func parseAudioCaps(q string) map[string]bool {
	caps := map[string]bool{}
	for _, t := range strings.Split(q, ",") {
		if t = strings.TrimSpace(strings.ToLower(t)); t != "" {
			caps[t] = true
		}
	}
	return caps
}

// audioDirectPlayable decides whether a probed audio codec can play inline in
// the requesting browser: universal codecs always; everything else only when
// the client advertised support via acaps. Unknown/unsupported codecs (token
// "") always transcode.
func audioDirectPlayable(probedCodec string, caps map[string]bool) bool {
	tok := normalizeAudioCodec(probedCodec)
	if tok == "" {
		return false
	}
	return universalAudioCodecs[tok] || caps[tok]
}

// audioExtUniversallySafe reports extensions whose codec is universally inline-
// playable, used as the safe fallback when ffprobe fails (so we never force HLS
// on a plain MP3 just because the probe timed out on a slow mount).
func audioExtUniversallySafe(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4a", ".aac":
		return true
	}
	return false
}

// localPlayAudioResp decides direct-play vs audio-only HLS for a local audio
// file. Safari/WebKit refuse FLAC/OGG/Opus/WAV inline, so anything the client
// can't direct-play (per acaps) is transcoded to AAC HLS via the SAME
// HLSSessionManager as video (with AudioOnly set → `-vn`). The previous code
// force-direct-played ALL audio, which silently failed on Safari for those
// codecs — this is the fix.
// localPlayAudioResp takes ctx (not the *gin.Context) so the batch handler can
// call it from goroutines without touching the shared context concurrently.
func localPlayAudioResp(ctx context.Context, abs, mount, path, token string) LocalPlayResp {
	probe, perr := probeLocalFile(ctx, abs)
	var acodec, container string
	if perr == nil {
		acodec = probe.AudioCodec
		container = probe.Container
	}
	return LocalPlayResp{
		Kind:      "direct",
		URL:       appendTokenToURL(token, buildLocalFileURL(mount, path)),
		ACodec:    acodec,
		Container: container,
	}
}

// LocalPlay handles GET /api/local/play?mount=NAME&path=REL — probes the file
// and returns either { kind: "direct", url } or { kind: "hls", url }. The
// frontend just consumes `url` and trusts the kind for any wrapper logic
// (subtitles via WebVTT, etc.).
func LocalPlay(b *lb.Browser, lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")

		abs, ok := resolveLocalFile(b, c, mount, path)
		if !ok {
			return
		}

		token := localPlayToken(c)

		isAudio := isAudioByExt(path)
		var resp LocalPlayResp
		if isAudio {
			resp = localPlayAudioResp(c.Request.Context(), abs, mount, path, token)
		} else {
			resp = localPlayVideoResp(c.Request.Context(), c.Query("transcode") == "hls", abs, mount, path, token)
		}
		// Track in the library so local media shows in Continue Watching and gets
		// resume — same as torrents (StreamAdd). Best-effort; never blocks playback.
		resp.LibraryID = upsertLocalLibrary(c, lib, mount, path, isAudio)
		c.JSON(http.StatusOK, resp)
	}
}

// LocalPlayBatchItem is one file's resolution within a batch response — mirrors
// LocalPlayResp plus the path, with a per-file Error so one unprobeable file
// never fails the whole list.
type LocalPlayBatchItem struct {
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	URL       string `json:"url,omitempty"`
	VCodec    string `json:"vcodec,omitempty"`
	ACodec    string `json:"acodec,omitempty"`
	Container string `json:"container,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

const (
	localPlayBatchMax         = 500 // cap files per batch (ffprobe is heavy)
	localPlayBatchConcurrency = 4   // bound parallel ffprobe
)

// LocalPlayBatch handles POST /api/local/play/batch {mount, paths:[...]} →
// {items:[...]} — resolves direct-vs-HLS + the playable URL for MANY files in
// ONE call, so pre-warming a playlist costs a single round-trip instead of one
// GET /api/local/play (ffprobe) per file (the frontend N+1 when opening an
// album). It only RESOLVES (to seed the playback URL cache); it does NOT upsert
// the library — that stays on the actual play via GET /api/local/play.
func LocalPlayBatch(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Mount string   `json:"mount"`
			Paths []string `json:"paths"`
			// ForceHLS mirrors synthesizeLocalInfo's per-device choice (iOS forces
			// video to HLS): applied ONLY to video files below (audio ignores it),
			// so a pre-warmed URL matches exactly what the actual play resolves —
			// otherwise iOS would read a cached "direct" URL and stall on video.
			ForceHLS bool `json:"forceHLS"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Mount == "" || len(req.Paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if len(req.Paths) > localPlayBatchMax {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many paths"})
			return
		}
		if !CheckMountAccess(b, c, req.Mount) {
			return
		}
		// Extract everything from the *gin.Context ONCE — the goroutines below must
		// not touch the shared context concurrently.
		ctx := c.Request.Context()
		token := localPlayToken(c)
		username := scopeUser(c)

		items := make([]LocalPlayBatchItem, len(req.Paths))
		sem := make(chan struct{}, localPlayBatchConcurrency)
		var wg sync.WaitGroup
		for i, p := range req.Paths {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, p string) {
				defer wg.Done()
				defer func() { <-sem }()
				items[i] = resolveBatchItem(ctx, b, req.Mount, p, username, token, req.ForceHLS)
			}(i, p)
		}
		wg.Wait()
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

// resolveBatchItem resolves ONE file for LocalPlayBatch: scope+resolve the path,
// stat it, then reuse the same audio/video resolvers as the single GET. Extracted
// from the goroutine so LocalPlayBatch stays well under the cognitive-complexity
// gate. Returns a per-file Error instead of failing the whole batch.
func resolveBatchItem(ctx context.Context, b *lb.Browser, mount, p, username, token string, forceHLS bool) LocalPlayBatchItem {
	abs, err := b.ResolvePathFor(mount, p, username)
	if err != nil {
		return LocalPlayBatchItem{Path: p, Error: err.Error()}
	}
	if st, serr := os.Stat(abs); serr != nil || st.IsDir() {
		return LocalPlayBatchItem{Path: p, Error: "not found"}
	}
	var resp LocalPlayResp
	if isAudioByExt(p) {
		resp = localPlayAudioResp(ctx, abs, mount, p, token)
	} else {
		resp = localPlayVideoResp(ctx, forceHLS, abs, mount, p, token)
	}
	return LocalPlayBatchItem{
		Path: p, Kind: resp.Kind, URL: resp.URL,
		VCodec: resp.VCodec, ACodec: resp.ACodec, Container: resp.Container, Reason: resp.Reason,
	}
}

// localInfoHash derives the SAME `local-<base64url(json)>` pseudo info-hash the
// frontend builds (buildLocalHash) so the library row keys on the value the
// deep-link (?play=local-…) and the player already use. CRITICAL: disable Go's
// default HTML escaping (it would turn & < > into \u00XX, which JS's
// JSON.stringify does NOT) and strip Encoder's trailing newline — otherwise the
// hash diverges for paths with those chars (e.g. "Simon & Garfunkel").
func localInfoHash(mount, path string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
	}{mount, path})
	raw := bytes.TrimRight(buf.Bytes(), "\n")
	return "local-" + base64.RawURLEncoding.EncodeToString(raw)
}

// upsertLocalLibrary records the local file in the user's library and returns
// the row id (0 when tracking is unavailable). Mirrors StreamAdd: still upserts
// in incognito but flags the row so it stays out of normal listings.
func upsertLocalLibrary(c *gin.Context, lib *library.Store, mount, path string, isAudio bool) int {
	if lib == nil {
		return 0
	}
	userID, _, _ := auth.UserIDFromCtx(c)
	hash := localInfoHash(mount, path)
	name := path
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		name = path[i+1:]
	}
	kind := "video"
	if isAudio {
		kind = "audio"
	}
	e, err := lib.Upsert(library.UpsertInput{
		UserID:    userID,
		InfoHash:  hash,
		Magnet:    "magnet:?xt=urn:btih:" + hash,
		Name:      name,
		Kind:      kind,
		Incognito: middleware.IsIncognito(c),
	})
	if err != nil {
		return 0
	}
	return e.ID
}

func buildLocalFileURL(mount, path string) string {
	p := url.Values{}
	p.Set("mount", mount)
	p.Set("path", path)
	return "/api/local/file?" + p.Encode()
}

func buildLocalHLSURL(mount, path string) string {
	p := url.Values{}
	p.Set("mount", mount)
	p.Set("path", path)
	return "/api/local/hls/index.m3u8?" + p.Encode()
}

func isAudioByExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".wav", ".opus":
		return true
	}
	return false
}
