package reports

import (
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// A password with characters that URL-escaping would rewrite must survive
// into the DSN unchanged.
//
// This is a regression test for a real failure: the DSN was assembled by hand
// with url.QueryEscape on the password, but the driver's parser takes the
// password literally and never decodes it. The escaped form was therefore sent
// as the password, and MySQL answered "Access denied ... (using password:
// YES)" -- which reads like a wrong credential rather than a bug in this file.
func TestDSNCarriesAwkwardPasswordsVerbatim(t *testing.T) {
	for _, pw := range []string{
		"Tr0ub4dor&3$x",
		"has spaces and +plus",
		"sym!bols#and%percent",
		"slash/and:colon",
		"at@sign@twice",
		"plain",
	} {
		t.Setenv("DOCFIND_DB_DSN", "")
		t.Setenv("DOCFIND_DB_USER", "reports_ro")
		t.Setenv("DOCFIND_DB_PASSWORD", pw)
		t.Setenv("DOCFIND_DB_HOST", "db.example.com:3306")
		t.Setenv("DOCFIND_DB_NAME", "reports_demo")

		dsn, err := DSNFromEnv()
		if err != nil {
			t.Fatalf("DSNFromEnv with password %q: %v", pw, err)
		}
		// Parse it back with the same parser the driver uses at connect time.
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("the driver could not parse our own DSN for password %q: %v", pw, err)
		}
		if cfg.Passwd != pw {
			t.Errorf("password round-trip failed:\n  configured: %q\n  driver saw: %q", pw, cfg.Passwd)
		}
		if cfg.User != "reports_ro" {
			t.Errorf("user round-trip failed: %q", cfg.User)
		}
		if cfg.DBName != "reports_demo" {
			t.Errorf("database round-trip failed: %q", cfg.DBName)
		}
	}
}

// A host without a port is a natural thing to configure and must not produce
// an address the driver cannot dial.
func TestDSNAddsDefaultPort(t *testing.T) {
	t.Setenv("DOCFIND_DB_DSN", "")
	t.Setenv("DOCFIND_DB_USER", "u")
	t.Setenv("DOCFIND_DB_PASSWORD", "p")
	t.Setenv("DOCFIND_DB_HOST", "db.example.com")
	t.Setenv("DOCFIND_DB_NAME", "db")

	dsn, err := DSNFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "db.example.com:3306" {
		t.Errorf("Addr = %q, want the default port appended", cfg.Addr)
	}
}

// An explicit DSN is passed through untouched, for anyone who needs an option
// this helper does not expose.
func TestExplicitDSNWins(t *testing.T) {
	const raw = "someone:secret@tcp(db.internal:3307)/other?parseTime=true"
	t.Setenv("DOCFIND_DB_DSN", raw)
	dsn, err := DSNFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if dsn != raw {
		t.Errorf("DSN = %q, want it passed through unchanged", dsn)
	}
}

func TestMissingSettingsAreReported(t *testing.T) {
	t.Setenv("DOCFIND_DB_DSN", "")
	t.Setenv("DOCFIND_DB_USER", "")
	t.Setenv("DOCFIND_DB_PASSWORD", "")
	t.Setenv("DOCFIND_DB_HOST", "")
	t.Setenv("DOCFIND_DB_NAME", "")
	if _, err := DSNFromEnv(); err == nil {
		t.Fatal("an empty configuration was accepted")
	}
}

// Whatever is logged or shown on a settings screen must not carry the password.
func TestRedactDSNHidesThePassword(t *testing.T) {
	got := RedactDSN("reports_ro:Tr0ub4dor&3$x@tcp(db.example.com:3306)/reports_demo")
	if strings.Contains(got, "Tr0ub4dor") {
		t.Errorf("RedactDSN leaked the password: %q", got)
	}
	if !strings.Contains(got, "reports_ro") || !strings.Contains(got, "reports_demo") {
		t.Errorf("RedactDSN removed too much to be useful: %q", got)
	}
}
