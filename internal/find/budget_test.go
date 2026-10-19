package find

import (
	"bytes"
	"compress/gzip"
	"os"
	"strings"
	"testing"
)

// Route budgets: how many compressed kilobytes each page and each shared
// file may weigh. Measured on 2026-10-18 and set about fifteen per cent above
// that, so ordinary work fits and a page that doubles -- an inlined library,
// a data table pasted into a script -- fails here rather than on a phone on a
// slow line. Raising a number is allowed; it should be a decision, made in
// the diff where somebody can see it, not something that happens by drift.
var budgetsKB = map[string]int{
	// pages
	"dashboard.html": 57, "builder.html": 51, "settings.html": 18, "login.html": 9,
	"offline.html": 4, "error.html": 3,
	// shared files
	"app.css": 61, "uiverse.css": 10, "nav.js": 52, "glassnav.js": 5, "meganav.js": 7,
	"useredit.js": 7, "datepick.js": 5, "sw.js": 4, "twostep.js": 4, "passkeys.js": 4,
	"roles.js": 3, "colresize.js": 3,
}

func TestPrefetchIsPagesOnly(t *testing.T) {
	nav := readPage(t, "nav.js")
	for _, want := range []string{"rules.type = 'speculationrules'", "{ not: { href_matches: '/api/*' } }", "eagerness: 'moderate'"} {
		if !strings.Contains(nav, want) {
			t.Errorf("nav.js speculation rules lost %q", want)
		}
	}
	if strings.Contains(nav, "prerender: [") {
		t.Error("nav.js prerenders pages; it should only prefetch them")
	}
}

func TestEveryPageStaysInsideItsBudget(t *testing.T) {
	for name, kb := range budgetsKB {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, 6)
		zw.Write(b)
		zw.Close()
		if got := buf.Len(); got > kb*1024 {
			t.Errorf("%s is %d KB compressed, over its %d KB budget", name, got/1024, kb)
		}
	}
}
