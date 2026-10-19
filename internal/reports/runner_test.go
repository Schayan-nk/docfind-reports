package reports

import (
	"strings"
	"testing"
)

// Paging must not turn the query into a derived table.
//
// Regression test for a real failure: the pager used to wrap queries in
// "SELECT * FROM (...) AS _report", and a derived table requires unique
// column names. A report may legitimately select `a.*` alongside `b.id`, and
// MySQL rejects that with "Error 1060: Duplicate column name 'id'" even
// though the query is valid on its own.
func TestAppendLimitDoesNotWrapInASubquery(t *testing.T) {
	got, err := appendLimit("SELECT a.*, b.id FROM t AS a JOIN b ON 1=1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(got), "FROM (") {
		t.Errorf("query was wrapped in a derived table:\n%s", got)
	}
	if !strings.HasSuffix(got, "LIMIT ? OFFSET ?") {
		t.Errorf("limit was not appended:\n%s", got)
	}
}

// A query ending in a line comment would swallow an appended clause written on
// the same line, silently returning every row instead of one page.
func TestAppendLimitSurvivesTrailingComment(t *testing.T) {
	got, err := appendLimit("SELECT 1 -- trailing note")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != "LIMIT ? OFFSET ?" {
		t.Errorf("LIMIT did not land on its own line; last line is %q", last)
	}
}

func TestAppendLimitStripsTrailingSemicolon(t *testing.T) {
	got, err := appendLimit("SELECT 1;  \n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, ";") {
		t.Errorf("semicolon survived, which would make the appended clause a second statement:\n%s", got)
	}
}

// A report that pages itself would collide with the pager, so say so clearly
// rather than emitting SQL with two LIMIT clauses.
func TestAppendLimitRefusesAQueryThatAlreadyPages(t *testing.T) {
	for _, q := range []string{
		"SELECT 1 LIMIT 10",
		"SELECT 1 LIMIT 10 OFFSET 5",
		"SELECT 1 LIMIT 5, 10",
	} {
		if _, err := appendLimit(q); err == nil {
			t.Errorf("appendLimit(%q) was accepted; expected a clear refusal", q)
		}
	}
	// A LIMIT inside a subquery is not the query's own final clause.
	if _, err := appendLimit("SELECT * FROM (SELECT a FROM t LIMIT 5) x WHERE a > 1"); err != nil {
		t.Errorf("a LIMIT inside a subquery was mistaken for the query's own: %v", err)
	}
}
