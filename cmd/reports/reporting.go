package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/find"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// startReporting turns the dashboard on: the .sql reports in the -reports
// folder, run against the reporting database DOCFIND_DB_* describes. Either
// missing is normal -- the builder brings its own connections -- so this logs
// what it found and carries on. The returned function closes the database.
func startReporting(app *find.Server, o options) func() {
	cat, cerrs := reports.LoadCatalog(o.reportsDir)
	for _, e := range cerrs {
		logf("report definition problem: %v", e)
	}
	if cat.Count() == 0 {
		logf("no reports found in %s", o.reportsDir)
		return nil
	}
	dsn, err := reports.DSNFromEnv()
	if err != nil {
		logf("the shipped reports are off: %v", err)
		return nil
	}
	// Not pinged here on purpose: a reporting database that is briefly
	// unreachable must not stop the server starting. A failed query reports
	// itself, per request.
	rdb, err := reports.Open(dsn, 60*time.Second)
	if err != nil {
		logf("the shipped reports are off: %v", err)
		return nil
	}
	// Before the permissions below are built from the catalogue, so a report
	// on another server is grantable exactly when that server is attached.
	attachSources(rdb, cat)
	app.UseReports(cat, rdb)
	// Report pages, charts and monthly summaries answer from their last good
	// copy at once and refresh behind the answer; the copies are kept on disk
	// beside the accounts, so they survive a restart and a reporting database
	// that is away (internal/snapshot).
	app.UseReportSnapshots(context.Background(), dataFilePath(o.usersFile, "snapshots"),
		reportRefreshEvery(os.Getenv("DOCFIND_REPORT_REFRESH_SECONDS")))
	// One permission per report, so "may open the reports page" and "may see
	// this report" are separate answers. Generated from the catalogue, so a
	// new .sql file becomes grantable with no code change.
	app.UsePermissions(auth.NewCatalogue().WithReports(reportPerms(cat)).WithReportGroups(reportGroups(cat)))
	logf("reporting enabled: %d reports, %s", cat.Count(), reports.RedactDSN(dsn))
	return func() { rdb.Close() }
}

// reportRefreshEvery is how long a report's answer is served before it is
// refreshed: DOCFIND_REPORT_REFRESH_SECONDS, held to one to five minutes, two
// by default.
func reportRefreshEvery(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 2 * time.Minute
	}
	return time.Duration(min(max(n, 60), 300)) * time.Second
}

// attachSources connects the other servers reports may read ("-- source:
// <name>"), one DOCFIND_SOURCE_<NAME>_DSN each. The catalogue is told first,
// whatever happens below: a report on a server that is not attached stays out
// of every list, which is the answer wanted when none is configured. Not
// pinged, for the reason the reporting database is not: a server that is
// briefly away must not stop this one starting.
func attachSources(rdb *reports.DB, cat *reports.Catalog) {
	cat.UseSources(rdb.HasSource)
	dsns, errs := reports.SourcesFromEnv(os.Environ())
	for _, e := range errs {
		logf("report source ignored: %v", e)
	}
	for name, dsn := range dsns {
		db, err := reports.OpenSource(dsn)
		if err == nil {
			err = rdb.AttachSource(name, reports.DriverSQLServer, db)
			if err != nil {
				db.Close()
			}
		}
		if err != nil {
			logf("report source %s is off: %v", name, err)
			continue
		}
		logf("report source %s: %s", name, reports.DescribeSource(dsn))
	}
}

// startBuilder opens the report builder: the databases an administrator has
// added and the queries they have saved, beside the accounts file because
// they are state this deployment must not lose. The connection strings are
// sealed with a key kept beside them (datasources.json.key), created on
// first use.
//
// Both stores or neither: a source list with nowhere to save a report, or
// saved reports with no connection to run them, both present a working
// interface that cannot finish the job. Reported and skipped rather than
// fatal: the shipped reports must not depend on it.
func startBuilder(app *find.Server, usersFile string) func() {
	srcPath := dataFilePath(usersFile, "datasources.json")
	repPath := dataFilePath(usersFile, "customreports.json")
	ss, err := reports.OpenSourceStore(srcPath)
	if err != nil {
		logf("report builder is off: %v", err)
		return nil
	}
	cs, err := reports.OpenCustomStore(repPath)
	if err != nil {
		logf("report builder is off: %v", err)
		ss.Close()
		return nil
	}
	app.UseBuilder(ss, cs)
	logf("report builder enabled: %d connections, %d saved reports (%s)", ss.Count(), cs.Count(), srcPath)
	return func() { ss.Close() }
}
