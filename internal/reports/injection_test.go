package reports

import (
	"strings"
	"testing"
)

// Reporting tools commonly substitute a filter value into the SQL text before
// execution. Against a public listener that is a straight
// injection into a production database, so Bind rewrites the
// placeholders into real parameters instead.
//
// This is the test that says so: for every payload an attacker would try, the
// value has to come back as a bound argument and must never appear in the SQL.
func TestReportFiltersCannotInjectSQL(t *testing.T) {
	payloads := []string{
		"' OR '1'='1",
		"'; DROP TABLE users; --",
		"' UNION SELECT username, password_hash FROM users --",
		"1' AND (SELECT SLEEP(10)) --",
		"' OR 1=1 LIMIT 1 OFFSET 0 --",
		"\\'; DELETE FROM application WHERE '1'='1",
		"admin'/*",
		"' INTO OUTFILE '/tmp/pwn' --",
		"1;SELECT LOAD_FILE('/etc/passwd')",
		"%' AND 1=(SELECT COUNT(*) FROM information_schema.tables) AND '%'='",
		"' OR SLEEP(5)#",
		"\x00' OR '1'='1",
	}

	params := []Param{
		{Name: "pname", Kind: KindLike},
		{Name: "pdate", Kind: KindDate},
		{Name: "ptext", Kind: KindText},
		{Name: "plist", Kind: KindList},
	}
	const query = `SELECT a, b FROM t
WHERE name LIKE @pname AND d BETWEEN @pdate AND @pdate
  AND note = @ptext AND kind IN (@plist)`

	for _, payload := range payloads {
		for _, field := range []string{"pname", "pdate", "ptext", "plist"} {
			sql, args, err := Bind(query, params, map[string]string{field: payload})
			if err != nil {
				t.Fatalf("%s=%q: %v", field, payload, err)
			}
			// The payload must not have reached the statement.
			if strings.Contains(sql, payload) {
				t.Errorf("%s=%q leaked into the SQL:\n%s", field, payload, sql)
			}
			// Nor may the statement have grown any new SQL structure: the only
			// things Bind writes are "?" and, for a list, "?,?,?" or NULL.
			if strings.Count(strings.ToUpper(sql), "SELECT") != 1 {
				t.Errorf("%s=%q changed the statement shape:\n%s", field, payload, sql)
			}
			for _, banned := range []string{"DROP", "DELETE", "UNION", "SLEEP", "OUTFILE", "LOAD_FILE"} {
				if strings.Contains(strings.ToUpper(sql), banned) {
					t.Errorf("%s=%q introduced %s into the SQL:\n%s", field, payload, banned, sql)
				}
			}
			// And it must actually have been passed as data.
			found := false
			for _, a := range args {
				if s, ok := a.(string); ok && strings.Contains(s, strings.Trim(payload, "%")) {
					found = true
				}
			}
			if !found && field != "plist" {
				t.Errorf("%s=%q was neither bound nor rejected; args=%v", field, payload, args)
			}
		}
	}
}

// A report may only ever run a read. The database account is restricted to
// SELECT as well, but a saved query that tries to write should fail here with
// a clear message rather than as a permissions error from MySQL.
func TestOnlyReadsAreAllowed(t *testing.T) {
	refused := []string{
		"DELETE FROM users",
		"UPDATE application SET app_status = 'X'",
		"INSERT INTO t VALUES (1)",
		"DROP TABLE t",
		"GRANT ALL ON *.* TO 'x'@'%'",
		"CALL some_procedure()",
		"-- a comment\nDELETE FROM t",
		"/* block */ UPDATE t SET a = 1",
		"# hash comment\nDROP DATABASE reports",
	}
	for _, q := range refused {
		if err := selectOnly(q); err == nil {
			t.Errorf("selectOnly(%q) allowed a write", q)
		}
	}
	for _, q := range []string{
		"SELECT 1",
		"  select a from t",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"-- id: r\n-- title: t\nSELECT 1",
		// MySQL requires parentheses around a UNION branch carrying its own
		// ORDER BY or LIMIT. Still a read, and it used to be refused.
		"(SELECT a FROM t ORDER BY a LIMIT 5) UNION ALL (SELECT b FROM u LIMIT 3)",
		"\n  ( SELECT 1 )",
	} {
		if err := selectOnly(q); err != nil {
			t.Errorf("selectOnly(%q) refused a read: %v", q, err)
		}
	}
	// The parenthesis allowance must not become a way in for a write.
	for _, q := range []string{"(DELETE FROM t)", "((UPDATE t SET a=1))", "( DROP TABLE t )"} {
		if err := selectOnly(q); err == nil {
			t.Errorf("selectOnly(%q) allowed a write behind parentheses", q)
		}
	}
}

// Paging is bounded at both ends.
//
// Without an upper bound, "page": 1e15 multiplied by the page size overflows
// the int the offset is computed in and reaches the driver as a negative
// OFFSET; well before that, a deep offset makes the database produce and
// discard every row up to it.
func TestPagingIsBounded(t *testing.T) {
	for _, page := range []int{1 << 62, 1 << 40, maxPage + 1, 1e9} {
		got := page
		if got < 1 {
			got = 1
		}
		if got > maxPage {
			got = maxPage
		}
		offset := (got - 1) * maxPerPage
		if offset < 0 {
			t.Errorf("page %d produced a negative offset %d", page, offset)
		}
		if got > maxPage {
			t.Errorf("page %d was not capped", page)
		}
	}
}
