package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func StreamFile(s *streamer.Streamer, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
			return
		}
		if serveFromCompletedStore(c, store, s, h, fileIdx) {
			return
		}
		serveFromStreamer(c, s, h, fileIdx)
	}
}

// serveFromCompletedStore serves a finished download straight from disk. Both
// row shapes are honored: per-file rows point at the file itself; whole-torrent
// rows point at the torrent's directory, so the requested file is located via
// the cached metainfo rel path (no torrent activation, no swarm dependency).
func serveFromCompletedStore(c *gin.Context, store *downloads.Store, s *streamer.Streamer, h metainfo.Hash, fileIdx int) bool {
	if store == nil {
		return false
	}
	userID, _, _ := auth.UserIDFromCtx(c)
	path, err := store.GetCompletedPathRel(h.HexString(), fileIdx, s.FileRelPath(h, fileIdx), userID)
	if err != nil || path == "" {
		return false
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	// Same stored-XSS guard as /api/local/file: torrent contents are hostile
	// by default and this endpoint serves them same-origin (JWT in
	// localStorage). HTML/SVG/JS are forced to download instead of rendering.
	lh.SetLocalFileSecurityHeaders(c, path)
	http.ServeFile(c.Writer, c.Request, path)
	return true
}

func serveFromStreamer(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, fileIdx int) {
	reader, file, err := s.FileReader(h, fileIdx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = reader.Close() }()
	lh.SetLocalFileSecurityHeaders(c, file.DisplayPath())
	http.ServeContent(c.Writer, c.Request, file.DisplayPath(), time.Time{}, reader)
}

// StreamPlaylistM3U handles GET /api/stream/playlist/:hash/:file.m3u — returns a
// small M3U playlist file that points back to the stream URL. Used by the "VLC"
// button: browsers download the .m3u, the OS opens it in the registered M3U
// handler (VLC on every desktop and iOS/Android with VLC installed).
//
// This is universally portable, unlike vlc:// or vlc-x-callback:// URL schemes
// which only work on subsets of devices and break on desktop VLC entirely.
func StreamPlaylistM3U(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
			return
		}

		info, err := resolveTorrentInfo(s, c.Request.Context(), h)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if fileIdx < 0 || fileIdx >= len(info.Files) {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrFileIdxOutOfRange})
			return
		}

		streamURL := buildStreamURL(c, h, fileIdx)
		title := info.Files[fileIdx].Path
		if title == "" {
			title = info.Name
		}
		m3u := "#EXTM3U\n" +
			"#EXTINF:-1," + title + "\n" +
			streamURL + "\n"

		filename := m3uFilename(info, fileIdx)
		c.Header(HeaderContentDisp, fmt.Sprintf(`attachment; filename="%s.m3u"`, filename))
		c.Data(http.StatusOK, "audio/x-mpegurl", []byte(m3u))
	}
}

// resolveTorrentInfo returns the TorrentInfo for the given hash. If the torrent
// is not currently active, it attempts a best-effort auto-add using a bare
// magnet so that VLC links survive cache evictions and container restarts.
func resolveTorrentInfo(s *streamer.Streamer, ctx context.Context, h metainfo.Hash) (*streamer.TorrentInfo, error) {
	info, err := s.Get(h)
	if err != nil {
		bareMagnet := MagnetPrefix + h.HexString()
		got, addErr := s.Add(ctx, bareMagnet)
		if addErr != nil {
			return nil, err
		}
		info = got
	}
	return info, nil
}

// buildStreamURL builds the absolute stream URL using the same scheme/host the
// client used to reach us, propagating auth token and optional ?transcode param.
func buildStreamURL(c *gin.Context, h metainfo.Hash, fileIdx int) string {
	scheme := "http"
	if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.Request.Host

	token := ""
	if h := c.Request.Header.Get(httpshared.HeaderAuthorization); strings.HasPrefix(h, auth.BearerPrefix) {
		token = strings.TrimPrefix(h, auth.BearerPrefix)
	}
	if token == "" {
		token = c.Query("token")
	}

	path := fmt.Sprintf("/api/stream/%s/%d", h.HexString(), fileIdx)
	if v := c.Query("transcode"); v == "h264" || v == "hevc" {
		path = fmt.Sprintf("/api/stream/transcode/%s/%d?video=%s", h.HexString(), fileIdx, v)
	}

	base := fmt.Sprintf("%s://%s%s", scheme, host, path)
	if token == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "token=" + token
}

// m3uFilename builds a filesystem-safe filename derived from the playing
// file's basename. Example: "Season 1/S01E01 - Pilot.mkv" → "S01E01 - Pilot".
func m3uFilename(info *streamer.TorrentInfo, fileIdx int) string {
	base := info.Files[fileIdx].Path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	safe := strings.NewReplacer(`"`, "", "\r", "", "\n", "", "\\", "").Replace(base)
	if safe == "" {
		safe = "stream"
	}
	return safe
}
