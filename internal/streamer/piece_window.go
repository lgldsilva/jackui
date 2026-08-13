package streamer

import (
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/types"
)

// pieceWindow is the set of piece-index ranges (half-open) a streaming
// playhead should bias. Computed without touching anacrolix so it is
// unit-testable.
//
//   - Now: playhead → +window (next few HLS segments)
//   - High: last tailBytes of the file (moov / Cues)
//   - Everything that just left Now is demoted to Normal so a seek does not
//     keep the old 32 MiB competing with the new cursor
type pieceWindow struct {
	nowBegin, nowEnd   int
	highBegin, highEnd int
}

const streamTailBytes = 8 << 20 // matches warmTail

type pieceWindowInput struct {
	fileBegin  int
	fileEnd    int
	fileOffset int64
	fileLength int64
	pieceLen   int64
	playhead   int64
	window     int64
	tail       int64
}

func computePieceWindow(in pieceWindowInput) pieceWindow {
	if in.pieceLen <= 0 || in.fileEnd <= in.fileBegin || in.fileLength <= 0 {
		return pieceWindow{}
	}
	if in.window <= 0 {
		in.window = streamReadaheadDefault
	}
	if in.tail <= 0 {
		in.tail = streamTailBytes
	}
	play := in.playhead
	if play < 0 {
		play = 0
	}
	if play > in.fileLength {
		play = in.fileLength
	}
	nowStart := in.fileOffset + play
	nowStop := in.fileOffset + play + in.window
	fileStop := in.fileOffset + in.fileLength
	if nowStop > fileStop {
		nowStop = fileStop
	}
	tailStart := fileStop - in.tail
	if tailStart < in.fileOffset {
		tailStart = in.fileOffset
	}

	w := pieceWindow{
		nowBegin:  clampPiece(pieceBegin(nowStart, in.pieceLen), in.fileBegin, in.fileEnd),
		nowEnd:    clampPiece(pieceEndExclusive(nowStop, in.pieceLen), in.fileBegin, in.fileEnd),
		highBegin: clampPiece(pieceBegin(tailStart, in.pieceLen), in.fileBegin, in.fileEnd),
		highEnd:   in.fileEnd,
	}
	if w.nowEnd < w.nowBegin {
		w.nowEnd = w.nowBegin
	}
	if w.highEnd < w.highBegin {
		w.highEnd = w.highBegin
	}
	return w
}

func pieceBegin(off, pieceLen int64) int {
	if off < 0 {
		return 0
	}
	return int(off / pieceLen)
}

func pieceEndExclusive(endOff, pieceLen int64) int {
	if endOff <= 0 {
		return 0
	}
	return int((endOff + pieceLen - 1) / pieceLen)
}

func clampPiece(i, lo, hi int) int {
	if i < lo {
		return lo
	}
	if i > hi {
		return hi
	}
	return i
}

func (w pieceWindow) empty() bool {
	return w.nowEnd <= w.nowBegin && w.highEnd <= w.highBegin
}

func applyPieceWindow(t *torrent.Torrent, prev, next pieceWindow) {
	if t == nil || next.empty() {
		return
	}
	n := int(t.NumPieces())
	// Demote pieces that just left the Now window and are not the tail.
	for i := prev.nowBegin; i < prev.nowEnd; i++ {
		if i < 0 || i >= n {
			continue
		}
		if inPieceRange(i, next.nowBegin, next.nowEnd) || inPieceRange(i, next.highBegin, next.highEnd) {
			continue
		}
		t.Piece(i).SetPriority(types.PiecePriorityNormal)
	}
	setPieceRange(t, next.highBegin, next.highEnd, types.PiecePriorityHigh)
	setPieceRange(t, next.nowBegin, next.nowEnd, types.PiecePriorityNow)
}

func inPieceRange(i, begin, end int) bool {
	return i >= begin && i < end
}

func setPieceRange(t *torrent.Torrent, begin, end int, prio types.PiecePriority) {
	n := int(t.NumPieces())
	if begin < 0 {
		begin = 0
	}
	if end > n {
		end = n
	}
	for i := begin; i < end; i++ {
		t.Piece(i).SetPriority(prio)
	}
}
