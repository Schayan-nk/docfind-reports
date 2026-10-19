package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Reports that read another server.
//
// A shipped report reads the reporting database (DOCFIND_DB_*) unless its
// file names another server:
//
//	-- source: finance
//
// and that server is attached at start-up from the environment, one
// connection string per name (SourcesFromEnv):
//
//	DOCFIND_SOURCE_FINANCE_DSN=sqlserver://user:password@db.example.com:1433?database=books
//
// The rest of the report is unchanged: the same directives, the same filters,
// the same page, chart, monthly banner and export, and the same permission
// per report. Only the connection and the SQL dialect differ, and both are
// decided here, in one place, so runner.go asks three questions (how to bind,
// how to page, where to send it) and never has to know which server answers.
//
// Why not the report builder's connections, which already reach SQL Server?
// Those are typed in through the interface by whoever holds the builder and
// sealed in a file; their reports live in the builder and run with its caps.
// A shipped report is granted one by one like the others, and its sign-in is
// the server's configuration, like the reporting database's. Reusing the
// builder would have put shipped reports behind a screen few people may open,
// and a production credential in a place anybody holding action:sources can
// replace.

// extSource is one attached server.
type extSource struct {
	driver Driver
	db     *sql.DB
}

// extSources is the set a DB knows about. The zero value is empty and usable,
// so a DB opened by the tests needs no set-up to answer "not attached".
type extSources struct {
	mu sync.RWMutex
	m  map[string]*extSource
}

// AttachSource makes a named server available to reports that declare it.
//
// SQL Server only, for now, because that is what the paging below is written
// for; a MySQL source would page with LIMIT and need a branch of its own here.
func (d *DB) AttachSource(name string, driver Driver, db *sql.DB) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validSourceName(name) {
		return fmt.Errorf("reports: %q is not a usable source name", name)
	}
	if driver != DriverSQLServer {
		return fmt.Errorf("reports: source %s: only SQL Server sources are supported", name)
	}
	if db == nil {
		return fmt.Errorf("reports: source %s: no connection", name)
	}
	d.ext.mu.Lock()
	defer d.ext.mu.Unlock()
	if d.ext.m == nil {
		d.ext.m = map[string]*extSource{}
	}
	if old, ok := d.ext.m[name]; ok && old.db != db {
		old.db.Close()
	}
	d.ext.m[name] = &extSource{driver: driver, db: db}
	return nil
}

// HasSource reports whether a source is attached. Handed to the catalogue, so a
// report on a server this deployment was never given is absent rather than a
// dashboard entry that fails every time it is opened.
func (d *DB) HasSource(name string) bool {
	if d == nil {
		return false
	}
	d.ext.mu.RLock()
	defer d.ext.mu.RUnlock()
	_, ok := d.ext.m[strings.ToLower(name)]
	return ok
}

// The variable that attaches one server: DOCFIND_SOURCE_<NAME>_DSN.
const (
	sourceEnvPrefix = "DOCFIND_SOURCE_"
	sourceEnvSuffix = "_DSN"
)

// SourcesFromEnv finds the other servers this deployment was given, as name
// to connection string, from environment entries ("KEY=value", the way
// os.Environ returns them).
//
// A variable whose name is not usable, or whose value is not a SQL Server
// URL, is reported rather than skipped: "I set it and nothing appeared" is
// otherwise a question with no answer in the log. An empty value is the
// ordinary way to switch one off, and says nothing.
func SourcesFromEnv(environ []string) (map[string]string, []error) {
	out := map[string]string{}
	var errs []error
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		upper := strings.ToUpper(key)
		if !ok || len(upper) <= len(sourceEnvPrefix)+len(sourceEnvSuffix) ||
			!strings.HasPrefix(upper, sourceEnvPrefix) || !strings.HasSuffix(upper, sourceEnvSuffix) {
			continue
		}
		name := strings.ToLower(key[len(sourceEnvPrefix) : len(key)-len(sourceEnvSuffix)])
		value = strings.TrimSpace(value)
		switch {
		case value == "":
		case !validSourceName(name):
			errs = append(errs, fmt.Errorf("%s: %q is not a usable source name (letters, digits and _)", key, name))
		case !strings.HasPrefix(strings.ToLower(value), "sqlserver://"):
			errs = append(errs, fmt.Errorf("%s: only SQL Server sources are supported (sqlserver://...)", key))
		default:
			out[name] = value
		}
	}
	return out, errs
}

// OpenSource opens one server's pool, without connecting: like the reporting
// database, a server that is briefly away must not stop this one starting,
// and a report that cannot reach it says so when it is opened.
//
// Three connections at most. Another server is usually somebody else's
// production system, and a handful of reports at a few milliseconds each need
// no more.
func OpenSource(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("reports: %w", err)
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(15 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

// DescribeSource is where a source points, without its sign-in, for the
// start-up log.
func DescribeSource(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "sqlserver://…"
	}
	if name := u.Query().Get("database"); name != "" {
		return u.Host + " / " + name
	}
	return u.Host
}

// closeSources shuts every attached pool; called by Close.
func (d *DB) closeSources() {
	d.ext.mu.Lock()
	defer d.ext.mu.Unlock()
	for name, s := range d.ext.m {
		s.db.Close()
		delete(d.ext.m, name)
	}
}

// sourceFor is the server a report reads: nil for the reporting MySQL.
func (d *DB) sourceFor(r Report) (*extSource, error) {
	if r.Source == "" {
		return nil, nil
	}
	d.ext.mu.RLock()
	defer d.ext.mu.RUnlock()
	s, ok := d.ext.m[r.Source]
	if !ok {
		return nil, fmt.Errorf("reports: %s reads %q, which this server is not connected to", r.ID, r.Source)
	}
	return s, nil
}

// bindReport binds a report's filters in the placeholder style of the server
// it reads.
//
// For another server, the typed text is also folded to Persian letters first.
// A SQL Server with an Arabic collation (Arabic_CI_AS) treats the Arabic ي and
// ك as letters different from ی and ک, and its reports' queries are expected
// to fold the column the same way -- so both sides meet in Persian whichever
// keyboard the name was typed on, there or here. Not done for the reporting
// database, whose data and whose readers are assumed to agree already.
func (d *DB) bindReport(r Report, values map[string]string) (string, []any, error) {
	if r.Source == "" {
		return Bind(r.SQL, r.Params, values)
	}
	return BindFor(DriverSQLServer, r.SQL, r.Params, foldedValues(r.Params, values))
}

// pageReport puts one page's bounds on a bound query.
//
// SQL Server has no LIMIT. OFFSET ... FETCH is its equivalent and needs the
// query to end in an ORDER BY, which a report on another server is required to
// have at load (checkSourced) -- a page without an order is a page whose rows
// may repeat or vanish between one click and the next.
func (d *DB) pageReport(r Report, bound string, args []any, limit, offset int) (string, []any, error) {
	if r.Source == "" {
		paged, err := appendLimit(bound)
		if err != nil {
			return "", nil, err
		}
		return paged, append(args, limit, offset), nil
	}
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(bound), ";"))
	n := len(args)
	paged := trimmed + "\nOFFSET @p" + itoaSmall(n+1) + " ROWS FETCH NEXT @p" + itoaSmall(n+2) + " ROWS ONLY"
	return paged, append(args, offset, limit), nil
}

// queryReport sends a bound query to the server the report reads. done closes
// whatever was opened and must be called once the rows have been read.
//
// Another server's query runs inside a transaction that is always rolled back
// (readonlyrun.go), as the builder's do: the account is read-only already, and
// this makes "these reports only read" true on the server as well as in the
// account's grants.
func (d *DB) queryReport(ctx context.Context, r Report, query string, args []any) (*sql.Rows, func(), error) {
	src, err := d.sourceFor(r)
	if err != nil {
		return nil, nil, err
	}
	if src == nil {
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, nil, err
		}
		return rows, func() { rows.Close() }, nil
	}
	return queryReadOnly(ctx, src.db, src.driver, query, args...)
}

// foldedValues is values with Arabic letters folded to Persian in every text
// filter, defaults included -- a default is typed by somebody too.
func foldedValues(params []Param, values map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range values {
		out[k] = v
	}
	for _, p := range params {
		switch p.Kind {
		case KindLike, KindText, KindAnyOf:
			v, given := out[p.Name]
			if !given || strings.TrimSpace(v) == "" {
				v = p.Default
			}
			if v != "" {
				out[p.Name] = persianLetters(v)
			}
		}
	}
	return out
}

// persianLetters replaces the Arabic yeh and kaf with the Persian ones.
func persianLetters(s string) string {
	return strings.NewReplacer("ي", "ی", "ك", "ک", "ى", "ی").Replace(s)
}

// validSourceName keeps a source name to lower-case letters, digits and "_",
// so it can never be confused with anything else on a directive line.
func validSourceName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// checkSourced refuses, at load, a report on another server that could not run
// there. Reported against the file, so a deploy notices it, rather than as a
// driver error the first time somebody opens the page.
func checkSourced(r Report) error {
	if r.Source == "" {
		return nil
	}
	if !validSourceName(r.Source) {
		return fmt.Errorf("source %q is not a usable name (lower-case letters, digits and _)", r.Source)
	}
	// The completions are a SELECT DISTINCT against the reporting MySQL, which
	// holds none of another server's tables.
	for _, p := range r.Params {
		if p.Suggest != nil {
			return fmt.Errorf("@%s suggests from %s, but this report reads %s; "+
				"suggestions only come from the reporting database", p.Name, p.Suggest, r.Source)
		}
	}
	return checkSQLServerPaging(r.SQL)
}

// checkSQLServerPaging makes sure OFFSET ... FETCH can be appended: the query
// ends in its own ORDER BY, and carries nothing that would collide with the
// clause -- a TOP in the outer SELECT (SQL Server refuses TOP and OFFSET in one
// query), or its own OFFSET, FOR XML or OPTION.
func checkSQLServerPaging(sqlText string) error {
	words := topLevelWords(sqlText)
	order := -1
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "ORDER" && words[i+1] == "BY" {
			order = i
		}
	}
	if order < 0 {
		return errors.New("a report on SQL Server must end with ORDER BY, so its pages keep their rows in order")
	}
	for i, w := range words {
		switch w {
		case "SELECT":
			next := i + 1
			if next < len(words) && (words[next] == "DISTINCT" || words[next] == "ALL") {
				next++
			}
			if next < len(words) && words[next] == "TOP" {
				return errors.New("remove the TOP from the outer SELECT: the dashboard pages the result itself, " +
					"and SQL Server refuses TOP and OFFSET in one query")
			}
		case "OFFSET", "FETCH", "OPTION", "FOR":
			if i > order {
				return fmt.Errorf("the query ends with its own %s; the dashboard pages the result itself, so remove it", w)
			}
		}
	}
	return nil
}

// topLevelWords is the query's words outside every parenthesis, string,
// bracketed name and comment, upper-cased: what the outermost statement says,
// without an ORDER BY inside OVER (...) or a subquery's TOP being mistaken for
// the outer one's.
func topLevelWords(sqlText string) []string {
	runes := []rune(sqlText)
	var out []string
	depth := 0
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
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i = skipBlockComment(runes, i)
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case isIdentRune(c) || c == '@':
			j := i + 1
			for j < len(runes) && (isIdentRune(runes[j]) || runes[j] == '@') {
				j++
			}
			if depth == 0 {
				out = append(out, strings.ToUpper(string(runes[i:j])))
			}
			i = j
			continue
		}
		i++
	}
	return out
}
