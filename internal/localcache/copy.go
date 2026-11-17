package localcache

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// partialSuffix marks a copy still in progress. The file only takes its final
// name once complete, so a crash never leaves a truncated file there.
const partialSuffix = ".partial"

const errTooLarge = "larger than cache"

// runJob copies the source file to its cache path, reporting progress, then
// marks it ready and evicts down to the size cap. Errors are recorded on the
// entry (the UI surfaces them) rather than panicking.
func (c *Cache) runJob(j job) {
	e, ctx, cancel := c.beginCopy(j.key)
	if e == nil {
		return
	}
	defer cancel()
	partial := e.CachePath + partialSuffix
	err := c.copyFile(ctx, e, j.srcAbs, partial)
	if err == nil {
		err = os.Rename(partial, e.CachePath)
	}
	if err != nil {
		_ = os.Remove(partial)
		c.failEntry(e, err)
		return
	}
	if !c.markReady(j.key, e) {
		_ = os.Remove(e.CachePath) // removed while the copy was finishing
		return
	}
	c.evict()
	c.saveIndex()
}

// beginCopy claims the queued entry for key and returns it with the context
// its copy must honour. A nil entry means there is nothing to do: the entry was
// removed, or it is not waiting (a stale job left behind by Remove followed by
// a fresh Enqueue of the same key, whose own job handles it).
func (c *Cache) beginCopy(k string) (*entry, context.Context, context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || e.Status != StatusQueued {
		return nil, nil, nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	e.cancel = cancel
	e.Status, e.Err = StatusCopying, ""
	return e, ctx, cancel
}

// failEntry records the error on e itself rather than by key, so a cancelled
// copy never marks an entry re-enqueued under the same key.
func (c *Cache) failEntry(e *entry, err error) {
	c.mu.Lock()
	e.Status, e.Err, e.cancel = StatusError, err.Error(), nil
	c.mu.Unlock()
}

// markReady publishes e as ready unless it left the map during the copy.
func (c *Cache) markReady(k string, e *entry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.cancel = nil
	if c.entries[k] != e {
		return false
	}
	e.Status = StatusReady
	e.LastAccess = c.now()
	return true
}

// copyFile checks ctx between chunks, so a cancel takes effect within one
// chunk rather than after the whole (possibly multi-GB) file. A read already
// blocked inside the remote mount still has to return first.
func (c *Cache) copyFile(ctx context.Context, e *entry, srcAbs, dst string) error {
	// #nosec G304 -- path validated by Browser.ResolvePath (guards traversal/symlink) or derived from internal hash/config
	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	// #nosec G304 -- path validated by Browser.ResolvePath (guards traversal/symlink) or derived from internal hash/config
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, copyChunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			c.mu.Lock()
			e.Copied += int64(n)
			c.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return out.Close()
}

// sweepPartials deletes copies interrupted by a crash or a forced exit; they
// are in no index, so nothing else would ever reclaim them.
func (c *Cache) sweepPartials() {
	ents, err := os.ReadDir(c.root)
	if err != nil {
		return
	}
	for _, d := range ents {
		if !d.IsDir() && strings.HasSuffix(d.Name(), partialSuffix) {
			_ = os.Remove(filepath.Join(c.root, d.Name()))
		}
	}
}
