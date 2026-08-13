package streamer

import "testing"

const testPiece = int64(1 << 20) // 1 MiB

func TestComputePieceWindow_StartOfFile(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 100,
		fileOffset: 0, fileLength: 100 << 20, pieceLen: testPiece,
		playhead: 0, window: 32 << 20, tail: 8 << 20,
	})
	if w.nowBegin != 0 || w.nowEnd != 32 {
		t.Fatalf("now = [%d,%d) want [0,32)", w.nowBegin, w.nowEnd)
	}
	if w.highBegin != 92 || w.highEnd != 100 {
		t.Fatalf("high = [%d,%d) want [92,100)", w.highBegin, w.highEnd)
	}
}

func TestComputePieceWindow_MidFileSeek(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 100,
		fileOffset: 0, fileLength: 100 << 20, pieceLen: testPiece,
		playhead: 50 << 20, window: 32 << 20, tail: 8 << 20,
	})
	if w.nowBegin != 50 || w.nowEnd != 82 {
		t.Fatalf("now = [%d,%d) want [50,82)", w.nowBegin, w.nowEnd)
	}
}

func TestComputePieceWindow_PlayheadInTail(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 100,
		fileOffset: 0, fileLength: 100 << 20, pieceLen: testPiece,
		playhead: 95 << 20, window: 32 << 20, tail: 8 << 20,
	})
	if w.nowBegin != 95 || w.nowEnd != 100 {
		t.Fatalf("now = [%d,%d) want [95,100)", w.nowBegin, w.nowEnd)
	}
	if w.highBegin != 92 || w.highEnd != 100 {
		t.Fatalf("high = [%d,%d) want [92,100)", w.highBegin, w.highEnd)
	}
}

func TestComputePieceWindow_FileNotAtTorrentStart(t *testing.T) {
	t.Parallel()
	// File occupies torrent pieces [10, 110).
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 10, fileEnd: 110,
		fileOffset: 10 << 20, fileLength: 100 << 20, pieceLen: testPiece,
		playhead: 0, window: 32 << 20, tail: 8 << 20,
	})
	if w.nowBegin != 10 || w.nowEnd != 42 {
		t.Fatalf("now = [%d,%d) want [10,42)", w.nowBegin, w.nowEnd)
	}
	if w.highBegin != 102 || w.highEnd != 110 {
		t.Fatalf("high = [%d,%d) want [102,110)", w.highBegin, w.highEnd)
	}
}

func TestComputePieceWindow_TinyFile(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 3,
		fileOffset: 0, fileLength: 3 << 20, pieceLen: testPiece,
		playhead: 0, window: 32 << 20, tail: 8 << 20,
	})
	if w.nowBegin != 0 || w.nowEnd != 3 {
		t.Fatalf("now = [%d,%d) want [0,3)", w.nowBegin, w.nowEnd)
	}
	if w.highBegin != 0 || w.highEnd != 3 {
		t.Fatalf("entire tiny file is also the tail: high=%v", w)
	}
}

func TestComputePieceWindow_Invalid(t *testing.T) {
	t.Parallel()
	if !computePieceWindow(pieceWindowInput{}).empty() {
		t.Fatal("zero input should be empty")
	}
	if !computePieceWindow(pieceWindowInput{fileBegin: 5, fileEnd: 5, pieceLen: 1, fileLength: 1}).empty() {
		t.Fatal("empty file range should be empty")
	}
}

func TestComputePieceWindow_ClampsPlayhead(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 10,
		fileOffset: 0, fileLength: 10 << 20, pieceLen: testPiece,
		playhead: -100, window: 2 << 20, tail: 1 << 20,
	})
	if w.nowBegin != 0 {
		t.Fatalf("negative playhead should start at 0, nowBegin=%d", w.nowBegin)
	}
	w = computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 10,
		fileOffset: 0, fileLength: 10 << 20, pieceLen: testPiece,
		playhead: 99 << 20, window: 2 << 20, tail: 1 << 20,
	})
	if w.nowBegin != 10 || w.nowEnd != 10 {
		t.Fatalf("past-EOF playhead should collapse now, got [%d,%d)", w.nowBegin, w.nowEnd)
	}
}

func TestApplyPieceWindow_NoPanicOnNilAndEmpty(t *testing.T) {
	t.Parallel()
	applyPieceWindow(nil, pieceWindow{}, pieceWindow{})
	applyPieceWindow(nil, pieceWindow{nowBegin: 0, nowEnd: 4}, pieceWindow{})
}

func TestApplyPieceWindow_OnRealTorrent(t *testing.T) {
	const pieceLen = 1 << 14
	data := make([]byte, 40*pieceLen)
	s, hash := activeMultiPiece(t, data, pieceLen)
	s.mu.Lock()
	tor := s.active[hash].t
	s.mu.Unlock()
	prev := pieceWindow{nowBegin: 0, nowEnd: 4, highBegin: 35, highEnd: 40}
	next := pieceWindow{nowBegin: 10, nowEnd: 14, highBegin: 35, highEnd: 40}
	applyPieceWindow(tor, pieceWindow{}, prev)
	applyPieceWindow(tor, prev, next)
}

func TestTrackingReader_ApplyWindowGuards(t *testing.T) {
	t.Parallel()
	r := &trackingReader{}
	r.applyWindow(10) // no file → no-op
	r.window = 0
	r.file = nil
	r.applyWindow(-1)
}

func TestApplyPieceWindow_ClampsOutOfRangePrev(t *testing.T) {
	const pieceLen = 1 << 14
	data := make([]byte, 8*pieceLen)
	s, hash := activeMultiPiece(t, data, pieceLen)
	s.mu.Lock()
	tor := s.active[hash].t
	s.mu.Unlock()
	// prev window outside the torrent so the demote loop hits the i<0 / i>=n branches
	applyPieceWindow(tor, pieceWindow{nowBegin: -3, nowEnd: 2}, pieceWindow{nowBegin: 1, nowEnd: 3, highBegin: 6, highEnd: 8})
	applyPieceWindow(tor, pieceWindow{nowBegin: 0, nowEnd: 2}, pieceWindow{nowBegin: 1, nowEnd: 3, highBegin: 6, highEnd: 20})
}

func TestSetPieceRange_Clamps(t *testing.T) {
	const pieceLen = 1 << 14
	data := make([]byte, 4*pieceLen)
	s, hash := activeMultiPiece(t, data, pieceLen)
	s.mu.Lock()
	tor := s.active[hash].t
	s.mu.Unlock()
	setPieceRange(tor, -2, 99, 0)
}

func TestPieceBeginEndAndClamp(t *testing.T) {
	t.Parallel()
	if pieceBegin(-10, 1024) != 0 {
		t.Fatal("negative offset should start at piece 0")
	}
	if pieceEndExclusive(-1, 1024) != 0 || pieceEndExclusive(0, 1024) != 0 {
		t.Fatal("non-positive end should be 0")
	}
	if clampPiece(1, 3, 9) != 3 || clampPiece(12, 3, 9) != 9 || clampPiece(5, 3, 9) != 5 {
		t.Fatal("clampPiece")
	}
}

func TestComputePieceWindow_DefaultWindowAndTail(t *testing.T) {
	t.Parallel()
	w := computePieceWindow(pieceWindowInput{
		fileBegin: 0, fileEnd: 200,
		fileOffset: 0, fileLength: 200 << 20, pieceLen: testPiece,
		playhead: 0, window: 0, tail: 0,
	})
	if w.nowEnd != 32 {
		t.Fatalf("default window should be 32 MiB → 32 pieces, nowEnd=%d", w.nowEnd)
	}
	if w.highBegin != 192 {
		t.Fatalf("default tail 8 MiB → highBegin=%d want 192", w.highBegin)
	}
}

func TestInPieceRange(t *testing.T) {
	t.Parallel()
	if !inPieceRange(5, 5, 8) || inPieceRange(8, 5, 8) || inPieceRange(4, 5, 8) {
		t.Fatal("half-open range failed")
	}
}
