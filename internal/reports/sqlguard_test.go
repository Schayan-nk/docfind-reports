package reports

import (
	"strings"
	"testing"
)

// The queries a report writer legitimately needs must all pass.
//
// This half matters as much as the refusals. A guard that blocks real work
// gets switched off, and then there is no guard -- so every shape the ten
// existing reports use is represented here, including the ones that look
// alarming and are not.
func TestReadOnlyAllowsRealReportQueries(t *testing.T) {
	ok := []struct{ why, q string }{
		{"the simplest thing", `SELECT 1`},
		{"a plain read", `SELECT a, b FROM t WHERE a > 1 ORDER BY b`},
		{"lower case", `select * from orders`},
		{"a CTE", `WITH x AS (SELECT 1 AS n) SELECT n FROM x`},
		{"several joins, which is what " +
			"'connect a couple of tables together' means", `
			SELECT o.num, c.name, SUM(l.qty) AS qty
			FROM orders o
			JOIN customers c ON c.id = o.customer_id
			LEFT JOIN lines l ON l.order_id = o.id
			GROUP BY o.num, c.name
			HAVING SUM(l.qty) > 0`},
		{"a union in brackets", `(SELECT a FROM t1) UNION ALL (SELECT a FROM t2)`},
		{"a subquery", `SELECT * FROM (SELECT a FROM t) x WHERE x.a = 2`},
		{"a trailing semicolon, which people type out of habit", `SELECT 1;`},
		{"a line comment", "-- the monthly figures\nSELECT 1"},
		{"a block comment", "/* header */ SELECT 1"},
		{"a hash comment, MySQL style", "# note\nSELECT 1"},

		// The interesting ones: denied WORDS appearing where they are inert.
		{"a denied word inside a comment", "-- do not DELETE this report\nSELECT 1"},
		{"a denied word inside a block comment", "/* was an UPDATE once */ SELECT 1"},
		{"a denied word inside a string", `SELECT * FROM t WHERE note = 'please DROP off'`},
		{"a word that merely starts with a keyword", `SELECT selected, created_at, updates FROM t`},
		{"a column called order, bracketed", `SELECT [order], [set] FROM [use]`},
		{"a column called insert, backticked", "SELECT `insert`, `delete` FROM `t`"},
		{"an escaped quote inside a string", `SELECT 'it''s fine' AS a`},
		{"a backslash-escaped quote, MySQL", `SELECT 'a\'b' AS a`},
		{"a parameter placeholder", `SELECT * FROM t WHERE d >= @pfrom AND d <= @pto`},
		{"a semicolon inside a string is not a separator", `SELECT 'a;b' AS a`},
		{"a semicolon inside a comment is not a separator", "SELECT 1 -- ; DROP TABLE t"},
	}
	for _, c := range ok {
		if err := CheckReadOnly(c.q); err != nil {
			t.Errorf("refused a legitimate query (%s): %v\n  %s", c.why, err, c.q)
		}
	}
}

// And the ones that must never run.
func TestReadOnlyRefusesEverythingElse(t *testing.T) {
	bad := []struct{ why, q string }{
		{"the obvious write", `DELETE FROM orders`},
		{"an update", `UPDATE t SET a = 1`},
		{"an insert", `INSERT INTO t VALUES (1)`},
		{"schema loss", `DROP TABLE orders`},
		{"emptying a table", `TRUNCATE TABLE orders`},
		{"a schema change", `ALTER TABLE t ADD c INT`},
		{"creating something", `CREATE TABLE t (a INT)`},
		{"privileges", `GRANT ALL ON *.* TO 'x'@'%'`},

		// The ones that hide behind a leading SELECT.
		{"two statements", `SELECT 1; DROP TABLE t`},
		{"two statements, the second commented-looking", `SELECT 1;DELETE FROM t`},
		{"T-SQL SELECT INTO, which creates a table", `SELECT * INTO newtbl FROM t`},
		{"MySQL INTO OUTFILE, which writes a file", `SELECT * FROM t INTO OUTFILE '/tmp/x'`},
		{"MySQL INTO DUMPFILE", `SELECT a FROM t INTO DUMPFILE '/tmp/x'`},
		{"reading a file", `SELECT LOAD_FILE('/etc/passwd')`},
		{"a shell through SQL Server", `SELECT 1 FROM (SELECT 1) x; EXEC xp_cmdshell 'dir'`},
		{"xp_cmdshell without a second statement", `SELECT * FROM OPENROWSET('x','y','z')`},
		{"any extended procedure", `SELECT * FROM t WHERE a = xp_fileexist('c:\x')`},
		{"any stored procedure", `SELECT sp_who()`},
		{"holding the pool open", `SELECT SLEEP(100)`},
		{"the T-SQL version of that", `SELECT 1; WAITFOR DELAY '00:10:00'`},
		{"changing the session", `SET autocommit = 0`},
		{"changing database", `USE master`},
		{"a procedure call", `CALL something()`},
		{"transaction control", `BEGIN TRANSACTION`},
		{"a variable declaration, the usual smuggling route", `DECLARE @x INT`},
		{"nothing at all", ``},
		{"only a comment", `-- nothing here`},
		{"only whitespace", "  \n\t "},

		// Malformed in ways that would let a scan be fooled.
		{"an unclosed string, so the rest cannot be read", `SELECT 'abc`},
		{"an unclosed block comment", `SELECT 1 /* forever`},
		{"an unclosed bracket name", `SELECT [abc`},

		// MySQL versioned comments and optimizer hints run for real, so a
		// denied word hidden inside one must still be seen by the scan.
		{"SLEEP in a versioned comment", `SELECT 1 /*!00000 OR SLEEP(5)*/`},
		{"LOAD_FILE in a versioned comment", `SELECT 1 /*!50000 UNION SELECT LOAD_FILE('/etc/passwd')*/`},
		{"INTO OUTFILE in a versioned comment", `SELECT 1 /*! INTO OUTFILE '/tmp/x'*/`},
		{"a denied word in an optimizer hint", `SELECT /*+ SLEEP(5) */ 1`},
		{"a second statement smuggled in a versioned comment", `SELECT 1 /*!; DROP TABLE t*/`},
	}
	for _, c := range bad {
		if err := CheckReadOnly(c.q); err == nil {
			t.Errorf("ALLOWED a query that must be refused (%s):\n  %s", c.why, c.q)
		}
	}
}

// The refusal has to say what to change.
//
// An administrator in a text box who is told "invalid query" learns nothing
// and tries again at random, so the message names the word and the reason.
func TestReadOnlyErrorsNameTheProblem(t *testing.T) {
	cases := []struct{ q, want string }{
		{`DELETE FROM t`, "DELETE"},
		{`SELECT * INTO x FROM t`, "INTO"},
		{`SELECT 1; DROP TABLE t`, "one statement"},
		{`EXEC xp_cmdshell 'dir'`, "SELECT"}, // it does not even start as a read
		{`SELECT xp_cmdshell('dir')`, "xp_cmdshell"},
		{`SELECT 'abc`, "never closed"},
	}
	for _, c := range cases {
		err := CheckReadOnly(c.q)
		if err == nil {
			t.Errorf("%q was allowed", c.q)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("for %q the message was %q, which does not mention %q",
				c.q, err.Error(), c.want)
		}
	}
}

// Every example query in examples/reports must pass the guard.
//
// If the rule refuses the application's own reports it is the wrong rule, and
// this is the cheapest possible way to find that out. It reads the catalogue
// itself, so it covers whatever is actually there rather than a list
// repeated here.
func TestReadOnlyAllowsEveryShippedReport(t *testing.T) {
	c, errs := LoadCatalog(repoReportsDir(t))
	if c == nil {
		t.Skipf("no report directory to check: %v", errs)
	}
	n := 0
	// List() strips the SQL deliberately -- it is what the browser receives --
	// so the query has to come from Get(), which is the execution path.
	for _, listed := range c.List() {
		r, ok := c.Get(listed.ID)
		if !ok {
			t.Errorf("report %q is listed but Get() does not return it", listed.ID)
			continue
		}
		sql := r.SQL
		if strings.TrimSpace(sql) == "" {
			t.Errorf("report %q has no SQL", r.ID)
			continue
		}
		n++
		// The shipped queries carry @name placeholders, which the guard has to
		// tolerate -- they are bound later, not substituted here.
		if err := CheckReadOnly(sql); err != nil {
			t.Errorf("report %q is refused by the guard: %v", r.ID, err)
		}
	}
	if n == 0 {
		t.Error("no report SQL was checked, so this test proved nothing")
	}
	t.Logf("%d shipped report queries all pass the read-only guard", n)
}
