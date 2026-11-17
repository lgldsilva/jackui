package streamer

import "time"

// rateWindow is the shortest span a rate is computed over. Readers arriving
// inside it reuse the stored rate.
const rateWindow = time.Second

// sampleRate returns the entry's per-second down/up rates. Caller holds s.mu.
//
// The sample is SHARED by every reader (downloads list, player, global stats,
// Transmission RPC). Each used to diff against whichever reader came before it
// and overwrite the sample: one arriving right after another read (0, 0), or a
// burst divided by a few hundred ms. Now a rate is only recomputed once a full
// rateWindow has passed and is cached in between, so readers agree and the
// anacrolix counters (client lock) are read at most once per window per
// torrent, whatever the number of pollers.
func (e *entry) sampleRate(now time.Time, counters func() (read, written int64)) (downRate, upRate int64) {
	seeded := !e.lastSampleAt.IsZero()
	elapsed := now.Sub(e.lastSampleAt)
	if seeded && elapsed < rateWindow {
		return e.downRate, e.upRate
	}

	br, bw := counters()
	e.downRate, e.upRate = 0, 0
	if seeded {
		// A negative delta is a counter reset (torrent dropped + re-added).
		e.downRate = int64(float64(max(br-e.lastBytesRead, 0)) / elapsed.Seconds())
		e.upRate = int64(float64(max(bw-e.lastBytesWritten, 0)) / elapsed.Seconds())
	}
	e.lastBytesRead = br
	e.lastBytesWritten = bw
	e.lastSampleAt = now
	return e.downRate, e.upRate
}
