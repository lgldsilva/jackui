package handlers

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
	"github.com/lgldsilva/jackui/internal/parser"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

const errMissingMountOrPath = "missing mount or path"

// Local file probe/sidecar/auto endpoints — mirror /api/stream/{probe,sidecars,
// sidecar,subtrack} + /api/subtitles/auto, but keyed by mount+path instead of
// torrent hash+file index. The frontend's MediaSource union dispatches to
// these when source.kind === "local".

// localSubtitleExtensions matches the streamer's subtitleExtensions but is
// duplicated here because the streamer's map is unexported and we don't want
// a cross-package dep for 5 lines.
var localSubtitleExtensions = map[string]string{
	".srt": "srt",
	".vtt": "vtt",
	".ass": "ass",
	".ssa": "ssa",
	".sub": "sub",
}

// localProbeCache caches ffprobe results per absolute path + mtime, so opening
// the same file twice in a session doesn't re-probe.
type localProbeKey struct {
	path  string
	mtime int64
}

var (
	localProbeCacheMu sync.RWMutex
	localProbeCache   = make(map[localProbeKey]streamer.ProbeResult)
)

func LocalProbe(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPath})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		abs, st, ok := resolveLocalProbeFile(c, b, mount, path)
		if !ok {
			return
		}
		key := localProbeKey{abs, st.ModTime().UnixNano()}
		localProbeCacheMu.RLock()
		v, ok := localProbeCache[key]
		localProbeCacheMu.RUnlock()
		if ok {
			c.JSON(http.StatusOK, v)
			return
		}
		result, ok := runLocalFFProbe(c, abs)
		if !ok {
			return
		}
		localProbeCacheMu.Lock()
		if len(localProbeCache) >= 2000 {
			localProbeCache = make(map[localProbeKey]streamer.ProbeResult)
		}
		localProbeCache[key] = result
		localProbeCacheMu.Unlock()
		c.JSON(http.StatusOK, result)
	}
}

func resolveLocalProbeFile(c *gin.Context, b *local.Browser, mount, path string) (string, os.FileInfo, bool) {
	abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", nil, false
	}
	st, err := os.Stat(abs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
		return "", nil, false
	}
	return abs, st, true
}

func runLocalFFProbe(c *gin.Context, abs string) (streamer.ProbeResult, bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	// Unified probe: same ffprobe invocation + parser as the torrent path
	// (streamer.Probe), so /api/local/probe returns exactly the shape /stream/probe
	// does — no duplicate decoder to drift.
	result, err := streamer.ProbeLocal(ctx, abs)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return streamer.ProbeResult{}, false
	}
	return result, true
}

type localSidecarSub struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Language string `json:"language"`
	Format   string `json:"format"`
	Match    int    `json:"match"` // 2=basename match, 1=in same dir, 0=other
}

func collectDirSubs(dir, baseNoExt string) ([]localSidecarSub, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var subs []localSidecarSub
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		format, ok := localSubtitleExtensions[ext]
		if !ok {
			continue
		}
		match := 1
		if strings.HasPrefix(strings.TrimSuffix(e.Name(), ext), baseNoExt) {
			match = 2
		}
		info, _ := e.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		subs = append(subs, localSidecarSub{
			Name:     e.Name(),
			Size:     size,
			Language: detectLangFromName(e.Name()),
			Format:   format,
			Match:    match,
		})
	}
	sort.Slice(subs, func(i, j int) bool {
		return subs[i].Match > subs[j].Match
	})
	return subs, nil
}

// LocalSidecars handles GET /api/local/sidecars?mount=&path=
// Lists .srt/.vtt/.ass/.ssa files in the same directory as the video that
// share its base filename (or "match" loosely — any sub in the dir, ranked by
// basename overlap).
func LocalSidecars(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPath})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		dir := filepath.Dir(abs)
		baseNoExt := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))

		subs, err := collectDirSubs(dir, baseNoExt)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if subs == nil {
			subs = []localSidecarSub{}
		}
		c.JSON(http.StatusOK, subs)
	}
}

// LocalSidecarRead handles GET /api/local/sidecar?mount=&path=&name=
// Reads one sidecar file from the video's directory and returns it as WebVTT.
// SRT is converted with the existing subtitles.SRTToVTT helper. ASS/SSA fall
// through as text/plain (same fallback as the torrent sidecar handler).
func LocalSidecarRead(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		name := c.Query("name")
		if mount == "" || path == "" || name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount, path or name"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if strings.ContainsAny(name, "/\\") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid name"})
			return
		}
		abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		subPath := filepath.Join(filepath.Dir(abs), name)
		ext := strings.ToLower(filepath.Ext(name))
		format, ok := localSubtitleExtensions[ext]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported subtitle format"})
			return
		}
		raw, err := os.ReadFile(subPath)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		var body []byte
		switch format {
		case "srt":
			body = subtitles.SRTToVTT(raw)
		case "vtt":
			body = raw
		default:
			c.Header(ContentType, "text/plain; charset=utf-8")
			c.Header(CacheControl, CacheImmutable)
			_, _ = c.Writer.Write(raw)
			return
		}
		c.Header(ContentType, MIMEVTT)
		c.Header(CacheControl, CacheImmutable)
		_, _ = c.Writer.Write(body)
	}
}

func LocalSubtitlesAuto(b *local.Browser, subClient *subtitles.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		mount := ctx.Query("mount")
		path := ctx.Query("path")
		if mount == "" || path == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPath})
			return
		}
		if !checkMountAccess(b, ctx, mount) {
			return
		}
		abs, f, st, ok := resolveLocalFileWithStat(ctx, b, mount, path)
		if !ok {
			return
		}
		defer f.Close()
		langs := ctx.DefaultQuery("langs", "pt-BR,pt")
		hashRes, hashErr, query := computeOSHash(f, st, abs)
		opts := buildSearchOpts(query, langs, hashRes, hashErr)
		// query is extension-stripped (for the search); the response's "file"
		// field should carry the real filename with extension.
		serveAutoSubtitles(ctx, subClient, filepath.Base(abs), opts, hashRes, hashErr)
	}
}

func resolveLocalFileWithStat(ctx *gin.Context, b *local.Browser, mount, path string) (string, *os.File, os.FileInfo, bool) {
	abs, err := b.ResolvePath(mount, scopePath(b, ctx, mount, path))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", nil, nil, false
	}
	f, err := os.Open(abs)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return "", nil, nil, false
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", nil, nil, false
	}
	return abs, f, st, true
}

func computeOSHash(f *os.File, st os.FileInfo, abs string) (streamer.HashResult, error, string) {
	var hashRes streamer.HashResult
	var hashErr error
	if st.Size() >= 64*1024 {
		hashRes, hashErr = streamer.ComputeFileOSHash(f, st.Size())
	} else {
		hashErr = errors.New("file too small for OS hash")
	}
	baseName := filepath.Base(abs)
	return hashRes, hashErr, strings.TrimSuffix(baseName, filepath.Ext(baseName))
}

func buildSearchOpts(query, langs string, hashRes streamer.HashResult, hashErr error) subtitles.SearchOpts {
	parsed := parser.Parse(query)
	opts := subtitles.SearchOpts{
		Query:     query,
		Languages: langs,
		Season:    parsed.Season,
		Episode:   parsed.Episode,
	}
	if hashErr == nil {
		opts.MovieHash = hashRes.Hash
		opts.MovieBytesize = hashRes.Size
	}
	return opts
}

func serveAutoSubtitles(ctx *gin.Context, subClient *subtitles.Client, baseName string, opts subtitles.SearchOpts, hashRes streamer.HashResult, hashErr error) {
	results, err := subClient.SearchAuto(opts)
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{
			"error":   err.Error(),
			"osHash":  hashRes.Hash,
			"hashErr": errStrIfAny(hashErr),
			"file":    baseName,
		})
		return
	}
	if results == nil {
		results = []subtitles.Subtitle{}
	}
	ctx.JSON(http.StatusOK, gin.H{
		"osHash":  hashRes.Hash,
		"osSize":  hashRes.Size,
		"hashErr": errStrIfAny(hashErr),
		"file":    baseName,
		"results": results,
	})
}

// subExtractJobs dedupes in-flight background extractions so two players (or a
// retrying client) selecting the same track don't spawn duplicate ffmpeg runs.
// Keyed by the VTT cache key. Value is unused (presence == in flight).
var subExtractJobs sync.Map

// localSubVTTPath derives the on-disk VTT cache path for an extracted track,
// keyed by (abs, mtime, size, track) so a re-encoded/replaced source misses the
// stale cache. Lives under <cacheRoot>/subs so it shares the fast-disk volume
// and is wiped with the cache. Returns "" when no cache is configured.
func localSubVTTPath(cache *localcache.Cache, abs string, st os.FileInfo, track int) string {
	if cache == nil || st == nil {
		return ""
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", abs, st.ModTime().UnixNano(), st.Size(), track)))
	return filepath.Join(cache.Root(), "subs", hex.EncodeToString(sum[:])+".vtt")
}

// extractEmbeddedVTT runs ffmpeg to convert one embedded subtitle stream (by
// absolute ffprobe index) to WebVTT, returning the bytes. Image-based subs
// (PGS/VobSub) fail here — the frontend filters them via the probe's Image flag.
func extractEmbeddedVTT(ctx context.Context, src string, track int) ([]byte, error) {
	// -map 0:<absoluteIndex> selects the stream; ffmpeg accepts the absolute idx.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		ffHideBanner, ffLogLevel, "error",
		"-i", src,
		"-map", fmt.Sprintf("0:%d", track),
		"-c:s", "webvtt",
		"-f", "webvtt",
		"-",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// startBgSubExtract kicks off a deduped background extraction that reads the
// (slow, uncached) source to completion and writes the VTT to the cache, then
// future requests serve it instantly. Reading an embedded text sub requires
// demuxing the WHOLE container (subtitle packets are interleaved), so on a
// multi-GB rclone file this can take minutes — hence background, not blocking
// the player. Also enqueues the source for caching so playback + a re-extract
// get the fast local copy. No-op when the job is already in flight or no cache.
func startBgSubExtract(cache *localcache.Cache, mount, scoped, abs string, st os.FileInfo, track int, vttPath string) {
	if cache == nil || vttPath == "" {
		return
	}
	if _, loaded := subExtractJobs.LoadOrStore(vttPath, struct{}{}); loaded {
		return // already extracting this exact (file, track)
	}
	cache.Enqueue(mount, scoped, abs, st.Size()) // prime the fast-disk copy
	go func() {
		defer subExtractJobs.Delete(vttPath)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		data, err := extractEmbeddedVTT(ctx, abs, track)
		if err != nil {
			return // leave no file; the client retry triggers a fresh attempt
		}
		persistVTT(vttPath, data) // atomic temp+rename
	}()
}

func serveVTTBytes(c *gin.Context, data []byte) {
	c.Header(ContentType, MIMEVTT)
	c.Header(CacheControl, "public, max-age=3600")
	_, _ = c.Writer.Write(data)
}

// persistVTT atomically caches an extracted VTT (write temp + rename) so a
// concurrent reader never sees a half-written file. No-op when vttPath is "".
func persistVTT(vttPath string, data []byte) {
	if vttPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(vttPath), 0o755); err != nil {
		return
	}
	tmp := vttPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, vttPath)
}

// subtrackReq is the resolved subtitle-extract request (post validation).
type subtrackReq struct {
	mount, scoped, abs string
	st                 os.FileInfo
	track              int
}

// parseSubtrackReq validates the query params + mount access and resolves the
// file. Returns ok=false (after writing the error response) on any failure.
func parseSubtrackReq(b *local.Browser, c *gin.Context) (subtrackReq, bool) {
	mount, path, trackStr := c.Query("mount"), c.Query("path"), c.Query("track")
	if mount == "" || path == "" || trackStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount, path or track"})
		return subtrackReq{}, false
	}
	if !checkMountAccess(b, c, mount) {
		return subtrackReq{}, false
	}
	track, err := strconv.Atoi(trackStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track index"})
		return subtrackReq{}, false
	}
	scoped := scopePath(b, c, mount, path)
	abs, err := b.ResolvePath(mount, scoped)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return subtrackReq{}, false
	}
	st, _ := os.Stat(abs)
	return subtrackReq{mount: mount, scoped: scoped, abs: abs, st: st, track: track}, true
}

// serveCachedVTT serves a previously-extracted VTT from disk. Returns true when
// it handled the request.
func serveCachedVTT(c *gin.Context, vttPath string) bool {
	if vttPath == "" {
		return false
	}
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return false
	}
	serveVTTBytes(c, data)
	return true
}

// extractAndServe extracts the track synchronously (bounded by timeout), caches
// it under vttPath, and serves it; 502 on ffmpeg error.
func extractAndServe(c *gin.Context, src string, track int, timeout time.Duration, vttPath string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	data, err := extractEmbeddedVTT(ctx, src, track)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	persistVTT(vttPath, data)
	serveVTTBytes(c, data)
}

// LocalSubtitleExtract handles GET /api/local/subtrack?mount=&path=&track=
// Serves an embedded subtitle track as WebVTT. To avoid the old 2-minute hang →
// 502 on large rclone files (extraction must demux the whole container), it:
//  1. serves a previously-extracted VTT from the on-disk cache instantly;
//  2. else extracts SYNCHRONOUSLY when the source is on fast disk (a ready cache
//     copy) — seconds;
//  3. else (slow, uncached mount) kicks off a background extraction and returns
//     503 {code:"extracting"} so the client can retry shortly instead of hanging.
func LocalSubtitleExtract(b *local.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := parseSubtrackReq(b, c)
		if !ok {
			return
		}
		vttPath := localSubVTTPath(cache, req.abs, req.st, req.track)
		// 1) Already-extracted VTT on disk → instant.
		if serveCachedVTT(c, vttPath) {
			return
		}
		// 2) Fast-disk source (a ready cache copy) → extract synchronously.
		if cp, hit := cacheReady(cache, req.mount, req.scoped); hit {
			extractAndServe(c, cp.abs, req.track, 60*time.Second, vttPath)
			return
		}
		// 3a) No cache infra → legacy synchronous extract (small local files).
		if cache == nil || req.st == nil {
			extractAndServe(c, req.abs, req.track, 2*time.Minute, vttPath)
			return
		}
		// 3b) Slow uncached mount → background-extract + 503 so the client polls.
		startBgSubExtract(cache, req.mount, req.scoped, req.abs, req.st, req.track, vttPath)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":         "extracting embedded subtitle — retry shortly",
			"code":          "extracting",
			"retryAfterSec": 10,
		})
	}
}

// detectLangFromName is a tiny ISO-639 guesser from filename — covers the
// common cases for sidecar files. Reuses no streamer code so handlers stay
// self-contained.
func detectLangFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "pt-br"), strings.Contains(lower, "pt_br"), strings.Contains(lower, ".pob."), strings.Contains(lower, ".ptb."):
		return "pt-BR"
	case strings.Contains(lower, "pt-pt"), strings.Contains(lower, "pt_pt"):
		return "pt-PT"
	case strings.Contains(lower, ".pt."), strings.Contains(lower, ".por."), strings.Contains(lower, "portugue"):
		return "pt"
	case strings.Contains(lower, ".en."), strings.Contains(lower, ".eng."), strings.Contains(lower, "english"):
		return "en"
	case strings.Contains(lower, ".es."), strings.Contains(lower, ".spa."), strings.Contains(lower, "spanish"):
		return "es"
	case strings.Contains(lower, ".fr."), strings.Contains(lower, ".fra."), strings.Contains(lower, "french"):
		return "fr"
	}
	return ""
}

func errStrIfAny(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
