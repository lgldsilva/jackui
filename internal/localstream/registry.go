package localstream

import (
	"sync"
	"time"
)

// idleReapAfter: a session with no metered reads for this long is closed and
// removed. Covers the common case of a closed tab that never calls Release
// (the HLS path keeps a session alive only while ffmpeg keeps pulling bytes).
const idleReapAfter = 60 * time.Second

type entry struct {
	s    *Session
	refs int
}

// Registry tracks live read sessions keyed by a caller-chosen string (built
// from mount/path/user). It hands out Sessions, refcounts the direct-play
// path, and reaps idle sessions so a closed player doesn't leak file handles.
type Registry struct {
	mu             sync.Mutex
	sess           map[string]*entry
	readaheadBytes int
	now            func() time.Time
	stop           chan struct{}
}

// NewRegistry builds a Registry whose sessions read ahead readaheadMB at a
// time (0 → 16 MiB default) and starts the idle reaper.
func NewRegistry(readaheadMB int) *Registry {
	return newRegistry(readaheadMB, time.Now, true)
}

func newRegistry(readaheadMB int, nowFn func() time.Time, runGC bool) *Registry {
	bytes := readaheadMB << 20
	if bytes <= 0 {
		bytes = defaultReadaheadBytes
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	r := &Registry{
		sess:           make(map[string]*entry),
		readaheadBytes: bytes,
		now:            nowFn,
		stop:           make(chan struct{}),
	}
	if runGC {
		go r.gcLoop()
	}
	return r
}

// OpenShared returns the Session for key, creating it from f if absent and
// reusing (and closing the duplicate f) if present. Use this for sessions that
// outlive a single request (the HLS transcode source, deduped by key): the
// caller does NOT Release — the reaper closes it when ffmpeg stops pulling.
func (r *Registry) OpenShared(key string, f ReadSeekCloser, size int64) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sess[key]; ok {
		e.refs++
		_ = f.Close() // a live session already owns a handle for this key
		return e.s
	}
	s := newSession(key, f, size, r.readaheadBytes, r.now)
	r.sess[key] = &entry{s: s, refs: 1}
	return s
}

// OpenSolo returns a fresh Session bound to its own f and registers it under
// key for status lookup. Use this for the direct-play path, where concurrent
// http.ServeContent requests must NOT share a single cursor. Pair every
// OpenSolo with a deferred Release(key, sess).
func (r *Registry) OpenSolo(key string, f ReadSeekCloser, size int64) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := newSession(key, f, size, r.readaheadBytes, r.now)
	// Overwrite the status slot with the newest session; an older solo session
	// keeps working through the caller's own reference and is closed by its
	// Release. refs stays 1 per solo session.
	r.sess[key] = &entry{s: s, refs: 1}
	return s
}

// Release drops one reference from a solo session and closes it when the last
// one goes. It only unmaps key if the slot still points at sess, so it never
// evicts a newer session that replaced this one.
func (r *Registry) Release(key string, sess *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess.close()
	if e, ok := r.sess[key]; ok && e.s == sess {
		e.refs--
		if e.refs <= 0 {
			delete(r.sess, key)
		}
	}
}

// Get returns the live session for key, if any.
func (r *Registry) Get(key string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sess[key]
	if !ok {
		return nil, false
	}
	return e.s, true
}

// Close stops the reaper and closes every live session.
func (r *Registry) Close() {
	select {
	case <-r.stop:
		// already closed
	default:
		close(r.stop)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.sess {
		e.s.close()
		delete(r.sess, k)
	}
}

func (r *Registry) gcLoop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-tick.C:
			r.reap()
		}
	}
}

// reap closes and removes sessions idle past idleReapAfter, regardless of refs
// — a dangling refcount from a closed tab must not pin a file handle forever.
func (r *Registry) reap() {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.sess {
		if e.s.idleFor(now) > idleReapAfter {
			e.s.close()
			delete(r.sess, k)
		}
	}
}
