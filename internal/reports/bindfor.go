package reports

import "strings"

// Binding for whichever engine the report runs against.
//
// Bind emits "?" placeholders, which is what MySQL wants and what the ten
// shipped reports have always used. SQL Server does not accept them: measured
// against the live server, "WHERE EngineCode = ?" comes back as
//
//	mssql: Incorrect syntax near '?'. (102)
//
// while "@p1" and a named parameter both work. So a report an administrator
// points at SQL Server would have had every filter fail -- and fail at the
// moment somebody typed in a filter box, not when the report was saved, which
// is the worst time to find out.
//
// Bind itself is left alone. Its signature is used by the shipped catalogue
// and covered by its own tests, and threading a driver through it would change
// every one of those call sites to say "mysql" for no benefit. The rewrite
// happens after, and only when it is needed.

// BindFor binds params and returns SQL in the placeholder style the driver
// speaks.
func BindFor(driver Driver, sql string, params []Param, values map[string]string) (string, []any, error) {
	bound, args, err := Bind(sql, params, values)
	if err != nil {
		return "", nil, err
	}
	if driver == DriverSQLServer {
		bound = ordinalPlaceholders(bound)
	}
	return bound, args, nil
}

// ordinalPlaceholders rewrites each "?" as "@p1", "@p2" and so on.
//
// The numbering has to follow the ARGUMENT order, which is the order the
// question marks appear in -- that is what Bind guarantees, because it appends
// an argument at the moment it writes a placeholder.
//
// Quoted strings and comments are stepped over rather than scanned. A literal
// question mark inside them is not a placeholder, and rewriting one would
// change what the query means: "WHERE note LIKE '%?%'" is a search for a
// question mark, and turning it into "'%@p3%'" searches for something else
// entirely while still running perfectly happily.
//
// The skip helpers are bind.go's, and this walk steps over exactly what
// findPlaceholders steps over. Two different opinions about where a literal
// ends is how a rewrite like this goes wrong: the binder would bind a value
// this walk then failed to renumber, and the argument list and the statement
// would disagree about how many parameters there are.
func ordinalPlaceholders(sql string) string {
	var out strings.Builder
	out.Grow(len(sql) + 16)
	runes := []rune(sql)
	n := 0
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := skipQuoted(runes, i)
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			j := skipToEOL(runes, i)
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		case c == '#':
			j := skipToEOL(runes, i)
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			j := skipBlockComment(runes, i)
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		case c == '[':
			j := skipBracketed(runes, i)
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		case c == '?':
			n++
			out.WriteString("@p")
			out.WriteString(itoaSmall(n))
			i++
			continue
		}
		out.WriteRune(c)
		i++
	}
	return out.String()
}

// skipBracketed steps over a SQL Server delimited identifier, [like this].
//
// Applied on both walks, findPlaceholders included, even though brackets mean
// nothing to MySQL. Checked before relying on that: none of the ten shipped
// reports contains a "[" outside a string literal, so treating one as a
// delimiter costs them nothing, and having a single answer to "where does an
// identifier end" is worth more than a per-driver rule.
//
// A closing bracket is escaped by doubling it, the same convention the quote
// helper uses: [a]]b] is one identifier named "a]b".
func skipBracketed(r []rune, i int) int {
	for j := i + 1; j < len(r); j++ {
		if r[j] != ']' {
			continue
		}
		if j+1 < len(r) && r[j+1] == ']' {
			j++
			continue
		}
		return j + 1
	}
	// Unclosed. Returning the rest of the statement means a stray "[" swallows
	// the query and any real placeholder after it goes unbound -- which the
	// server then rejects, loudly. That is the better failure: the alternative
	// is rewriting question marks inside what might be an identifier and
	// running something subtly different from what was written.
	return len(r)
}

// itoaSmall formats a small positive number without pulling in strconv for
// one call. Placeholder counts are in the tens.
func itoaSmall(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
