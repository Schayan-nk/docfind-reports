package reports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sealed connection string must come back, and must not be readable from
// the file.
//
// Both halves matter. The first is the feature working; the second is the
// only reason the feature is allowed to store a password at all.
func TestConnectionStringsAreSealedOnDisk(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "datasources.json")

	store, err := OpenSourceStore(path)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}

	const secret = "demo_user:Tr0ub4dor&3$x@tcp(10.0.0.9:3306)/books?charset=utf8mb4"
	pub, err := store.Save(DataSource{Name: "books", Driver: DriverMySQL}, secret)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	if pub.ID == "" {
		t.Fatal("the saved source has no id")
	}

	// The file must not contain the password, the DSN, or any recognisable
	// fragment of it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	body := string(raw)
	for _, leak := range []string{secret, "Tr0ub4dor&3$x", "10.0.0.9", "demo_user:"} {
		if strings.Contains(body, leak) {
			t.Errorf("the stored file contains %q in the clear:\n%s", leak, body)
		}
	}

	// And it must still be usable: a second store, opening the same files,
	// reads it back. This is the part that proves the key was persisted and
	// not just held in memory.
	again, err := OpenSourceStore(path)
	if err != nil {
		t.Fatalf("reopening the store: %v", err)
	}
	list := again.List()
	if len(list) != 1 {
		t.Fatalf("reopened store has %d sources, want 1", len(list))
	}
	if list[0].Summary != "10.0.0.9:3306 / books" {
		t.Errorf("summary is %q, want the host and database", list[0].Summary)
	}
}

// The public shape cannot carry a secret, by construction.
//
// PublicSource has no DSN field, which is the point: "remember to blank it"
// is a rule a new route eventually forgets, and a type without the field
// cannot forget.
func TestPublicSourceHasNowhereToPutASecret(t *testing.T) {
	dir := tempDir(t)
	store, err := OpenSourceStore(filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := store.Save(
		DataSource{Name: "x", Driver: DriverSQLServer},
		"sqlserver://sa:hunter2@db.example:1433?database=erp")
	if err != nil {
		t.Fatal(err)
	}
	// Whatever is in the struct, serialised, must not hold the password.
	if strings.Contains(pub.Summary, "hunter2") || strings.Contains(pub.Summary, "sa:") {
		t.Errorf("the summary leaks credentials: %q", pub.Summary)
	}
	if pub.Summary != "db.example:1433 / erp" {
		t.Errorf("summary is %q, want host and database", pub.Summary)
	}
	if pub.DriverLbl != "SQL Server" {
		t.Errorf("driver label is %q", pub.DriverLbl)
	}
}

// Editing without re-typing the password.
//
// Somebody renaming a connection, or correcting its note, will not have the
// production password to hand -- and requiring it would mean either they do
// not fix the name or they go and find the password, neither of which is a
// good outcome.
func TestSavingWithoutADSNKeepsTheStoredOne(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "d.json")
	store, err := OpenSourceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := store.Save(DataSource{Name: "first", Driver: DriverMySQL},
		"u:p@tcp(h:3306)/db1")
	if err != nil {
		t.Fatal(err)
	}

	// Rename, no DSN supplied.
	renamed, err := store.Save(DataSource{ID: pub.ID, Name: "second", Driver: DriverMySQL, Note: "prod"}, "")
	if err != nil {
		t.Fatalf("renaming: %v", err)
	}
	if renamed.Name != "second" || renamed.Note != "prod" {
		t.Errorf("the rename did not take: %+v", renamed)
	}
	if renamed.Summary != "h:3306 / db1" {
		t.Errorf("the connection string was lost on rename: summary is %q", renamed.Summary)
	}
	if !renamed.CreatedAt.Equal(pub.CreatedAt) {
		t.Error("CreatedAt changed on an edit")
	}

	// A NEW source with no DSN is a different case and must be refused --
	// otherwise the list fills with entries that can never connect.
	if _, err := store.Save(DataSource{Name: "empty", Driver: DriverMySQL}, ""); err == nil {
		t.Error("a new source with no connection string was accepted")
	}
}

func TestSourceValidation(t *testing.T) {
	dir := tempDir(t)
	store, err := OpenSourceStore(filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		why  string
		d    DataSource
		dsn  string
		want string
	}{
		{"no name", DataSource{Driver: DriverMySQL}, "u:p@tcp(h)/d", "name"},
		{"no engine", DataSource{Name: "x"}, "u:p@tcp(h)/d", "engine"},
		{"an engine that does not exist", DataSource{Name: "x", Driver: "oracle"}, "u:p@tcp(h)/d", "engine"},
	}
	for _, c := range cases {
		_, err := store.Save(c.d, c.dsn)
		if err == nil {
			t.Errorf("%s was accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.why, err, c.want)
		}
	}
}

// Deleting removes it and forgets the pool.
func TestDeleteSource(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "d.json")
	store, err := OpenSourceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := store.Save(DataSource{Name: "x", Driver: DriverMySQL}, "u:p@tcp(h:1)/d")
	if err != nil {
		t.Fatal(err)
	}
	if !store.Has(pub.ID) {
		t.Fatal("saved source is not present")
	}
	if err := store.Delete(pub.ID); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if store.Has(pub.ID) {
		t.Error("the source is still there after deletion")
	}
	if store.Count() != 0 {
		t.Errorf("count is %d after deleting the only source", store.Count())
	}
	if err := store.Delete("src_nothing"); err == nil {
		t.Error("deleting something that does not exist reported success")
	}
	// And it is gone from the file, not only from memory.
	again, err := OpenSourceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Count() != 0 {
		t.Error("the deletion was not written to the file")
	}
}

// The three DSN shapes the two drivers accept.
func TestDescribeDSN(t *testing.T) {
	cases := []struct {
		why    string
		driver Driver
		dsn    string
		want   string
	}{
		{"MySQL, as the driver writes it", DriverMySQL,
			"demo_user:pw@tcp(db.example.com:3306)/reports_demo?charset=utf8mb4",
			"db.example.com:3306 / reports_demo"},
		{"MySQL with no parameters", DriverMySQL,
			"u:p@tcp(localhost:3306)/books", "localhost:3306 / books"},
		{"SQL Server as a URL", DriverSQLServer,
			"sqlserver://sa:pw@db.example.com:50447?database=ordersdb",
			"db.example.com:50447 / ordersdb"},
		{"SQL Server key=value, which is what people paste", DriverSQLServer,
			"server=db.example.com,50447;user id=sa;password=pw;database=ordersdb",
			"db.example.com,50447 / ordersdb"},
		{"key=value with spaces in the keys", DriverSQLServer,
			"Server=db1;Initial Catalog=erp;User ID=sa;Password=x",
			"db1 / erp"},
		{"nothing at all", DriverMySQL, "", "no connection string"},
	}
	for _, c := range cases {
		if got := describeDSN(c.driver, c.dsn); got != c.want {
			t.Errorf("%s:\n  describeDSN(%q)\n  = %q\n  want %q", c.why, c.dsn, got, c.want)
		}
	}
}

// A password must never survive into an error a browser will show.
func TestErrorsDoNotCarryThePassword(t *testing.T) {
	cases := []struct{ dsn, pw string }{
		{"demo_user:Tr0ub4dor&3$x@tcp(10.0.0.1:3306)/db", "Tr0ub4dor&3$x"},
		{"sqlserver://sa:hunter2@h:1433?database=d", "hunter2"},
		{"server=h;user id=sa;password=s3cret;database=d", "s3cret"},
	}
	for _, c := range cases {
		if got := passwordOf(c.dsn); got != c.pw {
			t.Errorf("passwordOf(%q) = %q, want %q", c.dsn, got, c.pw)
		}
		// A driver message that quoted the whole DSN, and one that quoted only
		// the password: both have to come back clean.
		for _, raw := range []string{
			"dial tcp: connect: connection refused (" + c.dsn + ")",
			"login failed for password " + c.pw,
		} {
			out := redactDSNError(raw, c.dsn)
			if strings.Contains(out, c.pw) {
				t.Errorf("the password survived redaction:\n  in:  %s\n  out: %s", raw, out)
			}
		}
	}
}

// A key file from another machine must fail loudly rather than quietly.
//
// On Windows the key is wrapped with DPAPI and bound to the machine, so a
// copied folder produces a key that cannot be unwrapped. The message has to
// say what happened, because the symptom -- every stored connection suddenly
// unreadable -- otherwise looks like data loss.
func TestACorruptKeyFileIsReportedClearly(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "d.json")
	if _, err := OpenSourceStore(path); err != nil {
		t.Fatal(err)
	}
	// Truncate the key to something that cannot be a 32-byte key.
	if err := os.WriteFile(path+".key", []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSourceStore(path)
	if err == nil {
		t.Fatal("a broken key file was accepted")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Errorf("the error does not mention the key: %v", err)
	}
}

// Sealing is not a fixed transformation.
//
// Two seals of the same string must differ, or the file leaks which
// connections share a password.
func TestSealingIsNotDeterministic(t *testing.T) {
	box, err := openSecretBox(filepath.Join(tempDir(t), "x"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := box.Seal("same input")
	if err != nil {
		t.Fatal(err)
	}
	b, err := box.Seal("same input")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two seals of the same value are identical, so the file shows which connections match")
	}
	for _, sealed := range []string{a, b} {
		got, err := box.Open(sealed)
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		if got != "same input" {
			t.Errorf("opened to %q", got)
		}
	}
	// An empty value round-trips as empty rather than erroring: that is how
	// "no change to the stored DSN" is represented.
	if s, err := box.Seal(""); err != nil || s != "" {
		t.Errorf("sealing empty gave %q, %v", s, err)
	}
	// Tampering must be detected, not silently decrypted to rubbish.
	if _, err := box.Open("not base64 at all !!"); err == nil {
		t.Error("garbage was accepted as a sealed value")
	}
}
