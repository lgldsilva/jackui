package streamer

import (
	"time"

	"github.com/anacrolix/torrent"
)

// entry is the internal bookkeeping for one active torrent.
type entry struct {
	t          *torrent.Torrent
	lastAccess time.Time
	// Rate sampling: anacrolix only exposes cumulative byte counters, so we cache the
	// previous sample and compute per-second rates from the delta between buildInfo calls.
	lastBytesRead    int64
	lastBytesWritten int64
	lastSampleAt     time.Time
	// paused tracks the soft-pause state. anacrolix has no native Pause; we
	// model it by setting MaxEstablishedConns to 0 so no new peers connect.
	paused bool
	// priority is the user-facing label ("low" | "normal" | "high"). Applied to
	// every file via File.SetPriority on transitions.
	priority string
	// viewers counts open player sessions watching this torrent (a "lease"). A
	// stream-only torrent with no viewers is ephemeral and should stop seeding
	// instead of lingering until the idle reaper. While viewers > 0 the torrent
	// survives — so closing one of several browsers doesn't kill the others.
	viewers int
	// dropTimer schedules the drop a short grace period after the LAST viewer
	// leaves (see ReleaseViewer/viewerGrace). AcquireViewer cancels it, so a
	// quick reopen — or React StrictMode's mount→unmount→mount in dev — doesn't
	// tear the torrent down mid-playback.
	dropTimer *time.Timer
}

// FileInfo is the JSON-friendly view of a file inside a torrent.
type FileInfo struct {
	Index      int     `json:"index"`
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	IsVideo    bool    `json:"isVideo"`
	Downloaded int64   `json:"downloaded"`
	Progress   float64 `json:"progress"` // 0..1
	Priority   string  `json:"priority"` // none|low|normal|high
}

// TorrentInfo is the JSON-friendly view returned to the frontend.
type TorrentInfo struct {
	InfoHash  string     `json:"infoHash"`
	Name      string     `json:"name"`
	TotalSize int64      `json:"totalSize"`
	Files     []FileInfo `json:"files"`
	Peers     int        `json:"peers"`
	Seeders   int        `json:"seeders"`
	DownRate  int64      `json:"downRate"` // bytes/sec, sampled between polls
	UpRate    int64      `json:"upRate"`   // bytes/sec, sampled between polls
	// Cumulative payload byte counters. BytesDownloaded is the completed bytes of
	// the selected pieces; BytesUploaded is what we've served this SESSION (the
	// anacrolix counter resets when the torrent is re-added — e.g. after a restart).
	BytesDownloaded int64   `json:"bytesDownloaded"`
	BytesUploaded   int64   `json:"bytesUploaded"`
	Progress        float64 `json:"progress"`
	PrimaryFile     int     `json:"primaryFile"` // suggested video file index
	// Status is one of "downloading", "paused", "seeding", "complete".
	// Surfaced for the Transmission-style downloads UI.
	Status string `json:"status,omitempty"`
	// Priority is the user-set piece priority ("low" | "normal" | "high"); empty
	// when the user has not changed it from the anacrolix default.
	Priority string   `json:"priority,omitempty"`
	Trackers []string `json:"trackers,omitempty"`
}

// GlobalRate aggregates download/upload rates across all active torrents.
type GlobalRate struct {
	DownRate       int64 `json:"downRate"`
	UpRate         int64 `json:"upRate"`
	ActiveTorrents int   `json:"activeTorrents"`
}

// PeerInfo is the JSON-friendly view of one connected peer, for the downloads
// "Peers" panel. anacrolix v1.61.0 doesn't export choke/interest, so Sending /
// Receiving are INFERRED from live transfer rates rather than read directly.
type PeerInfo struct {
	Addr         string  `json:"addr"`
	Client       string  `json:"client,omitempty"`
	Network      string  `json:"network,omitempty"` // "tcp" | "utp" | ...
	Availability float64 `json:"availability"`      // 0..1 fraction of pieces the peer has
	DownRate     int64   `json:"downRate"`          // bytes/s we receive from this peer
	UpRate       int64   `json:"upRate"`            // bytes/s we send to this peer
	Downloaded   int64   `json:"downloaded"`        // data bytes read from this peer
	Uploaded     int64   `json:"uploaded"`          // data bytes written to this peer
	IsSeeder     bool    `json:"isSeeder"`          // peer reports all pieces
	Receiving    bool    `json:"receiving"`         // inferred: downRate > 0
	Sending      bool    `json:"sending"`           // inferred: upRate > 0
	Encrypted    bool    `json:"encrypted,omitempty"`
}
