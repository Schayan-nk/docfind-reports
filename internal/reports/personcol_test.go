package reports

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A chart folds person names; a table that did not would show a chart of
// eight people above a table listing fourteen -- the same people, counted two
// different ways, with nothing saying which was real. Every name here is
// invented.
func TestFoldPersonCell(t *testing.T) {
	for _, c := range []struct {
		what, in, want string
	}{
		{"a job title and a team number come off",
			"زهرا مرادی کارشناس فروش 1-2", "زهرا مرادی"},
		{"the same person written the other way folds to the same name",
			"زهرا مرادی سرپرست تیم فروش 3 و سرپرست خرید داخلی", "زهرا مرادی"},
		{"a supervisor title comes off too",
			"آرش رحیمی سرپرست تیم فروش 2", "آرش رحیمی"},
		{"an office role is a title as well",
			"تینا امینی مسئول دفتر واحد 101", "تینا امینی"},
		{"a plain name is left alone", "نادر م. کریمی", "نادر م. کریمی"},
		{"empty stays empty", "", ""},
		{"whitespace is not mistaken for a name", "   ", "   "},
	} {
		if got := foldPersonCell(c.in); got != c.want {
			t.Errorf("%s: foldPersonCell(%q) = %q, want %q", c.what, c.in, got, c.want)
		}
	}
}

// A rolled-up cell holds several names. A report that rolls a record's people
// up with GROUP_CONCAT joins them with " - ", and folding has to reach inside
// rather than treat the whole cell as one name.
func TestFoldPersonCellRollUp(t *testing.T) {
	in := "زهرا مرادی کارشناس فروش 1-2 - آرش رحیمی سرپرست تیم فروش 2"
	want := "زهرا مرادی - آرش رحیمی"
	if got := foldPersonCell(in); got != want {
		t.Errorf("foldPersonCell(%q) = %q, want %q", in, got, want)
	}

	// And the same person twice under two titles is one entry, not two. This
	// is the case that makes a record look jointly handled when it was not.
	dup := "تینا امینی مسئول دفتر واحد 101 - تینا امینی کارشناس فروش 2-16"
	if got := foldPersonCell(dup); got != "تینا امینی" {
		t.Errorf("a repeated person should collapse to one entry, got %q", got)
	}
}

// A value that is all title and no name must survive. NormalisePerson returns
// empty for it, and an empty column reads as missing data rather than as odd
// data -- which is worse, because it is not recoverable by looking.
func TestFoldPersonCellNeverBlanksACell(t *testing.T) {
	for _, in := range []string{"کارشناس فروش", "سرپرست", "مسئول دفتر"} {
		if got := foldPersonCell(in); got == "" {
			t.Errorf("foldPersonCell(%q) blanked the cell", in)
		}
	}
}

// The directive names a column; the query decides what case it arrives in.
// A report declaring OWNER_NAME over a query that selected owner_name must
// still fold, or the directive silently does nothing.
func TestPersonIndexesMatchCaseInsensitively(t *testing.T) {
	cols := []string{"order_no", "owner_name", "status"}
	idx := personIndexes(cols, []string{"OWNER_NAME"})
	if len(idx) != 1 || idx[0] != 1 {
		t.Errorf("personIndexes = %v, want [1]", idx)
	}
	if got := personIndexes(cols, nil); got != nil {
		t.Errorf("no declared columns should fold nothing, got %v", got)
	}
	if got := personIndexes(cols, []string{"not_a_column"}); got != nil {
		t.Errorf("an unmatched name should fold nothing, got %v", got)
	}
}

// Folding must not disturb a row's other cells, and must leave a non-string
// cell alone rather than panicking -- a query can be edited and a numeric
// column renamed into the declared name.
func TestFoldPersonRowLeavesEverythingElseAlone(t *testing.T) {
	row := []any{1234, "زهرا مرادی کارشناس فروش 1-2", nil, "unchanged"}
	foldPersonRow(row, []int{1, 0, 2, 99})
	if row[0] != 1234 {
		t.Errorf("a numeric cell was changed to %v", row[0])
	}
	if row[1] != "زهرا مرادی" {
		t.Errorf("row[1] = %v, want the folded name", row[1])
	}
	if row[2] != nil {
		t.Errorf("a nil cell became %v", row[2])
	}
	if row[3] != "unchanged" {
		t.Errorf("row[3] = %v", row[3])
	}
}

// The whole point is that the table agrees with the chart. Against a
// reporting database holding the demo data (examples/demo-schema.sql): run
// each example report that declares a person column and assert no job title
// reaches the rows. Skipped when no database is configured.
func TestReportTablesFoldPersonColumnsLive(t *testing.T) {
	if os.Getenv("DOCFIND_DB_HOST") == "" && os.Getenv("DOCFIND_DB_DSN") == "" {
		t.Skip("no reporting database configured")
	}
	dsn, err := DSNFromEnv()
	if err != nil {
		t.Skip(err)
	}
	db, err := Open(dsn, 60*time.Second)
	if err != nil {
		t.Skip(err)
	}
	defer db.Close()

	cat, errs := LoadCatalog(repoReportsDir(t))
	if len(errs) > 0 {
		t.Fatalf("catalog problems: %v", errs)
	}

	// Titles that appear in these name columns. Any of them surviving into a
	// row means the fold did not reach that column.
	titles := []string{"کارشناس فروش", "سرپرست تیم", "مسئول دفتر"}

	checked := 0
	// List() strips the SQL, so the runnable copy comes back from Get(id).
	for _, listed := range cat.List() {
		if len(listed.PersonColumns) == 0 {
			continue
		}
		r, ok := cat.Get(listed.ID)
		if !ok {
			t.Errorf("%s is listed but Get() does not have it", listed.ID)
			continue
		}
		res, err := db.Run(context.Background(), r, nil, 1, 50)
		if err != nil {
			t.Errorf("%s: %v", r.ID, err)
			continue
		}
		idx := personIndexes(res.Columns, r.PersonColumns)
		if len(idx) == 0 {
			// The directive names a column this report does not return. Worth
			// reporting rather than passing quietly, because it means the
			// declaration is doing nothing.
			t.Logf("%s: declares %v but returns none of them -- check the names",
				r.ID, r.PersonColumns)
			continue
		}
		checked++
		for _, row := range res.Rows {
			for _, i := range idx {
				s, ok := row[i].(string)
				if !ok {
					continue
				}
				for _, ti := range titles {
					if strings.Contains(s, ti) {
						t.Errorf("%s column %q still reads %q -- the table and its chart "+
							"will disagree about how many people there are",
							r.ID, res.Columns[i], s)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no report returned a declared person column, so this test proved nothing")
	}
}
