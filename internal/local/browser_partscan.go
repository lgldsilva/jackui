package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Listing budgets for the nested ".part" scan. A finished library holds no
// ".part" at all, so an unbounded walk never short-circuits and reads every
// directory under every listed folder on each request. The scan is breadth-first
// (in-progress files sit near the top of a torrent folder), capped per entry so
// one deep tree can't starve its siblings and per listing so a wide folder stays
// cheap. Past the budget a folder is simply not flagged.
const (
	partScanDirsPerEntry   = 32
	partScanDirsPerListing = 512
)

type dirReader func(string) ([]os.DirEntry, error)

// listScan carries the per-request state of one List call.
type listScan struct {
	ctx     context.Context
	readDir dirReader
	budget  int // nested directory reads left for the whole listing
}

func newListScan(ctx context.Context, readDir dirReader) *listScan {
	return &listScan{ctx: ctx, readDir: readDir, budget: partScanDirsPerListing}
}

// inspectDir returns the number of non-hidden entries directly inside dirAbs and
// whether its tree holds a ".part" within the scan budget. The directory itself
// is read exactly once for both answers; a read error yields (0, false).
func (s *listScan) inspectDir(dirAbs string) (children int, incomplete bool) {
	des, err := s.readDir(dirAbs)
	if err != nil {
		return 0, false
	}
	children, incomplete, queue := scanLevel(dirAbs, des, nil)
	if incomplete {
		return children, true
	}
	return children, s.nestedHasPart(queue)
}

// nestedHasPart walks queue breadth-first until it finds a ".part", runs out of
// budget or the request is cancelled. Unreadable directories are skipped.
func (s *listScan) nestedHasPart(queue []string) bool {
	left := partScanDirsPerEntry
	for len(queue) > 0 && left > 0 && s.budget > 0 && s.ctx.Err() == nil {
		dir := queue[0]
		queue = queue[1:]
		left--
		s.budget--
		des, err := s.readDir(dir)
		if err != nil {
			continue
		}
		var part bool
		_, part, queue = scanLevel(dir, des, queue)
		if part {
			return true
		}
	}
	return false
}

// scanLevel inspects one directory's entries: the count of non-hidden names,
// whether any file is a ".part" (hidden ones included, as the full walk does),
// and queue extended with the subdirectories to visit next.
func scanLevel(dir string, des []os.DirEntry, queue []string) (visible int, part bool, next []string) {
	for _, de := range des {
		name := de.Name()
		if !strings.HasPrefix(name, ".") {
			visible++
		}
		if de.IsDir() {
			queue = append(queue, filepath.Join(dir, name))
			continue
		}
		if strings.HasSuffix(name, ".part") {
			part = true
		}
	}
	return visible, part, queue
}
