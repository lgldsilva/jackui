package downloads

import "testing"

func mkDownloadWithSource(t *testing.T) (*Store, int) {
	t.Helper()
	s := newTestStore(t)
	d, err := s.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "magnet:orig", Name: "Orig"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s, d.ID
}

func TestEnsureSource_IdempotentRefreshesSeeders(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	if err := s.EnsureSource(Source{DownloadID: id, Magnet: "m1", InfoHash: "h1", Tracker: "T", Seeders: 5}, SourceCandidate); err != nil {
		t.Fatalf("EnsureSource: %v", err)
	}
	// Same info_hash again with new seeder count → updates seeders, no duplicate.
	if err := s.EnsureSource(Source{DownloadID: id, Magnet: "m1", InfoHash: "h1", Tracker: "T", Seeders: 42}, SourceActive); err != nil {
		t.Fatalf("EnsureSource#2: %v", err)
	}
	list, _ := s.ListSources(id)
	if len(list) != 1 {
		t.Fatalf("expected 1 source (idempotent), got %d", len(list))
	}
	if list[0].Seeders != 42 {
		t.Errorf("seeders should refresh to 42, got %d", list[0].Seeders)
	}
	if list[0].Status != SourceCandidate {
		t.Errorf("status should NOT change on conflict, got %q", list[0].Status)
	}
}

func TestListSources_ActiveFirstThenSeeders(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "low", Seeders: 1}, SourceCandidate)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "high", Seeders: 99}, SourceCandidate)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "act", Seeders: 5}, SourceActive)
	list, _ := s.ListSources(id)
	if len(list) != 3 || list[0].InfoHash != "act" {
		t.Fatalf("active should sort first, got %+v", list)
	}
	if list[1].InfoHash != "high" || list[2].InfoHash != "low" {
		t.Errorf("rest should be by seeders desc, got %s,%s", list[1].InfoHash, list[2].InfoHash)
	}
}

func TestNextSource_SkipsActiveFailedAndCooldown(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "act", Seeders: 10}, SourceActive)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "cand", Seeders: 7}, SourceCandidate)
	// A failed source must never be returned.
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "dead", Seeders: 99}, SourceFailed)

	next, err := s.NextSource(id, 30)
	if err != nil {
		t.Fatalf("NextSource: %v", err)
	}
	if next == nil || next.InfoHash != "cand" {
		t.Fatalf("expected candidate 'cand', got %+v", next)
	}
}

func TestMarkSourceTried_CooldownThenFailed(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "h", Seeders: 5}, SourceCandidate)
	src, _ := s.SourceByInfoHash(id, "h")

	// First try → cooldown.
	_ = s.MarkSourceTried(src.ID, 2)
	got, _ := s.SourceByInfoHash(id, "h")
	if got.Status != SourceCooldown || got.Tries != 1 {
		t.Fatalf("after 1st try: status=%q tries=%d, want cooldown/1", got.Status, got.Tries)
	}
	// Second try reaches maxTries=2 → failed.
	_ = s.MarkSourceTried(src.ID, 2)
	got, _ = s.SourceByInfoHash(id, "h")
	if got.Status != SourceFailed {
		t.Fatalf("after 2nd try: status=%q, want failed", got.Status)
	}
}

func TestNextSource_CooldownEligibleAfterWindow(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "h", Seeders: 5}, SourceCandidate)
	src, _ := s.SourceByInfoHash(id, "h")
	_ = s.MarkSourceTried(src.ID, 5) // → cooldown, last_tried = now

	// With a 30-min cooldown, the just-tried source is NOT yet eligible.
	if next, _ := s.NextSource(id, 30); next != nil {
		t.Errorf("source in fresh cooldown should not be returned, got %+v", next)
	}
	// With a 0-min cooldown, it's immediately eligible again.
	if next, _ := s.NextSource(id, 0); next == nil {
		t.Error("with 0 cooldown the source should be eligible")
	}
}

func TestActivateSource_SetsActiveAndMagnet(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "old", Magnet: "magnet:old"}, SourceActive)
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "new", Magnet: "magnet:new"}, SourceCandidate)
	newSrc, _ := s.SourceByInfoHash(id, "new")

	if err := s.ActivateSource(id, newSrc.ID, "magnet:new"); err != nil {
		t.Fatalf("ActivateSource: %v", err)
	}
	// The download's active_magnet now points at the new source.
	d, _ := s.Get(1, id)
	if d.ActiveMagnet != "magnet:new" || d.EffectiveMagnet() != "magnet:new" {
		t.Errorf("active_magnet not set: %q", d.ActiveMagnet)
	}
	// Exactly one active source, and it's the new one.
	list, _ := s.ListSources(id)
	active := 0
	for _, src := range list {
		if src.Status == SourceActive {
			active++
			if src.InfoHash != "new" {
				t.Errorf("wrong active source: %s", src.InfoHash)
			}
		}
	}
	if active != 1 {
		t.Errorf("expected exactly 1 active source, got %d", active)
	}
}

func TestHasSources(t *testing.T) {
	s, id := mkDownloadWithSource(t)
	if has, _ := s.HasSources(id); has {
		t.Error("new download should have no sources")
	}
	_ = s.EnsureSource(Source{DownloadID: id, InfoHash: "h"}, SourceActive)
	if has, _ := s.HasSources(id); !has {
		t.Error("should report sources after EnsureSource")
	}
}
