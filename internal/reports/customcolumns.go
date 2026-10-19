package reports

import (
	"fmt"
	"strings"
)

// What an authored report's columns are called, and which of them are shown.
//
// This is the layer that decides whether a report somebody wrote in the
// builder reads like a shipped one or like a database client. A shipped report
// declares "-- column: WEIGHT | وزن" and gets a Persian heading; without this an
// authored report shows "WEIGHT", or worse "wt", because an alias somebody
// typed to save keystrokes becomes the heading a manager reads.
//
// Two levels, the catalogue's own (see ColumnView.LabelOf):
//
//	SELECT x AS "وزن"   the query said so; nothing overrides that
//	column label here   the author said so
//
// and otherwise the column name as the database returns it.

// ColumnSpec is what an authored report says about one of its columns.
type ColumnSpec struct {
	// Name is the column as the query returns it, matched case-insensitively.
	Name string `json:"name"`
	// Label overrides the heading. Empty keeps the query's own name.
	Label string `json:"label,omitempty"`
	// Hide keeps it out of the table and the CSV.
	Hide bool `json:"hide,omitempty"`
	// Show forces a column on. Nothing is hidden unless a report says so, so
	// this only matters for a column some other rule would hide.
	Show bool `json:"show,omitempty"`
	// Person marks a column holding people's names written with a job title
	// after them, so the title is folded away (people.go).
	//
	// A system that stores "<name> <job title> <unit>" writes one person once
	// per role they have held, and the consequence is not cosmetic: a chart
	// grouped on that column draws one person as two bars, and a table beside
	// it counts more people than the chart above it, with nothing to say which
	// count is real.
	Person bool `json:"person,omitempty"`
}

// CleanColumns normalises what the browser sent.
//
// Unknown column names are KEPT rather than refused. A column list is written
// against one result and the query is then edited; refusing a name that is no
// longer selected would block a save over an edit the author has not finished.
// They cost nothing -- a label for a column that does not exist labels nothing.
func CleanColumns(in []ColumnSpec) ([]ColumnSpec, error) {
	seen := map[string]bool{}
	out := make([]ColumnSpec, 0, len(in))
	for _, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Label = strings.TrimSpace(c.Label)
		if c.Name == "" {
			continue
		}
		lower := strings.ToLower(c.Name)
		if seen[lower] {
			return nil, fmt.Errorf("the column %q is described twice", c.Name)
		}
		seen[lower] = true
		if c.Hide && c.Show {
			// Both would be a contradiction resolved silently by whichever
			// check ran first, which is the kind of thing that has somebody
			// staring at a column wondering why the checkbox does nothing.
			return nil, fmt.Errorf("%q is set to both hidden and shown; pick one", c.Name)
		}
		// A row saying nothing is not worth storing: no label, not hidden, not
		// forced on, not a person. Dropping them keeps the saved file about
		// decisions rather than about every column that has ever been in the
		// query.
		if c.Label == "" && !c.Hide && !c.Show && !c.Person {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ViewOfColumns turns an authored report's column list into the same view the
// shipped catalogue uses.
func ViewOfColumns(specs []ColumnSpec) ColumnView {
	v := ColumnView{
		Labels: map[string]string{},
		Hide:   map[string]bool{},
		Show:   map[string]bool{},
	}
	for _, c := range specs {
		lower := strings.ToLower(strings.TrimSpace(c.Name))
		if c.Label != "" {
			v.Labels[lower] = c.Label
		}
		if c.Hide {
			v.Hide[lower] = true
		}
		if c.Show {
			v.Show[lower] = true
		}
	}
	return v
}

// Describe fills in a result's headings and which columns are shown.
//
// Hiding here is DISPLAY-ONLY: the values still travel to the browser, and the
// page leaves them out of the table. That is the opposite of Visible(), which
// drops hidden columns from the payload entirely, and the difference is
// deliberate.
//
// Visible() drops them because the shipped reports are read by anybody with
// the reports permission, and an internal identifier landing in a CSV somebody
// emails out is a quiet way to leak it. An authored report is different on
// both counts: only someone holding the builder can open it at all, and they
// wrote the SELECT -- they have already chosen to read that column. What they
// want is for it not to clutter the table.
//
// And it has to work that way, because the machinery needs the values: a
// detail query keyed on a record number cannot open a row whose key never
// arrived, and a chart plotting a column cannot plot one that was dropped.
// Hiding the key while still opening rows by it is the ordinary case, not a
// corner.
//
// The CSV is the exception, and honours hiding -- see writeCustomCSV. A
// download is a copy somebody keeps, and matching what was on screen is what
// makes it the same report.
//
// Takes the column list rather than a prepared view, and does the folding
// too, because the two have to happen together. Every path that returns a
// result to the builder calls this one function; splitting it into "label the
// columns" and "fold the names" would leave several call sites each able to
// forget the second, and forgetting it is how a chart and the table under it
// end up counting different people.
func (res *CustomResult) Describe(specs []ColumnSpec) {
	if res == nil {
		return
	}
	view := ViewOfColumns(specs)
	res.Headings = make([]string, len(res.Columns))
	res.Hidden = make([]bool, len(res.Columns))
	for i, c := range res.Columns {
		res.Headings[i] = view.LabelOf(c)
		res.Hidden[i] = !view.Shows(c)
	}

	// Job titles folded out of the ROWS -- so the table, the chart drawn from
	// them and the CSV exported from them all count the same people. What the
	// database holds is untouched.
	var people []string
	for _, c := range specs {
		if c.Person {
			people = append(people, c.Name)
		}
	}
	if idx := personIndexes(res.Columns, people); len(idx) > 0 {
		for _, row := range res.Rows {
			foldPersonRow(row, idx)
		}
	}
}

// SuggestColumns describes a result's columns to the builder, so the column
// editor opens already filled in rather than empty.
//
// Each entry says what the column will be called and where that name came
// from, because "وزن, from the query" and "wt, because nothing names it"
// need different actions from the reader and look identical otherwise.
type ColumnSuggestion struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Source is "query" (the SELECT aliased it into Persian), "custom" (this
	// report says so) or "" (nothing does, so the raw name is showing).
	Source string `json:"source"`
	Hidden bool   `json:"hidden,omitempty"`
}

// SuggestColumns answers "what will these columns be called".
func SuggestColumns(columns []string, specs []ColumnSpec) []ColumnSuggestion {
	view := ViewOfColumns(specs)
	custom := map[string]bool{}
	for _, c := range specs {
		if c.Label != "" {
			custom[strings.ToLower(strings.TrimSpace(c.Name))] = true
		}
	}

	out := make([]ColumnSuggestion, 0, len(columns))
	for _, c := range columns {
		lower := strings.ToLower(c)
		s := ColumnSuggestion{
			Name:   c,
			Label:  view.LabelOf(c),
			Hidden: !view.Shows(c),
		}
		switch {
		case hasPersian(c):
			s.Source = "query"
		case custom[lower]:
			s.Source = "custom"
		}
		out = append(out, s)
	}
	return out
}
