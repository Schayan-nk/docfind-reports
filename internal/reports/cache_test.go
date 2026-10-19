package reports

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Single-flight is the property that actually protects the database, so it is
// the one tested hardest. A plain TTL cache would still let N simultaneous
// misses become N concurrent query storms against a pool of eight
// connections, which is the pile-up this exists to prevent.
func TestCacheCollapsesConcurrentMisses(t *testing.T) {
	c := newResultCache(time.Minute)

	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]any, 20)
	fromCache := make([]bool, 20)

	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, hit, err := c.do("analysis", func() (any, error) {
				calls.Add(1)
				<-release // hold every caller inside the single flight
				return "computed", nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i], fromCache[i] = v, hit
		}(i)
	}

	// Let the goroutines pile up on the same key before anything completes.
	time.Sleep(80 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("the work ran %d times for 20 concurrent callers; single-flight collapsed nothing", got)
	}
	for i, v := range results {
		if v != "computed" {
			t.Errorf("caller %d got %v, want the shared result", i, v)
		}
	}
	// Exactly one caller did the work; the rest were served from the entry.
	joined := 0
	for _, hit := range fromCache {
		if hit {
			joined++
		}
	}
	if joined != 19 {
		t.Errorf("%d callers reported a cache hit, want 19", joined)
	}
}

func TestCacheReusesWithinTTLAndRecomputesAfter(t *testing.T) {
	c := newResultCache(120 * time.Millisecond)
	var calls int

	compute := func() (any, error) { calls++; return calls, nil }

	if _, hit, _ := c.do("k", compute); hit {
		t.Error("the first call reported a cache hit")
	}
	if _, hit, _ := c.do("k", compute); !hit {
		t.Error("a call inside the TTL recomputed instead of reusing")
	}
	if calls != 1 {
		t.Fatalf("computed %d times inside the TTL, want 1", calls)
	}

	time.Sleep(160 * time.Millisecond)
	if _, hit, _ := c.do("k", compute); hit {
		t.Error("a call after the TTL was served from cache")
	}
	if calls != 2 {
		t.Errorf("computed %d times in total, want 2", calls)
	}
}

// A transient failure must not be served for the whole TTL: that turns a
// two-second database blip into a minute of a broken page.
func TestCacheDoesNotCacheErrors(t *testing.T) {
	c := newResultCache(time.Minute)
	boom := errors.New("connection refused")
	var calls int

	for i := range 3 {
		_, _, err := c.do("k", func() (any, error) { calls++; return nil, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("call %d returned %v, want the error", i, err)
		}
	}
	if calls != 3 {
		t.Errorf("the failing work ran %d times, want 3: an error was cached", calls)
	}

	// And a success after the failures must be reachable immediately.
	v, _, err := c.do("k", func() (any, error) { return "ok", nil })
	if err != nil || v != "ok" {
		t.Errorf("recovery returned (%v, %v)", v, err)
	}
}

func TestCacheKeysAreIndependent(t *testing.T) {
	c := newResultCache(time.Minute)
	a, _, _ := c.do("window:all", func() (any, error) { return "A", nil })
	b, _, _ := c.do("window:1404", func() (any, error) { return "B", nil })
	if a != "A" || b != "B" {
		t.Errorf("keys bled into each other: got %v and %v", a, b)
	}
}

// A zero TTL or a nil cache must degrade to calling straight through, so the
// caching layer can never be the reason a report stops working.
func TestCacheDisabledPassesThrough(t *testing.T) {
	var calls int
	compute := func() (any, error) { calls++; return calls, nil }

	for _, c := range []*resultCache{nil, newResultCache(0)} {
		calls = 0
		if _, hit, _ := c.do("k", compute); hit {
			t.Error("a disabled cache reported a hit")
		}
		if _, _, _ = c.do("k", compute); calls != 2 {
			t.Errorf("a disabled cache computed %d times, want 2", calls)
		}
	}
}
