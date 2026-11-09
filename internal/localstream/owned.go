package localstream

// OwnedSession is a Session whose lifetime belongs to a single owner — the
// HLS transcoder that reads it. Unlike a bare *Session it implements
// io.Closer, so the transcoder's closeIfCloser actually releases it (when a
// duplicate request loses the GetOrStart race, and when the HLS session
// stops). The registry's idle reaper never closes it: an ffmpeg that finished
// encoding stops reading but may seek-restart later.
type OwnedSession struct {
	*Session
	r *Registry
}

// Close releases the session (idempotent).
func (o *OwnedSession) Close() error {
	o.r.releaseOwned(o.Session)
	return nil
}

// OpenOwned returns a fresh owned Session bound to its own f (independent
// cursor, like OpenSolo) and makes it the status slot for key. The caller —
// or whoever it hands the source to — must Close it.
func (r *Registry) OpenOwned(key string, f ReadSeekCloser, size int64) *OwnedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := newSession(key, f, size, r.readaheadBytes, r.now)
	r.ownedSeq++
	r.owned[s] = ownedRef{key: key, seq: r.ownedSeq}
	r.sess[key] = &entry{s: s, refs: 1}
	return &OwnedSession{Session: s, r: r}
}

type ownedRef struct {
	key string
	seq uint64 // creation order — the newest survivor takes the status slot back
}

func (r *Registry) releaseOwned(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.owned[s]
	if !ok {
		return
	}
	delete(r.owned, s)
	s.close()
	if e, ok := r.sess[ref.key]; ok && e.s == s {
		delete(r.sess, ref.key)
		if next := r.newestOwnedLocked(ref.key); next != nil {
			r.sess[ref.key] = &entry{s: next, refs: 1}
		}
	}
}

func (r *Registry) newestOwnedLocked(key string) *Session {
	var best *Session
	var bestSeq uint64
	for s, ref := range r.owned {
		if ref.key == key && ref.seq > bestSeq {
			best, bestSeq = s, ref.seq
		}
	}
	return best
}

func (r *Registry) isOwnedLocked(s *Session) bool {
	_, ok := r.owned[s]
	return ok
}
