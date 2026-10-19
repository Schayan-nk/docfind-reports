package reports

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanDetailRefusesWhatCannotWork(t *testing.T) {
	cases := []struct {
		why  string
		in   DetailSpec
		want string
	}{
		{"nothing identifying the row",
			DetailSpec{SQL: "SELECT 1 WHERE a = @key"}, "identifies a row"},
		{"not a read",
			DetailSpec{SQL: "DELETE FROM quotes WHERE a = @key", Key: "id"}, "DELETE"},

		// The one that matters. A detail query with no @key returns the same
		// rows under every master row: it looks like it works, and it is never
		// what anybody meant.
		{"a detail that ignores the row it is about",
			DetailSpec{SQL: "SELECT * FROM quotes", Key: "id"}, "@key"},
		{"an @key that is only inside a string literal is not a use of it",
			DetailSpec{SQL: "SELECT * FROM quotes WHERE note = 'see @key'", Key: "id"}, "@key"},
		{"nor one in a comment",
			DetailSpec{SQL: "SELECT * FROM quotes -- filter by @key later", Key: "id"}, "@key"},
	}
	for _, c := range cases {
		spec := c.in
		_, err := CleanDetail(&spec)
		if err == nil {
			t.Errorf("%s: accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the message does not mention %q:\n  %v", c.why, c.want, err)
		}
	}
}

func TestCleanDetailNormalises(t *testing.T) {
	// No query means no detail, which is what most reports are.
	for _, empty := range []*DetailSpec{nil, {}, {SQL: "  "}} {
		got, err := CleanDetail(empty)
		if err != nil {
			t.Fatalf("an empty detail was an error: %v", err)
		}
		if got != nil {
			t.Errorf("an empty detail became %+v, want nil", got)
		}
	}

	got, err := CleanDetail(&DetailSpec{
		SQL:   "  SELECT * FROM order_lines WHERE order_id = @key  ",
		Key:   "  order_id  ",
		Title: "  اقلام سفارش  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "order_id" || got.Title != "اقلام سفارش" {
		t.Errorf("not trimmed: %+v", got)
	}
	if strings.HasPrefix(got.SQL, " ") || strings.HasSuffix(got.SQL, " ") {
		t.Errorf("the query was not trimmed: %q", got.SQL)
	}
}

// @key belongs to the detail query, so a filter cannot be called that -- it
// would be overwritten the moment somebody opened a row, and only then.
func TestKeyIsReservedAsAFilterName(t *testing.T) {
	_, err := CleanParams(DriverMySQL, "SELECT 1 WHERE a = @key",
		[]Param{{Name: "key", Kind: KindText}})
	if err == nil {
		t.Fatal("a filter named key was accepted")
	}
	if !strings.Contains(err.Error(), "@key") {
		t.Errorf("the message does not explain which name is reserved: %v", err)
	}
}

// The detail query gets the report's own filters as well as @key, because a
// detail is a view of the same data and a date range on the master must not
// silently widen underneath it.
func TestDetailSeesTheReportsOwnFilters(t *testing.T) {
	reportParams := []Param{
		{Name: "month", Kind: KindText},
		// A filter of the reserved name, which CleanParams refuses anyway --
		// here to prove the real @key wins rather than being shadowed.
		{Name: detailKeyParam, Kind: KindLike},
	}
	got := detailParams(reportParams)

	var keys, months int
	for _, p := range got {
		switch p.Name {
		case detailKeyParam:
			keys++
			if p.Kind != KindText {
				t.Errorf("@key is bound as %q, want a plain value", p.Kind)
			}
		case "month":
			months++
		}
	}
	if keys != 1 {
		t.Errorf("@key appears %d times, want exactly one", keys)
	}
	if months != 1 {
		t.Error("the report's own filter did not reach the detail query")
	}
}

// Master and detail against a MySQL holding the demo data
// (examples/demo-schema.sql), which is the only place the relationship can
// be shown to hold.
//
// The assertion that matters is that opening two different master rows gives
// DIFFERENT details. A detail query whose parameter was ignored would return
// the same rows every time and look perfectly plausible.
func TestMasterDetailAgainstLiveMySQL(t *testing.T) {
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
	custom, err := OpenCustomStore(filepath.Join(tempDir(t), "r.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Orders and their line items: the demo data's master and detail.
	rep, err := custom.Save(CustomReport{
		SourceID: src.ID,
		Title:    "سفارش‌ها و اقلام",
		SQL:      "SELECT order_no AS order_no, status AS status FROM orders ORDER BY order_no",
		Detail: &DetailSpec{
			Key:   "order_no",
			Title: "اقلام این سفارش",
			SQL: "SELECT l.item AS item, l.qty AS qty FROM order_lines l " +
				"JOIN orders o ON o.id = l.order_id WHERE o.order_no = @key " +
				"ORDER BY l.item",
		},
	}, store)
	if err != nil {
		t.Fatalf("saving a master-detail report: %v", err)
	}
	if rep.Detail == nil {
		t.Fatal("the detail was dropped on save")
	}

	master, err := custom.RunSaved(context.Background(), store, rep.ID, nil, 1, 50)
	if err != nil {
		t.Fatalf("running the master: %v", err)
	}
	if len(master.Rows) < 2 {
		t.Skipf("only %d master rows; this test needs two to compare", len(master.Rows))
	}

	// Every master row's detail, so the comparison is over real variety
	// rather than the first two rows happening to differ.
	seen := map[string]int{}
	withRows := 0
	for _, row := range master.Rows {
		key := asString(row[0])
		d, err := custom.RunDetail(context.Background(), store, rep.ID, key, nil, 1, 50)
		if err != nil {
			t.Fatalf("running the detail for %q: %v", key, err)
		}
		if len(d.Rows) > 0 {
			withRows++
		}
		// A fingerprint of what came back, so "different rows for different
		// keys" can be counted rather than eyeballed.
		var b strings.Builder
		for _, dr := range d.Rows {
			for _, cell := range dr {
				b.WriteString(asString(cell))
				b.WriteByte('|')
			}
			b.WriteByte(';')
		}
		seen[b.String()]++
	}
	t.Logf("%d master rows, %d had details, %d distinct detail results",
		len(master.Rows), withRows, len(seen))

	if withRows == 0 {
		t.Fatal("no master row had any detail rows, so nothing here is proven")
	}
	if len(seen) < 2 {
		t.Error("every master row returned the same detail rows, so @key is not being " +
			"bound -- which is exactly the failure that looks like it works")
	}

	// And an impossible key returns nothing, which is the other half: a
	// parameter that is ignored would still return rows here.
	none, err := custom.RunDetail(context.Background(), store, rep.ID,
		"-999999", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Rows) != 0 {
		t.Errorf("an impossible key returned %d detail rows", len(none.Rows))
	}
}
