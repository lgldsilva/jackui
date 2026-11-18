// Package events holds the Prometheus counters for incident-class events
// (HLS/transcode, ffprobe, downloads worker). It is a leaf package — it imports
// nothing from internal/ — so the packages that emit the events (transcode,
// streamer, downloads) can depend on it without a cycle through
// internal/metrics, which itself imports streamer and transcode.
//
// Cardinality contract: every label value is one of a small fixed set declared
// below. Callers never pass free-form strings (info_hash, file names, user ids,
// paths, error text); anything outside the set is folded into "other".
package events

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Fallback label value for anything outside a declared set.
const other = "other"

// HLS session failure reasons.
const (
	HLSFailStartError    = "start_error"       // buildSession failed (caps, source server, ffmpeg launch)
	HLSFailFFmpegExit    = "ffmpeg_exit"       // encoder exited non-zero on its own (not cancelled/superseded)
	HLSFailCUDAOOM       = "cuda_oom_fallback" // HW decoder OOM → relaunched in software decode
	HLSFailRelaunchError = "relaunch_error"    // the software-decode relaunch itself failed
)

// HLS admission events.
const (
	HLSAdmitRejectedCap = "rejected_cap"      // ErrTooManySessions → HTTP 429
	HLSAdmitEvictedLRU  = "evicted_owner_lru" // per-owner cap made room by stopping the owner's LRU session
)

// ffprobe call sites and failure reasons.
const (
	ProbeMedia    = "media"    // stream/local track probe (streamer.runFFprobe)
	ProbeDuration = "duration" // HLS duration probe (transcode.probeDurationSeekable)

	ProbeReasonTimeout    = "timeout"     // ctx deadline killed ffprobe
	ProbeReasonError      = "error"       // non-zero exit / exec failure
	ProbeReasonParse      = "parse"       // output was not the expected JSON
	ProbeReasonNoDuration = "no_duration" // probe ran but yielded no duration → EVENT fallback
)

// Download completion-move granularity.
const (
	DownloadKindSingle = "single" // one row (a file, or the whole-torrent sentinel)
	DownloadKindGroup  = "group"  // a multi-file selected torrent moved as one unit
)

var (
	hlsModes      = []string{"vod", "event"}
	hlsDecodes    = []string{"hw", "sw"}
	hlsFailures   = []string{HLSFailStartError, HLSFailFFmpegExit, HLSFailCUDAOOM, HLSFailRelaunchError}
	hlsAdmissions = []string{HLSAdmitRejectedCap, HLSAdmitEvictedLRU}
	probes        = []string{ProbeMedia, ProbeDuration}
	probeReasons  = []string{ProbeReasonTimeout, ProbeReasonError, ProbeReasonParse, ProbeReasonNoDuration}
	initOutcomes  = []string{"retry", "failed"}
	dlKinds       = []string{DownloadKindSingle, DownloadKindGroup}
)

var (
	// HLSSessionsStarted counts HLS sessions whose first ffmpeg launch succeeded.
	HLSSessionsStarted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_hls_sessions_started_total",
		Help: "HLS transcode sessions started, by playlist mode (vod|event) and decode path (hw|sw)",
	}, []string{"mode", "decode"})

	// HLSSessionFailures counts HLS session failures by reason class.
	HLSSessionFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_hls_session_failures_total",
		Help: "HLS transcode session failures by reason class",
	}, []string{"reason"})

	// HLSAdmission counts session-cap admission outcomes other than a plain admit.
	HLSAdmission = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_hls_admission_events_total",
		Help: "HLS session-cap events: rejected_cap (HTTP 429) and evicted_owner_lru",
	}, []string{"event"})

	// FFprobeFailures counts failed ffprobe invocations.
	FFprobeFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_ffprobe_failures_total",
		Help: "ffprobe failures by call site (media|duration) and reason class",
	}, []string{"probe", "reason"})

	// DownloadInitFailures counts downloads-worker init failures.
	DownloadInitFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_downloads_init_failures_total",
		Help: "Download init (EnsureActive/GotInfo) failures: retry = transient, failed = gave up after max retries",
	}, []string{"outcome"})

	// DownloadStalls counts no-seed stall demotions (one per torrent).
	DownloadStalls = promauto.NewCounter(prometheus.CounterOpts{
		Name: "jackui_downloads_stalls_total",
		Help: "Torrents demoted back to the queue by stall detection (no progress / no seed)",
	})

	// DownloadStallPauses counts torrents paused after reaching MaxStalls.
	DownloadStallPauses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "jackui_downloads_stall_pauses_total",
		Help: "Torrents paused after reaching the max-stalls limit",
	})

	// DownloadCompletions counts downloads finalized as completed.
	DownloadCompletions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_downloads_completions_total",
		Help: "Downloads finalized (completion move done), by unit kind (single|group)",
	}, []string{"kind"})

	// DownloadMoveFailures counts completion moves that gave up after retries.
	DownloadMoveFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_downloads_move_failures_total",
		Help: "Completion moves that failed for good (after retries), by unit kind (single|group)",
	}, []string{"kind"})
)

// Pre-create every bounded series at 0 so rate()/increase() work from the
// first scrape and dashboards don't show "no data" for incidents that never
// happened.
func init() {
	for _, m := range hlsModes {
		for _, d := range hlsDecodes {
			HLSSessionsStarted.WithLabelValues(m, d)
		}
	}
	initVec(HLSSessionFailures, hlsFailures)
	initVec(HLSAdmission, hlsAdmissions)
	for _, p := range probes {
		for _, r := range probeReasons {
			FFprobeFailures.WithLabelValues(p, r)
		}
	}
	initVec(DownloadInitFailures, initOutcomes)
	initVec(DownloadCompletions, dlKinds)
	initVec(DownloadMoveFailures, dlKinds)
}

func initVec(v *prometheus.CounterVec, values []string) {
	for _, val := range values {
		v.WithLabelValues(val)
	}
}

// bounded returns v when it is in allowed, else "other".
func bounded(v string, allowed []string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return other
}

// HLSSessionStarted records a session whose first launch succeeded.
func HLSSessionStarted(vod, swDecode bool) {
	mode, decode := "event", "hw"
	if vod {
		mode = "vod"
	}
	if swDecode {
		decode = "sw"
	}
	HLSSessionsStarted.WithLabelValues(mode, decode).Inc()
}

// HLSSessionFailed records a session failure of the given reason class.
func HLSSessionFailed(reason string) {
	HLSSessionFailures.WithLabelValues(bounded(reason, hlsFailures)).Inc()
}

// HLSAdmissionEvent records a cap rejection or an owner-LRU eviction.
func HLSAdmissionEvent(event string) {
	HLSAdmission.WithLabelValues(bounded(event, hlsAdmissions)).Inc()
}

// FFprobeFailed records an ffprobe failure with an explicit reason class.
func FFprobeFailed(probe, reason string) {
	FFprobeFailures.WithLabelValues(bounded(probe, probes), bounded(reason, probeReasons)).Inc()
}

// FFprobeExecFailed classifies a failed ffprobe exec by its context: a deadline
// is a timeout, anything else an error. A cancelled context (the client went
// away, shutdown) is not an ffprobe failure and is not counted.
func FFprobeExecFailed(ctx context.Context, probe string) {
	switch err := ctx.Err(); {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		FFprobeFailed(probe, ProbeReasonTimeout)
	default:
		FFprobeFailed(probe, ProbeReasonError)
	}
}

// DownloadInitFailed records a downloads-worker init failure; final is true
// when the retries are exhausted and the row goes `failed`.
func DownloadInitFailed(final bool) {
	outcome := "retry"
	if final {
		outcome = "failed"
	}
	DownloadInitFailures.WithLabelValues(outcome).Inc()
}

// DownloadStalled records one stall demotion of a torrent; paused is true when
// it also crossed MaxStalls and was paused.
func DownloadStalled(paused bool) {
	DownloadStalls.Inc()
	if paused {
		DownloadStallPauses.Inc()
	}
}

// DownloadCompleted records a finalized download of the given kind.
func DownloadCompleted(kind string) {
	DownloadCompletions.WithLabelValues(bounded(kind, dlKinds)).Inc()
}

// DownloadMoveFailed records a completion move that gave up.
func DownloadMoveFailed(kind string) {
	DownloadMoveFailures.WithLabelValues(bounded(kind, dlKinds)).Inc()
}
