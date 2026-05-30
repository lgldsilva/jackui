package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/parser"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/subtitles"
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

var localProbeCache sync.Map // localProbeKey → streamer.ProbeResult

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
		if v, ok := localProbeCache.Load(key); ok {
			c.JSON(http.StatusOK, v.(streamer.ProbeResult))
			return
		}
		result, ok := runLocalFFProbe(c, abs)
		if !ok {
			return
		}
		localProbeCache.Store(key, result)
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
	cmd := exec.CommandContext(ctx, "ffprobe",
		ffHideBanner, ffLogLevel, "error",
		"-of", "json",
		"-show_streams",
		"-show_format",
		"-i", abs,
	)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "ffprobe: " + err.Error()})
		return streamer.ProbeResult{}, false
	}
	result, perr := parseFFProbeStreams(out)
	if perr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": perr.Error()})
		return streamer.ProbeResult{}, false
	}
	return result, true
}

// parseFFProbeStreams decodes ffprobe's JSON into the same ProbeResult shape
// the torrent path returns (streamer.Probe).
func parseFFProbeStreams(out []byte) (streamer.ProbeResult, error) {
	var parsed struct {
		Streams []struct {
			Index       int               `json:"index"`
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Channels    int               `json:"channels"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
				Forced  int `json:"forced"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return streamer.ProbeResult{}, fmt.Errorf("decode ffprobe: %w", err)
	}
	result := streamer.ProbeResult{
		Audio:     []streamer.Track{},
		Subtitles: []streamer.Track{},
	}
	for _, st := range parsed.Streams {
		var lang, title string
		if st.Tags != nil {
			lang = strings.ToLower(st.Tags["language"])
			title = st.Tags["title"]
		}
		t := streamer.Track{
			Index:    st.Index,
			Codec:    st.CodecName,
			Language: lang,
			Title:    title,
			Channels: st.Channels,
			Default:  st.Disposition.Default == 1,
			Forced:   st.Disposition.Forced == 1,
		}
		switch st.CodecType {
		case "audio":
			t.Type = "audio"
			result.Audio = append(result.Audio, t)
		case "subtitle":
			t.Type = "subtitle"
			// Image-based subs (PGS/VobSub) can't be transcoded to VTT for
			// browsers — flag so the UI can grey them out.
			t.Image = st.CodecName == "hdmv_pgs_subtitle" || st.CodecName == "dvd_subtitle" || st.CodecName == "dvb_subtitle"
			result.Subtitles = append(result.Subtitles, t)
		}
	}
	if parsed.Format.Duration != "" {
		var d float64
		_, _ = fmt.Sscanf(parsed.Format.Duration, "%f", &d)
		result.DurationSec = d
	}
	return result, nil
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

// LocalSubtitleExtract handles GET /api/local/subtrack?mount=&path=&track=
// Extracts an embedded subtitle stream by ABSOLUTE ffprobe stream index and
// converts to WebVTT via ffmpeg. Image-based subs (PGS/VobSub) fail here —
// the frontend should filter them out using the probe response's Image flag.
func LocalSubtitleExtract(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		trackStr := c.Query("track")
		if mount == "" || path == "" || trackStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount, path or track"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		track, err := strconv.Atoi(trackStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track index"})
			return
		}
		abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		// -map 0:s:N selects the Nth subtitle stream relatively; we receive the
		// absolute ffprobe index but ffmpeg's stream specifier prefers relative.
		// Easier: use 0:<absoluteIndex>. ffmpeg accepts both.
		cmd := exec.CommandContext(ctx, "ffmpeg",
			ffHideBanner, ffLogLevel, "error",
			"-i", abs,
			"-map", fmt.Sprintf("0:%d", track),
			"-c:s", "webvtt",
			"-f", "webvtt",
			"-",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error":  "ffmpeg: " + err.Error(),
				"stderr": stderr.String(),
			})
			return
		}
		c.Header(ContentType, MIMEVTT)
		c.Header(CacheControl, "public, max-age=3600")
		_, _ = c.Writer.Write(stdout.Bytes())
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
