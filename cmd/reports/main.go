// Command reports is docfind's reporting on its own: a dashboard of saved SQL
// reports, each with its filters, table, chart, monthly summary and export,
// and a builder in which the people allowed to connect databases (MySQL, SQL
// Server, PostgreSQL) write new reports in the browser. Every query is
// checked to be a read before it runs, and runs on a read-only session.
//
// The shipped reports are .sql files in the folder -reports names, read from
// the reporting database DOCFIND_DB_* describes; the builder's connections
// and reports are kept beside the accounts file. Accounts come from the file
// given with -users, and from PostgreSQL (DOCFIND_PG_*) when one is
// configured. Run with -add-user first to create the first administrator.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/find"
)

// isLoopbackHost reports whether an address only this machine can reach.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

var version = "dev"

const defaultPort = 8320

func main() {
	if err := run(); err != nil {
		logf("error: %s", err.Error())
		os.Exit(1)
	}
}

// run is the reports server from start to finish: a one-shot command, or the
// server until a signal ends it.
func run() error {
	o := parseOptions()
	o.loadEnv()
	if handled, err := o.runOneShot(); handled {
		return err
	}
	find.SetVersion(version)
	startAppLog(o.usersFile)
	defer stopAppLog()

	if o.usersFile == "" {
		return fmt.Errorf("reports need accounts: pass -users <path to the accounts file> (and create the first one with -add-user)")
	}
	token, err := accessToken(true)
	if err != nil {
		return err
	}
	acc, err := openAccounts(o.usersFile)
	if err != nil {
		return err
	}
	if acc.pgDB != nil {
		defer acc.pgDB.Close()
	}
	ln, url, err := listen(o, acc.users, token)
	if err != nil {
		return err
	}

	app := find.NewServer(token)
	app.UsePermissions(auth.NewCatalogue())
	var closers cleanups
	defer func() { closers.run() }()
	closers.add(useAccounts(app, acc, o))
	// The shipped reports and the builder: each is optional, and each says in
	// the log why it is off (reporting.go).
	closers.add(startReporting(app, o))
	closers.add(startBuilder(app, o.usersFile))

	srv, err := newHTTPServer(app)
	if err != nil {
		return err
	}
	go serve(srv, ln)
	logf("ready: %s", url)

	waitForSignal()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// waitForSignal blocks until the program is interrupted.
func waitForSignal() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
}
