package transmissionrpc

import "time"

// rpcSessionTTL is the ABSOLUTE lifetime of an RPC session, counted from the
// Basic-Auth handshake (not from the last use). A sliding/idle window would
// never expire for an *arr that polls every few seconds, so a disabled
// account, a demoted admin or a changed password would keep working until the
// process restarts. Expiry is invisible to clients: the next request gets the
// standard 409 and re-runs the handshake, which re-checks password, status
// and role.
const rpcSessionTTL = time.Hour

// rpcSession is one authenticated RPC client. The role is captured at
// handshake time so session reuse doesn't need a DB hit per RPC.
type rpcSession struct {
	userID  int
	admin   bool
	expires time.Time
}

// openSession registers sid for the user and drops every expired session
// (pruning on insert keeps the map bounded without a background sweeper).
func (h *Handler) openSession(sid string, userID int, admin bool) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, s := range h.sessions {
		if !now.Before(s.expires) {
			delete(h.sessions, k)
		}
	}
	h.sessions[sid] = rpcSession{userID: userID, admin: admin, expires: now.Add(rpcSessionTTL)}
}

// lookupSession resolves a live session id to its identity.
func (h *Handler) lookupSession(sid string) (rpcIdentity, bool) {
	h.mu.RLock()
	s, ok := h.sessions[sid]
	h.mu.RUnlock()
	if !ok || !time.Now().Before(s.expires) {
		return rpcIdentity{}, false
	}
	return rpcIdentity{userID: s.userID, admin: s.admin, sessionID: sid}, true
}

// adminOnlyMethods mutate state shared by every user (global rate limits,
// queue and alt-speed settings) — admin-only, like POST /api/stream/limits.
var adminOnlyMethods = map[string]bool{
	"session-set": true,
	"group-set":   true,
}
