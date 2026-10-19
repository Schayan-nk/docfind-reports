package reports

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/obs"
)

// Slow-query logging for the reporting database.
//
// pgstore has its own version of this, hung off pgx's query tracer -- one
// hook that covers every query pgstore ever issues. This package cannot do
// that: it reaches its database through database/sql, which has no
// equivalent hook, and the driver varies with what an administrator
// configured (driver.go). So the same policy is applied at the two places in
// this file that actually call QueryContext -- Run, and streamWithTimeout,
// which Stream and streamLimited (and so Aggregate and MonthlySeries) both
// run through -- rather than at every report that gets executed.

// slowQueryThreshold is DOCFIND_SLOW_QUERY_MS, 500 ms by default -- the same
// setting and default pgstore's tracer reads, so one number governs both
// halves of the application's database traffic.
func slowQueryThreshold() time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DOCFIND_SLOW_QUERY_MS"))); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return 500 * time.Millisecond
}

// logSlowQuery records one query that took longer than the threshold: its
// duration, which path ran it, which configured report it belongs to, and the
// query's own SQL text -- never the argument values bound into it, which is
// where names, numbers and documents live.
func logSlowQuery(ctx context.Context, op, reportID, sql string, started time.Time) {
	d := time.Since(started)
	if d < slowQueryThreshold() {
		return
	}
	obs.Default().Write("slow_query", "warn", obs.Fields{
		"db":         "mysql",
		"ms":         float64(d.Microseconds()) / 1000,
		"op":         op,
		"report_id":  reportID,
		"sql":        compactSQL(sql, 200),
		"request_id": obs.RequestID(ctx),
	})
}

// compactSQL folds a statement's whitespace and caps its length so it fits on
// one log line.
func compactSQL(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
