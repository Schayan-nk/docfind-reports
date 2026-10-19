package reports

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A temporary directory whose cleanup does not fail the test on Windows.
//
// t.TempDir() removes its directory at the end of a test and FAILS the test if
// it cannot. On Windows a file that was written moments earlier can still be
// held briefly by something outside this process -- a scanner, the search
// indexer -- and RemoveAll then reports "The directory is not empty". Measured
// here: two of five full-suite runs failed that way, in two different tests,
// with every assertion in them passing. The store tests are the ones it lands
// on because they write several small files and finish immediately.
//
// That is a bad failure to leave in place. Not because it is serious, but
// because a suite that fails two runs in five for a reason unrelated to the
// code teaches everybody to re-run it and stop reading, and the next failure
// that IS real gets the same treatment.
//
// So: retried, and a residue is LOGGED rather than failed. Logged rather than
// ignored on purpose -- a genuine leaked file handle would keep the directory
// undeletable through every retry, on every run, and the log line is what
// would say so. A flake appears once and is gone on the retry.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "reports-"+safeTestName(t.Name())+"-")
	if err != nil {
		t.Fatalf("temporary directory: %v", err)
	}
	t.Cleanup(func() {
		// Four attempts over about a third of a second. Long enough for a
		// scanner to let go, short enough not to slow the suite.
		for i := 0; ; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			} else if i == 3 {
				t.Logf("could not remove %s: %v -- harmless if this is the "+
					"only run it happens on; a file handle this package failed "+
					"to close would do it every time", dir, err)
				return
			}
			time.Sleep(80 * time.Millisecond)
		}
	})
	return dir
}

// safeTestName makes a test's name usable in a filename.
//
// A subtest's name carries its parent and its case, separated by a slash --
// and MkdirTemp refuses a pattern containing a separator. Learned the hard
// way: without this, a helper written to stop spurious failures caused eight
// real ones the first time it ran under a table-driven test.
func safeTestName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
