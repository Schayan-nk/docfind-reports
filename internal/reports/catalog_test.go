package reports

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoReportsDir locates the example catalogue, examples/reports.
//
// go test runs in the package's own directory, so it is two levels up. The
// test file's own location (runtime.Caller) is only the fallback: it is the
// path the binary was COMPILED at, which is wrong for a test binary built on
// one machine and run on another.
func repoReportsDir(t *testing.T) string {
	t.Helper()
	if fi, err := os.Stat(filepath.Join("..", "..", "examples", "reports")); err == nil && fi.IsDir() {
		return filepath.Join("..", "..", "examples", "reports")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "examples", "reports")
}

// Every shipped report must parse, and every @placeholder in its SQL must be
// declared. A report whose parameters do not line up fails at load here rather
// than the first time somebody opens the dashboard.
func TestShippedReportsAreValid(t *testing.T) {
	cat, errs := LoadCatalog(repoReportsDir(t))
	for _, err := range errs {
		t.Errorf("catalog error: %v", err)
	}
	if cat.Count() == 0 {
		t.Fatal("no reports loaded")
	}
	t.Logf("loaded %d reports", cat.Count())

	for _, meta := range cat.List() {
		r, ok := cat.Get(meta.ID)
		if !ok {
			t.Fatalf("listed report %q could not be fetched", meta.ID)
		}
		if r.Title == "" {
			t.Errorf("%s: no title", r.ID)
		}
		// Binding with no values exercises every default and every kind.
		sqlText, args, err := Bind(r.SQL, r.Params, nil)
		if err != nil {
			t.Errorf("%s: %v", r.ID, err)
			continue
		}
		if strings.Contains(sqlText, "@") {
			t.Errorf("%s: an @placeholder survived binding:\n%s", r.ID, firstAtLine(sqlText))
		}
		if err := selectOnly(r.SQL); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
		t.Logf("%-20s %2d params, %2d bound args", r.ID, len(r.Params), len(args))
	}
}

// The list handed to the browser must never carry the SQL: it names internal
// tables and process UIDs that a viewer has no reason to receive.
func TestListedReportsCarryNoSQL(t *testing.T) {
	cat, _ := LoadCatalog(repoReportsDir(t))
	for _, r := range cat.List() {
		if r.SQL != "" {
			t.Errorf("%s: List() leaked its SQL", r.ID)
		}
	}
}

// A charted report has to name columns its own query actually returns, or the
// dashboard draws an empty chart with no clue why.
//
// Which columns are required depends on how the bar height is produced:
// "agg=count" tallies rows and needs only a label, while "agg=sum" and the
// pre-aggregated form both need a value column to add up or read.
func TestChartColumnsExistInQuery(t *testing.T) {
	cat, _ := LoadCatalog(repoReportsDir(t))
	charted := 0
	for _, meta := range cat.List() {
		if meta.Chart == "" {
			continue
		}
		charted++
		r, _ := cat.Get(meta.ID)

		required := []string{meta.LabelColumn}
		if !strings.EqualFold(meta.ChartAgg, "count") {
			required = append(required, meta.ValueColumn)
		}
		for _, col := range required {
			if col == "" {
				t.Errorf("%s: chart is missing a column it needs (agg=%q)", meta.ID, meta.ChartAgg)
				continue
			}
			// Case-insensitively, because these queries spell the same view
			// column three different ways and the chart resolves it that way
			// too.
			if !strings.Contains(strings.ToLower(r.SQL), strings.ToLower(col)) {
				t.Errorf("%s: chart column %q does not appear in the query", meta.ID, col)
			}
		}
		if agg := strings.ToLower(meta.ChartAgg); agg != "" && agg != "count" && agg != "sum" {
			t.Errorf("%s: unknown chart aggregation %q", meta.ID, meta.ChartAgg)
		}
	}
	// Every report is meant to have a chart now; a silent drop to zero charted
	// reports would otherwise pass this test trivially.
	if charted != cat.Count() {
		t.Errorf("%d of %d reports declare a chart; all of them should", charted, cat.Count())
	}
}

func firstAtLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "@") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// "-- total:" names the columns a «جمع» row adds up: in the order written,
// lower-cased, and in a shipped report each one a column its query returns --
// a misspelt name would otherwise be a total row with nothing in it.
func TestTotalColumns(t *testing.T) {
	r, err := parseReport("-- id: x\n-- total: Opening_Balance, balance ,\nSELECT 1 AS opening_balance, 2 AS balance")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.TotalColumns, ","); got != "opening_balance,balance" {
		t.Errorf("total columns %q", got)
	}

	files, err := filepath.Glob(filepath.Join(repoReportsDir(t), "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no shipped reports found (%v)", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := parseReport(string(raw))
		if err != nil {
			continue // TestShippedReportsAreValid reports it
		}
		for _, c := range r.TotalColumns {
			if !regexp.MustCompile(`(?i)\bAS\s+` + regexp.QuoteMeta(c) + `\b`).MatchString(r.SQL) {
				t.Errorf("%s: total column %s is not a column the query returns", filepath.Base(f), c)
			}
		}
	}
}

// "-- family:" and "-- variant:" make several reports one on the dashboard,
// chosen with dropdowns; in the example set every member of a family has its
// own choice, and a first choice is either a report of its own or a choice
// between several -- never both.
func TestReportFamilies(t *testing.T) {
	r, err := parseReport("-- id: x\n-- family: warehouses | گزارش انبارها\n-- variant: انبار مرکزی | رسمی\nSELECT 1 AS a")
	if err != nil {
		t.Fatal(err)
	}
	if r.Family != "warehouses" || r.FamilyTitle != "گزارش انبارها" || strings.Join(r.Variant, "/") != "انبار مرکزی/رسمی" {
		t.Errorf("family %q %q, variant %q", r.Family, r.FamilyTitle, r.Variant)
	}

	files, _ := filepath.Glob(filepath.Join(repoReportsDir(t), "*.sql"))
	seen := map[string]string{}      // family + variant -> file
	depth := map[string]int{}        // family + first choice -> levels
	members := map[string][]string{} // family -> files
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		r, err := parseReport(string(raw))
		if err != nil || r.Family == "" {
			continue
		}
		members[r.Family] = append(members[r.Family], filepath.Base(f))
		if len(r.Variant) == 0 || len(r.Variant) > 2 || r.FamilyTitle == "" {
			t.Errorf("%s: a family member needs a title and one or two choices, has %q %q", filepath.Base(f), r.FamilyTitle, r.Variant)
			continue
		}
		k := r.Family + "/" + strings.Join(r.Variant, "/")
		if other, dup := seen[k]; dup {
			t.Errorf("%s and %s are the same choice %q", other, filepath.Base(f), k)
		}
		seen[k] = filepath.Base(f)
		first := r.Family + "/" + r.Variant[0]
		if d, ok := depth[first]; ok && d != len(r.Variant) {
			t.Errorf("%s: %q is both a report and a choice between reports", filepath.Base(f), r.Variant[0])
		}
		depth[first] = len(r.Variant)
	}
	// The examples ship one family of two.
	if got := len(members["warehouses"]); got != 2 {
		t.Errorf("«گزارش انبارها» has %d members, want 2: %v", got, members["warehouses"])
	}
}

func TestParseParamRejectsUnknownKind(t *testing.T) {
	if _, err := parseParam("x | wobble | Label"); err == nil {
		t.Error("an unknown parameter kind was accepted")
	}
	if _, err := parseParam("onlyname"); err == nil {
		t.Error("a param with no kind was accepted")
	}
}

func TestSelectOnlyRejectsWrites(t *testing.T) {
	for _, bad := range []string{
		"DELETE FROM application",
		"  update application set x=1",
		"/* sneaky */ DROP TABLE application",
		"-- comment\nINSERT INTO t VALUES (1)",
	} {
		if err := selectOnly(bad); err == nil {
			t.Errorf("selectOnly accepted a write: %q", bad)
		}
	}
	for _, good := range []string{
		"SELECT 1",
		"  select * from t",
		"-- a note\nSELECT 1",
		"/* header */ SELECT 1",
		"WITH x AS (SELECT 1) SELECT * FROM x",
	} {
		if err := selectOnly(good); err != nil {
			t.Errorf("selectOnly rejected a read: %q -> %v", good, err)
		}
	}
}
