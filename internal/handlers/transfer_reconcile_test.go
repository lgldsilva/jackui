package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Boot reconcile: um promote persistido (interrompido por restart) é re-submetido
// e a cópia conclui — o destino passa a existir e o pending é removido.
func Test_ReconcilePromote_ResumesCopy(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	tr := transfer.New()

	src := filepath.Join(t.TempDir(), "movie.mkv")
	dst := filepath.Join(t.TempDir(), "dest", "movie.mkv")
	if err := os.WriteFile(src, []byte("conteudo-do-filme"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "movie.mkv", FilePath: src})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)

	payload, _ := json.Marshal(promotePayload{DownloadID: d.ID, UserID: 0, KeepSeeding: false})
	if _, err := pending.Add(transfer.Pending{Kind: "promote", Src: src, Dst: dst, Payload: string(payload)}); err != nil {
		t.Fatal(err)
	}

	ReconcilePendingTransfers(pending, tr, store, s)

	// A cópia roda em background (tr.Submit → goroutine). Espera concluir.
	moved := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dst); err == nil {
			moved = true
			break
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à goroutine de cópia
	}
	if !moved {
		t.Fatal("destino não foi criado pela reconciliação")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("origem deveria ser removida após o move")
	}
	// pending limpo + file_path atualizado.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l, _ := pending.List(); len(l) == 0 {
			break
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à goroutine de cópia
	}
	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("pending deveria estar vazio, got %d", len(l))
	}
	if up, _ := store.Get(0, d.ID); up == nil || up.FilePath != dst {
		t.Errorf("file_path não atualizado: %+v", up)
	}
}

// Origem ausente + destino presente = a cópia já tinha concluído antes do
// crash: reconcilia re-apontando o file_path e limpa o pending (sem re-copiar).
func Test_ReconcilePromote_SrcGoneDstPresent(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()

	dst := filepath.Join(t.TempDir(), "done.mkv")
	if err := os.WriteFile(dst, []byte("ja-copiado"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "done.mkv", FilePath: "/gone/done.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)
	payload, _ := json.Marshal(promotePayload{DownloadID: d.ID, UserID: 0})
	_, _ = pending.Add(transfer.Pending{Kind: "promote", Src: "/gone/done.mkv", Dst: dst, Payload: string(payload)})

	ReconcilePendingTransfers(pending, transfer.New(), store, s)

	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("pending deveria ser limpo, got %d", len(l))
	}
	if up, _ := store.Get(0, d.ID); up == nil || up.FilePath != dst {
		t.Errorf("file_path deveria apontar pro destino existente: %+v", up)
	}
}

// Kind desconhecido é descartado (não trava a fila de reconciliação).
func Test_Reconcile_UnknownKindDropped(t *testing.T) {
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	_, _ = pending.Add(transfer.Pending{Kind: "bogus", Src: "a", Dst: "b"})
	ReconcilePendingTransfers(pending, transfer.New(), hgAStore(t), streamer.NewForTesting())
	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("kind desconhecido deveria ser removido, got %d", len(l))
	}
}

func Test_shouldSerialize_Modes(t *testing.T) {
	// serial: força sequencial em qualquer disco.
	if !shouldSerialize(transferModeSerial, "/qualquer/coisa") {
		t.Error("modo serial deveria serializar")
	}
	// parallel: nunca serializa (ignora detecção de HDD).
	if shouldSerialize(transferModeParallel, "/qualquer/coisa") {
		t.Error("modo parallel não deveria serializar")
	}
	// auto / "" : delega à detecção de disco — path inexistente → false.
	if shouldSerialize(transferModeAuto, "/no/such/path-xyz") {
		t.Error("auto em path inexistente deveria ser false (não-rotacional)")
	}
	if shouldSerialize("", "/no/such/path-xyz") {
		t.Error("vazio = auto")
	}
}

func Test_transferMode_NilSafe(t *testing.T) {
	if transferMode(nil) != "" {
		t.Error("transferMode(nil) deveria ser vazio")
	}
}
