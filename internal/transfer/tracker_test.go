package transfer

import (
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
	j.FileDone()
	j.Done()
	j.AddBytesFunc()(5) // não deve dar panic
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
