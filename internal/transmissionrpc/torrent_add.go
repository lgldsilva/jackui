package transmissionrpc

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/downloads"
)

// ─── torrent-add ───────────────────────────────────────────────────────────

type torrentAddArgs struct {
	filename          string
	metainfoB64       string
	downloadDir       string
	paused            bool
	peerLimit         float64
	bandwidthPriority float64
	labels            []string
}

func (h *Handler) methodTorrentAdd(args map[string]interface{}, userID int) rpcResponse {
	ta := parseTorrentAddArgs(args)

	if ta.filename == "" && ta.metainfoB64 == "" {
		return failResp("missing 'filename' or 'metainfo' argument")
	}

	var infoHash, magnet, name string

	if ta.metainfoB64 != "" {
		var err error
		infoHash, name, magnet, err = h.addTorrentMetainfo(ta.metainfoB64)
		if err != nil {
			return failResp(err.Error())
		}
	} else {
		var err error
		infoHash, magnet, err = h.addTorrentFilename(ta.filename)
		if err != nil {
			return failResp(err.Error())
		}
	}

	return h.finalizeTorrentAdd(userID, infoHash, name, magnet, ta)
}

func parseTorrentAddArgs(args map[string]interface{}) torrentAddArgs {
	return torrentAddArgs{
		filename:          argString(args, "filename"),
		metainfoB64:       argString(args, "metainfo"),
		downloadDir:       argString(args, "download-dir"),
		paused:            argBool(args, "paused"),
		peerLimit:         argFloat(args, "peer-limit"),
		bandwidthPriority: argFloat(args, "bandwidth-priority"),
		labels:            argStringSlice(args, "labels"),
	}
}

// argStringSlice reads a JSON array of strings (e.g. Transmission "labels").
func argStringSlice(args map[string]interface{}, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func argString(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

func argBool(args map[string]interface{}, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func argFloat(args map[string]interface{}, key string) float64 {
	f, _ := args[key].(float64)
	return f
}

// resolveCategory derives the download's category: an explicit client label
// (e.g. *arr sends "labels":["Movies"]) wins over the path-based guess.
func (h *Handler) resolveCategory(downloadDir string, labels []string) string {
	if len(labels) > 0 && labels[0] != "" {
		return labels[0]
	}
	return extractCategory(downloadDir)
}

func (h *Handler) finalizeTorrentAdd(userID int, infoHash, name, magnet string, ta torrentAddArgs) rpcResponse {
	category := h.resolveCategory(ta.downloadDir, ta.labels)
	shortHash := infoHash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	if name == "" {
		name = shortHash + "..."
	}
	// FileIndexWholeTorrent (NOT FileIndexAuto): an *arr client (Sonarr/Radarr)
	// adding via RPC expects Transmission semantics — the ENTIRE release on disk
	// so it can import every file. A season pack or multi-file release would
	// import broken if we only fetched the single "best file" (pickBestFile).
	// The JackUI UI keeps single-file/streaming behavior via its own create path
	// (handlers/downloads.go), which passes an explicit FileIndex.
	d, err := h.store.Create(downloads.Download{
		UserID: userID, InfoHash: infoHash, FileIndex: downloads.FileIndexWholeTorrent,
		Name: name, Magnet: magnet, Category: category, Source: downloads.SourceArr,
	})
	if err != nil {
		return failResp(fmt.Sprintf("failed to create download: %v", err))
	}
	if ta.paused {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
	}
	h.applyAddPeerLimit(*d, ta.peerLimit)
	h.applyAddPriority(*d, ta.bandwidthPriority)
	return successResp(map[string]interface{}{
		"torrent-added": map[string]interface{}{
			"id": d.ID, "hashString": infoHash, "name": d.Name, "downloadDir": ta.downloadDir,
		},
	})
}

// addTorrentMetainfo processa um .torrent em base64 e retorna infoHash, nome, magnet.
func (h *Handler) addTorrentMetainfo(b64 string) (infoHash, name, magnet string, err error) {
	data, decodeErr := base64.StdEncoding.DecodeString(b64)
	if decodeErr != nil {
		return "", "", "", fmt.Errorf("invalid base64 metainfo")
	}
	if h.streamer != nil {
		hash, n, ierr := h.streamer.ImportTorrentBytes(data)
		if ierr != nil {
			return "", "", "", fmt.Errorf("failed to parse metainfo: %v", ierr)
		}
		infoHash = hash
		name = n
	} else {
		mi, loadErr := metainfo.Load(bytes.NewReader(data))
		if loadErr != nil {
			return "", "", "", fmt.Errorf("invalid torrent metainfo")
		}
		infoHash = mi.HashInfoBytes().HexString()
	}
	magnet = magnetPrefix + infoHash
	return infoHash, name, magnet, nil
}

// addTorrentFilename processa um filename (magnet, URL, ou infoHash) e retorna infoHash, magnet.
func (h *Handler) addTorrentFilename(filename string) (infoHash, magnet string, err error) {
	magnet = filename
	infoHash = extractInfoHash(filename)
	if infoHash == "" {
		lower := strings.ToLower(filename)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			hash, fetchErr := fetchTorrentHash(filename)
			if fetchErr != nil {
				return "", "", fmt.Errorf("failed to fetch torrent: %v", fetchErr)
			}
			infoHash = hash
		} else {
			return "", "", fmt.Errorf(valUnsupportedFilename)
		}
	} else if !strings.HasPrefix(strings.ToLower(filename), "magnet:") {
		magnet = magnetPrefix + infoHash
	}
	return infoHash, magnet, nil
}

// applyAddPeerLimit aplica peer-limit ao torrent se o streamer estiver ativo.
func (h *Handler) applyAddPeerLimit(d downloads.Download, limit float64) {
	if limit <= 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		if t, ok := h.streamer.Client().Torrent(hh); ok {
			t.SetMaxEstablishedConns(int(limit))
		}
	}
}

// applyAddPriority maps Transmission's bandwidth-priority to both the streamer's
// piece priority (if active) and the download's queue priority column, so a
// torrent added via a *arr client lands in the right queue tier.
func (h *Handler) applyAddPriority(d downloads.Download, priority float64) {
	if priority == 0 {
		return
	}
	label := "normal"
	switch int(priority) {
	case -1:
		label = downloads.PriorityLow
	case 1:
		label = downloads.PriorityHigh
	}
	_ = h.store.SetPriority(d.UserID, d.ID, label)
	if h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		_ = h.streamer.SetPriority(hh, label)
	}
}

// blockInternalIP recusa conexões a IPs internos/loopback/link-local. Roda no
// Control do dialer, DEPOIS do DNS resolver, então também barra DNS-rebinding
// (um host que resolve p/ 127.0.0.1 / 169.254.169.254 / 10.x etc.).
func blockInternalIP(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("acesso bloqueado a IP interno: %s", host)
	}
	return nil
}

// fetchTorrentHash downloads a .torrent file from a URL and returns its
// infoHash. Uses a short timeout (30s) so the RPC handler doesn't block long.
// Bloqueia IPs internos (SSRF): a URL vem do cliente RPC (*arr).
func fetchTorrentHash(url string) (string, error) {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("esquema de URL não suportado")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalIP}).DialContext,
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	mi, err := metainfo.Load(io.LimitReader(resp.Body, maxTorrentFileBytes))
	if err != nil {
		return "", fmt.Errorf("parse torrent: %w", err)
	}

	hash := mi.HashInfoBytes().HexString()
	if hash == "" {
		return "", fmt.Errorf("empty infoHash from torrent")
	}
	return strings.ToLower(hash), nil
}

func extractInfoHash(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(strings.ToLower(s), "magnet:") {
		return infoHashFromMagnet(s)
	}
	if len(s) == 40 {
		return validHexHash(s)
	}
	return ""
}

func infoHashFromMagnet(s string) string {
	query := s
	if idx := strings.Index(s, "?"); idx >= 0 {
		query = s[idx+1:]
	}
	for _, p := range strings.Split(query, "&") {
		if strings.HasPrefix(strings.ToLower(p), "xt=urn:btih:") {
			return strings.ToLower(strings.TrimPrefix(p, "xt=urn:btih:"))
		}
	}
	return ""
}

// validHexHash returns the lowercased hash if it's 40 hex chars, else "".
func validHexHash(s string) string {
	lower := strings.ToLower(s)
	for _, c := range lower {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return lower
}

func extractCategory(downloadDir string) string {
	if downloadDir == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(downloadDir, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return parts[len(parts)-1]
}
