package reports

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A tiny fake database/sql driver, registered once per test under a unique
// name, so Run/Aggregate/MonthlySeries can be exercised end to end without a
// real MySQL connection. It answers every query with the same fixed rows and
// counts how many times it was actually asked -- the property the report
// cache exists to bound.
type fakeReportDriver struct {
	calls atomic.Int32
	cols  []string
	rows  [][]driver.Value
	// hook, when set, runs inside Query before returning -- used to hold
	// concurrent callers inside a single query the way a real slow query
	// would, so a single-flight collapse can be observed rather than assumed.
	hook func()
}

func (f *fakeReportDriver) Open(name string) (driver.Conn, error) { return &fakeReportConn{f}, nil }

type fakeReportConn struct{ d *fakeReportDriver }

func (c *fakeReportConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("fakeReportConn: Prepare unsupported; Query is used directly")
}
func (c *fakeReportConn) Close() error { return nil }
func (c *fakeReportConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fakeReportConn: transactions unsupported")
}

// Query implements driver.Queryer, which is what lets database/sql call this
// directly from QueryContext without ever needing Prepare.
func (c *fakeReportConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	c.d.calls.Add(1)
	if c.d.hook != nil {
		c.d.hook()
	}
	rows := make([][]driver.Value, len(c.d.rows))
	copy(rows, c.d.rows)
	return &fakeReportRows{cols: c.d.cols, rows: rows}, nil
}

type fakeReportRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *fakeReportRows) Columns() []string { return r.cols }
func (r *fakeReportRows) Close() error      { return nil }
func (r *fakeReportRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

// driverSeq gives each test its own driver name; sql.Register panics on a
// name registered twice, and tests in this file run concurrently with the
// rest of the package.
var driverSeq atomic.Int64

// newFakeReportDB wires a *DB straight to a fake in-process driver, with its
// own report cache at the given TTL -- short for expiry tests, long enough to
// not race for single-flight ones.
func newFakeReportDB(t *testing.T, fd *fakeReportDriver, ttl time.Duration) *DB {
	t.Helper()
	name := fmt.Sprintf("fakereport-%d", driverSeq.Add(1))
	sql.Register(name, fd)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", name, err)
	}
	t.Cleanup(func() { db.Close() })
	return &DB{
		db:          db,
		timeout:     5 * time.Second,
		reportCache: newResultCache(ttl),
	}
}

func countReport(id string) Report {
	return Report{ID: id, SQL: "SELECT status FROM widgets"}
}

// N concurrent callers asking for the same report and filters must reach the
// database once, joined on the entry the first caller started -- the same
// pile-up the analysis cache was built to prevent (see cache.go), now also
// guarding the per-report paths.
func TestRunCacheCollapsesConcurrentCallers(t *testing.T) {
	release := make(chan struct{})
	var inFlight atomic.Int32
	fd := &fakeReportDriver{
		cols: []string{"status"},
		rows: [][]driver.Value{{[]byte("open")}},
		hook: func() { inFlight.Add(1); <-release },
	}
	d := newFakeReportDB(t, fd, time.Minute)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := d.Run(context.Background(), countReport("widget"),
				map[string]string{"status": "open"}, 1, 50)
			errs[i] = err
			if err == nil && (len(res.Rows) != 1 || res.Rows[0][0] != "open") {
				t.Errorf("caller %d got %+v, want the shared row", i, res)
			}
		}(i)
	}

	time.Sleep(80 * time.Millisecond) // let every caller pile up on the same key
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := fd.calls.Load(); got != 1 {
		t.Errorf("the database was queried %d times for %d concurrent callers; want 1", got, n)
	}
}

// A call after the TTL must recompute rather than serve a stale page --
// unlike the analysis cache's minute, a report's 15s window has to expire
// quickly enough that a changed filter (or a changed row) is visible soon.
func TestRunCacheExpiresAfterTTL(t *testing.T) {
	fd := &fakeReportDriver{cols: []string{"status"}, rows: [][]driver.Value{{[]byte("open")}}}
	d := newFakeReportDB(t, fd, 100*time.Millisecond)
	rep := countReport("widget")
	values := map[string]string{"status": "open"}

	if _, err := d.Run(context.Background(), rep, values, 1, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(context.Background(), rep, values, 1, 50); err != nil {
		t.Fatal(err)
	}
	if got := fd.calls.Load(); got != 1 {
		t.Fatalf("two calls inside the TTL reached the database %d times, want 1", got)
	}

	time.Sleep(160 * time.Millisecond)
	if _, err := d.Run(context.Background(), rep, values, 1, 50); err != nil {
		t.Fatal(err)
	}
	if got := fd.calls.Load(); got != 2 {
		t.Errorf("a call after the TTL reached the database %d times in total, want 2", got)
	}
}

// The cache key must include everything that changes what Run returns:
// filters, page and page size. Two requests differing in any one of those are
// different questions and must not answer each other from cache.
func TestRunCacheKeyCoversFiltersAndPaging(t *testing.T) {
	fd := &fakeReportDriver{cols: []string{"status"}, rows: [][]driver.Value{{[]byte("open")}}}
	d := newFakeReportDB(t, fd, time.Minute)
	rep := countReport("widget")

	calls := int32(0)
	run := func(values map[string]string, page, perPage int) {
		if _, err := d.Run(context.Background(), rep, values, page, perPage); err != nil {
			t.Fatal(err)
		}
	}

	run(map[string]string{"status": "open"}, 1, 50)
	calls++
	run(map[string]string{"status": "open"}, 1, 50) // repeat: same key, cache hit
	run(map[string]string{"status": "closed"}, 1, 50)
	calls++ // different filter value
	run(map[string]string{"status": "open"}, 2, 50)
	calls++ // different page
	run(map[string]string{"status": "open"}, 1, 25)
	calls++ // different page size

	if got := fd.calls.Load(); got != calls {
		t.Errorf("database was queried %d times, want %d -- the key is not covering every input that changes the result", got, calls)
	}
}

// Aggregate and MonthlySeries sit behind the same report cache as Run, so a
// page that opens the table, the chart and the monthly strip for the same
// report and filters (the normal case) costs one query per widget rather than
// one per render.
func TestAggregateAndMonthlySeriesAreCached(t *testing.T) {
	fd := &fakeReportDriver{
		cols: []string{"status", "created"},
		rows: [][]driver.Value{
			{[]byte("open"), []byte("1404/03/12")},
			{[]byte("open"), []byte("1404/03/20")},
			{[]byte("closed"), []byte("1404/04/01")},
		},
	}
	d := newFakeReportDB(t, fd, time.Minute)
	rep := Report{
		ID: "widget", SQL: "SELECT status, created FROM widgets",
		LabelColumn: "status", ChartAgg: "count",
		MonthlyColumn: "created",
	}
	values := map[string]string{}

	if _, err := d.Aggregate(context.Background(), rep, values); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Aggregate(context.Background(), rep, values); err != nil {
		t.Fatal(err)
	}
	if got := fd.calls.Load(); got != 1 {
		t.Errorf("Aggregate reached the database %d times for two identical calls, want 1", got)
	}

	if _, err := d.MonthlySeries(context.Background(), rep, values); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MonthlySeries(context.Background(), rep, values); err != nil {
		t.Fatal(err)
	}
	// One more than Aggregate's tally: MonthlySeries is keyed separately
	// ("monthly|..." vs "chart|...") so the two widgets do not answer each
	// other from the same entry.
	if got := fd.calls.Load(); got != 2 {
		t.Errorf("database was queried %d times in total after two cached MonthlySeries calls, want 2", got)
	}
}
