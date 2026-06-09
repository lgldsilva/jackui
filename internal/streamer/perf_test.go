package streamer

import (
	"testing"

	"github.com/anacrolix/torrent"
)

// ListenPort expõe a porta de peer BitTorrent (usada pelo session-get do RPC).
func TestListenPortGetter(t *testing.T) {
	s := &Streamer{cfg: Config{ListenPort: 51470}}
	if got := s.ListenPort(); got != 51470 {
		t.Errorf("ListenPort() = %d, want 51470", got)
	}
}

func TestStreamReadaheadDefaultAndSetter(t *testing.T) {
	s := NewForTesting()
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("readahead default = %d, queria %d", got, streamReadaheadDefault)
	}
	s.SetStreamReadahead(8)
	if got := s.streamReadahead(); got != 8<<20 {
		t.Errorf("após Set(8) = %d, queria %d", got, 8<<20)
	}
	// 0/negativo volta ao default.
	s.SetStreamReadahead(0)
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("após Set(0) = %d, queria default %d", got, streamReadaheadDefault)
	}
	s.SetStreamReadahead(-5)
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("após Set(-5) = %d, queria default", got)
	}
}

func TestApplyPeerTuning(t *testing.T) {
	// >0 sobrescreve; 0 preserva o default da lib.
	base := torrent.NewDefaultClientConfig()
	defConns := base.EstablishedConnsPerTorrent
	defHashers := base.PieceHashersPerTorrent

	tcfg := torrent.NewDefaultClientConfig()
	applyPeerTuning(tcfg, Config{MaxConnsPerTorrent: 120, HalfOpenConns: 40, PeersHighWater: 900, PieceHashers: 6})
	if tcfg.EstablishedConnsPerTorrent != 120 {
		t.Errorf("conns = %d, queria 120", tcfg.EstablishedConnsPerTorrent)
	}
	if tcfg.HalfOpenConnsPerTorrent != 40 {
		t.Errorf("halfOpen = %d, queria 40", tcfg.HalfOpenConnsPerTorrent)
	}
	if tcfg.TorrentPeersHighWater != 900 {
		t.Errorf("peersHighWater = %d, queria 900", tcfg.TorrentPeersHighWater)
	}
	if tcfg.PieceHashersPerTorrent != 6 {
		t.Errorf("pieceHashers = %d, queria 6", tcfg.PieceHashersPerTorrent)
	}
	_ = defHashers

	// Tudo-zero não muda nada.
	tcfg2 := torrent.NewDefaultClientConfig()
	applyPeerTuning(tcfg2, Config{})
	if tcfg2.EstablishedConnsPerTorrent != defConns {
		t.Errorf("conns mexido com config zero: %d, queria %d", tcfg2.EstablishedConnsPerTorrent, defConns)
	}
}

// New com backend mmap cria o client, registra o storageImpl e Close o fecha
// sem panic. Valida o caminho de storage configurável de ponta a ponta.
func TestNewWithMmapStorageClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, StorageBackend: "mmap", ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New(mmap): %v", err)
	}
	if s.storageImpl == nil {
		t.Fatal("backend mmap deveria ter setado storageImpl não-nil")
	}
	s.Close() // deve fechar o mmap sem panic
}

// New com backend file (default) NÃO seta storageImpl — usa o FileStorage gerido
// pelo client.
func TestNewWithFileStorageHasNoExplicitImpl(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, StorageBackend: "file", ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New(file): %v", err)
	}
	defer s.Close()
	if s.storageImpl != nil {
		t.Error("backend file não deveria setar storageImpl explícito")
	}
}

// Readahead configurado no New é refletido por streamReadahead.
func TestNewAppliesConfiguredReadahead(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, Readahead: 16 << 20, ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	// Via o acessor exportado (mesmo caminho que outros pacotes usam).
	if got := s.StreamReadaheadForTesting(); got != 16<<20 {
		t.Errorf("readahead = %d, queria %d", got, 16<<20)
	}
}
