package handlers

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// hlsSessionKey deve isolar sessões por VARIANTE e por ÁUDIO, mantendo a chave
// single-variant legada (variant<0 && audio<0) idêntica ao pré-Phase-2. A ordem
// é `-v` antes de `-a` (o EffectiveKey ainda anexa -vod/-evt depois).
func TestHlsSessionKeyMatrix(t *testing.T) {
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	base := h.HexString() + "-3"

	cases := []struct {
		variant, audio int
		wantSuffix     string
	}{
		{-1, -1, ""},     // legado single-variant
		{0, -1, "-v0"},   // variante sem áudio escolhido
		{-1, 2, "-a2"},   // áudio sem variante (path legado com ?audio)
		{1, 2, "-v1-a2"}, // ambos, na ordem v depois a
		{2, 0, "-v2-a0"}, // audio 0 é escolha explícita (>=0)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		got := hlsSessionKey(h, 3, c.variant, c.audio)
		want := base + c.wantSuffix
		if got != want {
			t.Errorf("hlsSessionKey(_,3,%d,%d) = %q, want %q", c.variant, c.audio, got, want)
		}
		if !strings.HasPrefix(got, base) {
			t.Errorf("chave %q não começa com %q", got, base)
		}
		if seen[got] {
			t.Errorf("colisão de chave: %q repetida (variantes/áudios distintos devem gerar chaves distintas)", got)
		}
		seen[got] = true
	}
}
