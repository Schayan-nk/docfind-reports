package reports

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// A short-lived result cache with single-flight, in front of the expensive
// read paths.
//
// The problem it solves is not speed, it is pile-up. A page load asks for a
// report's table, chart and monthly summary at once, against a pool capped at
// eight connections (see Open) -- and that pool usually points at a database
// another application uses in production. A handful of people opening the
// same report at once is enough to occupy every connection and leave that
// application waiting behind them.
//
// Single-flight is the half that matters. A plain TTL cache still lets N
// simultaneous misses become N concurrent query storms; collapsing them into
// one shared computation is what bounds the load. The TTL then keeps a busy
// dashboard off the database entirely between refreshes.
//
// Errors are deliberately NOT cached. A transient failure -- the DB
// restarting, a dropped connection -- would otherwise be served for the whole
// TTL, turning a two-second blip into a minute of a broken page.
type resultCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	ready chan struct{} // closed once val/err are set
	val   any
	err   error
	at    time.Time
}

func newResultCache(ttl time.Duration) *resultCache {
	return &resultCache{ttl: ttl, entries: map[string]*cacheEntry{}}
}

// do returns the cached value for key, or computes it exactly once on behalf
// of every caller that asks while the computation is in flight.
//
// The second return value reports whether the value came from an existing
// entry rather than this caller's own work, which is what the caller logs or
// surfaces as "cached".
func (c *resultCache) do(key string, fn func() (any, error)) (any, bool, error) {
	if c == nil || c.ttl <= 0 {
		v, err := fn()
		return v, false, err
	}

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		// An entry that is still being computed is joined rather than
		// duplicated, however stale it might turn out to be: the alternative
		// is starting a second identical query storm.
		inFlight := false
		select {
		case <-e.ready:
		default:
			inFlight = true
		}
		if inFlight || time.Since(e.at) < c.ttl {
			c.mu.Unlock()
			<-e.ready
			return e.val, true, e.err
		}
		// Expired and finished: fall through and replace it.
	}

	e := &cacheEntry{ready: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	e.val, e.err = fn()
	e.at = time.Now()
	close(e.ready)

	if e.err != nil {
		// Do not serve a failure for the rest of the TTL.
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return e.val, false, e.err
}

// filterKey renders a set of filter values so it can key a cache entry.
//
// Sorted, because Go's map iteration order is deliberately random and an
// unsorted rendering would produce a different key for the same filters and
// make the cache miss every time -- which is the failure that looks like it is
// working.
func filterKey(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	names := make([]string, 0, len(values))
	for k, v := range values {
		if strings.TrimSpace(v) == "" {
			continue // an empty filter is the same as an absent one
		}
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('=')
		b.WriteString(values[n])
		b.WriteByte(';')
	}
	return b.String()
}
