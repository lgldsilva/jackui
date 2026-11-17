package streamer

import (
	"testing"
	"time"
)

// steadyCounters models a torrent moving 1000 B/s down and 500 B/s up since
// start, and counts how often the (client-locked) counters are read.
type steadyCounters struct {
	start time.Time
	now   time.Time
	reads int
}

func (c *steadyCounters) read() (int64, int64) {
	c.reads++
	ms := c.now.Sub(c.start).Milliseconds()
	return ms, ms / 2
}

// Two pollers on the usual 2s cadence, 100ms apart (downloads list + player).
// The second one used to land inside the first one's 250ms guard and read
// (0, 0), and it re-read the counters under the client lock every time.
func TestSampleRate_PollersShareOneSample(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	c := &steadyCounters{start: start}
	e := &entry{lastSampleAt: start}

	const ticks = 5
	for i := 1; i <= ticks; i++ {
		c.now = start.Add(time.Duration(i) * 2 * time.Second)
		if d, u := e.sampleRate(c.now, c.read); d != 1000 || u != 500 {
			t.Fatalf("tick %d poller A = (%d, %d), want (1000, 500)", i, d, u)
		}
		c.now = c.now.Add(100 * time.Millisecond)
		if d, u := e.sampleRate(c.now, c.read); d != 1000 || u != 500 {
			t.Fatalf("tick %d poller B = (%d, %d), want (1000, 500) — a second reader must see the shared rate", i, d, u)
		}
	}
	if c.reads != ticks {
		t.Fatalf("counters read %d times for %d sampling windows, want %d", c.reads, ticks, ticks)
	}
}

// A burst that lands between two close readers must not be divided by the
// short gap between them: the second reader keeps the shared rate.
func TestSampleRate_CloseReaderDoesNotSkew(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	var total int64
	counters := func() (int64, int64) { return total, 0 }
	e := &entry{lastSampleAt: start}

	total = 2000
	if d, _ := e.sampleRate(start.Add(2*time.Second), counters); d != 1000 {
		t.Fatalf("first window = %d, want 1000", d)
	}
	total = 12000 // 10 kB burst within 300ms
	if d, _ := e.sampleRate(start.Add(2300*time.Millisecond), counters); d != 1000 {
		t.Fatalf("close reader = %d, want the shared 1000 (not the burst over 300ms)", d)
	}
	// The burst is accounted for in the next full window: 10000 B over 2s.
	if d, _ := e.sampleRate(start.Add(4*time.Second), counters); d != 5000 {
		t.Fatalf("next window = %d, want 5000", d)
	}
}

func TestSampleRate_SeedClampAndClock(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	read, written := int64(4096), int64(1024)
	reads := 0
	counters := func() (int64, int64) { reads++; return read, written }

	// Zero sample time: seed from the counters and report nothing yet.
	e := &entry{}
	if d, u := e.sampleRate(start, counters); d != 0 || u != 0 {
		t.Fatalf("seed sample = (%d, %d), want (0, 0)", d, u)
	}
	if e.lastSampleAt != start || e.lastBytesRead != 4096 || e.lastBytesWritten != 1024 {
		t.Fatalf("seed state = %+v", e)
	}

	// Counter reset (torrent dropped and re-added): clamp to zero.
	read, written = 10, 10
	if d, u := e.sampleRate(start.Add(time.Second), counters); d != 0 || u != 0 {
		t.Fatalf("counter reset = (%d, %d), want (0, 0)", d, u)
	}

	read, written = 3010, 1510
	if d, u := e.sampleRate(start.Add(3*time.Second), counters); d != 1500 || u != 750 {
		t.Fatalf("window = (%d, %d), want (1500, 750)", d, u)
	}

	// A caller whose clock reading predates the stored sample (it took `now`
	// before waiting on the lock) reuses the sample instead of dividing by a
	// negative span.
	before := reads
	if d, u := e.sampleRate(start.Add(2*time.Second), counters); d != 1500 || u != 750 {
		t.Fatalf("stale clock = (%d, %d), want (1500, 750)", d, u)
	}
	if reads != before {
		t.Fatalf("stale clock read the counters %d extra time(s)", reads-before)
	}
}
