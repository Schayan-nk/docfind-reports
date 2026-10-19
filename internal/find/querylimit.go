package find

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
)

// A ceiling on how fast one account may run report queries.
//
// Why this is narrow rather than the blanket limit the checklists ask for
// ---------------------------------------------------------------------
// The usual advice is a limit across the whole API -- a hundred requests every
// fifteen minutes is the figure most of them name. Applied here that would
// break the application with its own traffic: the home page polls the
// workflow queue and the calendar, and four endpoints polled every thirty
// seconds is already eight requests a minute, or a hundred and twenty in the
// window those numbers allow.
//
// So the limit goes where the cost is. /api/builder/run and
// /api/builder/detail hand a query somebody typed to somebody else's
// production database, which this application does not own and nobody here
// would like to explain a load spike to. Everything else the
// API does is served from memory or from a file this process already holds.
//
// The number is chosen to be invisible to a person and fatal to a loop. Nobody
// types sixty queries in a minute; a script with a mistake in it does sixty in
// a second. That is the only distinction this needs to draw -- it is not a
// defence against a determined administrator, who could run one query that
// takes an hour, and nothing here pretends otherwise.
const (
	queryBurst  = 60
	queryWindow = time.Minute
)

// queryLimiter counts recent queries per account.
type queryLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
	gcAt time.Time
}

func newQueryLimiter() *queryLimiter {
	return &queryLimiter{seen: map[string][]time.Time{}, gcAt: time.Now()}
}

// allow records one query and reports whether it may proceed.
//
// A sliding window of timestamps rather than a counter that resets: a counter
// reset on the minute lets twice the limit through across the boundary, which
// is exactly the burst this exists to stop.
func (q *queryLimiter) allow(key string) (bool, time.Duration) {
	now := time.Now()
	cutoff := now.Add(-queryWindow)

	q.mu.Lock()
	defer q.mu.Unlock()

	// Drop accounts nobody has used in a while, so a long-running process does
	// not accumulate one slice per person who ever signed in.
	if now.Sub(q.gcAt) > 10*time.Minute {
		for k, ts := range q.seen {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(q.seen, k)
			}
		}
		q.gcAt = now
	}

	kept := q.seen[key][:0]
	for _, t := range q.seen[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= queryBurst {
		// How long until the oldest falls out of the window, which is when
		// one more becomes possible.
		wait := queryWindow - now.Sub(kept[0])
		if wait < time.Second {
			wait = time.Second
		}
		q.seen[key] = kept
		return false, wait
	}
	q.seen[key] = append(kept, now)
	return true, 0
}

// limitQueries refuses a caller who is running report queries faster than a
// person could be reading them.
//
// Keyed on the account rather than the address: two administrators behind one
// office NAT are two people, and a limit that cannot tell them apart punishes
// the second for the first's work.
// oneAtATime lets each person run ONE builder query at a time.
//
// The rate limit above counts starts; it does not stop somebody with three
// tabs from holding three of the builder pool's four connections to another
// team's production database, each on a thirty-second query. A second query
// while the first is still running is refused with the reason, and the first
// one is unaffected.
func (s *Server) oneAtATime(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := "anonymous"
		if u := auth.UserFrom(r); u != nil {
			key = u.Username
		}
		if _, busy := s.builderBusy.LoadOrStore(key, struct{}{}); busy {
			writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{
				"error": "پرس‌وجوی قبلی شما هنوز در حال اجراست؛ صبر کنید تا تمام شود.",
			})
			return
		}
		defer s.builderBusy.Delete(key)
		next(w, r)
	}
}

func (s *Server) limitQueries(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := "anonymous"
		if u := auth.UserFrom(r); u != nil {
			key = u.Username
		}
		ok, wait := s.queryLimit.allow(key)
		if !ok {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds()+0.5)))
			writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{
				"error": "پرس‌وجوها خیلی سریع پشت سر هم اجرا شده‌اند؛ چند لحظه صبر کنید.",
			})
			return
		}
		next(w, r)
	}
}
