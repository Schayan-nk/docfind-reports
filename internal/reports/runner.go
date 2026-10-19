package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Report is one dashboard widget backed by a saved query.
type Report struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Params []Param `json:"params"`
	// Chart names a default visualisation for the UI. Empty means the
	// report is a table only, which most of these are: a list of orders is
	// not improved by being drawn as a pie.
	Chart string `json:"chart,omitempty"`
	// Labels maps a column to the axis it drives, for the charted reports.
	LabelColumn string `json:"label_column,omitempty"`
	ValueColumn string `json:"value_column,omitempty"`
	// ChartAgg is how the value is derived when the query does not already
	// return one row per bar: "count" tallies rows per label, "sum" adds up
	// ValueColumn per label. Empty means the query already aggregated and the
	// rows can be plotted as they arrive.
	ChartAgg string `json:"chart_agg,omitempty"`
	// ChartNormalise says the label column holds people's names, which in this
	// database carry their job title -- so the same person appears once per
	// role and would otherwise get one bar each.
	ChartNormalise string `json:"chart_normalise,omitempty"`
	// SeriesColumn and StackColumn split each bar in two directions.
	//
	// One bar per expert answers "who is carrying the most", which is a
	// narrower question than the one people actually ask of this report: a
	// hundred open cases and a hundred cancelled ones are not the same
	// workload, and a tender is not an inquiry. SeriesColumn becomes the
	// coloured breakdown inside a bar; StackColumn puts each of its values on
	// a bar of its own, side by side under the same label. Either may be set
	// without the other.
	SeriesColumn string `json:"series_column,omitempty"`
	StackColumn  string `json:"stack_column,omitempty"`
	// MonthlyColumn names a Jalali date column ("1404/03/12") to build the
	// monthly summary chart from. The grouping happens in the browser over the
	// rows already returned, so the chart follows the filters the reader has
	// applied instead of needing a second query that duplicates the WHERE
	// clause and drifts from it.
	MonthlyColumn string `json:"monthly_column,omitempty"`
	MonthlyLabel  string `json:"monthly_label,omitempty"`
	// MonthlyCalendar says which calendar that column is written in, and is
	// "jalali" unless the file says otherwise.
	//
	// A Jalali text column needs no conversion, but a query that formats its
	// dates with DATE_FORMAT(...,'%d/%m/%Y') has no Jalali column anywhere in
	// its result. Reading "12/03/2024"
	// as Jalali would bucket it 621 years early, so the calendar has to be
	// stated rather than sniffed: a Gregorian day-first date and a Jalali one
	// are the same shape, and only the file knows which was written.
	MonthlyCalendar string `json:"monthly_calendar,omitempty"`
	// MonthlyWhyNot carries the reason a report has no monthly summary, for the
	// two that cannot have one.
	//
	// Written as "-- monthly: none | <reason>". An empty panel invites the
	// reader to assume the feature is broken or the data is zero; a panel that
	// says "these rows are totals per expert and carry no date" answers the
	// question instead. Only set when MonthlyColumn is empty.
	MonthlyWhyNot string `json:"monthly_why_not,omitempty"`
	// PersonColumns names the result columns holding a person's name, so the
	// table renders them the way the chart and the filter list already do.
	//
	// The raw column carries a job title and a team number after the name, so
	// the same person is written several different ways -- and a chart that
	// folds them beside a table that does not makes the page disagree with
	// itself: eight bars over a table listing fourteen experts, with no way to
	// tell which reading is the real one. The value in the database is
	// untouched; this is only how it is shown.
	PersonColumns []string `json:"person_columns,omitempty"`
	// TotalColumns names the columns added up in a «جمع» row at the foot of
	// the table ("-- total: opening_balance, balance"), lower-cased. Summed in
	// the browser over the rows on screen, as a foot row rather than a row
	// from the query, so sorting, the row numbers and the chart leave it out.
	TotalColumns []string `json:"total_columns,omitempty"`
	// Description is a line of context shown on the report list.
	Description string `json:"description,omitempty"`
	// Source is another server the report reads (extsource.go); Group, the
	// heading it is filed under on the dashboard and the permission screen.
	Source string `json:"source,omitempty"`
	Group  string `json:"group,omitempty"`
	// Family gathers reports the dashboard shows as ONE, chosen between with
	// dropdowns ("-- family: warehouses | گزارش انبارها"); Variant is this
	// one's place among them, a path of one or two names ("-- variant: انبار
	// مرکزی | رسمی") -- the first dropdown, then the second where the first
	// choice has more than one.
	Family       string   `json:"family,omitempty"`
	FamilyTitle  string   `json:"family_title,omitempty"`
	FamilyLevels []string `json:"family_levels,omitempty"` // the dropdowns' labels
	Variant      []string `json:"variant,omitempty"`
	// ColumnLabels overrides a column heading for this report, keyed by the
	// lower-cased column name.
	ColumnLabels map[string]string `json:"-"`
	// HideColumns drops a column from the table and the export; ShowColumns
	// forces one back that would otherwise be hidden as an internal id.
	HideColumns map[string]bool `json:"-"`
	ShowColumns map[string]bool `json:"-"`
	SQL         string          `json:"-"` // never sent to the browser
	// Body is the whole definition this report was parsed from -- directives
	// and query. Kept so the catalogue can be seeded into PostgreSQL from the
	// files that shipped, and so an editor can show what it is editing.
	//
	// json:"-" for the same reason SQL is, and it matters more: Body CONTAINS
	// the SQL, so emitting it would undo that decision by a side door.
	Body string `json:"-"`
}

// DB runs reports against the reporting database.
type DB struct {
	db      *sql.DB
	timeout time.Duration
	// reportCache fronts the per-widget paths -- Run, Aggregate and
	// MonthlySeries -- so several readers open to the same report with the
	// same filters cost one query rather than one each.
	reportCache *resultCache
	// ext holds the other servers a report may read (extsource.go).
	ext extSources
}

// reportCacheTTL is how long a report's page, chart or monthly summary is
// reused. Short on purpose: a report is something people
// filter and re-filter while they work, and a stale minute would make a
// changed filter look like it did nothing for anyone unlucky enough to share
// a key with a recent request. Fifteen seconds is enough to collapse a page
// load's own table+chart+monthly trio, and a burst of readers opening the
// same widget, without outliving how fresh a report needs to look.
const reportCacheTTL = 15 * time.Second

// Open connects using a DSN.
//
// The DSN carries a password, so it is read from the environment rather than
// a config file or a flag: a flag shows up in the process list, and a config
// file tends to end up in a repository.
func Open(dsn string, timeout time.Duration) (*DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("reports: no database configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("reports: opening database: %w", err)
	}
	// A dashboard opened by a dozen people at once should not open a
	// connection per widget against the production database.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &DB{
		db:          db,
		timeout:     timeout,
		reportCache: newResultCache(reportCacheTTL),
	}, nil
}

func (d *DB) Close() error {
	d.closeSources()
	return d.db.Close()
}

// Result is one page of a report.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Page    int      `json:"page"`
	PerPage int      `json:"per_page"`
	HasMore bool     `json:"has_more"`
	TookMs  int64    `json:"took_ms"`
}

const (
	defaultPerPage = 100
	maxPerPage     = 1000
	// maxPage caps how deep paging may go. At the largest page size this is
	// still ten million rows in, far beyond any report anyone reads through,
	// and it keeps the computed offset well inside an int.
	maxPage = 10000
)

// selectOnly rejects anything that is not a read.
//
// The account this runs as is already restricted to SELECT, which is the real
// protection; this is a second line so that a mistake in a saved report fails
// here, with a clear message, rather than as a permissions error from the
// database.
func selectOnly(sqlText string) error {
	trimmed := strings.TrimSpace(sqlText)
	// Skip any leading comment block so the check sees the real first word.
	for strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
		r := []rune(trimmed)
		var j int
		switch {
		case strings.HasPrefix(trimmed, "/*"):
			j = skipBlockComment(r, 0)
		default:
			j = skipToEOL(r, 0)
		}
		trimmed = strings.TrimSpace(string(r[j:]))
	}
	// A leading "(" is still a read: MySQL requires parentheses around a
	// branch of a UNION that carries its own ORDER BY or LIMIT, so
	// "(SELECT …) UNION ALL (SELECT …)" is both valid and read-only. Rejecting
	// it forced those queries to be rewritten into a shape the database liked
	// less, for no gain in safety.
	trimmed = strings.TrimLeft(trimmed, "( \t\r\n")
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return errors.New("reports: only SELECT queries may be run")
	}
	return nil
}

// appendLimit puts a LIMIT/OFFSET on the end of a query.
//
// Trailing semicolons and whitespace are removed first, and the clause goes on
// its own line: a query ending in a "-- comment" would otherwise swallow the
// limit and quietly return every row.
func appendLimit(sqlText string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(sqlText), ";")
	trimmed = strings.TrimSpace(trimmed)
	if endsWithLimit(trimmed) {
		return "", errors.New("reports: this report's query already ends with its own LIMIT; " +
			"remove it and let the dashboard page the results")
	}
	return trimmed + "\nLIMIT ? OFFSET ?", nil
}

// endsWithLimit reports whether a query's own final clause is a LIMIT, which
// would collide with the one being appended. A LIMIT inside a subquery is
// fine and is not what this looks for.
func endsWithLimit(sqlText string) bool {
	fields := strings.Fields(strings.ToUpper(sqlText))
	// "LIMIT n", "LIMIT n, m" or "LIMIT n OFFSET m" -- so look back a few.
	for i := len(fields) - 1; i >= 0 && i >= len(fields)-4; i-- {
		if fields[i] == "LIMIT" {
			return true
		}
	}
	return false
}

// Run executes a report and returns one page.
//
// Paging happens in the database rather than by reading everything and
// slicing: these queries join several tables and can return tens of thousands
// of rows, and materialising all of them to show a hundred would put the whole
// result in this process's memory for no benefit.
//
// Behind the ~15s single-flight cache (see cache.go and reportCacheTTL): the
// dashboard's own polling and several readers opening the same widget cost one
// query rather than one per request. Safe to share the same *Result with every
// caller because nobody downstream edits it in place -- handleReportRun
// narrows a row with reports.Project, which allocates a fresh slice, rather
// than editing Rows itself.
func (d *DB) Run(ctx context.Context, r Report, values map[string]string, page, perPage int) (*Result, error) {
	defer obs.Track(ctx, obs.DB)()
	key := fmt.Sprintf("run|%s|%s|%d|%d", r.ID, filterKey(values), page, perPage)
	v, _, err := d.reportCache.do(key, func() (any, error) {
		return d.runQuery(ctx, r, values, page, perPage)
	})
	if err != nil {
		return nil, err
	}
	res, _ := v.(*Result)
	return res, nil
}

// runQuery is Run without the cache, doing the actual query.
func (d *DB) runQuery(ctx context.Context, r Report, values map[string]string, page, perPage int) (*Result, error) {
	if err := selectOnly(r.SQL); err != nil {
		return nil, err
	}
	bound, args, err := d.bindReport(r, values)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	// An upper bound as well as a lower one.
	//
	// Two reasons. A very large page multiplied by the page size overflows the
	// int the offset is computed in, which turns into a negative OFFSET and a
	// driver error. Long before that, a deep offset is genuinely expensive:
	// the database has to produce and discard every row up to it, so
	// "page=1000000" is a request to scan the entire join and return nothing.
	if page > maxPage {
		page = maxPage
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}

	// One row beyond the page, so "is there a next page" needs no COUNT(*)
	// over the whole join.
	limit := perPage + 1
	offset := (page - 1) * perPage

	// Appended rather than wrapped in "SELECT * FROM (...) AS _report".
	// Wrapping makes the query a derived table, and a derived table must have
	// unique column names -- and a report may legitimately select `a.*`
	// alongside `b.id`, so the wrapper fails with "Duplicate column name 'id'"
	// on a query that is perfectly valid on its own.
	// Appending also applies the limit across a UNION as a whole, which is
	// what paging a combined result should do.
	// LIMIT for MySQL, OFFSET ... FETCH for a report on SQL Server.
	paged, args, err := d.pageReport(r, bound, args, limit, offset)
	if err != nil {
		return nil, err
	}

	c, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	started := time.Now()
	rows, done, err := d.queryReport(c, r, paged, args)
	logSlowQuery(ctx, "run", r.ID, paged, started)
	if err != nil {
		return nil, fmt.Errorf("reports: running %s: %w", r.ID, err)
	}
	defer done()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := &Result{Columns: cols, Page: page, PerPage: perPage, Rows: [][]any{}}
	people := personIndexes(cols, r.PersonColumns)
	for rows.Next() {
		row, err := scanRow(rows, len(cols))
		if err != nil {
			return nil, err
		}
		// The table shows a person's name the way this report's chart and
		// filter list already show it.
		foldPersonRow(row, people)
		// Only a row that would be shown says there is a next page.
		if len(out.Rows) == perPage {
			out.HasMore = true
			break
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.TookMs = time.Since(started).Milliseconds()
	return out, nil
}

// Stream runs a report and hands each row to fn as it arrives.
//
// Used for exports, which must not be bounded by a page and must not be held
// in memory: a 90,000-row CSV streams out in constant space this way.
func (d *DB) Stream(ctx context.Context, r Report, values map[string]string, fn func(cols []string, row []any) error) error {
	defer obs.Track(ctx, obs.DB)()
	// Exports get their own, longer budget: the point is completeness, and a
	// full extract legitimately takes longer than a screenful.
	return d.streamWithTimeout(ctx, r, values, 10*time.Minute, fn)
}

// nowMs is the millisecond clock used for the "took" figures.
func nowMs() int64 { return time.Now().UnixMilli() }

// streamWithTimeout is Stream with a caller-chosen budget, so a chart can be
// bounded tightly while an export stays generous.
func (d *DB) streamWithTimeout(ctx context.Context, r Report, values map[string]string,
	timeout time.Duration, fn func(cols []string, row []any) error) error {
	if err := selectOnly(r.SQL); err != nil {
		return err
	}
	bound, args, err := d.bindReport(r, values)
	if err != nil {
		return err
	}

	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	rows, done, err := d.queryReport(c, r, bound, args)
	logSlowQuery(ctx, "stream", r.ID, bound, started)
	if err != nil {
		return fmt.Errorf("reports: streaming %s: %w", r.ID, err)
	}
	defer done()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	// Folded here too, so an export agrees with the screen it was taken from.
	// A CSV that spells one person three ways is the same defect further from
	// anyone who can notice it -- it becomes three rows in somebody's pivot
	// table.
	people := personIndexes(cols, r.PersonColumns)
	for rows.Next() {
		row, err := scanRow(rows, len(cols))
		if err != nil {
			return err
		}
		foldPersonRow(row, people)
		if err := fn(cols, row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// scanRow reads one row as plain values.
//
// Everything arrives as []byte from this driver; those are converted to
// strings so the JSON the browser receives holds readable Persian text rather
// than base64, which is what encoding/json does with a byte slice.
func scanRow(rows *sql.Rows, n int) ([]any, error) {
	raw := make([]any, n)
	ptrs := make([]any, n)
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]any, n)
	for i, v := range raw {
		switch t := v.(type) {
		case []byte:
			out[i] = string(t)
		case time.Time:
			out[i] = t.Format("2006-01-02 15:04:05")
		default:
			out[i] = v
		}
	}
	return out, nil
}
