package find

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// The report builder is the most powerful thing in this application: it
// connects to a database of somebody's choosing and runs SQL against it. So
// the two properties worth a test are the boundary and the secret.
//
//   - Every route is refused to anybody who is not an administrator.
//   - The connection string never appears in any response, to anybody,
//     including the administrator who typed it.
//
// Both are asserted against the real router rather than by calling handlers
// directly, because what is being tested IS the wiring: a handler that checks
// nothing is fine if the route registration guards it, and a perfect handler
// is worthless if somebody registers it without the guard.

// builderTestServer returns a server with accounts on, the builder wired, and
// one administrator and one viewer.
func builderTestServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	dir := tempDir(t)

	users, err := auth.OpenStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if _, err := users.Create("boss", "Boss", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatalf("creating the admin: %v", err)
	}
	if _, err := users.Create("reader", "Reader", "correct horse battery", auth.RoleViewer); err != nil {
		t.Fatalf("creating the viewer: %v", err)
	}
	// The builder belongs to named accounts: its five permissions are
	// explicit, so no role gives them, an administrator's included. Granted
	// to boss here the way -set-grant would; the deny path is the viewer.
	if err := users.SetGrants("boss", map[string]auth.GrantState{
		auth.PageBuilder: auth.GrantAllow, auth.ActionBuilderRun: auth.GrantAllow,
		auth.ActionBuilderSave: auth.GrantAllow, auth.ActionSources: auth.GrantAllow,
		auth.ActionBuilderExport: auth.GrantAllow,
	}); err != nil {
		t.Fatalf("granting the builder: %v", err)
	}

	key, err := auth.LoadOrCreateKey(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	signer := auth.NewSigner(key, time.Hour)

	s := NewServer("")
	s.UseAccounts(users, signer, false)
	// The permission catalogue, as main.go installs it. Without it every
	// permission is denied to everybody but an administrator, so a test
	// server that skipped this would be exercising a fail-closed default
	// rather than the access model people actually get.
	s.UsePermissions(auth.NewCatalogue())

	sources, err := reports.OpenSourceStore(filepath.Join(dir, "datasources.json"))
	if err != nil {
		t.Fatalf("source store: %v", err)
	}
	custom, err := reports.OpenCustomStore(filepath.Join(dir, "customreports.json"))
	if err != nil {
		t.Fatalf("report store: %v", err)
	}
	s.UseBuilder(sources, custom)
	// The fixtures point at 127.0.0.1, which the builder refuses unless the
	// operator names it (ssrfguard.go) -- as a test database beside the server
	// would have to be. The refusal itself is ssrfguard_test.go's.
	s.allowedHosts = []string{"127.0.0.1"}
	t.Cleanup(sources.Close)

	adminTok := signFor(t, signer, users, "boss")
	viewerTok := signFor(t, signer, users, "reader")
	return s, adminTok, viewerTok, filepath.Join(dir, "datasources.json")
}

// Every builder route, refused to a viewer and to an anonymous caller.
func TestBuilderRoutesAreAdminOnly(t *testing.T) {
	s, admin, viewer, _ := builderTestServer(t)

	routes := []struct{ method, path, body string }{
		{"GET", "/api/builder/sources", ""},
		{"POST", "/api/builder/sources", `{"name":"x","driver":"mysql","dsn":"u:p@tcp(h:1)/d"}`},
		{"DELETE", "/api/builder/sources?id=src_x", ""},
		{"POST", "/api/builder/test", `{"driver":"mysql","dsn":"u:p@tcp(h:1)/d"}`},
		{"GET", "/api/builder/schema?source=src_x", ""},
		{"POST", "/api/builder/run", `{"source":"src_x","sql":"SELECT 1"}`},
		{"POST", "/api/builder/params", `{"sql":"SELECT 1 WHERE a = @x"}`},
		{"POST", "/api/builder/detail", `{"source":"src_x","sql":"SELECT 1 WHERE a = @key","key":"1"}`},
		{"GET", "/api/builder/suggest?source=src_x&table=t&column=c&q=a", ""},
		{"GET", "/api/builder/reports", ""},
		{"POST", "/api/builder/reports", `{"title":"t","source_id":"src_x","sql":"SELECT 1"}`},
		{"DELETE", "/api/builder/reports?id=rep_x", ""},
		{"GET", "/api/builder/export?id=rep_x", ""},
	}

	for _, e := range routes {
		// A viewer must be refused, not merely fail later for another reason.
		if got := asUser(t, s, e.method, e.path, viewer, e.body).Code; got != http.StatusForbidden {
			t.Errorf("%s %s as a VIEWER returned %d, want 403", e.method, e.path, got)
		}
		// And so must somebody with no session at all.
		code := asUser(t, s, e.method, e.path, "", e.body).Code
		if code != http.StatusForbidden && code != http.StatusUnauthorized {
			t.Errorf("%s %s with NO session returned %d, want 401 or 403", e.method, e.path, code)
		}
		// The administrator must get past the guard. What comes back may well
		// be an error -- src_x does not exist -- but it must not be 403,
		// because that would mean the permission is wrong rather than the id.
		if got := asUser(t, s, e.method, e.path, admin, e.body).Code; got == http.StatusForbidden {
			t.Errorf("%s %s as an ADMIN returned 403, so the permission is too strict", e.method, e.path)
		}
	}
}

// The connection string must not come back out of any route.
//
// This is the property that lets the feature store a database password at
// all. It is asserted across every response an administrator can obtain,
// because a leak in one of them is a leak.
func TestTheConnectionStringNeverLeaves(t *testing.T) {
	s, admin, _, storePath := builderTestServer(t)

	const dsn = "demo_user:Tr0ub4dor&3$x@tcp(10.9.9.9:3306)/secretdb?charset=utf8mb4"
	rec := asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"prod","driver":"mysql","dsn":`+quote(dsn)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("saving the source returned %d: %s", rec.Code, rec.Body.String())
	}
	// Even the response to the save itself, which is the one place a naive
	// implementation echoes the request back.
	assertNoSecret(t, "the save response", rec.Body.String(), dsn)

	list := asUser(t, s, "GET", "/api/builder/sources", admin, "")
	assertNoSecret(t, "the source list", list.Body.String(), dsn)
	// It has to be useful, though: the host and database identify the server
	// and must be there, or the list is unreadable.
	if !strings.Contains(list.Body.String(), "10.9.9.9:3306") ||
		!strings.Contains(list.Body.String(), "secretdb") {
		t.Errorf("the list does not identify the server at all:\n%s", list.Body.String())
	}

	// A failed connection test: the drivers put the DSN in their error
	// messages, and this is the response most likely to carry it.
	test := asUser(t, s, "POST", "/api/builder/test", admin,
		`{"driver":"mysql","dsn":`+quote(dsn)+`}`)
	assertNoSecret(t, "the connection test", test.Body.String(), dsn)

	// And the file on disk.
	raw, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	assertNoSecret(t, "the stored file", string(raw), dsn)
}

// assertNoSecret checks a response for the DSN and for its password alone.
func assertNoSecret(t *testing.T, what, body, dsn string) {
	t.Helper()
	if strings.Contains(body, dsn) {
		t.Errorf("%s contains the whole connection string:\n%s", what, body)
	}
	for _, frag := range []string{"Tr0ub4dor&3$x", "demo_user:P@s"} {
		if strings.Contains(body, frag) {
			t.Errorf("%s contains %q:\n%s", what, frag, body)
		}
	}
}

// A saved report round-trips, and a query that is not a read is refused
// through the route rather than only in the package.
func TestBuilderSavesAndGuardsThroughTheRoutes(t *testing.T) {
	s, admin, _, _ := builderTestServer(t)

	src := asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"db","driver":"mysql","dsn":"u:p@tcp(127.0.0.1:1)/d"}`)
	if src.Code != http.StatusOK {
		t.Fatalf("saving the source: %d %s", src.Code, src.Body.String())
	}
	id := jsonField(t, src.Body.String(), "id")
	if id == "" {
		t.Fatal("the saved source has no id")
	}

	// A write must be refused when SAVING, so nobody discovers a week later
	// that the report they wrote cannot run.
	bad := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"danger","source_id":`+quote(id)+`,"sql":"DELETE FROM t"}`)
	if bad.Code == http.StatusOK {
		t.Error("a DELETE was accepted as a saved report")
	}
	if !strings.Contains(bad.Body.String(), "DELETE") {
		t.Errorf("the refusal does not say which word was the problem: %s", bad.Body.String())
	}

	// A read is accepted.
	ok := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"fine","description":"a count","source_id":`+quote(id)+`,"sql":"SELECT 1 AS n"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("a SELECT was refused: %d %s", ok.Code, ok.Body.String())
	}
	// The author comes from the session, not the request.
	if got := jsonField(t, ok.Body.String(), "author"); got != "boss" {
		t.Errorf("author is %q, want the signed-in account", got)
	}

	// And a request that tries to claim somebody else wrote it must not win.
	spoof := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"spoofed","source_id":`+quote(id)+`,"sql":"SELECT 2","author":"someone-else"}`)
	if got := jsonField(t, spoof.Body.String(), "author"); got != "boss" {
		t.Errorf("a request set the author to %q; it must come from the session", got)
	}

	// It is listed.
	list := asUser(t, s, "GET", "/api/builder/reports", admin, "")
	if !strings.Contains(list.Body.String(), "fine") {
		t.Errorf("the saved report is not in the list:\n%s", list.Body.String())
	}

	// Running a write through the run route is refused too.
	run := asUser(t, s, "POST", "/api/builder/run", admin,
		`{"source":`+quote(id)+`,"sql":"DROP TABLE t"}`)
	if run.Code == http.StatusOK {
		t.Error("DROP TABLE was accepted by the run route")
	}
}

// Filters, through the routes.
//
// The interesting assertions are about what the page is told and what it is
// refused, because the page builds its filter editor entirely out of the
// former and an administrator's only warning is the latter.
func TestBuilderFiltersThroughTheRoutes(t *testing.T) {
	s, admin, _, _ := builderTestServer(t)

	mysql := jsonField(t, asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"my","driver":"mysql","dsn":"u:p@tcp(127.0.0.1:1)/d"}`).Body.String(), "id")
	mssql := jsonField(t, asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"ms","driver":"sqlserver","dsn":"sqlserver://u:p@127.0.0.1:1?database=d"}`).
		Body.String(), "id")
	if mysql == "" || mssql == "" {
		t.Fatal("the connections were not saved")
	}

	// Discovery: the page asks which filters a draft declares, and must be told
	// about the real one and not about the @ inside a literal.
	disc := asUser(t, s, "POST", "/api/builder/params", admin,
		`{"sql":"SELECT * FROM t WHERE note = 'ask @sales' AND code = @who"}`)
	if disc.Code != http.StatusOK {
		t.Fatalf("discovery returned %d: %s", disc.Code, disc.Body.String())
	}
	body := disc.Body.String()
	if !strings.Contains(body, `"who"`) {
		t.Errorf("discovery missed the filter the query declares:\n%s", body)
	}
	if strings.Contains(body, `"sales"`) {
		t.Errorf("discovery offered an @ inside a string literal as a filter:\n%s", body)
	}
	// And the filter types come with it, so the page does not carry its own
	// copy of the list.
	if !strings.Contains(body, `"kinds"`) || !strings.Contains(body, `"anyof"`) {
		t.Errorf("discovery did not return the filter types:\n%s", body)
	}

	// A filter the query uses but nothing declares is refused at SAVE time,
	// which is the whole point -- Bind would refuse it anyway, but only once
	// somebody tried to run it.
	undecl := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"u","source_id":`+quote(mysql)+`,"sql":"SELECT 1 WHERE a = @ghost"}`)
	if undecl.Code == http.StatusOK {
		t.Error("a report using @ghost with no filter defined was saved")
	}
	if !strings.Contains(undecl.Body.String(), "@ghost") {
		t.Errorf("the refusal does not name the filter: %s", undecl.Body.String())
	}

	// REGEXP is MySQL's, so this type must be refused on the SQL Server
	// connection and accepted on the MySQL one. Same body, different source:
	// that is what makes it a test of the engine rule rather than of the type.
	const anyBody = `{"title":"a","sql":"SELECT 1 WHERE a REGEXP @firms",` +
		`"params":[{"name":"firms","kind":"anyof"}],"source_id":`
	if rec := asUser(t, s, "POST", "/api/builder/reports", admin,
		anyBody+quote(mssql)+`}`); rec.Code == http.StatusOK {
		t.Error("an anyof filter was saved against SQL Server, which has no REGEXP")
	}
	if rec := asUser(t, s, "POST", "/api/builder/reports", admin,
		anyBody+quote(mysql)+`}`); rec.Code != http.StatusOK {
		t.Errorf("an anyof filter was refused against MySQL: %s", rec.Body.String())
	}

	// A name the scanner could never match. It would save happily and then
	// bind to nothing, which is the failure this check exists to prevent.
	bad := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"b","source_id":`+quote(mysql)+`,"sql":"SELECT 1",`+
			`"params":[{"name":"order-date","kind":"text"}]}`)
	if bad.Code == http.StatusOK {
		t.Error("a filter named order-date was accepted; @order-date can never match it")
	}

	// A filter round-trips with its label, default and options intact -- the
	// page rebuilds its editor from exactly this.
	saved := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"round trip","source_id":`+quote(mysql)+`,`+
			`"sql":"SELECT 1 WHERE s LIKE @status",`+
			`"params":[{"name":"status","label":"وضعیت","kind":"choice","default":"TO_DO",`+
			`"options":[{"value":"TO_DO","label":"در جریان"},{"value":"DONE"}]}]}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("saving with a choice filter: %d %s", saved.Code, saved.Body.String())
	}
	for _, want := range []string{`"status"`, "وضعیت", `"TO_DO"`, "در جریان", `"choice"`} {
		if !strings.Contains(saved.Body.String(), want) {
			t.Errorf("the saved report lost %s:\n%s", want, saved.Body.String())
		}
	}
	// An option with no label of its own labels itself, so listing four
	// statuses does not mean typing each twice.
	if !strings.Contains(saved.Body.String(), `"value":"DONE","label":"DONE"`) {
		t.Errorf("an unlabelled option did not fall back to its value:\n%s", saved.Body.String())
	}
}

// Charts, through the routes.
//
// The page builds its chart editor from what this returns and redraws from the
// saved spec, so the field names are the contract.
func TestBuilderChartsThroughTheRoutes(t *testing.T) {
	s, admin, _, _ := builderTestServer(t)
	src := jsonField(t, asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"my","driver":"mysql","dsn":"u:p@tcp(127.0.0.1:1)/d"}`).Body.String(), "id")

	// The chart types and aggregations ride along with the filter discovery,
	// because the page needs all of them before it can render either editor.
	disc := asUser(t, s, "POST", "/api/builder/params", admin, `{"sql":"SELECT 1"}`).Body.String()
	for _, want := range []string{`"charts"`, `"chartAggs"`, `"doughnut"`, `"hbar"`, `"count"`} {
		if !strings.Contains(disc, want) {
			t.Errorf("discovery did not carry %s:\n%s", want, disc)
		}
	}

	// A chart round-trips.
	ok := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"with a chart","source_id":`+quote(src)+`,"sql":"SELECT 1 AS a, 2 AS b",`+
			`"chart":{"kind":"hbar","label":"a","values":["b"],"agg":"sum",`+
			`"stacked":true,"title":"نمودار"}}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("a chart was refused: %d %s", ok.Code, ok.Body.String())
	}
	for _, want := range []string{`"kind":"hbar"`, `"label":"a"`, `"stacked":true`, "نمودار"} {
		if !strings.Contains(ok.Body.String(), want) {
			t.Errorf("the saved chart lost %s:\n%s", want, ok.Body.String())
		}
	}

	// A report with no chart must not come back carrying an empty one -- the
	// page treats "has a chart" as a single question and an empty object
	// would answer it wrongly.
	none := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"no chart","source_id":`+quote(src)+`,"sql":"SELECT 1 AS a"}`)
	if strings.Contains(none.Body.String(), `"chart"`) {
		t.Errorf("a report with no chart came back with a chart field:\n%s", none.Body.String())
	}

	// A pie of two columns is refused at save, not silently truncated to one
	// under a legend naming both.
	bad := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"bad pie","source_id":`+quote(src)+`,"sql":"SELECT 1 AS a, 2 AS b, 3 AS c",`+
			`"chart":{"kind":"pie","label":"a","values":["b","c"]}}`)
	if bad.Code == http.StatusOK {
		t.Error("a pie of two columns was saved")
	}

	// Every type the page offers arrives in the discovery response, with the
	// capability flags the page reads to build its controls. A type it could
	// draw but not hear about is a type nobody can pick.
	for _, want := range []string{
		`"sbar"`, `"mixed"`, `"polar"`, `"radar"`, `"scatter"`, `"hist"`,
		`"chartBuckets"`, `"no_agg"`, `"bins"`, `"numeric_label"`, `"stackable"`,
	} {
		if !strings.Contains(disc, want) {
			t.Errorf("discovery did not carry %s, so the page cannot offer it:\n%s", want, disc)
		}
	}

	// The newer chart settings round-trip. A distribution stores its bin count
	// and drops the label it has no use for; a logarithmic axis is kept.
	hist := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"distribution","source_id":`+quote(src)+`,"sql":"SELECT 1 AS wt",`+
			`"chart":{"kind":"hist","values":["wt"],"bins":20,"label":"ignored","log_scale":true}}`)
	if hist.Code != http.StatusOK {
		t.Fatalf("a distribution chart was refused: %s", hist.Body.String())
	}
	if !strings.Contains(hist.Body.String(), `"bins":20`) {
		t.Errorf("the bin count did not round-trip:\n%s", hist.Body.String())
	}
	if strings.Contains(hist.Body.String(), `"label":"ignored"`) {
		t.Errorf("a distribution kept a label it cannot use:\n%s", hist.Body.String())
	}

	// And a scatter comes back not aggregating, whatever was asked for.
	sc := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"scatter","source_id":`+quote(src)+`,"sql":"SELECT 1 AS wt, 2 AS qty",`+
			`"chart":{"kind":"scatter","label":"wt","values":["qty"],"agg":"sum"}}`)
	if sc.Code != http.StatusOK {
		t.Fatalf("a scatter was refused: %s", sc.Body.String())
	}
	if !strings.Contains(sc.Body.String(), `"agg":"none"`) {
		t.Errorf("a scatter did not come back un-aggregated:\n%s", sc.Body.String())
	}
}

// Master and detail, through the routes.
func TestBuilderDetailThroughTheRoutes(t *testing.T) {
	s, admin, _, _ := builderTestServer(t)
	src := jsonField(t, asUser(t, s, "POST", "/api/builder/sources", admin,
		`{"name":"my","driver":"mysql","dsn":"u:p@tcp(127.0.0.1:1)/d"}`).Body.String(), "id")

	// A detail query that ignores the row it is about is refused at SAVE. It
	// would return the same rows under every master row, which looks like it
	// works -- the failure worth catching early.
	blind := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"blind","source_id":`+quote(src)+`,"sql":"SELECT 1 AS a",`+
			`"detail":{"key":"a","sql":"SELECT * FROM quotes"}}`)
	if blind.Code == http.StatusOK {
		t.Error("a detail query with no @key was saved")
	}
	if !strings.Contains(blind.Body.String(), "@key") {
		t.Errorf("the refusal does not say what is missing: %s", blind.Body.String())
	}

	// So is one with no key column, and one that is not a read.
	for _, body := range []string{
		`{"title":"nokey","source_id":` + quote(src) + `,"sql":"SELECT 1 AS a",` +
			`"detail":{"sql":"SELECT * FROM q WHERE i = @key"}}`,
		`{"title":"write","source_id":` + quote(src) + `,"sql":"SELECT 1 AS a",` +
			`"detail":{"key":"a","sql":"DELETE FROM q WHERE i = @key"}}`,
	} {
		if rec := asUser(t, s, "POST", "/api/builder/reports", admin, body); rec.Code == http.StatusOK {
			t.Errorf("an unusable detail was saved: %s", body)
		}
	}

	// A good one round-trips.
	ok := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"master detail","source_id":`+quote(src)+`,"sql":"SELECT 1 AS case_no",`+
			`"detail":{"key":"case_no","title":"فورواردرها",`+
			`"sql":"SELECT f FROM q WHERE n = @key"}}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("a valid detail was refused: %d %s", ok.Code, ok.Body.String())
	}
	for _, want := range []string{`"key":"case_no"`, "فورواردرها", "@key"} {
		if !strings.Contains(ok.Body.String(), want) {
			t.Errorf("the saved detail lost %s:\n%s", want, ok.Body.String())
		}
	}

	// A report with no detail must not come back carrying an empty one.
	none := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"plain","source_id":`+quote(src)+`,"sql":"SELECT 1 AS a"}`)
	if strings.Contains(none.Body.String(), `"detail"`) {
		t.Errorf("a report with no detail came back with a detail field:\n%s", none.Body.String())
	}

	// And "key" cannot be a filter name, because @key is the clicked row.
	clash := asUser(t, s, "POST", "/api/builder/reports", admin,
		`{"title":"clash","source_id":`+quote(src)+`,"sql":"SELECT 1 WHERE a = @key",`+
			`"params":[{"name":"key","kind":"text"}]}`)
	if clash.Code == http.StatusOK {
		t.Error("a filter named key was accepted; @key belongs to the detail query")
	}
}

// When the feature is not configured, the routes say so rather than panicking
// on a nil store.
func TestBuilderRoutesWithoutTheFeature(t *testing.T) {
	dir := tempDir(t)
	users, err := auth.OpenStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("boss", "Boss", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	key, err := auth.LoadOrCreateKey(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	signer := auth.NewSigner(key, time.Hour)
	s := NewServer("")
	s.UseAccounts(users, signer, false)
	// Deliberately no UseBuilder, and deliberately no UsePermissions either:
	// this tests the feature-off path (404) rather than the access path (403),
	// and boss is an administrator, who reaches a route whose permission is
	// undeclared. So the handler gets to report the feature missing, which is
	// the thing being tested.

	tok := signFor(t, signer, users, "boss")
	if s.BuilderEnabled() {
		t.Error("the builder reports itself enabled with no stores")
	}
	rec := asUser(t, s, "GET", "/api/builder/sources", tok, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("with no stores the route returned %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// quote makes a JSON string literal, so a password full of punctuation can go
// in a test body without hand-escaping.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsonField pulls one top-level string field out of a response without
// needing a struct for every shape.
func jsonField(t *testing.T, body, name string) string {
	t.Helper()
	key := `"` + name + `":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
