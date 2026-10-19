package find

import (
	"strings"
	"testing"
)

// The builder's chart reads numbers out of columns that are TEXT.
//
// This is a tripwire, not a unit test, and it is here because the function it
// guards -- toNumber in builder.html -- is JavaScript inside an embedded page
// and no Go test can call it. What it can do is fail if one of the four guards
// that function needs is deleted, because each of them prevents a chart that
// is DRAWN, is WRONG, and says nothing about it. That failure mode is why this
// exists: an exception is noticed, a plausible wrong number is not.
//
// Each case names the value that broke it. All four were measured against the
// live ProcessMaker database, in the browser, against these exact columns.
func TestBuilderNumberParsingKeepsItsGuards(t *testing.T) {
	page := string(builderHTML)

	if !strings.Contains(page, "function toNumber(") {
		t.Fatal("builder.html no longer has toNumber; the chart is reading numbers some other way " +
			"and this test needs rewriting against it")
	}

	guards := []struct {
		// needle is the code that must still be there.
		needle string
		// why is what goes wrong without it, in terms of a real value.
		why string
	}{{
		needle: `[0-9][\/:][0-9]`,
		why: "WEIGHT and the date columns are VARCHAR. Without the date guard, the Jalali date " +
			"\"1405/04/10\" has its slashes stripped and plots as 14,050,410 -- measured: 21 of 21 " +
			"rows of END_PERSIANDATE_LABEL. With it they count as unreadable and the reader is told",
	}, {
		needle: "lastDot > lastComma",
		why: "without resolving the separators by position, \"1.234,56\" and \"1,234.56\" cannot both " +
			"be read; one of them comes out a thousand times wrong",
	}, {
		needle: `,[0-9]{3}(?![0-9])`,
		why: "the live WEIGHT column holds \"40,000\". parseFloat gives 40, so a bar would be drawn at " +
			"one thousandth of its value, and \"1,5\" must still read as 1.5 rather than 15",
	}, {
		needle: "۰-۹",
		why: "Persian digits appear in these columns, and a value of \"۱۲۳۴\" with no folding is not " +
			"a number at all -- the bar silently disappears instead of being counted as unreadable",
	}}

	for _, g := range guards {
		if !strings.Contains(page, g.needle) {
			t.Errorf("builder.html lost the guard %q.\n  %s", g.needle, g.why)
		}
	}

	// Date grouping truncates a Jalali date STRING to its month, which is the
	// only way to get a month out of these columns without a calendar
	// library -- and it must refuse a label that is not a date, or a month
	// grouping over a column of forwarder names would draw a chart built from
	// fragments of them.
	if !strings.Contains(page, "function bucketLabel(") {
		t.Error("builder.html no longer groups dates for charts; the shipped reports' " +
			"monthly banner has no equivalent again")
	} else if !strings.Contains(page, `^(\d{4})[-\/](\d{1,2})`) {
		t.Error("the date grouping no longer checks that the label IS a date, so a month " +
			"grouping on a text column would truncate its values instead of leaving them")
	}
	// And grouped labels must be sorted. Measured: a query with no ORDER BY on
	// its date column produced the months 04, 05, 03, 06 in that order, and a
	// trend line through those is a zigzag that looks like a trend.
	if !strings.Contains(page, "if (spec.bucket) {") {
		t.Error("grouped chart labels are no longer sorted; a monthly trend from a query " +
			"with no ORDER BY would draw its months out of sequence")
	}

	// The gallery of previews, and the two things about it that were bugs.
	//
	// A preview canvas must sit in a container of a fixed height: Chart.js
	// responsive mode sizes the canvas to its parent and the parent to its
	// contents, so a canvas given its own height grew from 74px to 360px on
	// the first draw, each feeding the other. The same feedback that once
	// froze the reports page.
	if !strings.Contains(page, "function renderGallery(") {
		t.Error("the chart gallery is gone; choosing a chart is a dropdown of names again")
	} else if !strings.Contains(page, ".gcv { position: relative; height: 74px; }") {
		t.Error("the preview canvases no longer have a fixed-height container, so each " +
			"one will grow its tile and be grown by it in turn")
	}
	// And the previews must be drawn by the SAME config builder as the real
	// chart, or a tile can promise a picture the chart does not deliver.
	if !strings.Contains(page, "chartConfig(d, spec, true)") ||
		!strings.Contains(page, "chartConfig(d, spec, false)") {
		t.Error("the gallery and the chart no longer share chartConfig, so a preview can " +
			"differ from what it previews")
	}
	// pointLabels are not ticks: hiding the ticks left three Persian category
	// names bigger than the radar they labelled.
	if !strings.Contains(page, "pointLabels: { display: !small }") {
		t.Error("radar previews will show their point labels, which at 74px are larger " +
			"than the shape they label")
	}

	// Three silent-wrong-chart guards, each found by drawing the case and
	// looking at what came out.
	silent := []struct{ needle, why string }{{
		needle: "function countNonPositive(",
		why: "a logarithmic axis omits everything at or below nought WITHOUT saying so: " +
			"asked to draw [-5, 10, 0, 100] Chart.js produced an axis starting at 1 and " +
			"drew two bars of the four. Counting them is what lets the page report it",
	}, {
		needle: "if (!(hi > lo)) {",
		why: "a distribution whose values are all identical has ONE range. Without this " +
			"it invented the rest: three rows all reading 7, asked for five ranges, came " +
			"out as 7–8 with a count of three followed by four empty ranges up to 12",
	}, {
		needle: "noNumbers",
		why: "a column of pure text folds to a row of zeroes, which draws as flat bars " +
			"and reads as \"everything is nought\" rather than \"this column holds no " +
			"numbers\" -- so it must be refused rather than drawn",
	}}
	for _, g := range silent {
		if !strings.Contains(page, g.needle) {
			t.Errorf("builder.html lost the guard %q.\n  %s", g.needle, g.why)
		}
	}

	// And the count of what could not be read has to reach the page. A parser
	// that refuses a value silently is no better than one that mis-reads it.
	for _, needed := range []string{"unreadable", "chartWarn"} {
		if !strings.Contains(page, needed) {
			t.Errorf("builder.html no longer reports unreadable values (%q is gone); a chart drawn "+
				"from half a column would say nothing about the other half", needed)
		}
	}

	// Every one of those notes describes the chart that is on screen, so when
	// the chart goes the notes go with it -- through one function, because the
	// three places that hide the chart each used to leave them standing.
	//
	// Measured: a query naming a column that did not exist failed, the chart
	// was hidden, and "this date column has no day in it" stayed on screen
	// describing a query that no longer existed.
	if !strings.Contains(page, "function noChart(") {
		t.Error("builder.html no longer clears the chart and its notes together, so a note " +
			"about a chart that has been hidden can stay on screen")
	}
	if strings.Contains(page, "box.hidden = true;\n    if (chartObj) { chartObj.destroy(); chartObj = null; }") {
		t.Error("a place that hides the chart does it inline again instead of through " +
			"noChart(), so it will leave the previous chart's notes on screen")
	}
}

// The period comparison regroups one series into one per period, in the page.
//
// The Go side validates the SETTING -- that a comparison has a monthly
// grouping to split (TestPeriodComparisonNeedsAMonthlyGrouping) -- but the
// regrouping itself is JavaScript over the fetched rows, and no Go test can
// call it. These are the guards that stop it drawing a comparison that is
// wrong rather than one that is missing.
func TestBuilderPeriodComparisonKeepsItsGuards(t *testing.T) {
	page := string(builderHTML)

	for _, fn := range []string{"function splitPeriod(", "function comparePeriods("} {
		if !strings.Contains(page, fn) {
			t.Fatalf("builder.html no longer has %s; period comparison is gone and this "+
				"test needs rewriting against whatever replaced it", fn)
		}
	}

	guards := []struct{ needle, why string }{{
		needle: "byPosition.has(p) ? byPosition.get(p) : null",
		why: "a period with no row at a position is a GAP, not a zero. Filled with nought, " +
			"a part-finished year draws a line collapsing to the axis for every month it " +
			"has not reached -- which reads as \"we sold nothing\" rather than \"no data yet\"",
	}, {
		needle: "periods.length < 2",
		why: "a comparison of ONE period is a single line under a legend promising two. " +
			"Counting them is what lets the page say the query has to cover both",
	}, {
		needle: "if (!m[3]) return null;",
		why: "month-on-month puts the DAY on the axis, and a \"1405/03\" label has no day " +
			"in it. Without this the month would be read as the day and every period would " +
			"have exactly one point",
	}, {
		needle: "if (bucket === 'day') {",
		why: "month-on-month needs a grouping that LEAVES the day in the label, and the " +
			"month grouping truncates it away. Without the day grouping the mode is " +
			"offered, saved, and can never once draw -- which is how it first shipped",
	}, {
		needle: "mode.needs_bucket !== CHART.bucket",
		why: "each comparison is paired with one grouping, and changing the grouping fires " +
			"the read BEFORE the row is rebuilt. Unchecked, switching months to days keeps " +
			"a year-on-year comparison whose positions then collide -- every day of a " +
			"month lands on the same one and all but the last is silently overwritten",
	}, {
		needle: "compareNote",
		why: "when the comparison cannot be drawn the chart still draws -- as the one " +
			"continuous line it was. Unsaid, the reader set a comparison, got a picture, " +
			"and cannot tell it is not the one they asked for",
	}, {
		needle: "d.compared ?",
		why: "which periods are being compared has to be named in the footer: a query " +
			"covering half of 1405 compares a full year against a partial one, and the " +
			"legend alone does not show that",
	}}
	for _, g := range guards {
		if !strings.Contains(page, g.needle) {
			t.Errorf("builder.html lost the guard %q.\n  %s", g.needle, g.why)
		}
	}

	// Years are not quantities. faNum is an Intl.NumberFormat with grouping on,
	// so it renders the year 1404 as "۱٬۴۰۴" -- and cannot render the period
	// label "1405/03" at all. Measured: the first version of the footer did
	// exactly this.
	if !strings.Contains(page, "const faDigits =") {
		t.Error("builder.html has no faDigits; period labels formatted with faNum come out " +
			"as \"۱٬۴۰۴\" for the year 1404")
	}
	if strings.Contains(page, "d.compared.map(faNum") {
		t.Error("the compared periods are formatted with faNum, which groups digits: the " +
			"year 1404 will render as \"۱٬۴۰۴\"")
	}

	// The comparison must run on the AGGREGATED series, not alongside the
	// aggregation: it regroups totals that are already correct. Computed
	// earlier it would mean aggregating twice and keeping the two in step.
	cmpAt := strings.Index(page, "const cmp = comparePeriods(out, spec)")
	outAt := strings.Index(page, "const out = { missing, labels, series, unreadable")
	if cmpAt < 0 || outAt < 0 || cmpAt < outAt {
		t.Error("the period comparison no longer runs on the already-aggregated series, so " +
			"the chart's totals can drift from the table's")
	}

	// And the dropdown offers only the modes the CURRENT grouping can draw,
	// filtered on what each mode says it needs. Listing all of them would put
	// a choice in front of the reader that the server refuses on save.
	if !strings.Contains(page, "x.needs_bucket === c.bucket") {
		t.Error("the comparison dropdown no longer filters by the grouping each mode " +
			"needs, so it offers modes that cannot draw and are refused on save")
	}
	if !strings.Contains(page, "#cAgg, #cLabel, #cBucket, #cCompare") {
		t.Error("changing the comparison no longer rebuilds the settings row, so its hint " +
			"will describe the previously chosen mode")
	}
}
