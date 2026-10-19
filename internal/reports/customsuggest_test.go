package reports

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A suggestion source names a TABLE and a COLUMN, and identifiers cannot be
// bound as parameters -- so they are concatenated into the statement and have
// to be proven rather than trusted.
//
// This is the test that lets the feature exist. CleanParams refused the
// shipped Suggest field outright for exactly this reason; the authored
// equivalent is allowed only because safeIdentifier holds it to letters,
// digits and underscore. Every case below is a way somebody could try to get
// something else in there.
func TestASuggestionSourceCannotCarryAnythingButAnIdentifier(t *testing.T) {
	bad := []struct{ why, table, column string }{
		{"a backtick, which would close MySQL's quoting",
			"orders` WHERE 1=1 UNION SELECT 1 -- ", "owner_name"},
		{"a bracket, which would close SQL Server's",
			"orders] WHERE 1=1 -- ", "owner_name"},
		{"a semicolon",
			"orders; DROP TABLE users", "owner_name"},
		{"a comment marker",
			"orders -- x", "owner_name"},
		{"a block comment",
			"orders /* x */", "owner_name"},
		{"a space, which is how every one of the above starts",
			"or ders", "owner_name"},
		{"a quote",
			"orders'", "owner_name"},
		{"the same things in the COLUMN, which is used three times in the statement",
			"orders", "owner_name` , (SELECT 1) AS x -- "},
		{"a parenthesis in the column",
			"orders", "COUNT(*)"},
		{"a dotted name, because the statement quotes each part itself",
			"reports_demo.orders", "owner_name"},
		{"a leading digit, which no identifier here uses",
			"1table", "owner_name"},
		{"empty",
			"", "owner_name"},
	}

	for _, c := range bad {
		// Refused at SAVE, so it never reaches storage...
		_, err := CleanParams(DriverMySQL, "SELECT 1 WHERE a LIKE @x",
			[]Param{{Name: "x", Kind: KindLike, SuggestTable: c.table, SuggestColumn: c.column}})
		if err == nil {
			t.Errorf("%s: saved a suggestion source of %q.%q", c.why, c.table, c.column)
		}

		// ...and refused again by the query path, so a file edited by hand
		// cannot get one in either. Two checks because they answer different
		// questions: the first explains, the second guarantees.
		store, serr := OpenSourceStore(filepath.Join(tempDir(t), "d.json"))
		if serr != nil {
			t.Fatal(serr)
		}
		_, qerr := store.SuggestValues(context.Background(), "src_x", c.table, c.column, false, "")
		store.Close()
		if qerr == nil {
			t.Errorf("%s: the query path accepted %q.%q", c.why, c.table, c.column)
		}
	}

	// And a name that IS an identifier is accepted, or the check is useless.
	good, err := CleanParams(DriverMySQL, "SELECT 1 WHERE a LIKE @x",
		[]Param{{Name: "x", Kind: KindLike,
			SuggestTable: "orders", SuggestColumn: "owner_name", SuggestPerson: true}})
	if err != nil {
		t.Fatalf("a valid suggestion source was refused: %v", err)
	}
	if !good[0].Suggestable {
		t.Error("a valid source did not mark the filter as suggestable")
	}
	if !good[0].SuggestPerson {
		t.Error("the person flag was lost")
	}
	// Suggest itself stays un-settable: that field is the catalogue's, read
	// from a .sql file by somebody who has read this package.
	if good[0].Suggest != nil {
		t.Error("the shipped Suggest field was settable from a request")
	}
}

// Half a source is a mistake worth naming, and a filter that offers its own
// values does not also need completions.
func TestSuggestionSourceShapeIsChecked(t *testing.T) {
	cases := []struct {
		why  string
		p    Param
		want string
	}{
		{"a table with no column",
			Param{Name: "x", Kind: KindLike, SuggestTable: "t"}, "both a table and a column"},
		{"a column with no table",
			Param{Name: "x", Kind: KindLike, SuggestColumn: "c"}, "both a table and a column"},
		{"a dropdown, which already lists its values",
			Param{Name: "x", Kind: KindChoice, Options: []Option{{Value: "a"}},
				SuggestTable: "t", SuggestColumn: "c"}, "already offers its own"},
		{"a date, which has a picker",
			Param{Name: "x", Kind: KindDate, SuggestTable: "t", SuggestColumn: "c"},
			"already offers its own"},
	}
	for _, c := range cases {
		_, err := CleanParams(DriverMySQL, "SELECT 1 WHERE a = @x", []Param{c.p})
		if err == nil {
			t.Errorf("%s: accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the message does not say %q:\n  %v", c.why, c.want, err)
		}
	}

	// Neither half is no source at all, which is the normal case.
	got, err := CleanParams(DriverMySQL, "SELECT 1 WHERE a = @x",
		[]Param{{Name: "x", Kind: KindText, SuggestPerson: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Suggestable || got[0].SuggestPerson {
		t.Errorf("a filter with no source claims to suggest: %+v", got[0])
	}
}

// Identifiers are quoted for the engine they run against, and the shipped path
// only ever needed MySQL's.
func TestSuggestionIdentifiersAreQuotedPerEngine(t *testing.T) {
	if got := quoteIdentifier(DriverMySQL, "owner_name"); got != "`owner_name`" {
		t.Errorf("MySQL quoting is %q", got)
	}
	if got := quoteIdentifier(DriverSQLServer, "owner_name"); got != "[owner_name]" {
		t.Errorf("SQL Server quoting is %q", got)
	}
}

// Against a MySQL holding the demo data (examples/demo-schema.sql), because
// what this has to prove is that the completions are real values from the
// real column.
func TestSuggestionsAgainstLiveMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("DOCFIND_DB_DSN"))
	if dsn == "" {
		t.Skip("DOCFIND_DB_DSN is not set")
	}
	store, err := OpenSourceStore(filepath.Join(tempDir(t), "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src, err := store.Save(DataSource{Name: "demo", Driver: DriverMySQL}, dsn)
	if err != nil {
		t.Fatal(err)
	}

	all, err := store.SuggestValues(context.Background(), src.ID,
		"orders", "owner_name", true, "")
	if err != nil {
		t.Fatalf("suggesting: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no completions at all from a column known to hold names")
	}
	t.Logf("%d completions: %v", len(all), all)

	// A prefix narrows it, and to a subset of what was offered unfiltered.
	first := all[0]
	prefix := string([]rune(first)[:3])
	some, err := store.SuggestValues(context.Background(), src.ID,
		"orders", "owner_name", true, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(some) == 0 {
		t.Errorf("the prefix %q of %q matched nothing", prefix, first)
	}
	for _, v := range some {
		if !strings.Contains(strings.ToLower(v), strings.ToLower(prefix)) {
			t.Errorf("%q does not contain the typed %q", v, prefix)
		}
	}

	// Something nobody is called matches nothing -- so the filtering is real
	// rather than the whole column coming back regardless.
	none, err := store.SuggestValues(context.Background(), src.ID,
		"orders", "owner_name", true, "zzzz-no-such-name")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("an impossible prefix returned %d completions", len(none))
	}

	// Folding: asked for names, the same colleague under two job titles is
	// one entry. Compared against the unfolded read of the same column.
	raw, err := store.SuggestValues(context.Background(), src.ID,
		"orders", "owner_name", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) > len(raw) {
		t.Errorf("folding produced MORE entries (%d) than not folding (%d)", len(all), len(raw))
	}
	t.Logf("folded %d, raw %d", len(all), len(raw))
}
