package find

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// Per-report access, and the API the permission matrix edits.
//
// THIS IS THE FEATURE THAT WAS ASKED FOR: "set permission for every section,
// every widget, every report, every part separately for each user". The route
// gating proved a page can be withheld; this proves ONE REPORT can be, which
// is the finer half and the one the old model could not express at all.

// reportCatalogue writes a few report definitions and loads them.
func reportCatalogue(t *testing.T, ids ...string) *reports.Catalog {
	t.Helper()
	dir := tempDir(t)
	for _, id := range ids {
		body := "-- id: " + id + "\n-- title: Report " + id + "\nSELECT 1;\n"
		if err := os.WriteFile(filepath.Join(dir, id+".sql"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cat, errs := reports.LoadCatalog(dir)
	for _, err := range errs {
		t.Fatalf("loading the fixture catalogue: %v", err)
	}
	if cat.Count() != len(ids) {
		t.Fatalf("catalogue holds %d reports, wrote %d", cat.Count(), len(ids))
	}
	return cat
}

// withReports wires a report catalogue and the matching permission catalogue.
func withReports(t *testing.T, s *Server, ids ...string) {
	t.Helper()
	cat := reportCatalogue(t, ids...)
	s.reports = cat
	labels := map[string]string{}
	for _, rep := range cat.List() {
		labels[rep.ID] = rep.Title
	}
	s.UsePermissions(auth.NewCatalogue().WithReports(ids, labels))
}

// requestAs is a request carrying u, for testing a decision directly.
//
// The permission decisions below are plain functions over a request, so they
// can be asked the question without minting a token and going round through
// HTTP -- which would test the router as much as the decision, and could not
// reach the ones no route calls.
func requestAs(t *testing.T, u *auth.User) *http.Request {
	t.Helper()
	return auth.WithUser(httptest.NewRequest("GET", "/", nil), u)
}

// ONE REPORT, NOT THE REPORTS. A viewer granted exactly one report sees that
// one, can run it, and is refused the other by id -- which is the bit that
// matters, because hiding a report from a list while leaving it runnable would
// be a permission that only tidies the menu.
func TestAViewerGrantedOneReportSeesAndRunsOnlyThatOne(t *testing.T) {
	s, _, _, _ := builderTestServer(t)
	withReports(t, s, "guarantees", "salaries")

	if err := s.guard.Users.SetGrants("reader", map[string]auth.GrantState{
		auth.PageDashboard:            auth.GrantAllow,
		auth.ReportPerm("guarantees"): auth.GrantAllow,
	}); err != nil {
		t.Fatal(err)
	}
	u, err := s.guard.Users.Get("reader")
	if err != nil {
		t.Fatal(err)
	}
	r := requestAs(t, u)

	vis := s.visibleReports(r)
	if len(vis) != 1 || vis[0].ID != "guarantees" {
		t.Fatalf("the viewer sees %d report(s): %+v; want only guarantees", len(vis), vis)
	}
	if !s.mayRunReport(r, "guarantees") {
		t.Error("the granted report cannot be run")
	}
	if s.mayRunReport(r, "salaries") {
		t.Error("a report that was never granted can be run by id")
	}
}

// A manager is NOT handed every report. This is the change from the old model,
// where a manager held view_all_reports and saw the lot.
func TestAManagerHoldsNoReportWithoutAGrant(t *testing.T) {
	s, _, _, _ := builderTestServer(t)
	withReports(t, s, "guarantees", "salaries")

	if _, err := s.guard.Users.Create("mgr", "Manager", "correct horse battery", auth.RoleManager); err != nil {
		t.Fatal(err)
	}
	u, err := s.guard.Users.Get("mgr")
	if err != nil {
		t.Fatal(err)
	}
	r := requestAs(t, u)
	if got := s.visibleReports(r); len(got) != 0 {
		t.Errorf("a manager sees %d report(s) with no grant: %+v", len(got), got)
	}

	if err := s.guard.Users.SetGrants("mgr", map[string]auth.GrantState{
		auth.ReportPerm("salaries"): auth.GrantAllow,
	}); err != nil {
		t.Fatal(err)
	}
	u, _ = s.guard.Users.Get("mgr")
	r = requestAs(t, u)
	got := s.visibleReports(r)
	if len(got) != 1 || got[0].ID != "salaries" {
		t.Errorf("after one grant the manager sees %+v", got)
	}
}

// An administrator holds every report without anybody granting them one.
func TestAnAdministratorSeesEveryReport(t *testing.T) {
	s, _, _, _ := builderTestServer(t)
	withReports(t, s, "guarantees", "salaries", "tenders")

	u, err := s.guard.Users.Get("boss")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.visibleReports(requestAs(t, u)); len(got) != 3 {
		t.Errorf("an administrator sees %d of 3 reports", len(got))
	}
}

// The catalogue endpoint gives the editor everything it needs to draw itself.
func TestThePermissionCatalogueEndpointDescribesWhatCanBeGranted(t *testing.T) {
	s, adminTok, viewerTok, _ := builderTestServer(t)

	rec := asUser(t, s, "GET", "/api/permissions", adminTok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d", rec.Code)
	}
	var body struct {
		Groups []struct {
			Name  string `json:"name"`
			Items []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
				Kind  string `json:"kind"`
			} `json:"items"`
		} `json:"groups"`
		Templates map[string][]string `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Groups) < 4 {
		t.Errorf("only %d group(s); the editor needs the catalogue grouped", len(body.Groups))
	}
	items := 0
	for _, g := range body.Groups {
		if g.Name == "" {
			t.Error("a group has no heading")
		}
		for _, it := range g.Items {
			if it.ID == "" || it.Label == "" || it.Kind == "" {
				t.Errorf("an item is incomplete: %+v", it)
			}
			items++
		}
	}
	// The eleven built-in permissions (auth/catalogue.go) at the least; a
	// catalogue that reaches the page short of them lost some on the way.
	if items < 11 {
		t.Errorf("the catalogue reached the page with only %d items", items)
	}
	for _, role := range []string{"admin", "manager", "viewer"} {
		if len(body.Templates[role]) == 0 {
			t.Errorf("no template for %s, so the editor cannot show the role default", role)
		}
	}

	// And it is administrative: a viewer may not read it.
	if rec := asUser(t, s, "GET", "/api/permissions", viewerTok, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer read the permission catalogue: %d", rec.Code)
	}
}

// Saving grants round-trips, and refuses what it should.
func TestSavingGrantsRoundTripsAndRefusesNonsense(t *testing.T) {
	s, adminTok, _, _ := builderTestServer(t)

	body := `{"username":"reader","grants":{"` + auth.PageBuilder + `":"allow"}}`
	rec := asUser(t, s, "PUT", "/api/permissions/user", adminTok, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("saving answered %d: %s", rec.Code, rec.Body.String())
	}

	rec = asUser(t, s, "GET", "/api/permissions/user?u=reader", adminTok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading answered %d", rec.Code)
	}
	var got struct {
		Grants    map[string]string `json:"grants"`
		Effective []string          `json:"effective"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Grants[auth.PageBuilder] != "allow" {
		t.Errorf("the override did not persist: %v", got.Grants)
	}
	held := map[string]bool{}
	for _, p := range got.Effective {
		held[p] = true
	}
	if !held[auth.PageBuilder] {
		t.Error("the effective set does not reflect the override")
	}

	// AN UNDECLARED ID IS REFUSED, not stored and ignored. Stored, the screen
	// would show access saved that will never exist.
	for _, bad := range []string{
		`{"username":"reader","grants":{"action:invented":"allow"}}`,
		`{"username":"reader","grants":{"` + auth.PageBuilder + `":"maybe"}}`,
		`{"username":"","grants":{}}`,
		`{"username":"nobody-here","grants":{}}`,
	} {
		rec := asUser(t, s, "PUT", "/api/permissions/user", adminTok, bad)
		if rec.Code == http.StatusOK {
			t.Errorf("accepted %s", bad)
		}
	}

	// Clearing the set returns the account to inheriting its template.
	rec = asUser(t, s, "PUT", "/api/permissions/user", adminTok,
		`{"username":"reader","grants":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing answered %d: %s", rec.Code, rec.Body.String())
	}
	u, _ := s.guard.Users.Get("reader")
	if len(u.Grants) != 0 {
		t.Errorf("grants survived a clear: %v", u.Grants)
	}
	if s.can(requestAs(t, u), auth.PageBuilder) {
		t.Error("the viewer still holds the builder after the override was cleared")
	}
}
