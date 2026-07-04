package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
)

const extTorrent = ".torrent"

// maxRedirects caps the redirect chain we follow manually (see ssrfSafeClient).
const maxRedirects = 10

// ssrfSafeClient fetches the user-supplied .torrent URL. The download is capped
// at maxTorrentBytes (shared with import.go) so a hostile URL can't stream
// gigabytes into RAM. The Dialer.Control
// hook runs AFTER DNS resolution with the concrete destination IP, so it blocks
// loopback and link-local / cloud-metadata targets even under DNS rebinding,
// while still allowing the private LAN (Jackett at 192.168.x and public
// trackers keep working).
//
// CheckRedirect returns ErrUseLastResponse so the client NEVER follows a redirect
// on its own: indexer links (Jackett /dl/magnetdownload, /dl/damagnet) answer with
// a 302 to a `magnet:` URI, and the default client would try to dial the `magnet`
// scheme (no host) and fail with a 502. resolveTorrentToMagnet inspects each hop
// instead — capturing a magnet Location, or following http(s) ones manually (still
// through this SSRF-guarded transport).
var ssrfSafeClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || isBlockedFetchIP(ip) {
					return fmt.Errorf("endereço de destino não permitido")
				}
				return nil
			},
		}).DialContext,
	},
}

// isBlockedFetchIP rejects loopback (127.0.0.0/8, ::1), link-local incl. the
// 169.254.169.254 cloud-metadata endpoint (169.254.0.0/16, fe80::/10) and the
// unspecified address. Private LAN ranges stay allowed on purpose.
func isBlockedFetchIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// validateFetchScheme allows only http/https, blocking file://, gopher://, etc.
func validateFetchScheme(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL inválida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("esquema não permitido")
	}
	return nil
}

func ConvertTorrentToMagnet() gin.HandlerFunc {
	return func(c *gin.Context) {
		torrentURL := c.Query("url")
		if torrentURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "URL requerida"})
			return
		}

		res, cerr := resolveTorrentToMagnet(torrentURL)
		if cerr != nil {
			c.JSON(cerr.Code, gin.H{"error": cerr.Message})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"magnet":   res.magnet,
			"infoHash": res.infoHash,
			"name":     res.name,
		})
	}
}

type convertErr struct {
	Code    int
	Message string
}

// torrentResolution is the {magnet, infoHash, name} the converter hands back,
// whether it came from a parsed .torrent body or a captured magnet redirect.
type torrentResolution struct {
	magnet   string
	infoHash string
	name     string
}

// resolveTorrentToMagnet fetches torrentURL, following http(s) redirects manually
// (the client never auto-follows — see ssrfSafeClient). A `magnet:` redirect is
// captured directly; otherwise the final 200 body is parsed as a .torrent.
func resolveTorrentToMagnet(torrentURL string) (*torrentResolution, *convertErr) {
	current := torrentURL
	for hop := 0; hop < maxRedirects; hop++ {
		if err := validateFetchScheme(current); err != nil {
			return nil, &convertErr{http.StatusBadRequest, err.Error()}
		}
		resp, err := ssrfSafeClient.Get(current)
		if err != nil {
			return nil, &convertErr{http.StatusBadGateway, fmt.Sprintf("falha ao baixar .torrent: %v", err)}
		}
		if !isRedirectStatus(resp.StatusCode) {
			return parseTorrentResponse(resp)
		}
		res, next, cerr := followRedirect(resp)
		if cerr != nil {
			return nil, cerr
		}
		if res != nil {
			return res, nil
		}
		current = next
	}
	return nil, &convertErr{http.StatusBadGateway, "muitos redirects ao resolver .torrent"}
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// followRedirect closes resp and returns either a captured magnet resolution
// (Location is a magnet: URI) or the next http(s) URL to fetch.
func followRedirect(resp *http.Response) (*torrentResolution, string, *convertErr) {
	defer func() { _ = resp.Body.Close() }()
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if loc == "" {
		return nil, "", &convertErr{http.StatusBadGateway, "redirect sem cabeçalho Location"}
	}
	if strings.HasPrefix(strings.ToLower(loc), "magnet:") {
		res, cerr := magnetResolution(loc)
		return res, "", cerr
	}
	u, err := resp.Location() // resolves relative redirects against the request URL
	if err != nil {
		return nil, "", &convertErr{http.StatusBadGateway, "Location de redirect inválido"}
	}
	return nil, u.String(), nil
}

// magnetResolution parses a captured magnet: URI into the converter's result.
func magnetResolution(magnetURI string) (*torrentResolution, *convertErr) {
	mi, err := metainfo.ParseMagnetUri(magnetURI)
	if err != nil {
		return nil, &convertErr{http.StatusBadGateway, fmt.Sprintf("magnet do indexador inválido: %v", err)}
	}
	return &torrentResolution{
		magnet:   magnetURI,
		infoHash: mi.InfoHash.HexString(),
		name:     mi.DisplayName,
	}, nil
}

// parseTorrentResponse reads the final response as a .torrent file and builds a
// magnet from its metainfo. It closes resp.
func parseTorrentResponse(resp *http.Response) (*torrentResolution, *convertErr) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &convertErr{http.StatusBadGateway, fmt.Sprintf("servidor retornou erro %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentBytes))
	if err != nil {
		return nil, &convertErr{http.StatusInternalServerError, "falha ao ler bytes do torrent"}
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, &convertErr{http.StatusBadRequest, fmt.Sprintf("falha ao ler metainfo do torrent: %v", err)}
	}
	name := ""
	if info, err := mi.UnmarshalInfo(); err == nil && info.Name != "" {
		name = info.Name
	}
	infoHash := mi.HashInfoBytes().HexString()
	return &torrentResolution{
		magnet:   buildMagnetFromMetainfo(mi, infoHash, name),
		infoHash: infoHash,
		name:     name,
	}, nil
}

func buildMagnetFromMetainfo(mi *metainfo.MetaInfo, infoHash, name string) string {
	magnet := MagnetPrefix + infoHash
	if name != "" {
		magnet += "&dn=" + url.QueryEscape(name)
	}
	for _, group := range mi.AnnounceList {
		for _, tr := range group {
			magnet += "&tr=" + url.QueryEscape(tr)
		}
	}
	if len(mi.AnnounceList) == 0 && mi.Announce != "" {
		magnet += "&tr=" + url.QueryEscape(mi.Announce)
	}
	return magnet
}

func ConvertMagnetToTorrent(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		magnet := c.Query("magnet")
		if magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet link requerido"})
			return
		}
		mi, err := metainfo.ParseMagnetUri(magnet)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet link inválido"})
			return
		}
		h := mi.InfoHash
		if ensureMetainfo(c, s, h, magnet) {
			return
		}
		serveTorrentFile(c, s, h, &mi)
	}
}

func ensureMetainfo(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, magnet string) bool {
	path := s.MetainfoPath(h)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return false
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.Add(ctx, magnet); err != nil {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": fmt.Sprintf("tempo limite atingido aguardando metadados: %v", err)})
		return true
	}
	return false
}

func serveTorrentFile(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, mi *metainfo.Magnet) {
	path := s.MetainfoPath(h)
	f, err := os.Open(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("falha ao ler arquivo .torrent gerado: %v", err)})
		return
	}
	defer func() { _ = f.Close() }()

	filename := resolveTorrentFilename(path, h, mi)
	c.Header(httpshared.ContentType, "application/x-bittorrent")
	c.Header(HeaderContentDisp, fmt.Sprintf("attachment; filename=%s", url.PathEscape(filename)))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, f)
}

func resolveTorrentFilename(path string, h metainfo.Hash, mi *metainfo.Magnet) string {
	if loaded, err := metainfo.LoadFromFile(path); err == nil {
		if info, err := loaded.UnmarshalInfo(); err == nil && info.Name != "" {
			return info.Name + extTorrent
		}
	} else if mi.DisplayName != "" {
		return mi.DisplayName + extTorrent
	}
	return h.HexString() + extTorrent
}
