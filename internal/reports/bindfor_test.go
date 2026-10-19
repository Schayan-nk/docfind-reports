package reports

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestOrdinalPlaceholders(t *testing.T) {
	cases := []struct{ why, in, want string }{
		{"one", "WHERE a = ?", "WHERE a = @p1"},
		{"several, numbered in order", "WHERE a = ? AND b = ? AND c = ?",
			"WHERE a = @p1 AND b = @p2 AND c = @p3"},
		{"an IN group, which Bind expands to a run of them",
			"WHERE a IN (?,?,?)", "WHERE a IN (@p1,@p2,@p3)"},
		{"past ten, so the digits are not assumed to be one",
			"?,?,?,?,?,?,?,?,?,?,?", "@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11"},
		{"none at all", "SELECT 1", "SELECT 1"},

		// The ones that matter. A question mark inside a literal is not a
		// placeholder, and rewriting it changes what the query searches for
		// while still running perfectly happily -- which is the worst kind of
		// wrong.
		{"a literal question mark is left alone",
			"WHERE note LIKE '%?%'", "WHERE note LIKE '%?%'"},
		{"a literal one beside a real one",
			"WHERE note LIKE '%?%' AND a = ?", "WHERE note LIKE '%?%' AND a = @p1"},
		{"and the numbering does not count the literal",
			"WHERE a = ? AND note = '?' AND b = ?", "WHERE a = @p1 AND note = '?' AND b = @p2"},
		{"inside a line comment", "-- is this ok?\nWHERE a = ?", "-- is this ok?\nWHERE a = @p1"},
		{"inside a block comment", "/* ? */ WHERE a = ?", "/* ? */ WHERE a = @p1"},
		{"inside a bracketed name is not special but must not break",
			"SELECT [a?b] FROM t WHERE c = ?", "SELECT [a?b] FROM t WHERE c = @p1"},
	}
	for _, c := range cases {
		if got := ordinalPlaceholders(c.in); got != c.want {
			t.Errorf("%s:\n  in   %q\n  got  %q\n  want %q", c.why, c.in, got, c.want)
		}
	}
}

// BindFor must leave MySQL alone and convert for SQL Server.
func TestBindForKeepsMySQLUnchanged(t *testing.T) {
	params := []Param{{Name: "a", Kind: KindText}, {Name: "b", Kind: KindLike}}
	const q = "SELECT * FROM t WHERE x = @a AND y LIKE @b"

	my, myArgs, err := BindFor(DriverMySQL, q, params, map[string]string{"a": "1", "b": "ab"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(my, "@p") || !strings.Contains(my, "?") {
		t.Errorf("MySQL binding was rewritten: %q", my)
	}

	ms, msArgs, err := BindFor(DriverSQLServer, q, params, map[string]string{"a": "1", "b": "ab"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ms, "?") {
		t.Errorf("SQL Server binding still has a question mark: %q", ms)
	}
	if !strings.Contains(ms, "@p1") || !strings.Contains(ms, "@p2") {
		t.Errorf("SQL Server binding is not ordinal: %q", ms)
	}
	// Same arguments either way -- only the placeholder syntax differs.
	if len(myArgs) != len(msArgs) {
		t.Errorf("argument counts differ: %d vs %d", len(myArgs), len(msArgs))
	}
	for i := range myArgs {
		if myArgs[i] != msArgs[i] {
			t.Errorf("argument %d differs: %v vs %v", i, myArgs[i], msArgs[i])
		}
	}
}

// Against the live SQL Server, because the whole reason this file exists is a
// driver behaviour that only the server can confirm.
func TestSQLServerAcceptsWhatBindForProduces(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("DOCFIND_TEST_MSSQL_DSN"))
	if dsn == "" {
		t.Skip("DOCFIND_TEST_MSSQL_DSN is not set")
	}
	store, err := OpenSourceStore(tempDir(t) + "/d.json")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src, err := store.Save(DataSource{Name: "mssql", Driver: DriverSQLServer}, dsn)
	if err != nil {
		t.Fatal(err)
	}

	params := []Param{
		{Name: "code", Kind: KindText, Default: "1"},
		{Name: "rad", Kind: KindLike},
	}
	const q = `
		SELECT COUNT(*) AS n
		FROM BPM.BpmCaseExtraInfo
		WHERE EngineCode = @code AND RadNumber LIKE @rad`

	bound, args, err := BindFor(DriverSQLServer, q, params, map[string]string{"rad": "#80"})
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	t.Logf("bound: %s", strings.Join(strings.Fields(bound), " "))
	t.Logf("args : %v", args)

	res, err := store.RunCustomWith(context.Background(), src.ID, bound, args, 1, 10)
	if err != nil {
		t.Fatalf("SQL Server refused the bound query: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(res.Rows))
	}
	t.Logf("matched %v cases", res.Rows[0][0])

	// The filter has to actually filter: an unmatchable pattern must return
	// zero, or the parameter is being ignored and every filter is a no-op
	// that looks like it works.
	bound2, args2, err := BindFor(DriverSQLServer, q, params,
		map[string]string{"rad": "#no-such-case-number"})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := store.RunCustomWith(context.Background(), src.ID, bound2, args2, 1, 10)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if asString(res2.Rows[0][0]) != "0" {
		t.Errorf("an unmatchable filter returned %v, so the parameter is being ignored",
			res2.Rows[0][0])
	}
}
