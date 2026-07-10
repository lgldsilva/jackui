package streamer

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

const (
	magnetPrefix           = "magnet:"
	errFileIndexOutOfRange = "file index %d out of range"
)

func (s *Streamer) metainfoPath(h metainfo.Hash) string {
	return filepath.Join(s.metainfoDir, h.HexString()+".torrent")
}

func (s *Streamer) MetainfoPath(h metainfo.Hash) string {
	return s.metainfoPath(h)
}

// ParseMagnet validates a magnet URI and extracts its info hash + display
// name without touching the network. Used by the import flow to preview what
// a pasted magnet resolves to before committing it to favorites.
func (s *Streamer) ParseMagnet(magnet string) (hash, name string, err error) {
	if i := strings.Index(magnet, magnetPrefix); i >= 0 {
		magnet = magnet[i:]
	}
	mi, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return "", "", fmt.Errorf("magnet inválido: %w", err)
	}
	name = mi.DisplayName
	if name == "" {
		name = mi.InfoHash.HexString()
	}
	return mi.InfoHash.HexString(), name, nil
}

// ImportTorrentBytes parses a raw .torrent file, persists its metainfo to the
// cache (so a later play skips the DHT round-trip), and returns the info hash
// + torrent name. Does NOT add the torrent to the active set — the import flow
// only records a favorite; playback adds it on demand.
func (s *Streamer) ImportTorrentBytes(data []byte) (hash, name string, err error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf(".torrent inválido: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", "", fmt.Errorf("metadados do .torrent ilegíveis: %w", err)
	}
	h := mi.HashInfoBytes()
	// Persist so playback is instant (no DHT). Best-effort.
	if s.metainfoDir != "" {
		path := s.metainfoPath(h)
		if f, ferr := os.CreateTemp(s.metainfoDir, ".tmp-*.torrent"); ferr == nil {
			if werr := mi.Write(f); werr == nil {
				_ = f.Close()
				_ = os.Rename(f.Name(), path)
			} else {
				_ = f.Close()
				_ = os.Remove(f.Name())
			}
		}
	}
	name = info.Name
	if name == "" {
		name = h.HexString()
	}
	return h.HexString(), name, nil
}

// loadCachedMetainfo reads a persisted .torrent for the given hash.
// Returns nil if absent or unreadable — caller falls back to magnet flow.
func (s *Streamer) loadCachedMetainfo(h metainfo.Hash) *metainfo.MetaInfo {
	if s.metainfoDir == "" {
		return nil
	}
	mi, err := metainfo.LoadFromFile(s.metainfoPath(h))
	if err != nil {
		return nil
	}
	return mi
}

// persistMetainfo writes the torrent's metainfo to disk so the next cold
// open skips DHT. Best-effort — errors are logged but don't fail the Add.
func (s *Streamer) persistMetainfo(t *torrent.Torrent) {
	if s.metainfoDir == "" || t == nil {
		return
	}
	mi := t.Metainfo()
	path := s.metainfoPath(t.InfoHash())
	// Write to tmp + rename for atomicity (avoid leaving a half-written
	// file that loadCachedMetainfo would treat as garbage). Disk-full / perms
	// failures were silently swallowed — the .torrent cache never built and
	// future plays fell back to slow DHT with no log to explain why; surface it.
	short := t.InfoHash().HexString()[:8]
	f, err := os.CreateTemp(s.metainfoDir, ".tmp-*.torrent")
	if err != nil {
		log.Printf("streamer: persist metainfo (create temp) failed for %s: %v", short, err)
		return
	}
	if err := mi.Write(f); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (write) failed for %s: %v", short, err)
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (close) failed for %s: %v", short, err)
		return
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (rename) failed for %s: %v", short, err)
	}
}

// DropSeed para de auto-seedar um torrent de vez: remove o registro PERSISTENTE
// (.seeds.db) para que ele NÃO volte no próximo boot (resumeSeeding) e o dropa
// da memória. Usar nas ações EXPLÍCITAS do usuário (parar de seedar / remover
// torrent / excluir download) — ao contrário do Drop genérico (idle/health),
// que preserva o auto-seed. Sem isto, um torrent auto-seedado reaparecia como
// "ativo" para sempre, mesmo após ser removido.
func (s *Streamer) DropSeed(hash metainfo.Hash) {
	if s.seeds != nil {
		if err := s.seeds.Remove(hash.HexString()); err != nil {
			log.Printf("streamer: remove persisted seed %s failed: %v", hash.HexString()[:8], err)
		}
	}
	s.Drop(hash)
}

func (s *Streamer) maybePersistSeed(t *torrent.Torrent) {
	if s.seeds == nil || t == nil {
		return
	}
	s.mu.Lock()
	keep := s.shouldKeepSeeding(t)
	s.mu.Unlock()
	if !keep {
		return
	}
	if err := s.seeds.Add(t.InfoHash().HexString(), magnetFromTorrent(t), t.Name()); err != nil {
		log.Printf("streamer: persist seed %s failed: %v", t.InfoHash().HexString()[:8], err)
	}
}

// magnetFromTorrent reconstructs a magnet URI (info_hash + full announce list,
// passkeys included) good enough to re-add the torrent for seeding on boot.
func magnetFromTorrent(t *torrent.Torrent) string {
	m := metainfo.Magnet{InfoHash: t.InfoHash(), DisplayName: t.Name()}
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		m.Trackers = append(m.Trackers, tier...)
	}
	return m.String()
}
