package main

import (
	"log"
	"os"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/push"
	"github.com/lgldsilva/jackui/internal/transfer"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func initHistoryStore(deps *appDeps) {
	store, err := history.New(deps.db)
	if err != nil {
		log.Printf("Warning: failed to open history store: %v — history disabled", err)
		return
	}
	deps.historyStore = store
	log.Printf("History store: PostgreSQL")
	go func() {
		for {
			time.Sleep(24 * time.Hour)
			// #nosec G104 -- limpeza periodica best-effort em background
			store.Cleanup(90 * 24 * time.Hour)
		}
	}()
}

func initLibraryStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	l, err := library.New(deps.db)
	if err != nil {
		log.Printf("Warning: library store init failed: %v", err)
		return
	}
	deps.libraryStore = l
	log.Printf("Library: PostgreSQL")
	if mc := deps.streamSrv.MetadataCache(); mc != nil {
		n, mErr := l.RefreshStalePrimary(func(hash string) (int, bool) {
			meta := mc.Get(hash)
			if meta == nil {
				return 0, false
			}
			return meta.PrimaryFile, true
		})
		if mErr != nil {
			log.Printf("Library: stale primary refresh failed: %v", mErr)
		} else if n > 0 {
			log.Printf("Library: refreshed %d stale primary_file_index entries from metadata cache", n)
		}
	}
}

// initAudioMetaStore opens the DEDICATED .audio-metadata.db (kept off the
// library/history handles so a lazy tag read on a slow mount never serialises
// behind a Continue-Watching page load). Optional: a failure just disables the
// tag/cover cache (handlers fall back to live parsing), it never blocks boot.
func initAudioMetaStore(deps *appDeps) {
	am, err := audiometa.New(deps.db)
	if err != nil {
		log.Printf("Warning: audio metadata store init failed: %v", err)
		return
	}
	deps.audioMetaStore = am
	log.Printf("Audio metadata: PostgreSQL")
}

func initPlaylistsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	p, err := playlists.New(deps.db)
	if err != nil {
		log.Printf("Warning: playlists store init failed: %v", err)
		return
	}
	deps.playlistsStore = p
	log.Printf("Playlists: PostgreSQL")
}

func initDownloadsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	d, err := downloads.New(deps.db)
	if err != nil {
		log.Printf("Warning: downloads store init failed: %v", err)
		return
	}
	deps.downloadsStore = d
	log.Printf("Downloads: PostgreSQL")

	// Pending-transfers store: persists move/promote copy intents so a deploy or
	// crash mid-copy resumes them on the next boot (resume-aware copy finishes
	// only what's left). Best-effort — a failure here just disables resume.
	if ts, err := transfer.OpenStore(deps.db); err != nil {
		log.Printf("Warning: pending-transfers store init failed (resume disabled): %v", err)
	} else {
		deps.pendingTransfers = ts
		handlers.ReconcilePendingTransfers(ts, deps.transferTracker, d, deps.streamSrv)
	}

	deps.streamSrv.SetFilePathResolver(func(h metainfo.Hash, fileIdx int) (string, bool) {
		// FileRelPath lets the store resolve files inside whole-torrent rows
		// (file_path = destination DIRECTORY) without activating the torrent.
		relPath := deps.streamSrv.FileRelPath(h, fileIdx)
		path, err := d.GetCompletedPathRel(h.HexString(), fileIdx, relPath, -1)
		if err != nil || path == "" {
			return "", false
		}
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			return "", false
		}
		return path, true
	})

	usernameResolver := func(userID int) string {
		if deps.authStore == nil {
			return ""
		}
		u, err := deps.authStore.GetUserByID(userID)
		if err != nil || u == nil {
			return ""
		}
		return u.Username
	}
	// Live queue settings: the worker reads this each tick, so PUT /downloads/settings
	// (which mutates deps.cfg.DownloadsQueue) takes effect without a restart.
	queueSettings := func() downloads.QueueSettings {
		q := deps.cfg.DownloadsQueue
		return downloads.QueueSettings{
			MaxActive:         q.MaxActive,
			PerUserMaxActive:  q.PerUserMaxActive,
			StallThresholdMin: q.StallThresholdMin,
			MaxStalls:         q.MaxStalls,
			AgingStepMin:      q.AgingStepMin,
			AgingCap:          q.AgingCap,
			RotationEnabled:   q.RotationEnabled,
			AutoPromoteArr:    q.AutoPromoteArr,
		}
	}
	worker := downloads.NewWorker(downloads.WorkerConfig{
		Store:           d,
		Streamer:        deps.streamSrv,
		DataDir:         deps.streamCfg.DataDir,
		DownloadDir:     deps.cfg.Stream.DownloadDir,
		SharedDir:       deps.cfg.Stream.SharedDir,
		Interval:        2 * time.Second,
		NtfyBaseURL:     deps.cfg.Notifications.NtfyBaseURL,
		NtfyTopic:       deps.cfg.Notifications.NtfyDefaultTopic,
		NtfyToken:       deps.cfg.Notifications.NtfyToken,
		ResolveUsername: usernameResolver,
		FallbackUser:    deps.cfg.Auth.AdminUsername,
		Settings:        queueSettings,
		Jackett:         deps.jackettClient,
		AIClient:        deps.aiClient,
		TMDBClient:      deps.tmdbClient,
		Tracker:         deps.transferTracker,
	})
	deps.downloadsWkr = worker
	worker.Start()
	deps.addCleanup(worker.Stop)
	log.Printf("Downloads worker started (tick=2s, ntfy=%q)", deps.cfg.Notifications.NtfyDefaultTopic)
}

// migrateUserSubpathMounts relocates loose files at the root of any per-user
// (UserSubpath) mount into the owner's subdir, so turning a previously-shared
// mount into per-user doesn't hide existing files. Ownership comes from the
// downloads store (file_path → user_id → username); files with no download
// record (manual uploads, external sources) fall back to the admin's subdir.
// Idempotent and logged — runs every boot, a no-op once everything is scoped.
func migrateUserSubpathMounts(deps *appDeps) {
	if deps.localBrowser == nil || deps.downloadsStore == nil || deps.authStore == nil {
		return
	}
	users, err := deps.authStore.ListUsers()
	if err != nil {
		log.Printf("Warning: usersubpath migration skipped (list users: %v)", err)
		return
	}
	known := make(map[string]bool, len(users))
	for _, u := range users {
		known[u.Username] = true
	}
	fallback := deps.cfg.Auth.AdminUsername
	attribute := buildOwnerAttributor(deps)

	for _, m := range deps.cfg.External.Mounts {
		if !m.UserSubpath {
			continue
		}
		res, err := deps.localBrowser.MigrateToUserSubpath(m.Name, known, fallback, attribute)
		if err != nil {
			log.Printf("Warning: usersubpath migration for %q failed: %v", m.Name, err)
			continue
		}
		logMigrationResult(m.Name, res)
	}
}

// buildOwnerAttributor returns an attribute func mapping an absolute path to
// its owner's username via the downloads store (file_path → user_id → username).
func buildOwnerAttributor(deps *appDeps) func(abs string) (string, bool) {
	return func(abs string) (string, bool) {
		dls, err := deps.downloadsStore.FindByPathPrefix(abs)
		if err != nil || len(dls) == 0 {
			return "", false
		}
		u, err := deps.authStore.GetUserByID(dls[0].UserID)
		if err != nil || u == nil {
			return "", false
		}
		return u.Username, true
	}
}

// logMigrationResult logs a per-mount migration summary and each moved entry;
// a no-op when nothing was moved.
func logMigrationResult(mount string, res local.MigrationResult) {
	if res.Conflicts > 0 {
		// A loose root entry couldn't be scoped because <user>/<name> already exists.
		// We no longer mint "name (1)" dups, so this just means a manual merge is due.
		log.Printf("UserSubpath migration %q: %d entr(ies) left at root — per-user dest already exists (manual merge needed)", mount, res.Conflicts)
	}
	if len(res.Moved) == 0 {
		return
	}
	log.Printf("UserSubpath migration %q: moved %d entr(ies), %d already scoped, %d active (.part, left in place)", mount, len(res.Moved), res.Skipped, res.Active)
	for _, e := range res.Moved {
		suffix := ""
		if e.Fallback {
			suffix = " (sem dono → admin)"
		}
		log.Printf("  • %s → %s/%s", e.Name, e.ToUser, e.Name+suffix)
	}
}

// initPushStore opens the Web Push / in-app feed store and prepares the sender
// (generating the VAPID pair on first boot). Best-effort: failure only logs —
// the app works without push.
func initPushStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	p, err := push.New(deps.db)
	if err != nil {
		log.Printf("Warning: push store init failed: %v", err)
		return
	}
	deps.pushStore = p
	sender, err := push.NewSender(p)
	if err != nil {
		log.Printf("Warning: push sender init failed: %v", err)
		return
	}
	deps.pushSender = sender
	log.Printf("Web Push: enabled (PostgreSQL)")
}

func initWatchlistStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	w, err := watchlist.New(deps.db)
	if err != nil {
		log.Printf("Warning: watchlist store init failed: %v", err)
		return
	}
	deps.watchlistStore = w
	log.Printf("Watchlist: PostgreSQL")
	interval := time.Duration(deps.cfg.Notifications.WatchlistInterval) * time.Minute
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	notifier := &watchlist.NtfyPoster{BaseURL: deps.cfg.Notifications.NtfyBaseURL, Token: deps.cfg.Notifications.NtfyToken}
	worker := watchlist.NewWorker(w, deps.jackettClient, notifier, deps.cfg.Notifications.NtfyDefaultTopic, interval)
	if deps.downloadsStore != nil {
		worker.SetEnqueuer(deps.downloadsStore)
	}
	if deps.pushSender != nil {
		worker.SetUserNotifier(deps.pushSender)
	}
	worker.Start()
	deps.watchlistWkr = worker
	deps.addCleanup(worker.Stop)
	log.Printf("Watchlist worker: per-item scheduling (default interval=%s) default_topic=%q", interval, deps.cfg.Notifications.NtfyDefaultTopic)
}
