package find

import (
	"strings"
	"testing"
)

// The dock takes its destinations from nav.js's permission-checked list and
// never writes its own: a place the menu would hide must not appear here.
func TestGlassNavUsesTheCheckedDestinations(t *testing.T) {
	src := readPage(t, "glassnav.js")
	for _, want := range []string{"has('/dashboard')", "window.DocfindNav"} {
		if !strings.Contains(src, want) {
			t.Errorf("glassnav.js no longer gates on %s", want)
		}
	}
	nav := readPage(t, "nav.js")
	if !strings.Contains(nav, "mountGlassNav(visible, o, allowed)") {
		t.Error("nav.js does not hand the dock the filtered destinations")
	}
}

// The glass is darker than it looks like it needs to be (see app.css): the
// labels on it must keep their contrast over the light home panes.
func TestGlassKeepsItsContrastSettings(t *testing.T) {
	css := readPage(t, "app.css")
	for _, want := range []string{
		"--glass-bg: rgb(var(--glass-tint) / .72)",
		"brightness(.78)",
		"prefers-reduced-transparency: reduce",
		"prefers-reduced-motion: reduce",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lost %q", want)
		}
	}
}
