package reports

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Reading a connection string well enough to describe it, and no further.
//
// The interface has to show which server a report runs against -- a list of
// six connections all labelled "connection" is unusable -- and it must not
// show the password. So these functions pull out the identifying parts and
// deliberately understand nothing else: this is not a DSN parser, the drivers
// do that, and a second half-correct one here would only disagree with them.
//
// Three shapes have to be handled, because that is what the two drivers
// accept:
//
//	MySQL      user:pass@tcp(host:3306)/dbname?params
//	SQL Server sqlserver://user:pass@host:1433?database=dbname
//	SQL Server server=host,1433;user id=sa;password=...;database=dbname
//
// The last is the one people paste from other tools, so it has to work even
// though the URL form is what the documentation shows.

// describeDSN returns something like "db.example.com:3306 / reports".
//
// Never the user name and never the password. The user name is left out on
// purpose as well: it is not needed to recognise a server, and a screenshot
// of a report list is a likelier thing to exist than a screenshot of a
// settings page.
func describeDSN(driver Driver, dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "no connection string"
	}
	host, db := hostAndDatabase(driver, dsn)
	switch {
	case host != "" && db != "":
		return host + " / " + db
	case host != "":
		return host
	case db != "":
		return db
	}
	return "connection string stored"
}

func hostAndDatabase(driver Driver, dsn string) (host, db string) {
	// Key=value form, which SQL Server accepts and people paste.
	if looksLikeKeyValueDSN(dsn) {
		for _, part := range strings.Split(dsn, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(k, " ", ""))) {
			case "server", "datasource", "addr", "address", "host":
				host = strings.TrimSpace(v)
			case "database", "initialcatalog":
				db = strings.TrimSpace(v)
			}
		}
		return host, db
	}

	// URL form: sqlserver://user:pass@host:port?database=x
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			host = u.Host
			if d := strings.TrimPrefix(u.Path, "/"); d != "" {
				db = d
			}
			if q := u.Query().Get("database"); q != "" {
				db = q
			}
			return host, db
		}
	}

	// MySQL form: user:pass@tcp(host:port)/db?params
	if driver == DriverMySQL || strings.Contains(dsn, "@tcp(") {
		rest := dsn
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if o := strings.Index(rest, "("); o >= 0 {
			if c := strings.Index(rest[o:], ")"); c > 0 {
				host = rest[o+1 : o+c]
				rest = rest[o+c+1:]
			}
		}
		rest = strings.TrimPrefix(rest, "/")
		if q := strings.Index(rest, "?"); q >= 0 {
			rest = rest[:q]
		}
		db = rest
	}
	return host, db
}

// DSNHost returns the machine a connection string points at, without port or
// instance, for the SSRF check on builder connections (internal/netguard).
//
// FAILS TOWARDS THIS MACHINE. Every shape that names no host -- MySQL's
// "user:pass@/db", SQL Server's "(local)" and "." and "lpc:" -- means this
// machine to its driver, so it is reported as "localhost" and refused like it.
// A shape this cannot read gives "localhost" too, rather than a guess.
func DSNHost(driver Driver, dsn string) string {
	host, _ := hostAndDatabase(driver, strings.TrimSpace(dsn))
	host = strings.TrimSpace(host)
	low := strings.ToLower(host)
	for _, p := range []string{"tcp:", "np:", "admin:"} {
		if strings.HasPrefix(low, p) {
			host, low = host[len(p):], low[len(p):]
		}
	}
	if strings.HasPrefix(low, "lpc:") {
		return "localhost"
	}
	if i := strings.IndexAny(host, `,\`); i >= 0 { // host,1433 and host\INSTANCE
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	switch strings.ToLower(host) {
	case "", ".", "(local)", "(localdb)":
		return "localhost"
	}
	return host
}

// DSNReadsLocalFiles reports a MySQL connection string that lets the server
// ask for files off this machine (LOAD DATA LOCAL INFILE). A server somebody
// points the builder at could then read any file docfind can.
func DSNReadsLocalFiles(driver Driver, dsn string) bool {
	if driver != DriverMySQL {
		return false
	}
	_, params, _ := strings.Cut(dsn, "?")
	q, _ := url.ParseQuery(params)
	for k, v := range q {
		if strings.EqualFold(k, "allowAllFiles") && len(v) > 0 &&
			!strings.EqualFold(strings.TrimSpace(v[0]), "false") && strings.TrimSpace(v[0]) != "0" {
			return true
		}
	}
	return false
}

// passwordOf finds the password so it can be scrubbed from an error message.
//
// Best effort by design: it is used only to redact, so a miss costs a message
// that still had the string in it -- which is why redactDSNError also replaces
// the whole DSN, and this is the second pass for a driver that quoted just the
// password.
func passwordOf(dsn string) string {
	if looksLikeKeyValueDSN(dsn) {
		for _, part := range strings.Split(dsn, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(k, " ", ""))) {
			case "password", "pwd":
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			if pw, ok := u.User.Password(); ok {
				return pw
			}
		}
		return ""
	}
	// MySQL: user:pass@...
	at := strings.LastIndex(dsn, "@")
	if at <= 0 {
		return ""
	}
	cred := dsn[:at]
	if i := strings.Index(cred, ":"); i >= 0 {
		return cred[i+1:]
	}
	return ""
}

// looksLikeKeyValueDSN distinguishes "server=x;database=y" from a URL or a
// MySQL DSN. Both of the others contain a "://" or an "@", and neither uses
// a semicolon, so one marker either way is enough.
func looksLikeKeyValueDSN(dsn string) bool {
	return strings.Contains(dsn, ";") && strings.Contains(dsn, "=") &&
		!strings.Contains(dsn, "://") && !strings.Contains(dsn, "@tcp(")
}

// newSourceID returns an identifier for a saved connection.
//
// Random rather than the name slugified: a name can be edited and two
// connections can reasonably be called "reporting", and a report that
// references its source by id must not follow either change.
func newSourceID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reports: generating an id: %w", err)
	}
	return "src_" + hex.EncodeToString(b), nil
}
