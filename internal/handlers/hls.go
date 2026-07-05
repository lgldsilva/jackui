package handlers

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

type hlsCtx struct {
	c       *gin.Context
	s       *streamer.Streamer
	mgr     *transcode.HLSSessionManager
	store   *downloads.Store
	h       metainfo.Hash
	fileIdx int
}

// mediaSegQuery builds the query string appended to each segment URL. It
// carries the token (so <video> can authenticate) and, when set, the native_hls
// flag — the segment request must resolve to the SAME session key the master
// created (see HLSSessionManager.EffectiveKey), so both sides need the flag.
// With only a token the output is identical to the previous `?token=X`.
func mediaSegQuery(token string, nativeHLS bool) string {
	q := ""
	if token != "" {
		q = "?token=" + token
	}
	if nativeHLS {
		if q == "" {
			q = "?native_hls=1"
		} else {
			q += "&native_hls=1"
		}
	}
	return q
}

// withSegAudio anexa `audio=<n>` a cada linha de SEGMENTO da playlist quando o
// cliente escolheu uma faixa de áudio. Assim as requisições de segmento carregam
// a faixa e batem na MESMA sessão (keyed por áudio) que o master — senão o
// segmento cairia na sessão default. Feito por pós-processamento pra não alterar
// as assinaturas (testadas) de mediaSegQuery/buildVODPlaylist.
func withSegAudio(data []byte, audio string) []byte {
	if audio == "" {
		return data
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		sep := "?"
		if strings.Contains(trim, "?") {
			sep = "&"
		}
		lines[i] = trim + sep + "audio=" + audio
	}
	return []byte(strings.Join(lines, "\n"))
}

// buildVODPlaylist synthesises a finite HLS playlist covering the whole media
// duration: every segment is declared up front (with a token on each line) and
// EXT-X-ENDLIST marks it complete, so Safari renders a full seekbar instead of
// treating the stream as headless LIVE. Segments the encoder hasn't produced
// yet are generated on demand (seek-restart) when the player requests them.
func buildVODPlaylist(durationSec float64, token string, nativeHLS bool) []byte {
	n := int(math.Ceil(durationSec / httpshared.HLSVODSegDur))
	if n < 1 {
		n = 1
	}
	q := mediaSegQuery(token, nativeHLS)
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	// TARGETDURATION must be >= the longest EXTINF; segments are ~4s but allow
	// slack for the trailing partial segment and minor keyframe rounding.
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", httpshared.HLSVODSegDur+1)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		d := float64(httpshared.HLSVODSegDur)
		if i == n-1 {
			if last := durationSec - float64(i*httpshared.HLSVODSegDur); last > 0 && last < d {
				d = last
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", d)
		fmt.Fprintf(&b, "seg_%05d.ts%s\n", i, q)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

func StreamHLSMaster(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		hc := &hlsCtx{c: c, s: s, mgr: mgr, store: store, h: h, fileIdx: fileIdx}
		transcodeSource, transcodeSourceSize, complete := resolveTranscodeSource(hc)
		if transcodeSource == nil {
			return
		}
		sess, err := startHLSSession(hc, transcodeSource, transcodeSourceSize, complete)
		if err != nil {
			return
		}
		if !waitForMasterPlaylist(hc, sess) {
			return
		}
		serveHLSPlaylist(c, sess)
	}
}

// hlsSessionKey separa sessões HLS por faixa de áudio escolhida. Sem isso, trocar
// o áudio reusava a sessão/transcode em cache (com a faixa antiga) → a escolha não
// surtia efeito. Master e segmentos DEVEM derivar a mesma chave (o segmento lê
// ?audio= da própria URL, injetada por withSegAudio).
func hlsSessionKey(h metainfo.Hash, fileIdx, audioTrack int) string {
	k := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
	if audioTrack >= 0 {
		k += fmt.Sprintf("-a%d", audioTrack)
	}
	return k
}

func startHLSSession(hc *hlsCtx, source io.ReadSeekCloser, sourceSize int64, complete bool) (*transcode.HLSSession, error) {
	audioTrack := httpshared.ParseIntOr(hc.c.Query("audio"), -1)
	sess, err := hc.mgr.GetOrStart(hc.c.Request.Context(), transcode.HLSStartOpts{
		Key:        hlsSessionKey(hc.h, hc.fileIdx, audioTrack),
		Source:     source,
		SourceSize: sourceSize,
		NativeHLS:  httpshared.NativeHLSParam(hc.c),
		// A fully-downloaded torrent (served from the completed path on disk) is
		// complete & seekable — same VOD case as a local file. An in-progress
		// stream stays under the global vodMode (#61 Safari seek guard).
		ForceVOD:   complete,
		AudioTrack: audioTrack,
	})
	if err != nil {
		hc.c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, err
	}
	return sess, nil
}

func serveHLSPlaylist(c *gin.Context, sess *transcode.HLSSession) {
	if sess.IsVOD() {
		c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
		c.Data(http.StatusOK, httpshared.MIMEMPEGURL,
			withSegAudio(buildVODPlaylist(sess.DurationSec, c.Query("token"), httpshared.NativeHLSParam(c)), c.Query("audio")))
		return
	}
	data := readEventPlaylist(c, sess)
	if data == nil {
		return
	}
	c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
	c.Data(http.StatusOK, httpshared.MIMEMPEGURL, data)
}

func readEventPlaylist(c *gin.Context, sess *transcode.HLSSession) []byte {
	data, err := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "playlist not readable"})
		return nil
	}
	if q := mediaSegQuery(c.Query("token"), httpshared.NativeHLSParam(c)); q != "" {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			lines[i] = trim + q
		}
		data = []byte(strings.Join(lines, "\n"))
	}
	return withSegAudio(data, c.Query("audio"))
}

func StreamHLSSegment(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		segName := c.Param("seg")
		sess := resolveHLSSession(c, s, mgr, store, h, fileIdx, segName)
		if sess == nil {
			return
		}
		httpshared.EnsureVODSegment(sess, segName)
		httpshared.ServeSegment(c, sess, segName)
	}
}

// resolveHLSSession busca a sessão ativa; se ela sumiu (reapada/fechada),
// RESSUSCITA-A a partir do segmento pedido em vez de retornar 404. Sem isso, o
// Safari (VOD, playlist estática) responde ao 404 percorrendo a playlist INTEIRA
// em 404 — um burst de centenas de requisições — antes de refetchar a playlist.
// Respawnar no servidor torna a recuperação transparente: o segmento pedido é
// gerado e servido (200) na própria requisição.
func resolveHLSSession(c *gin.Context, s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store, h metainfo.Hash, fileIdx int, segName string) *transcode.HLSSession {
	// EffectiveKey must match the one the master used — hence native_hls is
	// carried on every segment URL (see mediaSegQuery).
	key := mgr.EffectiveKey(hlsSessionKey(h, fileIdx, httpshared.ParseIntOr(c.Query("audio"), -1)), httpshared.NativeHLSParam(c))
	if sess, err := getSession(mgr, key); err == nil {
		return sess
	}
	// Sem streamer (caminho degradado/teste) não há como respawnar → 404 e o
	// cliente refetcha a playlist.
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
		return nil
	}
	// Sessão ausente → respawn. resolveTranscodeSource resolve do store ou do
	// torrent (e já responde 404 se a fonte sumiu de vez).
	hc := &hlsCtx{c: c, s: s, mgr: mgr, store: store, h: h, fileIdx: fileIdx}
	source, size, complete := resolveTranscodeSource(hc)
	if source == nil {
		return nil
	}
	sess, err := startHLSSession(hc, source, size, complete)
	if err != nil {
		return nil
	}
	// O respawn começa no segmento 0; reposiciona o encoder no segmento pedido
	// pra não obrigar o player a esperar o transcode chegar lá sequencialmente.
	if idx, ok := transcode.ParseSegIndex(segName); ok && idx > 0 && sess.IsVOD() {
		_ = sess.RestartAt(idx)
	}
	return sess
}

// getSession is a small helper to look up an existing session without
// going through the start path. Avoids creating a duplicate ffmpeg if the
// client races and hits the segment handler before the playlist handler.
func getSession(mgr *transcode.HLSSessionManager, key string) (*transcode.HLSSession, error) {
	// Manager exposes only GetOrStart; we cheat by passing a nil-ish opts
	// but it'll dedupe on Key. The downside is theoretical: if the session
	// was reaped and the segment request arrived first, we'd start a new
	// ffmpeg without a Source. Mitigate by requiring Source non-nil there.
	// For simplicity, return an error here when the session isn't already
	// tracked — clients refetch the playlist which respawns properly.
	return mgr.Peek(key)
}

// resolveTranscodeSource tries the completed-download store first, then falls
// back to activating the torrent and opening a streaming reader. It returns the
// seekable input plus its size and whether it is a COMPLETE on-disk file (a
// finished download served from the completed path) — the caller forces VOD for
// complete sources. The in-progress streaming path returns complete=false (the
// #61 Safari seek guard).
func resolveTranscodeSource(hc *hlsCtx) (io.ReadSeekCloser, int64, bool) {
	if f, size, ok := openCompletedFile(hc); ok {
		return f, size, true
	}
	if _, err := hc.s.Get(hc.h); err != nil {
		bareMagnet := MagnetPrefix + hc.h.HexString()
		if _, addErr := hc.s.Add(hc.c.Request.Context(), bareMagnet); addErr != nil {
			hc.c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return nil, 0, false
		}
	}
	reader, file, err := hc.s.FileReader(hc.h, hc.fileIdx)
	if err != nil {
		hc.c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, 0, false
	}
	return reader, file.Length(), false
}

// openCompletedFile resolves a finished download (per-file or whole-torrent
// row) to its on-disk file, so completed items play from disk instead of
// re-downloading from the swarm.
func openCompletedFile(hc *hlsCtx) (io.ReadSeekCloser, int64, bool) {
	if hc.store == nil {
		return nil, 0, false
	}
	relPath := hc.s.FileRelPath(hc.h, hc.fileIdx)
	path, err := hc.store.GetCompletedPathRel(hc.h.HexString(), hc.fileIdx, relPath)
	if err != nil || path == "" {
		return nil, 0, false
	}
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return nil, 0, false
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	return f, stat.Size(), true
}

// waitForMasterPlaylist blocks until the first HLS segment is ready. On failure
// it classifies the reason (no_seeds, slow_download) and responds with 503.
func waitForMasterPlaylist(hc *hlsCtx, sess *transcode.HLSSession) bool {
	if err := sess.WaitForMaster(2 * time.Minute); err != nil {
		resp := gin.H{"error": err.Error(), "code": "transcode_failed"}
		if info, gerr := hc.s.Get(hc.h); gerr == nil {
			resp["downRate"] = info.DownRate
			resp["peers"] = info.Peers
			if hc.fileIdx >= 0 && hc.fileIdx < len(info.Files) {
				resp["fileProgress"] = info.Files[hc.fileIdx].Progress
				downloaded := info.Files[hc.fileIdx].Downloaded
				switch {
				case info.Peers == 0:
					resp["code"] = "no_seeds"
				case downloaded < 30<<20:
					resp["code"] = "slow_download"
				}
			}
		}
		hc.c.JSON(http.StatusServiceUnavailable, resp)
		return false
	}
	return true
}
