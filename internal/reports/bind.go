// Package reports runs saved SQL reports.
//
// A report's filters are written as @named placeholders, the way reporting
// tools commonly let a query take a value, and such tools typically
// substitute the value into the text before execution. That is safe enough on
// a LAN tool nobody outside can reach; it is not safe in an application on the
// public internet, where a substituted value is a straight SQL injection into
// a production database. So the named placeholders are rewritten to real bind
// parameters here, and the values never touch the SQL text.
package reports

import (
	"fmt"
	"strings"
)

// ParamKind decides how a value is bound and what the UI shows.
type ParamKind string

const (
	// KindText binds a plain value.
	KindText ParamKind = "text"
	// KindLike binds a LIKE pattern. An empty value becomes "%", which is
	// "no filter" -- and that is the difference between an unfiltered report
	// and an empty one.
	KindLike ParamKind = "like"
	// KindDate binds a date. Columns of Jalali dates are often *strings*,
	// compared with BETWEEN, so ordering only
	// holds while the format stays zero-padded and consistent.
	KindDate ParamKind = "date"
	// KindList expands to an IN (?,?,?) group. A list cannot be one bind
	// variable: binding "1,2" as a single value matches the literal string
	// "1,2" and silently returns nothing.
	KindList ParamKind = "list"
	// KindChoice is one value picked from a fixed set.
	//
	// Distinct from KindLike even though both sit against a LIKE clause: a
	// chosen value is bound whole, not wrapped in wildcards. Filtering status
	// by picking "TO_DO" should mean that status exactly, where "%TO_DO%"
	// would also match anything merely containing it. Empty still means "no
	// filter", which is "%".
	KindChoice ParamKind = "choice"
	// KindAnyOf is several values in one filter: the row matches if the column
	// contains ANY of them. It binds one argument, a REGEXP alternation, and
	// the query compares with REGEXP rather than LIKE.
	//
	// KindList cannot do this job. It expands to IN (?,?,?), which is an exact
	// match, and the columns people want to select several values on are often
	// GROUP_CONCAT roll-ups: one cell reads "North Co - South Co - West Co",
	// and IN ('South Co') matches none of those cells. What is wanted is
	// "contains any of these", and one LIKE cannot express it -- LIKE has no
	// alternation.
	//
	// The other shape considered was a kind expanding one @p into
	// "(col LIKE ? OR col LIKE ?)". It was rejected on the structure of this
	// file: Bind rewrites a placeholder where it stands and never learns what
	// column it sits against, so either the .sql would have to repeat the
	// column name to the binder in a directive of its own, or the binder would
	// have to read the SQL around the placeholder to find it. Both put SQL
	// parsing in the one place that exists to keep values out of SQL.
	//
	// Values arrive newline-separated, not comma-separated as KindList's are: a
	// company name can contain a comma, and splitting on one would silently
	// search for two firms that do not exist.
	KindAnyOf ParamKind = "anyof"
)

// Option is one entry in a choice filter: the value bound into the query, and
// the Persian text shown for it.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Param describes one filter a report accepts.
type Param struct {
	Name    string    `json:"name"`
	Label   string    `json:"label"`
	Kind    ParamKind `json:"kind"`
	Default string    `json:"default"`
	// Options turns the filter into a dropdown. Present for the columns whose
	// values are a fixed set the database already decides -- an order type, a
	// case status -- where a free-text box invites someone to type "در جریان"
	// against a column holding "TO_DO" and get nothing back with no
	// explanation.
	Options []Option `json:"options,omitempty"`
	// Suggest names where to look up completions while someone types. Used for
	// the open-ended name filters, where the set is too large for a dropdown
	// but a person still should not have to remember whether a colleague is
	// filed as "محمدرضا" or "محمد رضا".
	Suggest *SuggestSource `json:"-"`
	// Suggestable is what the browser is told: that completions exist, without
	// naming the table they come from.
	Suggestable bool `json:"suggestable,omitempty"`
	// Calendar says which calendar a date filter is expressed in. Almost every
	// date column here holds a Jalali date as text; one filters a real
	// DATETIME and needs Gregorian. Getting this wrong produces a filter that
	// silently matches nothing.
	Calendar string `json:"calendar,omitempty"`
	// BindCalendar is the calendar the value is bound in when it is not the
	// one the picker shows ("bind=gregorian"): a Jalali picker against a real
	// datetime column. Empty binds the value as typed. See datebind.go.
	BindCalendar string `json:"-"`
	// Required drops the «همه» entry from a dropdown: the choice switches what
	// is shown -- with or without VAT -- rather than narrowing the rows, so
	// "all" has no meaning, and an empty value is refused rather than bound.
	Required bool `json:"required,omitempty"`
	// SuggestTable and SuggestColumn are an AUTHORED report's suggestion
	// source, and unlike Suggest above they do cross the wire.
	//
	// Two separate strings rather than the SuggestSource struct, so the
	// difference is visible at a glance: these arrive from a browser and are
	// proven with safeIdentifier before they reach a statement, where
	// Suggest's are read from a .sql file by somebody who has read this
	// package. See customsuggest.go for why an administrator may name a
	// column here at all.
	SuggestTable  string `json:"suggest_table,omitempty"`
	SuggestColumn string `json:"suggest_column,omitempty"`
	// SuggestPerson folds job titles out of the suggested names, the way the
	// shipped lists do for the expert filters.
	SuggestPerson bool `json:"suggest_person,omitempty"`
}

// SuggestSource is the table and column a filter's completions come from.
//
// Held as identifiers rather than a query so the server builds the statement
// itself: identifiers cannot be bound as parameters, so anything reaching this
// position has to be validated rather than trusted, and a narrow
// "table.column" shape is far easier to validate than arbitrary SQL.
type SuggestSource struct {
	Table  string
	Column string
	// Person says the column holds people's names.
	//
	// Some systems store the job title with the name, so one person who has
	// held two roles is two distinct values -- and a DISTINCT list offers them
	// as two people. Set from the .sql file rather than guessed from
	// the parameter name: the catalog declares everything else about a filter
	// there, and a column full of company names must not be folded by
	// accident. See foldPeople for what it does and what it refuses to do.
	Person bool
}

// placeholderSpan is one @named placeholder, and where it sits.
//
// From and To are rune offsets, so runes[From:To] is the "@name" text itself.
// Held as offsets rather than just names because the binder has to replace
// each one in place, and the report builder has to be able to tell two
// different placeholders apart from two uses of the same one.
type placeholderSpan struct {
	Name     string
	From, To int
}

// findPlaceholders is the one place that decides what counts as a placeholder.
//
// Single definition on purpose. Three separate walks over a statement now need
// this answer -- the binder below, the builder's filter discovery, and the SQL
// Server placeholder rewrite in bindfor.go -- and two of them disagreeing is a
// specific, quiet bug: the builder would offer a filter the binder then refuses
// to recognise, or the rewrite would renumber something the binder never
// bound.
//
// Everything skipped here is somewhere an "@" is not a placeholder:
//
//	'text', "text", `name`   a value or a quoted identifier
//	[name]                   a SQL Server delimited identifier
//	-- ... , # ... , /* */    a comment
//	@@name                   a server variable, not a parameter
//
// The @@ case is what makes "SELECT @@VERSION" work -- the first thing someone
// is likely to type to check a new connection, and without this rule it would
// be read as a parameter named VERSION and rejected as undeclared.
func findPlaceholders(runes []rune) []placeholderSpan {
	var out []placeholderSpan
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(runes, i)
			continue
		case c == '[':
			i = skipBracketed(runes, i)
			continue
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			i = skipToEOL(runes, i)
			continue
		case c == '#':
			i = skipToEOL(runes, i)
			continue
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i = skipBlockComment(runes, i)
			continue
		case c == '@' && i+1 < len(runes) && runes[i+1] == '@':
			i += 2
			continue
		case c == '@':
			j := i + 1
			for j < len(runes) && isIdentRune(runes[j]) {
				j++
			}
			if j == i+1 {
				i++ // a bare "@" with no name after it
				continue
			}
			out = append(out, placeholderSpan{Name: string(runes[i+1 : j]), From: i, To: j})
			i = j
			continue
		}
		i++
	}
	return out
}

// Bind rewrites @named placeholders into ? placeholders and returns the
// arguments in the order the driver expects them.
//
// The "?" style is MySQL's. SQL Server rejects it, and BindFor rewrites what
// this produces for that driver -- see bindfor.go, which exists because the
// live server said so.
func Bind(sql string, params []Param, values map[string]string) (string, []any, error) {
	byName := make(map[string]Param, len(params))
	for _, p := range params {
		byName[p.Name] = p
	}

	var out strings.Builder
	out.Grow(len(sql) + 64)
	var args []any

	runes := []rune(sql)
	at := 0
	for _, sp := range findPlaceholders(runes) {
		// Everything between the last placeholder and this one travels through
		// untouched, comments and literals included.
		out.WriteString(string(runes[at:sp.From]))
		at = sp.To

		p, known := byName[sp.Name]
		if !known {
			return "", nil, fmt.Errorf("reports: query uses @%s but the report declares no such parameter", sp.Name)
		}
		raw, given := values[sp.Name]
		if !given || strings.TrimSpace(raw) == "" {
			raw = p.Default
		}

		switch p.Kind {
		case KindList:
			items := splitList(raw)
			if len(items) == 0 {
				// An empty list must not become "IN ()", which is a syntax
				// error. NULL matches nothing, which is the honest reading
				// of "the user selected no options".
				out.WriteString("NULL")
				break
			}
			out.WriteString(strings.TrimRight(strings.Repeat("?,", len(items)), ","))
			for _, it := range items {
				args = append(args, it)
			}
		case KindLike:
			out.WriteString("?")
			args = append(args, likePattern(raw))
		case KindAnyOf:
			out.WriteString("?")
			args = append(args, anyPattern(raw))
		case KindChoice:
			out.WriteString("?")
			// Bound whole rather than as a fragment. These sit against LIKE
			// clauses, where a string with no wildcard in it is an equality
			// test -- which is what picking one option from a list means.
			if v := strings.TrimSpace(raw); v != "" {
				args = append(args, v)
			} else if p.Required {
				return "", nil, fmt.Errorf("reports: @%s needs one of its options chosen", p.Name)
			} else {
				args = append(args, "%")
			}
		case KindDate:
			out.WriteString("?")
			v, err := bindDate(p, raw)
			if err != nil {
				return "", nil, err
			}
			args = append(args, v)
		default:
			out.WriteString("?")
			args = append(args, raw)
		}
	}
	out.WriteString(string(runes[at:]))
	return out.String(), args, nil
}

// likePattern turns what someone typed into a filter box into a LIKE pattern.
//
// Empty means "no filter", which is "%".
// Anything else is matched as a *fragment*: typing a customer's name should
// find every row containing it, which is what a filter box is for. Binding the
// text bare would produce "LIKE 'احمد'", an exact match that finds nothing
// unless the column holds precisely that and nothing more.
//
// A value that already contains a wildcard is left exactly as typed, so
// someone who knows LIKE syntax can anchor a match ("ORD-4%") instead of
// having it silently widened.
func likePattern(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "%"
	}
	if strings.ContainsAny(v, "%_") {
		return v
	}
	return "%" + v + "%"
}

// dotAll makes "." cover a newline, so a translated wildcard reaches as far as
// the "%" it came from.
//
// Measured on MySQL 8.0.31: "'a\nb' LIKE 'a%'" is 1 but "'a\nb' REGEXP '^a.*$'"
// is 0, and with this prefix it is 1 again. Applied to the whole pattern, where
// an inline flag holds to the end of its group.
const dotAll = "(?s)"

// anyEverything is the pattern an empty KindAnyOf filter binds: "no filter".
//
// Spelled out because the obvious choices are both wrong. An empty pattern is
// not "match everything" but an error -- MySQL 8.0.31 answers
// "Illegal argument to a regular expression" and the whole report fails -- and
// a pattern that only matched non-empty text would drop exactly the rows a
// report's COALESCE wrappers exist to keep. On MySQL 8.0.31 an empty value
// matches "(?s).*", so an unfiltered report keeps its rows with empty
// roll-ups.
const anyEverything = dotAll + ".*"

// anyPattern turns the values chosen in one filter into a single MySQL REGEXP
// pattern that matches a row containing any of them.
//
// Each value is matched as a fragment, which is what the LIKE filters these
// replaced did and what these columns require: the cell holds a list, so an
// exact match would find nothing. A value carrying a LIKE wildcard keeps the
// meaning it has always had here instead -- see likeToRegexp -- so somebody who
// anchored a search by typing "ORD-4%" still gets the anchored search rather
// than a hunt for a literal percent sign.
func anyPattern(raw string) string {
	items := splitLines(raw)
	if len(items) == 0 {
		return anyEverything
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if strings.ContainsAny(it, "%_") {
			// LIKE matches the whole string, so its translation is anchored.
			parts = append(parts, "^"+likeToRegexp(it)+"$")
			continue
		}
		parts = append(parts, regexpQuote(it))
	}
	// No grouping around the branches: alternation is the lowest-precedence
	// operator there is, so the anchors above belong to their own branch.
	return dotAll + strings.Join(parts, "|")
}

// regexpMeta is every character MySQL's regular expressions give a meaning to
// outside a bracket expression.
//
// Only those, never everything: MySQL 8 matches with ICU, where a backslash in
// front of an ordinary letter is itself an error, so escaping indiscriminately
// would turn a search for a name into a failed query.
const regexpMeta = `\^$.|?*+()[]{}`

// regexpQuote escapes a chosen value so it matches itself and nothing else.
//
// This is not tidiness. A company filed as "Example Co (North)" reads,
// unescaped, as a group: the pattern then matches "Example Co North", which
// no row holds, and the filter returns nothing. Names carry "." as well
// ("نادر م."), and a value with an unbalanced bracket would fail the entire
// query rather than merely mis-filter.
func regexpQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if r < 128 && strings.ContainsRune(regexpMeta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// likeToRegexp rewrites a LIKE pattern as the equivalent regular expression:
// "%" is any run of characters and "_" is any single one, everything else
// matches itself.
//
// The anchors LIKE implies are added by the caller, which is also where the
// dotAll flag is applied -- both belong to the whole pattern rather than to
// this fragment of it.
func likeToRegexp(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '%':
			b.WriteString(".*")
		case r == '_':
			b.WriteByte('.')
		case r < 128 && strings.ContainsRune(regexpMeta, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitLines turns the browser's newline-separated selection into its separate
// values. Blank lines are dropped, so a trailing newline is not an empty
// choice.
func splitLines(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "\n") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitList turns "1,2, 3" into the separate values an IN group needs.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func isIdentRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// skipQuoted returns the index just past a quoted run starting at i,
// honouring both doubled quotes (” ) and backslash escapes.
func skipQuoted(r []rune, i int) int {
	q := r[i]
	j := i + 1
	for j < len(r) {
		if r[j] == '\\' && q != '`' && j+1 < len(r) {
			j += 2
			continue
		}
		if r[j] == q {
			if j+1 < len(r) && r[j+1] == q {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(r)
}

func skipToEOL(r []rune, i int) int {
	for j := i; j < len(r); j++ {
		if r[j] == '\n' {
			return j + 1
		}
	}
	return len(r)
}

func skipBlockComment(r []rune, i int) int {
	for j := i + 2; j+1 < len(r); j++ {
		if r[j] == '*' && r[j+1] == '/' {
			return j + 2
		}
	}
	return len(r)
}
