package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/httpretry"
)

// Add/resolve de torrents (magnet/.torrent/cache) — extraído de streamer.go.
// Add loads a magnet OR an HTTP(S) URL to a .torrent file and waits for metadata.
// Returns the torrent info once available.
//
// For .torrent URLs (common in private trackers and some Jackett providers that
// don't return a magnet), we fetch the file, parse the metainfo, and add via
// AddTorrentSpec — same downstream behavior as magnet.
func (s *Streamer) Add(ctx context.Context, magnetOrURL string) (*TorrentInfo, error) {
	return s.addWithStorage(ctx, magnetOrURL, nil)
}

// AddForDownload is Add but writes the torrent's data DIRECTLY to its final
// destination on bulk storage (ds.BaseDir/<sanitize(name)>/...) instead of the
// SSD piece cache. Used by the downloads worker so torrents larger than the
// cache don't overflow it and the move-on-completion becomes a no-op. The
// streaming path (Add/EnsureActive) is unaffected — it passes ds=nil.
func (s *Streamer) AddForDownload(ctx context.Context, magnetOrURL string, ds DownloadStorageSpec) (*TorrentInfo, error) {
	return s.addWithStorage(ctx, magnetOrURL, &ds)
}

func (s *Streamer) addWithStorage(ctx context.Context, magnetOrURL string, ds *DownloadStorageSpec) (*TorrentInfo, error) {
	src := cleanSource(magnetOrURL)
	t, err := s.resolveSource(ctx, src, ds)
	if err != nil {
		return nil, err
	}
	t.AddTrackers(publicTrackers)
	if err := waitForMetadata(ctx, t, s.cfg.MetadataWait); err != nil {
		return nil, err
	}
	return s.registerTorrent(t), nil
}

func cleanSource(magnetOrURL string) string {
	src := strings.TrimSpace(magnetOrURL)
	src = strings.TrimPrefix(src, "\xef\xbb\xbf")
	return src
}

func (s *Streamer) resolveSource(ctx context.Context, src string, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	lower := strings.ToLower(src[:min(16, len(src))])
	switch {
	case isMagnet(lower, src):
		return s.resolveMagnet(src, ds)
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		return s.addFromTorrentURL(ctx, src, ds)
	default:
		return nil, fmt.Errorf("unsupported source — provide a magnet: or http(s):// URL (got %q)", firstChars(src, 30))
	}
}

func isMagnet(lower, src string) bool {
	if strings.HasPrefix(lower, magnetPrefix) || strings.Contains(lower, magnetPrefix) {
		return true
	}
	return false
}

func (s *Streamer) resolveMagnet(src string, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	if i := strings.Index(src, magnetPrefix); i >= 0 {
		src = src[i:]
	}
	if mi, err := metainfo.ParseMagnetUri(src); err == nil {
		if cached := s.loadCachedMetainfo(mi.InfoHash); cached != nil {
			return s.addCachedMetainfo(cached, mi.InfoHash, ds)
		}
	}
	return s.addMagnet(src, ds)
}

// addMagnet adds a magnet URI. With ds set (download-to-bulk), it builds a spec
// so the per-torrent bulk storage can be attached; otherwise it uses the plain
// AddMagnet path (default cache storage), unchanged.
func (s *Streamer) addMagnet(src string, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	if s.client == nil {
		return nil, errors.New("torrent client unavailable")
	}
	if ds == nil {
		t, err := s.client.AddMagnet(src)
		if err != nil {
			return nil, fmt.Errorf("add magnet: %w", err)
		}
		return t, nil
	}
	spec, err := torrent.TorrentSpecFromMagnetUri(src)
	if err != nil {
		return nil, fmt.Errorf("parse magnet: %w", err)
	}
	spec.Storage = s.downloadStorage(*ds)
	t, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("add magnet: %w", err)
	}
	return t, nil
}

// addCachedMetainfo adds a torrent from its cached .torrent file. When the
// download's files have been relocated out of DataDir (e.g. moved to bulk on
// completion), it attaches a per-torrent storage rooted at the real location so
// anacrolix verifies + SEEDS in place instead of re-downloading into the cache.
// Falls back to the default storage otherwise.
func (s *Streamer) addCachedMetainfo(cached *metainfo.MetaInfo, hash metainfo.Hash, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	spec := torrent.TorrentSpecFromMetaInfo(cached)
	switch {
	case ds != nil:
		spec.Storage = s.downloadStorage(*ds)
	default:
		if info, err := cached.UnmarshalInfo(); err == nil {
			if st := s.relocatedStorage(&info, hash); st != nil {
				spec.Storage = st
				log.Printf("streamer: seeding %s from relocated storage (file outside cache)", hash.HexString()[:8])
			}
		}
	}
	t, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("add cached metainfo: %w", err)
	}
	return t, nil
}

func waitForMetadata(ctx context.Context, t *torrent.Torrent, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-t.GotInfo():
		return nil
	case <-waitCtx.Done():
		return fmt.Errorf("timeout aguardando metadados do torrent (%s)", timeout)
	}
}

func (s *Streamer) registerTorrent(t *torrent.Torrent) *TorrentInfo {
	now := time.Now()
	s.mu.Lock()
	e, ok := s.active[t.InfoHash()]
	if ok {
		// Already active — REUSE the entry. A re-Add of an active torrent
		// (prefetching another file of the SAME torrent, a health probe, VLC
		// resolving info) must NOT discard the live viewer lease / pause /
		// priority state. Overwriting it reset viewers→0, so the next
		// ReleaseViewer scheduled a drop while playback was still going.
		e.t = t
		e.lastAccess = now
	} else {
		e = &entry{t: t, lastAccess: now, lastSampleAt: now}
		s.active[t.InfoHash()] = e
	}
	s.mu.Unlock()
	s.persistMetainfo(t)
	s.maybePersistSeed(t)
	info := s.buildInfo(e, true)
	if s.cache != nil {
		_ = s.cache.Set(info)
	}
	return info
}

func (s *Streamer) injectJackettAPIKey(torrentURL string) string {
	if s.cfg.JackettHost == "" {
		return torrentURL
	}
	u, err := url.Parse(torrentURL)
	if err != nil || u.Hostname() != s.cfg.JackettHost {
		return torrentURL
	}
	if s.cfg.JackettAPIKey == "" || u.Query().Get("apikey") != "" {
		return torrentURL
	}
	q := u.Query()
	q.Set("apikey", s.cfg.JackettAPIKey)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Streamer) addFromCapturedMagnet(magnet string, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	t, err := s.addMagnet(magnet, ds)
	if err != nil {
		return nil, fmt.Errorf("add magnet from redirect: %w", err)
	}
	return t, nil
}

func (s *Streamer) addFromTorrentResponse(resp *http.Response, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(".torrent URL returned %d", resp.StatusCode)
	}
	mi, err := metainfo.Load(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("parse .torrent: %w", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, fmt.Errorf("metainfo spec: %w", err)
	}
	if ds != nil {
		spec.Storage = s.downloadStorage(*ds)
	}
	t, _, err := s.client.AddTorrentSpec(spec)
	return t, err
}

// addFromTorrentURL handles a HTTP(S) URL that may either:
//   - Serve a .torrent file directly (binary bencoded body)
//   - Respond with a 301/302 redirect pointing to a magnet: URI
//
// The second case is what Jackett does for many providers (e.g., torrentdownload):
// the `/dl/...` endpoint redirects to `magnet:?xt=urn:btih:...`. The default Go
// http.Client follows the redirect and chokes on the magnet scheme.
//
// We detect the magnet redirect via CheckRedirect, capture the magnet URL, and
// add via AddMagnet instead of trying to fetch.
func (s *Streamer) addFromTorrentURL(ctx context.Context, torrentURL string, ds *DownloadStorageSpec) (*torrent.Torrent, error) {
	var capturedMagnet string

	torrentURL = s.injectJackettAPIKey(torrentURL)
	httpClient := newSSRFGuardedClient(s.cfg.JackettHost, &capturedMagnet)

	req, err := http.NewRequestWithContext(ctx, "GET", torrentURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Retry transient failures AND 404: an indexer that just published a release
	// often 404s the .torrent link for a few seconds before it propagates — the
	// "torrent URL returned 404" the user hit on a fresh card.
	resp, err := httpretry.Do(ctx, httpClient, req, httpretry.Policy{
		RetryOn: httpretry.RetryOnStatuses(http.StatusNotFound),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch torrent URL: %w", err)
	}
	defer resp.Body.Close()

	if capturedMagnet != "" {
		return s.addFromCapturedMagnet(capturedMagnet, ds)
	}
	return s.addFromTorrentResponse(resp, ds)
}
