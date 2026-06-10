package watchlist

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// Searcher is the subset of jackett.Client the worker depends on. Stays small
// so unit tests can fake it without spinning up an HTTP server.
type Searcher interface {
	Search(query, category string, indexers []string) ([]jackett.Result, error)
}

// Notifier publishes the new hit to the user's chosen channel. Implementations:
// the live ntfy.sh poster (NtfyPoster) and a test-friendly recorder.
type Notifier interface {
	Notify(ctx context.Context, topic, title, body, magnet string) error
}

// Enqueuer pushes a hit straight into the downloads queue — implemented by
// downloads.(*Store).EnqueueMagnet. Kept as an interface so tests can record
// calls without a real store.
type Enqueuer interface {
	EnqueueMagnet(userID int, infoHash, name, magnet, tracker string) error
}

// UserNotifier delivers a hit to the user's own channels (in-app feed + Web
// Push) — implemented by push.(*Sender). Runs alongside ntfy, which remains
// topic-based.
type UserNotifier interface {
	NotifyUser(ctx context.Context, userID int, title, body, magnet string) error
}

// maxAutoPerPass caps auto-downloads per watchlist per worker pass so a sudden
// burst of matching releases can't flood the queue in one sweep — the rest are
// still recorded as seen and notified, never silently dropped.
const maxAutoPerPass = 3

// Worker is a per-item scheduler: every tick (1 min) it checks only the
// watchlists whose next_check_at is due, then re-arms them from their own
// schedule. Kick(id) short-circuits the wait for a single item (used right after
// create so the user gets instant feedback). Stateless; safe to stop+restart.
type Worker struct {
	store        *Store
	searcher     Searcher
	notifier     Notifier
	enqueuer     Enqueuer     // optional: enables auto-download (nil = notify-only)
	userNotifier UserNotifier // optional: in-app feed + Web Push per user
	defaultTopic string
	interval     time.Duration // server default for "interval" items with Minutes <= 0
	tick         time.Duration // scheduler resolution (1 min; tests shrink it)
	startDelay   time.Duration // boot grace before the first pass (tests shrink it)
	kick         chan int      // watchlist IDs to check immediately
	stop         chan struct{}
	stopOnce     sync.Once      // guards close(stop) against a double Stop() panic
	wg           sync.WaitGroup // Stop() waits for the goroutine to actually exit
}

// SetEnqueuer enables auto-download by wiring the downloads queue. Call before
// Start — the worker goroutine reads the field without a lock.
func (w *Worker) SetEnqueuer(e Enqueuer) { w.enqueuer = e }

func NewWorker(store *Store, searcher Searcher, notifier Notifier, defaultTopic string, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if store != nil && store.DefaultEvery <= 0 {
		store.DefaultEvery = interval
	}
	return &Worker{
		store:        store,
		searcher:     searcher,
		notifier:     notifier,
		defaultTopic: defaultTopic,
		interval:     interval,
		tick:         time.Minute,
		startDelay:   30 * time.Second,
		kick:         make(chan int, 16),
		stop:         make(chan struct{}),
	}
}

// SetUserNotifier wires the per-user channel (in-app feed + Web Push). Call
// before Start — the worker goroutine reads the field without a lock.
func (w *Worker) SetUserNotifier(n UserNotifier) { w.userNotifier = n }

func (w *Worker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		// Don't fire instantly on startup — give jackett a beat to come up after
		// a container restart and avoid racing with library/auth migrations. But
		// make the wait INTERRUPTIBLE: a fast shutdown shouldn't block ~30s, nor
		// run a full poll against stores that are already closing.
		select {
		case <-w.stop:
			return
		case <-time.After(w.startDelay):
		}
		w.runDue()
		t := time.NewTicker(w.tick)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.runDue()
			case id := <-w.kick:
				w.checkOne(id)
			}
		}
	}()
}

// Kick schedules an immediate background check of one watchlist (e.g. right
// after create). Non-blocking: if the buffer is full the regular scheduled
// pass covers it. Safe on a nil Worker so handlers can stay nil-tolerant.
func (w *Worker) Kick(id int) {
	if w == nil {
		return
	}
	select {
	case w.kick <- id:
	default:
	}
}

// Stop signals the worker and waits for its goroutine to exit. sync.Once makes
// a second call safe (double close(stop) would otherwise panic); WaitGroup
// ensures no runOnce is still touching the stores after Stop returns.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
	w.wg.Wait()
}

// RunOnce checks every watchlist regardless of schedule — tests / manual triggers.
func (w *Worker) RunOnce() {
	lists, err := w.store.ListAll()
	if err != nil {
		log.Printf("watchlist: ListAll failed: %v", err)
		return
	}
	w.processLists(lists)
}

// runDue is the scheduled pass: only items whose next_check_at is due.
func (w *Worker) runDue() {
	lists, err := w.store.ListDue(time.Now())
	if err != nil {
		log.Printf("watchlist: ListDue failed: %v", err)
		return
	}
	w.processLists(lists)
}

// checkOne handles a Kick: immediate check of a single watchlist.
func (w *Worker) checkOne(id int) {
	wl, err := w.store.getByID(id)
	if err != nil {
		log.Printf("watchlist[%d]: kick lookup failed: %v", id, err)
		return
	}
	w.processLists([]Watchlist{*wl})
}

func (w *Worker) processLists(lists []Watchlist) {
	if len(lists) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, wl := range lists {
		w.processOne(ctx, &wl)
	}
}

func (w *Worker) processOne(ctx context.Context, wl *Watchlist) {
	// Re-arm BEFORE searching: a failing Jackett must not leave the item "due"
	// forever, or every scheduler tick would retry it. last_checked therefore
	// records the last ATTEMPT, matching the old fixed-interval behaviour.
	next := nextCheckTime(wl.Schedule, time.Now(), w.interval)
	if err := w.store.MarkChecked(wl.ID, next); err != nil {
		log.Printf("watchlist[%d]: MarkChecked failed: %v", wl.ID, err)
	}
	results, err := w.searcher.Search(wl.Query, wl.Category, nil)
	if err != nil {
		log.Printf("watchlist[%d]: jackett search failed: %v", wl.ID, err)
		return
	}
	topic := w.resolveTopic(wl)
	// The first pass after create (or after a query edit) only seeds the
	// "seen" baseline: auto-downloading the entire current result set of a
	// fresh query would flood the queue with releases the user already had
	// the chance to pick manually.
	autoBudget := maxAutoPerPass
	if wl.LastChecked.IsZero() {
		autoBudget = 0
	}
	for _, r := range results {
		if w.processOneResult(ctx, wl, topic, r, autoBudget > 0) {
			autoBudget--
		}
	}
}

func (w *Worker) resolveTopic(wl *Watchlist) string {
	if wl.NtfyTopic != "" {
		return wl.NtfyTopic
	}
	return w.defaultTopic
}

// processOneResult handles a single Jackett result; returns true when the hit
// was auto-enqueued into the downloads queue (consuming auto-download budget).
func (w *Worker) processOneResult(ctx context.Context, wl *Watchlist, topic string, r jackett.Result, allowAuto bool) bool {
	if r.Seeders < wl.MinSeeders {
		return false
	}
	if r.InfoHash == "" {
		return false
	}
	isNew, err := w.store.MarkSeen(wl.ID, r.InfoHash, r.Title, pickMagnet(r), r.Seeders, r.Size)
	if err != nil {
		log.Printf("watchlist[%d]: MarkSeen failed: %v", wl.ID, err)
		return false
	}
	if !isNew {
		return false
	}
	auto := w.maybeAutoDownload(wl, r, allowAuto)
	body := fmt.Sprintf("%d seeders · %s", r.Seeders, humanSize(r.Size))
	if auto {
		body = "⬇ na fila de downloads · " + body
	}
	// Per-user channel (in-app feed + Web Push) — independent of ntfy topics.
	if w.userNotifier != nil {
		if err := w.userNotifier.NotifyUser(ctx, wl.UserID, r.Title, body, pickMagnet(r)); err != nil {
			log.Printf("watchlist[%d]: user notify failed: %v", wl.ID, err)
		}
	}
	if topic == "" || w.notifier == nil {
		return auto
	}
	if err := w.notifier.Notify(ctx, topic, r.Title, body, pickMagnet(r)); err != nil {
		log.Printf("watchlist[%d]: notify failed: %v", wl.ID, err)
	}
	return auto
}

// maybeAutoDownload enqueues the hit when the watchlist opted in and the
// release passes the quality filters. Best-effort: an enqueue failure only
// logs — the user still gets the regular notification with the magnet.
func (w *Worker) maybeAutoDownload(wl *Watchlist, r jackett.Result, allowAuto bool) bool {
	if !allowAuto || !wl.AutoDownload || w.enqueuer == nil {
		return false
	}
	if !wl.MatchesFilters(r.Title, r.Size) {
		return false
	}
	if err := w.enqueuer.EnqueueMagnet(wl.UserID, r.InfoHash, r.Title, pickMagnet(r), r.Tracker); err != nil {
		log.Printf("watchlist[%d]: auto-download enqueue failed: %v", wl.ID, err)
		return false
	}
	if err := w.store.MarkAutoDownloaded(wl.ID, r.InfoHash); err != nil {
		log.Printf("watchlist[%d]: MarkAutoDownloaded failed: %v", wl.ID, err)
	}
	log.Printf("watchlist[%d]: auto-download enqueued %q (user %d)", wl.ID, r.Title, wl.UserID)
	return true
}

func pickMagnet(r jackett.Result) string {
	if r.MagnetURI != "" {
		return r.MagnetURI
	}
	return r.Link
}

func humanSize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / k
	u := 0
	for v >= k && u < len(units)-1 {
		v /= k
		u++
	}
	return fmt.Sprintf("%.2f %s", v, units[u])
}

// NtfyPoster posts to https://ntfy.sh/<topic> with a magnet click-action.
// Uses the public ntfy.sh by default; can point to a self-hosted instance.
type NtfyPoster struct {
	BaseURL string // default "https://ntfy.sh"
	Token   string // optional access token for protected topics (Authorization: Bearer)
	Client  *http.Client
}

func (n *NtfyPoster) Notify(ctx context.Context, topic, title, body, magnet string) error {
	if topic == "" {
		return nil
	}
	base := n.BaseURL
	if base == "" {
		base = "https://ntfy.sh"
	}
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	url := fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), topic)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "jackui,torrent")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	if magnet != "" {
		req.Header.Set("Actions", fmt.Sprintf("view, Abrir magnet, %s", magnet))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %d", resp.StatusCode)
	}
	return nil
}
