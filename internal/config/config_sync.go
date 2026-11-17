package config

import "sync"

// cfgMu guards every Config section that changes at RUNTIME: the settings
// handlers (stream, downloads queue, mounts, download clients, Jackett, port,
// subtitles) write under the write lock through Update / SetSubtitles, and
// runtime readers take a consistent COPY through the *Snapshot methods. Without
// it a torn read (a Go string or slice header is several words) can corrupt a
// response or panic a reader that lives OUTSIDE gin's per-request recovery (the
// bandwidth scheduler, the downloads worker, the AutoFetcher goroutines) and
// take the whole process down.
//
// Package-level (not a Config field) so Config stays a plain copyable struct;
// Config is a process singleton. Load-time writes run before the HTTP server
// accepts requests — single-goroutine, no lock needed. Fields nobody writes
// after boot (data dirs, auth, SMTP, AI, …) may still be read directly.
//
// Snapshots are shallow: writers must REPLACE a slice, never mutate one in
// place, so a snapshot's slices stay valid after the lock is released.
var cfgMu sync.RWMutex

// Update runs fn on the live config under the write lock. fn must not call a
// *Snapshot method or Save (the lock is not reentrant) and must not block.
func (c *Config) Update(fn func(*Config)) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	fn(c)
}

// StreamSnapshot returns a consistent copy of the live stream settings.
func (c *Config) StreamSnapshot() StreamConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.Stream
}

// DownloadsQueueSnapshot returns a consistent copy of the live queue settings.
func (c *Config) DownloadsQueueSnapshot() DownloadsQueueConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.DownloadsQueue
}

// MountsSnapshot returns the live external mounts list.
func (c *Config) MountsSnapshot() []ExternalMount {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.External.Mounts
}

// DownloadClientsSnapshot returns the live download clients list.
func (c *Config) DownloadClientsSnapshot() []DownloadClient {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.DownloadClients
}

// JackettSnapshot returns the live Jackett endpoint.
func (c *Config) JackettSnapshot() (url, apiKey string) {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.Jackett.URL, c.Jackett.APIKey
}

// PortSnapshot returns the configured listen port.
func (c *Config) PortSnapshot() int {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.Port
}

// SubtitlesSnapshot returns a consistent copy of the live subtitles settings.
// The ONLY sanctioned way to read cfg.Subtitles from runtime code.
func (c *Config) SubtitlesSnapshot() SubtitlesConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return c.Subtitles
}

// SetSubtitles atomically replaces the live subtitles settings (the write half
// of SubtitlesSnapshot). Callers that persist with Save keep the previous
// snapshot and restore it when Save fails (see SubtitlesUpdateSettings;
// MountsUpdate is the same snapshot→mutate→Save→rollback pattern).
func (c *Config) SetSubtitles(s SubtitlesConfig) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c.Subtitles = s
}
