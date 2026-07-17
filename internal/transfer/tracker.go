// Package transfer is a small in-memory tracker for long-running file
// move/copy operations (post-download move, Local-tab move, AI/promote), so the
// UI can show ONE consistent progress pattern for all of them: X/Y files, bytes
// done/total, transfer rate and ETA. It is the move-side analogue of
// internal/localstream (which meters streaming reads) and reuses the same
// windowed-rate idea. State is in-memory and lost on restart — for durable
// resume of the post-download move, see downloads.StatusMoving / RescueStuckMoving.
package transfer

import (
	"context"
	"io"
	"sync"
	"time"
)

// Status is the lifecycle of a transfer Job.
type Status string

const (
	// StatusQueued: registered but waiting for a concurrency slot (see Submit).
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// defaultMaxConcurrent bounds simultaneous transfers; the rest queue (FIFO via
// the semaphore). Mirrors the download queue's max_active idea: it avoids disk
// seek-thrashing on a single volume and bounds memory/FD/cache-protection.
const defaultMaxConcurrent = 3

const (
	// rateWindow is the sliding window used to compute the transfer rate
	// (mirrors internal/localstream's 3s window).
	rateWindow = 3 * time.Second
	// doneRetentionTTL keeps finished jobs visible briefly so the UI can show
	// the completion before they are pruned (lazy prune on List/Snapshot).
	doneRetentionTTL = 20 * time.Second
)

type sample struct {
	t time.Time
	n int64
}

// Snapshot is the immutable, JSON-serializable view of a Job for the UI.
type Snapshot struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	UserID     int     `json:"userId,omitempty"`
	Status     Status  `json:"status"`
	FilesDone  int     `json:"filesDone"`
	FilesTotal int     `json:"filesTotal"`
	BytesDone  int64   `json:"bytesDone"`
	BytesTotal int64   `json:"bytesTotal"`
	RatePerSec int64   `json:"ratePerSec"`
	ETASeconds int     `json:"etaSeconds"`
	Progress   float64 `json:"progress"` // 0..1 (by bytes when known, else by files)
	Error      string  `json:"error,omitempty"`
	StartedAt  string  `json:"startedAt"`
}

// Job is one tracked move/copy operation. Safe for concurrent use: producers
// call AddBytes/FileDone from the copy loop while the API reads Snapshot.
type Job struct {
	now func() time.Time // clock seam for tests

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	id         string
	label      string
	kind       string
	userID     int // owner; List/Cancel filter non-admin callers to their own jobs
	status     Status
	filesTotal int
	filesDone  int
	bytesTotal int64
	bytesDone  int64
	errMsg     string
	startedAt  time.Time
	updatedAt  time.Time
	samples    []sample
}

// ID returns the job's stable identifier (empty for a nil Job), so a producer can
// hand it back to the client to correlate with the dock entry.
func (j *Job) ID() string {
	if j == nil {
		return ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.id
}

// AddBytes records bytes transferred (drives progress + rate). No-op on <=0
// or a nil Job (so producers can stay agnostic to whether tracking is wired).
func (j *Job) AddBytes(n int64) {
	if j == nil || n <= 0 {
		return
	}
	j.mu.Lock()
	j.bytesDone += n
	now := j.now()
	j.updatedAt = now
	j.samples = append(j.samples, sample{now, n})
	j.pruneLocked(now)
	j.mu.Unlock()
}

// AddBytesFunc returns a callback for instrumenting an io.Copy via ProgressReader.
func (j *Job) AddBytesFunc() func(int64) {
	if j == nil {
		return func(int64) { /* no-op: nil Job has no progress to record */ }
	}
	return j.AddBytes
}

// AddSkipped advances progress by bytes that were NOT copied now because they
// already existed at the destination (a resumed transfer skipping a file that a
// previous run finished). Counts toward done/total/progress but does NOT enter
// the rate window — otherwise skipping a large file instantly would spike the
// reported speed to an absurd value.
func (j *Job) AddSkipped(n int64) {
	if j == nil || n <= 0 {
		return
	}
	j.mu.Lock()
	j.bytesDone += n
	j.updatedAt = j.now()
	j.mu.Unlock()
}

// FileDone increments the completed-files counter (X of Y).
func (j *Job) FileDone() {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.filesDone++
	j.updatedAt = j.now()
	j.mu.Unlock()
}

// SetBytesTotal sets/raises the known total byte size (when discovered late).
func (j *Job) SetBytesTotal(total int64) {
	if j == nil || total < 0 {
		return
	}
	j.mu.Lock()
	j.bytesTotal = total
	j.mu.Unlock()
}

// Done marks the job successful. Fail marks it failed with the error message.
func (j *Job) Done() { j.finish(StatusDone, "") }
func (j *Job) Fail(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	j.finish(StatusFailed, msg)
}

func (j *Job) finish(s Status, msg string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	// A canceled job stays canceled — a late Done()/Fail() from the producer
	// (which only notices the cancellation after it returns) must not resurrect it.
	if j.status != StatusCanceled {
		j.status = s
		j.errMsg = msg
		j.updatedAt = j.now()
	}
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel() // release the context now that the job is terminal
	}
}

// Context returns the job's cancellation context (Background for a nil job). A
// producer should pass it to its copy loop / retries so Tracker.Cancel aborts
// the work in flight.
func (j *Job) Context() context.Context {
	if j == nil || j.ctx == nil {
		return context.Background()
	}
	return j.ctx
}

// Canceled reports whether the job was canceled via Tracker.Cancel.
func (j *Job) Canceled() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status == StatusCanceled
}

// markCanceled flips a non-terminal job to canceled and cancels its context.
// Returns false if the job was already terminal (nothing to cancel).
func (j *Job) markCanceled() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	if j.status == StatusDone || j.status == StatusFailed || j.status == StatusCanceled {
		j.mu.Unlock()
		return false
	}
	j.status = StatusCanceled
	j.updatedAt = j.now()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (j *Job) pruneLocked(now time.Time) {
	cut := now.Add(-rateWindow)
	i := 0
	for i < len(j.samples) && j.samples[i].t.Before(cut) {
		i++
	}
	if i > 0 {
		j.samples = j.samples[i:]
	}
}

func (j *Job) rateLocked(now time.Time) int64 {
	j.pruneLocked(now)
	var sum int64
	for _, s := range j.samples {
		sum += s.n
	}
	if sum == 0 {
		return 0
	}
	return int64(float64(sum) / rateWindow.Seconds())
}

// Snapshot returns the current immutable view.
func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	rate := int64(0)
	if j.status == StatusRunning {
		rate = j.rateLocked(now)
	}
	progress := 0.0
	switch {
	case j.bytesTotal > 0:
		progress = float64(j.bytesDone) / float64(j.bytesTotal)
	case j.filesTotal > 0:
		progress = float64(j.filesDone) / float64(j.filesTotal)
	}
	if progress > 1 {
		progress = 1
	}
	eta := 0
	if rate > 0 && j.bytesTotal > j.bytesDone {
		eta = int((j.bytesTotal - j.bytesDone) / rate)
	}
	return Snapshot{
		ID: j.id, Label: j.label, Kind: j.kind, UserID: j.userID, Status: j.status,
		FilesDone: j.filesDone, FilesTotal: j.filesTotal,
		BytesDone: j.bytesDone, BytesTotal: j.bytesTotal,
		RatePerSec: rate, ETASeconds: eta, Progress: progress,
		Error: j.errMsg, StartedAt: j.startedAt.Format(time.RFC3339),
	}
}

func (j *Job) finishedBefore(t time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	// Only terminal jobs are prunable — a queued job (waiting for a slot) must
	// never be reaped just because its updatedAt is old.
	return (j.status == StatusDone || j.status == StatusFailed || j.status == StatusCanceled) && j.updatedAt.Before(t)
}

// markRunning flips a queued job to running and (re)stamps startedAt so rate/ETA
// measure from the actual start, not from when it was enqueued.
func (j *Job) markRunning() {
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.status == StatusQueued {
		j.status = StatusRunning
		now := j.now()
		j.startedAt = now
		j.updatedAt = now
	}
	j.mu.Unlock()
}

// Tracker holds the active and recently-finished jobs.
type Tracker struct {
	now func() time.Time

	mu   sync.Mutex
	seq  int64
	jobs []*Job // insertion order (newest last)

	// sem bounds concurrent Submit() jobs; the rest wait (queued). nil = unbounded
	// (e.g. a hand-built Tracker in tests that only uses Start()).
	sem chan struct{}
}

// New returns a Tracker using the wall clock. maxConcurrent (optional, default 3)
// caps simultaneous Submit() transfers; excess ones queue.
func New(maxConcurrent ...int) *Tracker {
	n := defaultMaxConcurrent
	if len(maxConcurrent) > 0 && maxConcurrent[0] > 0 {
		n = maxConcurrent[0]
	}
	return &Tracker{now: time.Now, sem: make(chan struct{}, n)}
}

// Start registers and returns a new RUNNING Job (no queueing). filesTotal/
// bytesTotal may be 0 when unknown (rate/ETA degrade gracefully). userID=0 is
// anonymous/system; prefer StartFor when the owner is known.
func (t *Tracker) Start(label, kind string, filesTotal int, bytesTotal int64) *Job {
	return t.StartFor(0, label, kind, filesTotal, bytesTotal)
}

// StartFor is Start with an explicit owner userID for multi-tenant filtering.
func (t *Tracker) StartFor(userID int, label, kind string, filesTotal int, bytesTotal int64) *Job {
	return t.startJob(userID, label, kind, filesTotal, bytesTotal, StatusRunning)
}

func (t *Tracker) startJob(userID int, label, kind string, filesTotal int, bytesTotal int64, status Status) *Job {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.seq++
	now := t.now()
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		now: t.now, id: idFor(t.seq, now), label: label, kind: kind, userID: userID,
		status: status, filesTotal: filesTotal, bytesTotal: bytesTotal,
		startedAt: now, updatedAt: now, ctx: ctx, cancel: cancel,
	}
	t.jobs = append(t.jobs, j)
	t.pruneLocked(now)
	t.mu.Unlock()
	return j
}

// Submit registers a job that starts QUEUED and runs fn once a concurrency slot
// frees (bounded by maxConcurrent; excess jobs wait FIFO). fn receives the now-
// running Job and owns its terminal Done()/Fail(). Returns immediately. On a nil
// Tracker, fn runs unbounded in a goroutine with a nil Job (tracking disabled).
func (t *Tracker) Submit(label, kind string, filesTotal int, bytesTotal int64, fn func(*Job)) *Job {
	return t.SubmitFor(0, label, kind, filesTotal, bytesTotal, fn)
}

// SubmitFor is Submit with an explicit owner userID.
func (t *Tracker) SubmitFor(userID int, label, kind string, filesTotal int, bytesTotal int64, fn func(*Job)) *Job {
	if t == nil {
		go fn(nil)
		return nil
	}
	j := t.startJob(userID, label, kind, filesTotal, bytesTotal, StatusQueued)
	go func() {
		if t.sem != nil {
			t.sem <- struct{}{} // blocks while queued
			defer func() { <-t.sem }()
		}
		j.markRunning()
		fn(j)
	}()
	return j
}

// Cancel cancels a tracked job by ID. When includeAll is false, only a job owned
// by userID may be canceled (userID 0 jobs are system-owned and cancelable by anyone).
func (t *Tracker) Cancel(id string, userID int, includeAll bool) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	var target *Job
	for _, j := range t.jobs {
		if j.ID() != id {
			continue
		}
		j.mu.Lock()
		owner := j.userID
		j.mu.Unlock()
		if includeAll || owner == 0 || owner == userID {
			target = j
		}
		break
	}
	t.mu.Unlock()
	if target == nil {
		return false
	}
	target.markCanceled()
	return true
}

// List returns snapshots of jobs (newest first). When includeAll is false, only
// jobs owned by userID (plus system jobs with userID 0) are included.
func (t *Tracker) List(userID int, includeAll bool) []Snapshot {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.pruneLocked(t.now())
	jobs := append([]*Job(nil), t.jobs...)
	t.mu.Unlock()
	out := make([]Snapshot, 0, len(jobs))
	for i := len(jobs) - 1; i >= 0; i-- { // newest first
		snap := jobs[i].Snapshot()
		if includeAll || snap.UserID == 0 || snap.UserID == userID {
			out = append(out, snap)
		}
	}
	return out
}

// ActiveCount returns how many jobs are still Queued or Running. Used by the
// graceful shutdown to decide whether to wait for in-flight moves.
func (t *Tracker) ActiveCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	jobs := append([]*Job(nil), t.jobs...)
	t.mu.Unlock()
	n := 0
	for _, j := range jobs {
		j.mu.Lock()
		s := j.status
		j.mu.Unlock()
		if s == StatusQueued || s == StatusRunning {
			n++
		}
	}
	return n
}

// WaitIdle blocks until no job is Queued/Running or ctx is done, polling every
// 200ms. Best-effort: anything still in flight when ctx expires is left to the
// durable boot rescue (downloads.RescueStuckMoving). Returns true if it drained.
func (t *Tracker) WaitIdle(ctx context.Context) bool {
	if t == nil {
		return true
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if t.ActiveCount() == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func (t *Tracker) pruneLocked(now time.Time) {
	cut := now.Add(-doneRetentionTTL)
	kept := t.jobs[:0]
	for _, j := range t.jobs {
		if !j.finishedBefore(cut) {
			kept = append(kept, j)
		}
	}
	t.jobs = kept
}

func idFor(seq int64, now time.Time) string {
	return "t" + itoa(now.UnixNano()) + "-" + itoa(seq)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ProgressReader wraps r so each Read reports the byte count via onBytes —
// drop-in for io.Copy(dst, ProgressReader(src, job.AddBytes)).
func ProgressReader(r io.Reader, onBytes func(int64)) io.Reader {
	return ProgressReaderCtx(context.Background(), r, onBytes)
}

// ProgressReaderCtx is ProgressReader that also aborts the copy when ctx is
// canceled (returns ctx.Err() from Read), so a Tracker.Cancel stops an
// in-progress file copy mid-stream. nil ctx → no cancellation (plain progress).
func ProgressReaderCtx(ctx context.Context, r io.Reader, onBytes func(int64)) io.Reader {
	if onBytes == nil && ctx == nil {
		return r
	}
	return &progressReader{ctx: ctx, r: r, onBytes: onBytes}
}

type progressReader struct {
	ctx     context.Context
	r       io.Reader
	onBytes func(int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	if pr.ctx != nil {
		if err := pr.ctx.Err(); err != nil {
			return 0, err
		}
	}
	n, err := pr.r.Read(p)
	if n > 0 && pr.onBytes != nil {
		pr.onBytes(int64(n))
	}
	return n, err
}
