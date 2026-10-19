package reports

import (
	"fmt"
	"strings"
)

// Column headings, and which columns are shown.
//
// A column's heading is, in order: the query's own name for it when that is
// already Persian (the author aliased it on purpose), the report's own label
// from a "-- column:" line in its .sql file (or the builder's column
// settings), and otherwise the column name as the database returns it.

// ColumnView is one report's decision about what its columns are called and
// which of them are shown.
//
// Shared by the shipped reports and the builder's, so a report written in the
// builder gets the same rule as one shipped as a file; a second copy of this
// logic would have meant headings that drift between the two.
type ColumnView struct {
	// Labels maps a lower-cased column name to its heading.
	Labels map[string]string
	// Hide keeps a column off the screen.
	Hide map[string]bool
	// Show forces one on, beating Hide.
	Show map[string]bool
}

// viewOf is the shipped catalogue's own view.
func viewOf(r Report) ColumnView {
	return ColumnView{Labels: r.ColumnLabels, Hide: r.HideColumns, Show: r.ShowColumns}
}

// LabelOf returns the heading to show for a column.
//
// A name already containing Persian is returned untouched: the query aliased
// it that way on purpose and the author's wording beats a lookup table.
func (v ColumnView) LabelOf(column string) string {
	if hasPersian(column) {
		return column
	}
	if custom, ok := v.Labels[strings.ToLower(column)]; ok {
		return custom
	}
	return column
}

// LabelFor returns the heading to show for a column of a shipped report.
func LabelFor(r Report, column string) string { return viewOf(r).LabelOf(column) }

// hasPersian reports whether s contains Arabic-script text, which for these
// reports means the column was already aliased into Persian.
func hasPersian(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

// Visible decides which columns of a result reach the browser.
//
// Returns the indexes to keep and their headings, so the same decision applies
// to the table on screen and to a CSV export: exporting columns the screen
// hides would be a surprising way to leak them into a spreadsheet someone
// emails out.
func Visible(r Report, columns []string) (keep []int, headings []string) {
	return viewOf(r).Visible(columns)
}

// Visible decides which columns of a result reach the browser.
func (v ColumnView) Visible(columns []string) (keep []int, headings []string) {
	for i, c := range columns {
		if !v.Shows(c) {
			continue
		}
		keep = append(keep, i)
		headings = append(headings, v.LabelOf(c))
	}
	return keep, headings
}

// Shows reports whether one column is shown.
//
// Separate from Visible because an authored report needs the answer per column
// without dropping the others: its detail key and its chart's columns have to
// keep arriving at the browser even when they are not in the table. See
// customcolumns.go, which explains why that is safe there and not here.
func (v ColumnView) Shows(column string) bool {
	lower := strings.ToLower(strings.TrimSpace(column))
	return v.Show[lower] || !v.Hide[lower]
}

// IsZeroDate reports whether a value is MySQL's zero date rather than a date.
//
// A DATETIME column that was never filled comes back as "0000-00-00 00:00:00".
// Printing it puts a column of impossible dates on screen that every reader
// has to learn to ignore.
func IsZeroDate(v any) bool {
	if v == nil {
		return true
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" {
		return true
	}
	// Any leading run of zeros and separators with no non-zero digit in the
	// date part: covers 0000-00-00, 0000/00/00 and the datetime forms.
	if !strings.HasPrefix(s, "0000") {
		return false
	}
	for _, r := range s {
		if r >= '1' && r <= '9' {
			return false
		}
	}
	return true
}

// BlankZeroDates replaces zero dates in a row with an empty cell.
//
// Emptied rather than dropped: which column it is still matters, and a report
// where one row has fewer cells than the next is a broken table.
func BlankZeroDates(row []any) {
	for i, v := range row {
		if IsZeroDate(v) {
			row[i] = ""
		}
	}
}

// AllZeroDates reports whether every value in a column is a zero date, which
// makes the column itself worth hiding rather than showing as blanks.
func AllZeroDates(rows [][]any, col int) bool {
	seen := false
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		if !IsZeroDate(row[col]) {
			return false
		}
		seen = true
	}
	return seen
}

// Project narrows one row to the kept columns.
func Project(row []any, keep []int) []any {
	out := make([]any, len(keep))
	for i, idx := range keep {
		if idx < len(row) {
			out[i] = row[idx]
		}
	}
	return out
}
