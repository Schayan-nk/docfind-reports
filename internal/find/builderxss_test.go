package find

import (
	"regexp"
	"strings"
	"testing"
)

// The builder renders arbitrary database content into the page, and must
// escape every piece of it.
//
// This surface is different in kind from the rest of the application. The
// other pages render an archive this service indexed itself; the builder
// renders whatever came back from a connection an administrator pointed at
// somebody else's database -- values, and COLUMN NAMES, which a query alias
// puts under the caller's control as readily as a cell. Anyone who can write
// a row into a reporting table can therefore choose markup that lands in an
// administrator's browser, and an administrator is precisely the session
// worth stealing here: they hold the connection passwords and arbitrary
// SELECT.
//
// Verified in the browser against the live instance before this was written:
// a query aliasing a column to `<img src=y onerror=...>` and selecting an
// `<img src=x onerror=...>` value rendered both as text, executed neither,
// and added no elements to the result box. These assertions hold that.
func TestBuilderEscapesEverythingFromTheDatabase(t *testing.T) {
	page := string(builderHTML)

	// The escaper itself, and the five characters it has to cover. Missing the
	// quotes matters as much as the angle brackets: the result table puts a
	// row key into an attribute, so a value carrying a quote could close it
	// and open an event handler without ever needing a "<".
	if !strings.Contains(page, "const esc = ") {
		t.Fatal("builder.html has no esc(); nothing here can be checked and the page is " +
			"rendering database content some other way")
	}
	for _, c := range []string{"&", "<", ">", `"`, "'"} {
		if !strings.Contains(page, "["+`&<>"'`+"]") {
			t.Errorf("the escaper no longer covers %q", c)
			break
		}
	}

	// Each place database content reaches the DOM, and what goes wrong
	// unescaped. These are exact renderings from the page: if one is edited,
	// this test should be re-read rather than merely re-pointed.
	sinks := []struct{ needle, why string }{{
		needle: "esc(v)",
		why: "a result CELL. The most obvious sink and the widest: every value of " +
			"every column of every query an administrator runs",
	}, {
		needle: "esc(heading)",
		why: "a column HEADING. A heading is not safer than a value -- it comes from the " +
			"query's alias or from the source table's own column name, both of which " +
			"someone else's database supplies",
	}, {
		needle: `esc(key)`,
		why: "the row key, which is written into a data-key ATTRIBUTE. Unescaped, a value " +
			"holding a quote closes the attribute and anything after it is markup",
	}, {
		needle: "esc(rr[i])",
		why: "a cell of the DETAIL panel, which renders a second query's rows the same way " +
			"the master table renders the first's",
	}, {
		needle: "esc(headingFor(res, i))",
		why:    "a heading in the detail panel, for the same reason as the master's",
	}}
	for _, s := range sinks {
		if !strings.Contains(page, s.needle) {
			t.Errorf("builder.html no longer escapes %s.\n  %s", s.needle, s.why)
		}
	}

	// And no result value may be interpolated raw. This catches the specific
	// regression of someone dropping esc() from one of the sinks above while
	// leaving the others intact -- the failure that would otherwise pass every
	// assertion here but one.
	raw := regexp.MustCompile(`\$\{\s*(row\[[^\]]+\]|rr\[[^\]]+\]|v)\s*\}`)
	if m := raw.FindAllString(page, -1); len(m) > 0 {
		t.Errorf("a database value is interpolated into markup without esc(): %v", m)
	}
}
