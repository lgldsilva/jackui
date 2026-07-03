package transfer

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestJobLifecycleAndProgress(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	tr := &Tracker{now: clk.Now}

	j := tr.Start("move X", "local-move", 2, 1000)
	if s := j.Snapshot(); s.Status != StatusRunning || s.FilesTotal != 2 || s.BytesTotal != 1000 {
		t.Fatalf("inicial: %+v", s)
	}

	j.AddBytes(500)
	clk.advance(time.Second)
	j.AddBytes(500)
	j.FileDone()
	j.FileDone()

	s := j.Snapshot()
	if s.BytesDone != 1000 || s.FilesDone != 2 {
		t.Fatalf("bytes/files: %+v", s)
	}
	if s.Progress != 1.0 {
		t.Fatalf("progress = %v, want 1.0", s.Progress)
	}
	if s.RatePerSec <= 0 {
		t.Fatalf("rate = %d, want > 0 (janela ativa)", s.RatePerSec)
	}

	j.Done()
	if got := j.Snapshot().Status; got != StatusDone {
		t.Fatalf("status = %q, want done", got)
	}
	if got := j.Snapshot().RatePerSec; got != 0 {
		t.Fatalf("rate após done = %d, want 0", got)
	}

	if n := len(tr.List()); n != 1 {
		t.Fatalf("List = %d jobs, want 1", n)
	}
	// Passada a retenção, o job concluído some.
	clk.advance(doneRetentionTTL + time.Second)
	if n := len(tr.List()); n != 0 {
		t.Fatalf("List após retenção = %d, want 0", n)
	}
}

func TestJobFail(t *testing.T) {
	tr := New()
	j := tr.Start("x", "promote", 1, 0)
	j.Fail(errors.New("boom"))
	s := j.Snapshot()
	if s.Status != StatusFailed || s.Error != "boom" {
		t.Fatalf("fail: %+v", s)
	}
}

func TestProgressReader(t *testing.T) {
	var got int64
	r := ProgressReader(strings.NewReader("hello world"), func(n int64) { got += n })
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	if got != 11 {
		t.Fatalf("reportou %d bytes, want 11", got)
	}
	// nil callback → passa o reader cru, sem panic.
	if _, err := io.Copy(io.Discard, ProgressReader(strings.NewReader("hi"), nil)); err != nil {
		t.Fatal(err)
	}
}

func TestNilJobSafe(t *testing.T) {
	var j *Job
	j.AddBytes(10)
	j.AddSkipped(10)
	j.FileDone()
	j.Done()
	j.AddBytesFunc()(5) // não deve dar panic
}

// AddSkipped avança o progresso (bytes já presentes no destino, resume) SEM
// entrar na janela de taxa — senão pular um arquivo grande dispararia um rate
// absurdo. Conferimos: bytesDone sobe, RatePerSec permanece 0.
func TestAddSkippedAdvancesProgressNotRate(t *testing.T) {
	tr := New()
	j := tr.Start("resume", "promote", 2, 1000)
	j.AddSkipped(600)
	s := j.Snapshot()
	if s.BytesDone != 600 {
		t.Fatalf("BytesDone = %d, want 600", s.BytesDone)
	}
	if s.RatePerSec != 0 {
		t.Fatalf("RatePerSec = %d, want 0 (skip não conta na taxa)", s.RatePerSec)
	}
	j.AddSkipped(-1) // guard: ignorado
	if j.Snapshot().BytesDone != 600 {
		t.Fatal("AddSkipped(<=0) deveria ser no-op")
	}
}

func TestSetBytesTotalLateAndGuards(t *testing.T) {
	tr := New()
	j := tr.Start("late", "promote", 0, 0) // totals unknown at start
	j.SetBytesTotal(-5)                    // negative is ignored (guard)
	j.SetBytesTotal(200)
	j.AddBytes(100)
	if s := j.Snapshot(); s.BytesTotal != 200 || s.Progress != 0.5 {
		t.Fatalf("late totals: %+v", s)
	}
	var nilJob *Job
	nilJob.SetBytesTotal(10) // no-op, must not panic
}

func TestAddBytesFuncOnLiveJob(t *testing.T) {
	tr := New()
	j := tr.Start("x", "local-move", 1, 100)
	j.AddBytesFunc()(40) // non-nil path delegates to AddBytes
	if s := j.Snapshot(); s.BytesDone != 40 {
		t.Fatalf("AddBytesFunc didn't apply: %+v", s)
	}
}

func TestRateWindowPrunesStaleSamples(t *testing.T) {
	clk := &fakeClock{t: time.Unix(2000, 0)}
	tr := &Tracker{now: clk.Now}
	j := tr.Start("move", "local-move", 0, 10000)
	j.AddBytes(1000)
	clk.advance(rateWindow + time.Second) // first sample falls out of the window
	j.AddBytes(2000)                      // pruneLocked trims the stale sample (i>0)
	s := j.Snapshot()
	if s.BytesDone != 3000 {
		t.Fatalf("bytesDone=%d, want 3000", s.BytesDone)
	}
}

func TestProgressByFilesWhenNoBytesTotal(t *testing.T) {
	tr := New()
	j := tr.Start("dir move", "local-move", 4, 0) // bytesTotal desconhecido
	j.FileDone()
	j.FileDone()
	if p := j.Snapshot().Progress; p != 0.5 {
		t.Fatalf("progress por arquivos = %v, want 0.5", p)
	}
}

// Submit bounds concurrency: with cap 1, the first job runs and the rest queue;
// releasing the running one lets the queue drain.
func TestSubmitBoundsConcurrencyAndQueues(t *testing.T) {
	tr := New(1)
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		tr.Submit("j", "local-move", 1, 0, func(j *Job) {
			started <- struct{}{}
			<-release
			j.Done()
		})
	}
	<-started // one job acquired the single slot and is running

	running, queued := 0, 0
	for _, s := range tr.List() {
		switch s.Status {
		case StatusRunning:
			running++
		case StatusQueued:
			queued++
		}
	}
	if running != 1 || queued != 2 {
		t.Fatalf("running=%d queued=%d, want 1/2 (cap=1)", running, queued)
	}

	close(release) // drain
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for _, s := range tr.List() {
			if s.Status == StatusDone {
				done++
			}
		}
		if done == 3 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("nem todos os 3 jobs concluíram após liberar a fila")
}

// Submit on a nil Tracker still runs fn (with a nil Job) — tracking disabled.
func TestSubmitNilTrackerRunsFn(t *testing.T) {
	var tr *Tracker
	done := make(chan struct{})
	tr.Submit("x", "local-move", 1, 0, func(j *Job) {
		if j != nil {
			t.Errorf("esperava Job nil no tracker nil")
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fn não rodou no tracker nil")
	}
}

// ActiveCount counts only Queued/Running jobs; WaitIdle drains them or times out.
func TestActiveCountAndWaitIdle(t *testing.T) {
	tr := New(2)

	if n := tr.ActiveCount(); n != 0 {
		t.Fatalf("ActiveCount em tracker vazio = %d, want 0", n)
	}

	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	for i := 0; i < 2; i++ {
		tr.Submit("move", "local-move", 1, 100, func(j *Job) {
			started.Done()
			<-release
			j.Done()
		})
	}
	started.Wait()

	if n := tr.ActiveCount(); n != 2 {
		t.Fatalf("ActiveCount com 2 rodando = %d, want 2", n)
	}

	// WaitIdle deve estourar o timeout enquanto os jobs seguem ativos.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	if tr.WaitIdle(ctx) {
		t.Error("WaitIdle retornou true com jobs ainda ativos")
	}
	cancel()

	// Libera os jobs → WaitIdle deve drenar dentro do timeout.
	close(release)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if !tr.WaitIdle(ctx2) {
		t.Errorf("WaitIdle não drenou após concluir os jobs (ativos=%d)", tr.ActiveCount())
	}
}

// WaitIdle/ActiveCount em *Tracker nil são no-ops seguros (tracking desabilitado).
func TestWaitIdleNilTracker(t *testing.T) {
	var tr *Tracker
	if tr.ActiveCount() != 0 {
		t.Error("ActiveCount nil != 0")
	}
	if !tr.WaitIdle(context.Background()) {
		t.Error("WaitIdle nil deve retornar true")
	}
}
