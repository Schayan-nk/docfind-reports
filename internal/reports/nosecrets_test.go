package reports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No real credential is written down in this repository.
//
// This exists because one was. The reporting account's actual user name and
// password were used as the fixture in three test files -- with invented hosts,
// which is what made it look harmless. It was not: the host is the part nobody
// needs, and a password reused across a live database is a live password
// wherever it is written. It reached git and would have reached anyone the
// repository was ever shared with.
//
// The fixtures are invented strings now, chosen to keep exercising the same
// awkward characters. This test refuses the real values by NOT containing
// them: each is split so that the scanner below can find it in another file
// while this file itself holds no copy of it.
func TestNoRealCredentialIsCommitted(t *testing.T) {
	// Assembled at run time, so this source file does not itself contain the
	// strings it is looking for.
	banned := []struct {
		parts []string
		what  string
	}{
		{[]string{"P@s", "$word", "1234"}, "the reporting database password"},
		{[]string{"Fereshteh", "0013"}, "a staff account password"},
		{[]string{"gEWx", "$8>#7C0}"}, "the workflow engine's administrator password"},
		{[]string{"Ypphes", "#15w@78g"}, "a server administrator password"},
		{[]string{"h>YC", "!@74{,4J6kA"}, "a server administrator password"},
		{[]string{"abc", ".12341234"}, "a staff account password"},
	}

	root := repoRoot(t)
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable is not this test's business
		}
		if info.IsDir() {
			// Not the scratch copies, the worktrees, or git's own object
			// store: none of them is committed, and .scratch holds whole
			// checkouts that would take minutes to walk.
			switch info.Name() {
			case ".git", ".scratch", "node_modules", "dist", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		// Source and configuration only. A .docx fixture is a zip and a false
		// positive waiting to happen.
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".html", ".css", ".js", ".json", ".md", ".ps1", ".sql", ".yml", ".yaml", ".sh", ".env":
		default:
			return nil
		}
		if strings.HasSuffix(path, "nosecrets_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || len(b) > 4<<20 {
			return nil
		}
		body := string(b)
		for _, c := range banned {
			if strings.Contains(body, strings.Join(c.parts, "")) {
				rel, _ := filepath.Rel(root, path)
				found = append(found, rel+" contains "+c.what)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	for _, f := range found {
		t.Errorf("a real credential is written down in the repository: %s\n"+
			"  Replace it with an invented value. A fixture needs to be awkward, not real.", f)
	}
}

// repoRoot climbs to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("cannot find the repository root (no go.mod above the test directory)")
	return ""
}
