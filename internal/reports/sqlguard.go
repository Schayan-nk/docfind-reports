package reports

import (
	"fmt"
	"strings"
)

// Refusing everything that is not a read.
//
// The report builder puts a text box in front of an administrator and runs
// what they type against a production database. That is a useful tool and it
// is also the most dangerous thing in this application, so the rule is narrow
// and enforced here rather than trusted to whoever is typing: one statement,
// and it must be a SELECT.
//
// This is a second line and not the only one. The first is the database
// account in the connection string, which should be able to read and nothing
// else -- a guard in Go cannot stop a query the server was willing to run, and
// a read-only account stops every one of them including the ones this file has
// not thought of. Both, because each catches what the other misses: the
// account cannot tell a report from a mistake, and this cannot tell a
// privileged account from a careful one.
//
// What it must survive is not a determined attacker -- an administrator who
// wants to drop a table has a database client and does not need this feature.
// It is the honest accident: a paste that picked up more than one statement, a
// DELETE left in a scratch query, a WHERE clause edited into an UPDATE. Those
// are what actually destroy data.

// Two kinds of dangerous word, and the distinction is the whole design.
//
// The first draft had one list: every write verb, refused wherever it
// appeared. It was wrong, and the test that reads the application's own ten
// reports is what said so -- workload_by_expert calls REPLACE(name, 'x', ”),
// which is MySQL's string function and has nothing to do with REPLACE INTO.
// A rule that refuses the product's own reports is the wrong rule, and the
// tempting fix -- a special case for REPLACE -- would have left the same
// mistake waiting for the next report that uses CREATE_TIME or a column
// called `load`.
//
// So the model is:
//
//   - A STATEMENT VERB is only dangerous in the one position where it starts a
//     statement. DELETE, DROP, UPDATE, REPLACE INTO, GRANT, SET, USE, DECLARE,
//     BEGIN and the rest cannot do anything in the middle of a SELECT -- there
//     is no valid SQL where they do. They are already excluded, twice over, by
//     the two structural rules below: the text must BEGIN with SELECT or WITH,
//     and it must be a single statement. So they are not in a word list at all,
//     and REPLACE() is free to be a function again.
//
//   - A word that is dangerous ANYWHERE is one that works inside a perfectly
//     ordinary SELECT. Those are the list below, and it is short because there
//     are not many: the file and shell reaches, the cross-server reaches, and
//     the ways to hold a connection open.
//
// The structural rules carry the weight; this list covers what they cannot
// see. That is also the honest description of what this feature needs, since
// what it is defending against is an accident -- a paste that picked up two
// statements, a DELETE left in a scratch query -- not an administrator who
// wants to drop a table and already has a database client.
var deniedAnywhere = map[string]string{
	// The writes and reads that hide inside a legal SELECT.
	//
	// INTO is the important one and it is not obvious: T-SQL's
	// "SELECT * INTO newtable FROM t" creates a table, and MySQL's
	// "SELECT ... INTO OUTFILE '/path'" writes a file. Both are single SELECT
	// statements. No report needs the keyword, so it goes.
	"into":     "writes -- SELECT INTO creates a table, INTO OUTFILE writes a file",
	"outfile":  "writes a file on the database server",
	"dumpfile": "writes a file on the database server",
	"infile":   "reads a file from the database server",

	// Running something this cannot look inside.
	"exec":    "runs something this cannot inspect",
	"execute": "runs something this cannot inspect",

	// Reaching other servers and the file system from within a query.
	"openrowset":     "reads outside this database",
	"opendatasource": "reads outside this database",
	"openquery":      "reads outside this database",
	"openxml":        "reads outside this database",

	// File and operating-system functions. The word scan keeps underscores,
	// so each arrives as a single token and has to be named in full.
	"load_file":         "reads a file from the database server",
	"loadfile":          "reads a file from the database server",
	"pg_read_file":      "reads a file from the database server",
	"pg_ls_dir":         "lists directories on the database server",
	"lo_import":         "reads a file from the database server",
	"lo_export":         "writes a file on the database server",
	"utl_file":          "reads and writes files on the database server",
	"sys_exec":          "runs an operating-system command",
	"sys_eval":          "runs an operating-system command",
	"fn_trace_gettable": "reads trace files from the database server",

	// Holding a pooled connection open is a one-line denial of service: the
	// pool is eight wide and shared with every other reader.
	"sleep":     "holds a database connection open",
	"benchmark": "holds a database connection open",
	"waitfor":   "holds a database connection open",
	"pg_sleep":  "holds a database connection open",
}

// CheckReadOnly returns nil when the text is a single SELECT and an error
// naming the problem otherwise.
//
// The error is written for the person in the builder, not for a log: it says
// which word was refused and why, because "invalid query" in a text box tells
// somebody nothing about what to change.
func CheckReadOnly(query string) error {
	stripped, err := stripSQL(query)
	if err != nil {
		return err
	}

	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" {
		return fmt.Errorf("the query is empty")
	}

	// One statement. A semicolon inside a string or a comment is already gone,
	// so anything left is a real separator -- and a trailing one is fine,
	// because people end statements out of habit.
	if i := strings.Index(strings.TrimSuffix(trimmed, ";"), ";"); i >= 0 {
		return fmt.Errorf("only one statement can be run at a time; there is a %q after the first",
			strings.TrimSpace(firstRunes(trimmed[i+1:], 24)))
	}
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))

	// It must open as a read. Leading brackets are allowed because a union is
	// often written "(SELECT ...) UNION (SELECT ...)".
	head := strings.TrimLeft(trimmed, "( \t\r\n")
	lower := strings.ToLower(head)
	if !(hasWordPrefix(lower, "select") || hasWordPrefix(lower, "with")) {
		return fmt.Errorf("a report query has to start with SELECT (or WITH); this starts with %q",
			firstRunes(head, 16))
	}

	for _, w := range words(stripped) {
		if why, bad := deniedAnywhere[w]; bad {
			return fmt.Errorf("%s is not allowed in a report query: it %s", strings.ToUpper(w), why)
		}
		// Every extended and stored procedure, not only the ones listed. A
		// report has no business calling any of them, and naming them
		// individually is a list that goes out of date.
		if strings.HasPrefix(w, "xp_") || strings.HasPrefix(w, "sp_") {
			return fmt.Errorf("%s is not allowed: stored and extended procedures can do things "+
				"a report cannot check, and xp_cmdshell runs operating-system commands", w)
		}
	}
	return nil
}

// stripSQL removes comments and the contents of string literals, so the word
// scan cannot be fooled by either.
//
// This is the part that has to be right. A denied word hidden in a comment is
// harmless and must not trip the check; the same word hidden in a string is
// also harmless; but a real statement smuggled after a comment marker or an
// unbalanced quote is not, so an unterminated literal is an error rather than
// something to guess at.
//
// String CONTENTS are replaced by an empty literal rather than deleted, so
// "WHERE name = 'x'" stays recognisable as a comparison.
func stripSQL(q string) (string, error) {
	var b strings.Builder
	r := []rune(q)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		// -- to end of line
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		// # to end of line (MySQL)
		case c == '#':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		// /* ... */, which does not nest in either engine. BUT a versioned
		// comment /*! ... */ and an optimizer hint /*+ ... */ are NOT inert:
		// MySQL executes the body of /*!NNNNN ... */ whenever its version
		// qualifies (/*!00000 always does). Blanking it like an ordinary
		// comment would hide a denied word (SLEEP, LOAD_FILE, INTO OUTFILE) from
		// the scan while MySQL ran it for real. So for those, consume only the
		// opener and let the body be scanned as live SQL; the lone */ that
		// closes it is handled as whitespace by the case below.
		case c == '/' && i+1 < len(r) && r[i+1] == '*' && i+2 < len(r) && (r[i+2] == '!' || r[i+2] == '+'):
			i += 3
			for i < len(r) && r[i] >= '0' && r[i] <= '9' { // version digits of /*!NNNNN
				i++
			}
			b.WriteByte(' ')
		case c == '*' && i+1 < len(r) && r[i+1] == '/':
			i += 2
			b.WriteByte(' ')
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			i += 2
			for i+1 < len(r) && !(r[i] == '*' && r[i+1] == '/') {
				i++
			}
			if i+1 >= len(r) {
				return "", fmt.Errorf("a /* comment is never closed")
			}
			i += 2
			b.WriteByte(' ')
		// 'single quoted', with '' as an escaped quote and \' in MySQL
		case c == '\'':
			i++
			for i < len(r) {
				if r[i] == '\\' && i+1 < len(r) {
					i += 2
					continue
				}
				if r[i] == '\'' {
					if i+1 < len(r) && r[i+1] == '\'' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			if i >= len(r) {
				return "", fmt.Errorf("a quoted value is never closed -- check the ' marks")
			}
			i++
			b.WriteString("''")
		// "double quoted": an identifier in standard SQL and a string in
		// MySQL. Either way its contents are not keywords.
		case c == '"':
			i++
			for i < len(r) {
				if r[i] == '\\' && i+1 < len(r) {
					i += 2
					continue
				}
				if r[i] == '"' {
					break
				}
				i++
			}
			if i >= len(r) {
				return "", fmt.Errorf("a double-quoted name is never closed")
			}
			i++
			b.WriteString(`""`)
		// `backticked` MySQL identifier
		case c == '`':
			i++
			for i < len(r) && r[i] != '`' {
				i++
			}
			if i >= len(r) {
				return "", fmt.Errorf("a `backticked` name is never closed")
			}
			i++
			b.WriteString("``")
		// [bracketed] T-SQL identifier. Kept as a placeholder rather than
		// dropped: [order] is a column name and must not read as the keyword.
		case c == '[':
			i++
			for i < len(r) && r[i] != ']' {
				i++
			}
			if i >= len(r) {
				return "", fmt.Errorf("a [bracketed] name is never closed")
			}
			i++
			b.WriteString("[]")
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String(), nil
}

// words splits on everything that cannot be part of an identifier, lower-cased.
// Underscores stay, so xp_cmdshell and sp_executesql arrive whole.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9'))
	})
}

// hasWordPrefix reports whether s starts with word, followed by something that
// cannot continue an identifier -- so "selected" does not count as "select".
func hasWordPrefix(s, word string) bool {
	if !strings.HasPrefix(s, word) {
		return false
	}
	if len(s) == len(word) {
		return true
	}
	c := s[len(word)]
	return !(c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'))
}

func firstRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
