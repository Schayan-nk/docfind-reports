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

// Running a query somebody typed, against a database somebody chose.
//
// Everything here exists to bound that. The guard in sqlguard.go decides
// whether the text may run at all; this decides how much it is allowed to
// cost, because a query that passes the guard can still be
// "SELECT * FROM a CROSS JOIN b" and take the server down.
//
// Four limits, and each answers a different way for this to go wrong:
//
//	the statement timeout   a query that never finishes
//	the row cap             a query that returns everything
//	the cell cap            a query that returns one enormous column
//	the pool width (4)      several of the above at once
//
// The timeout is enforced through the context, which both drivers honour by
// cancelling on the server rather than only abandoning the read locally --
// that distinction matters, because abandoning it would leave the query
// running on somebody else's production database with nobody waiting for it.

const (
	// customQueryTimeout bounds one run.
	//
	// Thirty seconds, not the sixty the configured reports get. An
	// administrator writing a query is sitting in front of it and iterating;
	// a slow answer is one they want to interrupt and fix rather than wait
	// out, and a shorter limit makes a runaway join fail while they still
	// remember what they changed.
	customQueryTimeout = 30 * time.Second

	// customRowCap is the most rows one page returns.
	//
	// The page always fetches one more than it shows, so "there is more" is a
	// fact rather than a guess -- the shipped reports do the same.
	customRowCap = 500

	// customCellCap truncates a single value.
	//
	// A TEXT column holding a document, or a SQL Server XML field, will
	// otherwise put megabytes into a JSON response that a browser then has to
	// render in a table cell. Truncated with a marker, so the reader knows the
	// value continues rather than believing that is all there is.
	customCellCap = 2000
)

// CustomResult is one page of an authored query.
type CustomResult struct {
	Columns []string `json:"columns"`
	// Types are the driver's own column type names, where it offers them.
	// Shown in the builder beside each column: knowing a column is DECIMAL
	// rather than VARCHAR is what tells somebody whether it can be summed,
	// and it saves a round trip of guessing.
	Types []string `json:"types"`
	// Headings are what each column is CALLED -- the Persian heading, where
	// anything knows one. Sent alongside Columns rather than instead of it:
	// the page shows Headings and wires its filters, chart and detail key to
	// Columns, because those have to name the column the query returns.
	Headings []string `json:"headings,omitempty"`
	// Hidden marks a column the table should leave out. Display-only; the
	// values still travel. See CustomResult.Describe for why.
	Hidden  []bool  `json:"hidden,omitempty"`
	Rows    [][]any `json:"rows"`
	Page    int     `json:"page"`
	PerPage int     `json:"per_page"`
	HasMore bool    `json:"has_more"`
	TookMs  int64   `json:"took_ms"`
	// Truncated counts values shortened by customCellCap, so the interface can
	// say so once rather than the reader wondering about every long cell.
	Truncated int `json:"truncated,omitempty"`
}

// RunCustomWith is RunCustom for a query that has already been bound.
//
// Separate entry point rather than a variadic argument, so it is visible at
// every call site whether values are being bound or not -- and so the guard
// below applies to both. A report with filters arrives here with its
// placeholders already rewritten for the driver and its values in args; see
// BindFor.
//
// The guard runs on the BOUND text. That is the right text to check: it is
// what the server will be asked to run, and the bound form contains no values
// -- they travel separately as parameters, which is the entire point. Checking
// the unbound form instead would mean checking something that is never
// executed.
func (s *SourceStore) RunCustomWith(ctx context.Context, sourceID, query string, args []any, page, perPage int) (*CustomResult, error) {
	defer obs.Track(ctx, obs.DB)()
	if err := CheckReadOnly(query); err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 100
	}
	if perPage > customRowCap {
		perPage = customRowCap
	}

	db, driver, err := s.pool(sourceID)
	if err != nil {
		return nil, err
	}

	// Paging without touching the query.
	//
	// Wrapping it -- SELECT * FROM (<query>) LIMIT/OFFSET -- was the first
	// idea and is wrong in both engines for the same reason: it changes the
	// query's own meaning. A derived table in MySQL needs an alias, ORDER BY
	// inside a subquery is not guaranteed to survive, and SQL Server refuses
	// ORDER BY in a subquery outright unless it is paired with a TOP or an
	// OFFSET. So the wrapper would break the ordering a report writer had
	// just put in, and silently.
	//
	// So paging is done by reading and discarding instead: the offset rows are
	// scanned past and thrown away. That costs the server the work of
	// producing them, which is the honest trade and is why the page cap is
	// 500 and the page number is bounded -- this is a builder preview, not a
	// way to walk a million rows.
	offset := (page - 1) * perPage
	if offset > 20000 {
		return nil, fmt.Errorf("this preview does not page that deep; add a WHERE clause "+
			"to narrow the query instead (asked for row %d)", offset)
	}

	c, cancel := context.WithTimeout(ctx, customQueryTimeout)
	defer cancel()

	start := time.Now()
	// Inside a transaction that never commits: see readonlyrun.go.
	rows, done, err := queryReadOnly(c, db, driver, query, args...)
	if err != nil {
		return nil, describeQueryError(err, driver, c)
	}
	defer done()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("the query ran but its columns could not be read: %w", err)
	}
	types := columnTypeNames(rows, len(cols))

	out := &CustomResult{
		Columns: cols, Types: types, Page: page, PerPage: perPage,
		Rows: make([][]any, 0, perPage),
	}

	skipped := 0
	for rows.Next() {
		if skipped < offset {
			skipped++
			continue
		}
		// One more than the page, so HasMore is observed rather than computed.
		if len(out.Rows) == perPage {
			out.HasMore = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("a row could not be read: %w", err)
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i] = normaliseCell(v, &out.Truncated)
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, describeQueryError(err, driver, c)
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// columnTypeNames reads the driver's type names, tolerating drivers that
// decline to give them.
func columnTypeNames(rows *sql.Rows, n int) []string {
	out := make([]string, n)
	cts, err := rows.ColumnTypes()
	if err != nil {
		return out
	}
	for i, ct := range cts {
		if i >= n {
			break
		}
		out[i] = ct.DatabaseTypeName()
	}
	return out
}

// normaliseCell turns a driver value into something JSON can carry.
//
// []byte is the important case: both drivers hand back text as bytes, and
// encoding/json would render that as base64 -- so a report of names would
// arrive as gibberish. Everything else is passed through, including nil,
// which the browser renders as an empty cell rather than the word "null".
func normaliseCell(v any, truncated *int) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(t)
		if len(s) > customCellCap {
			*truncated++
			return s[:customCellCap] + "…"
		}
		return s
	case string:
		if len(t) > customCellCap {
			*truncated++
			return t[:customCellCap] + "…"
		}
		return t
	case time.Time:
		// RFC 3339 so the browser can parse it; the report's own formatting
		// decides how it is shown.
		return t.Format(time.RFC3339)
	default:
		return v
	}
}

// describeQueryError turns a driver failure into something a report writer
// can act on.
//
// The two cases worth separating are a timeout and everything else. A
// cancelled context reads as an opaque driver error otherwise, and "the query
// took too long" is a different instruction from "the query is wrong".
func describeQueryError(err error, driver Driver, ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("the query was still running after %s and was cancelled on the server. "+
			"Narrow it with a WHERE clause, or check it uses an index",
			customQueryTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("the query was cancelled")
	}
	msg := err.Error()
	// Both drivers prefix their own name; it adds nothing for the reader.
	msg = strings.TrimPrefix(msg, "Error ")
	return fmt.Errorf("%s refused the query: %s", driver.Label(), msg)
}
