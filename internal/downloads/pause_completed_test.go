package downloads

import (
	"testing"
)

// Pausar um download JÁ CONCLUÍDO deixava a linha órfã: o status virava
// `paused`, mas Requeue (store_groups.go) recusa sair de `completed`, então o
// resume era no-op e a row ficava presa em `paused` para sempre. Na UI o item
// perdia as ações de concluído (Promover / Parar e remover / Abrir no local),
// que são condicionadas a status==='completed' — daí o relato "não sai da lista
// e não aparece a opção de excluir o torrent mantendo os arquivos".
//
// SetStatusForUser (pause-all) SEMPRE excluiu os terminais; o caminho single/
// batch é que não tinha a guarda. Aqui ela vira regra do store.
func TestSetStatusPausedIgnoresCompleted(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "aaa", 0)
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}

	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Errorf("status=%q, want %q — pausar um concluído deve ser no-op", got.Status, StatusCompleted)
	}
}

// Mesma guarda para `failed`: o card oferece "Tentar novamente" (resume), não
// pausar. Pausar um failed só o esconderia da aba de erros.
func TestSetStatusPausedIgnoresFailed(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "bbb", 0)
	if err := s.SetStatus(1, d.ID, StatusFailed); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}

	got, _ := s.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Errorf("status=%q, want %q — pausar um falho deve ser no-op", got.Status, StatusFailed)
	}
}

// A guarda vale só para o pause: completed→downloading continua livre (é o
// caminho de re-download / re-enqueue, e SetStatus(completed) reabilita o
// auto-seed limpando seed_stopped_at).
func TestSetStatusCompletedToDownloadingStillAllowed(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "ccc", 0)
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusDownloading); err != nil {
		t.Fatalf("SetStatus downloading: %v", err)
	}

	got, _ := s.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Errorf("status=%q, want %q — re-download não pode ser bloqueado", got.Status, StatusDownloading)
	}
}

// O batch (PATCH /downloads/batch/pause) usa SetStatusByIDs — mesma guarda, e
// `affected` precisa refletir só as linhas realmente pausadas para o frontend
// não anunciar sucesso sobre um no-op.
func TestSetStatusByIDsPausedSkipsTerminalRows(t *testing.T) {
	s := newTestStore(t)
	active := mustCreate(t, s, 1, "ddd", 0)
	completed := mustCreate(t, s, 1, "ddd", 1)
	failed := mustCreate(t, s, 1, "ddd", 2)
	if err := s.SetStatus(1, active.ID, StatusDownloading); err != nil {
		t.Fatalf("SetStatus downloading: %v", err)
	}
	if err := s.SetStatus(1, completed.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}
	if err := s.SetStatus(1, failed.ID, StatusFailed); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	n, err := s.SetStatusByIDs(1, []int{active.ID, completed.ID, failed.ID}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 1 {
		t.Errorf("affected=%d, want 1 — só a linha ativa pode ser pausada", n)
	}

	gotCompleted, _ := s.Get(1, completed.ID)
	if gotCompleted.Status != StatusCompleted {
		t.Errorf("completed virou %q no batch pause", gotCompleted.Status)
	}
	gotFailed, _ := s.Get(1, failed.ID)
	if gotFailed.Status != StatusFailed {
		t.Errorf("failed virou %q no batch pause", gotFailed.Status)
	}
	gotActive, _ := s.Get(1, active.ID)
	if gotActive.Status != StatusPaused {
		t.Errorf("linha ativa não foi pausada: %q", gotActive.Status)
	}
}

// SetStatusByIDs com outros status (ex.: o worker demovendo para queued) não
// herda a guarda — ela é específica do pause.
func TestSetStatusByIDsNonPausedUnaffectedByGuard(t *testing.T) {
	s := newTestStore(t)
	completed := mustCreate(t, s, 1, "eee", 0)
	if err := s.SetStatus(1, completed.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	n, err := s.SetStatusByIDs(1, []int{completed.ID}, StatusQueued)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 1 {
		t.Errorf("affected=%d, want 1 — re-enfileirar um concluído continua permitido", n)
	}
}

func mustCreate(t *testing.T, s *Store, userID int, infoHash string, fileIndex int) *Download {
	t.Helper()
	d, err := s.Create(Download{
		UserID: userID, InfoHash: infoHash, FileIndex: fileIndex,
		Magnet: "magnet:?xt=urn:btih:" + infoHash, Name: "x", FilePath: "x", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return d
}
