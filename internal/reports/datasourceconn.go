package reports

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/obs"
)

// Testing a connection before, and after, it is saved.

// TestResult is what the "test connection" button reports.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Server  string `json:"server,omitempty"`
	TookMs  int64  `json:"took_ms"`
	// Writable says the account can change data, and WritableWhy how. The
	// builder runs every query read-only regardless (readonlyrun.go), but an
	// account that could write is one clever query from doing so, and the
	// fix is an account that cannot.
	Writable    bool   `json:"writable,omitempty"`
	WritableWhy string `json:"writable_why,omitempty"`
}

// Test connects and asks the server what it is.
//
// Run against a dsn that has not been saved yet, so somebody can find out
// whether a connection string works before committing it -- which is the
// difference between one attempt and a saved entry that silently never works.
func (s *SourceStore) Test(ctx context.Context, driver Driver, dsn string) TestResult {
	defer obs.Track(ctx, obs.DB)()
	start := time.Now()
	if !driver.valid() {
		return TestResult{Message: fmt.Sprintf("%q is not an engine this can use", driver)}
	}
	if strings.TrimSpace(dsn) == "" {
		return TestResult{Message: "no connection string"}
	}
	if err := s.checkGuard(ctx, driver, dsn); err != nil {
		return TestResult{Message: err.Error(), TookMs: sinceMs(start)}
	}
	db, err := sql.Open(string(driver), dsn)
	if err != nil {
		return TestResult{Message: redactDSNError(err.Error(), dsn), TookMs: sinceMs(start)}
	}
	// One connection: a test needs no more, and it is somebody else's server.
	db.SetMaxOpenConns(1)
	defer db.Close()

	c, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		return TestResult{Message: redactDSNError(err.Error(), dsn), TookMs: sinceMs(start)}
	}

	// Which server, in its own words. Useful beyond confirming the connection:
	// it tells somebody they reached the server they meant, which a successful
	// ping does not.
	var version string
	q := "SELECT VERSION()"
	if driver == DriverSQLServer {
		q = "SELECT @@VERSION"
	}
	res := TestResult{OK: true, Message: "connected"}
	if err := db.QueryRowContext(c, q).Scan(&version); err == nil {
		// Connected but would not say is still a success: the ping is the claim.
		res.Server = firstLine(version)
	}
	res.Writable, res.WritableWhy = AccountCanWrite(c, db, driver)
	if res.Writable {
		res.Message = "connected -- but this account can change data (" + res.WritableWhy +
			"); a read-only account is safer for reports"
	}
	res.TookMs = sinceMs(start)
	return res
}

// TestSaved tests a connection already stored.
//
// Needed because the DSN never leaves the process: the browser holding a
// saved source has its name and its host summary and nothing it could send
// back to be tested. Without this, the test button would work only while
// somebody was typing a new connection -- which is the one moment they least
// need it, and never afterwards when a password has been rotated and a report
// has started failing.
func (s *SourceStore) TestSaved(ctx context.Context, id string) TestResult {
	defer obs.Track(ctx, obs.DB)()
	s.mu.RLock()
	d, ok := s.sources[id]
	s.mu.RUnlock()
	if !ok {
		return TestResult{Message: "no such connection"}
	}
	dsn, err := s.box.Open(d.DSN)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	return s.Test(ctx, d.Driver, dsn)
}
