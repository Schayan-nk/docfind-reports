package reports

import "strings"

// Folding a person column for display.
//
// Some systems write a job title and a team number after a name --
// "زهرا مرادی کارشناس فروش 1-8" and "زهرا مرادی کارشناس فروش 1-2 و سرپرست
// خرید داخلی" are one person -- so the raw value writes the same person
// several different ways. A chart folds them (normalise=person), and the
// filter suggestion lists fold them too. A table that did not would make a
// page disagree with itself: a chart of eight people above a table listing
// fourteen, and nothing to say which count is real.
//
// Applied at display, on both the paged screen result and the export, so the
// CSV somebody pivots agrees with the chart they saw. What the database holds
// is untouched.

// personIndexes maps a report's declared person columns onto the positions they
// occupy in a result.
//
// Matched case-insensitively because the case a column arrives in depends on
// how the query spelled it, and a directive naming OWNER_NAME for a query
// that selected owner_name should not silently do nothing.
func personIndexes(cols []string, declared []string) []int {
	if len(declared) == 0 {
		return nil
	}
	want := make(map[string]bool, len(declared))
	for _, d := range declared {
		want[strings.ToLower(strings.TrimSpace(d))] = true
	}
	var idx []int
	for i, c := range cols {
		if want[strings.ToLower(strings.TrimSpace(c))] {
			idx = append(idx, i)
		}
	}
	return idx
}

// foldPersonCell renders one person cell.
//
// A cell may hold several names: a report may roll a record's people up
// with GROUP_CONCAT, separated by " - ". Each name is folded on its
// own and repeats are dropped, which is the point -- the same person under two
// job titles collapses to one entry in the cell rather than appearing twice.
func foldPersonCell(v string) string {
	if strings.TrimSpace(v) == "" {
		return v
	}
	parts := strings.Split(v, personJoin)
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		n := NormalisePerson(p)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	// Never blank a cell this failed to make sense of. NormalisePerson returns
	// empty for a value that is all job title and no name, and a reader is
	// better served by an odd-looking name than by an empty column.
	if len(out) == 0 {
		return v
	}
	return strings.Join(out, personJoin)
}

// personJoin is the separator the reports' GROUP_CONCAT roll-ups use. Measured
// across every report definition: all ten of them use exactly this.
const personJoin = " - "

// foldPersonRow folds the named positions of one row in place.
//
// Only string cells are touched. A person column should never be anything else,
// but a query can be edited and a numeric column quietly renamed, and silently
// leaving a number alone is better than panicking on a report page.
func foldPersonRow(row []any, idx []int) {
	for _, i := range idx {
		if i < 0 || i >= len(row) {
			continue
		}
		if s, ok := row[i].(string); ok {
			row[i] = foldPersonCell(s)
		}
	}
}
