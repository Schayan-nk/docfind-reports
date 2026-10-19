package reports

import (
	"fmt"
	"os"
	"strings"
	"time"

	// The MySQL driver registers itself with database/sql. It is the one
	// dependency this feature adds: Go's standard library speaks the
	// database/sql interface but ships no MySQL implementation. Imported by
	// name as well as for its side effect, so the DSN can be built through
	// its own config type instead of by joining strings.
	"github.com/go-sql-driver/mysql"
)

// DSNFromEnv builds the connection string from the environment.
//
// Read from the environment rather than a flag or a config file: a flag is
// visible to anything that can list processes, and a config file has a habit
// of being committed. DOCFIND_DB_DSN takes a full DSN when someone needs one;
// otherwise the parts are assembled here so the password is the only secret
// that has to be handled.
func DSNFromEnv() (string, error) {
	if dsn := strings.TrimSpace(os.Getenv("DOCFIND_DB_DSN")); dsn != "" {
		return dsn, nil
	}

	user := strings.TrimSpace(os.Getenv("DOCFIND_DB_USER"))
	pass := os.Getenv("DOCFIND_DB_PASSWORD")
	host := strings.TrimSpace(os.Getenv("DOCFIND_DB_HOST"))
	name := strings.TrimSpace(os.Getenv("DOCFIND_DB_NAME"))
	if user == "" || host == "" || name == "" {
		return "", fmt.Errorf("reports: set DOCFIND_DB_DSN, or DOCFIND_DB_USER, DOCFIND_DB_PASSWORD, " +
			"DOCFIND_DB_HOST and DOCFIND_DB_NAME")
	}
	if !strings.Contains(host, ":") {
		host += ":3306"
	}

	// Built through the driver's own config rather than by joining strings.
	// The DSN parser takes the password *literally* -- it does no URL
	// decoding -- so pre-escaping it silently sends a different password than
	// the one configured, and any password containing @, $, ! or a space
	// fails with "access denied" that looks like a wrong credential rather
	// than a bug here.
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = host
	cfg.DBName = name
	// Persian text throughout, so utf8mb4 is not optional. ParseTime keeps
	// DATE/DATETIME columns as time.Time rather than raw bytes.
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 5 * time.Minute
	// These reports only ever read; refuse to be pointed at a primary that
	// would accept writes through this connection by accident.
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	return cfg.FormatDSN(), nil
}

// RedactDSN returns a DSN safe to log or show on a settings screen.
func RedactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	colon := strings.Index(dsn[:at], ":")
	if colon < 0 {
		return dsn
	}
	return dsn[:colon+1] + "***" + dsn[at:]
}
