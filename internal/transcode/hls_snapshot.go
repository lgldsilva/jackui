package transcode

import (
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// Inspeção/fechamento de sessões HLS (Peek/Close/Sessions/snapshot) — extraído de hls.go.
// closeIfCloser closes a source that supports it (torrent FileReader,
// *os.File). Sources owned elsewhere (e.g. localstream.Session, returned to
// its registry) simply don't implement io.Closer and pass through.
func closeIfCloser(src io.ReadSeeker) {
	if c, ok := src.(io.Closer); ok && c != nil {
		_ = c.Close()
	}
}

// Peek returns an existing session without starting one. Used by the
// segment handler which must NOT race the playlist handler into creating
// a duplicate ffmpeg. Returns an error when the session isn't tracked.
func (m *HLSSessionManager) Peek(key string) (*HLSSession, error) {
	if m == nil {
		return nil, errors.New("nil manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sess[key]
	if !ok {
		return nil, errors.New("session not found")
	}
	s.mu.Lock()
	s.LastAccess = time.Now()
	s.mu.Unlock()
	return s, nil
}

// Close terminates the session immediately. Called by handlers when the
// underlying torrent is dropped or the user explicitly cancels.
func (m *HLSSessionManager) Close(key string) {
	m.mu.Lock()
	s, ok := m.sess[key]
	if ok {
		delete(m.sess, key)
	}
	m.mu.Unlock()
	if ok {
		s.stop()
	}
}

// CloseForHash para TODAS as sessões HLS de um torrent (keys "<hash>-<fileIdx>").
// Chamado quando o player fecha (Drop) pra não deixar o ffmpeg do transcode
// órfão consumindo CPU até o idle-reaper (5min). Idempotente; no-op se não houver.
func (m *HLSSessionManager) CloseForHash(hashHex string) {
	if hashHex == "" {
		return
	}
	prefix := hashHex + "-"
	m.mu.Lock()
	var stopping []*HLSSession
	for k, s := range m.sess {
		if strings.HasPrefix(k, prefix) {
			stopping = append(stopping, s)
			delete(m.sess, k)
		}
	}
	m.mu.Unlock()
	for _, s := range stopping {
		s.stop()
	}
}

// logWriter routes ffmpeg stderr lines to log.Printf with a stable prefix.
type logWriter struct {
	prefix string
	buf    []byte
}

func newLogWriter(prefix string) *logWriter { return &logWriter{prefix: prefix} }

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			log.Print(w.prefix, line)
		}
	}
	return len(p), nil
}

// HLSSessionSnapshot is a read-only representation of an active transcode session.
type HLSSessionSnapshot struct {
	Key           string    `json:"key"`
	Codec         string    `json:"codec"`
	SegmentsReady int       `json:"segmentsReady"`
	StartedAt     time.Time `json:"startedAt"`
	LastActivity  time.Time `json:"lastActivity"`
	Pid           int       `json:"pid"`
}

// Sessions returns all currently active transcode sessions in the manager.
func (m *HLSSessionManager) Sessions() []HLSSessionSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var snapshots []HLSSessionSnapshot
	for key, s := range m.sess {
		snapshots = appendSnapshotIfActive(snapshots, key, s)
	}
	return snapshots
}

func appendSnapshotIfActive(snapshots []HLSSessionSnapshot, key string, s *HLSSession) []HLSSessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return snapshots
	}
	snapshots = append(snapshots, HLSSessionSnapshot{
		Key:           key,
		Codec:         sessionEncoder(s),
		StartedAt:     s.StartedAt,
		LastActivity:  s.LastAccess,
		Pid:           sessionPid(s),
		SegmentsReady: sessionSegmentsReady(s),
	})
	return snapshots
}

func sessionPid(s *HLSSession) int {
	if s.Cmd != nil && s.Cmd.Process != nil {
		return s.Cmd.Process.Pid
	}
	return 0
}

func sessionEncoder(s *HLSSession) string {
	if s.spec != nil {
		return s.spec.encoder
	}
	return "cpu"
}

func sessionSegmentsReady(s *HLSSession) int {
	if s.Dir == "" {
		return 0
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0
	}
	var n int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			n++
		}
	}
	return n
}
