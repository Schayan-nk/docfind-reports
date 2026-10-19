package reports

import (
	"strings"
	"testing"
)

// Every chart type the page offers has to be one CleanChart accepts, and its
// flags have to describe it truthfully -- the page reads the same flags to
// decide which controls to show, so a lie here is a control that appears and
// then refuses to save.
func TestEveryChartKindIsUsableAndHonestlyDescribed(t *testing.T) {
	kinds := ChartKinds()
	if len(kinds) < 12 {
		t.Errorf("only %d chart types are offered", len(kinds))
	}

	seen := map[string]bool{}
	for _, k := range kinds {
		if k.ID == "" || k.Label == "" || k.Hint == "" {
			t.Errorf("%+v: every type needs an id, a Persian label and a hint", k)
		}
		if seen[k.ID] {
			t.Errorf("the chart type %q is listed twice", k.ID)
		}
		seen[k.ID] = true

		// A spec built the way the page builds one for this kind must save.
		spec := &ChartSpec{Kind: k.ID, Label: "cat", Values: []string{"a", "b"}}
		if k.Single {
			spec.Values = []string{"a"}
		}
		if k.Bins {
			spec.Label = ""
			spec.Values = []string{"a"}
		}
		got, err := CleanChart(spec)
		if err != nil {
			t.Errorf("%s: a straightforward spec was refused: %v", k.ID, err)
			continue
		}
		if got == nil {
			t.Errorf("%s: a straightforward spec produced no chart", k.ID)
			continue
		}

		// The flags have to match what CleanChart actually does with the kind.
		if k.Bins {
			if got.Label != "" || got.Agg != "" {
				t.Errorf("%s says it uses ranges, but kept a label or an aggregation: %+v",
					k.ID, got)
			}
			if got.Bins < binsMin {
				t.Errorf("%s says it uses ranges, but got %d of them", k.ID, got.Bins)
			}
		}
		if k.NoAgg && got.Agg != "none" && !k.Bins {
			t.Errorf("%s says it does not aggregate, but its aggregation is %q", k.ID, got.Agg)
		}
		if !k.Stackable {
			stacked := *spec
			stacked.Stacked = true
			if s, err := CleanChart(&stacked); err == nil && s.Stacked {
				t.Errorf("%s is not stackable but kept the stacked flag", k.ID)
			}
		}
		if k.Single {
			two := *spec
			two.Values = []string{"a", "b"}
			if _, err := CleanChart(&two); err == nil {
				t.Errorf("%s says it plots one column but accepted two", k.ID)
			}
		}
		if k.MinValues > 1 {
			one := *spec
			one.Values = []string{"a"}
			if _, err := CleanChart(&one); err == nil {
				t.Errorf("%s says it needs %d columns but accepted one", k.ID, k.MinValues)
			}
		}
	}

	// The kinds this application's data was the reason for. Named explicitly,
	// so removing one is a decision somebody makes rather than a line that
	// quietly disappears.
	for _, want := range []string{
		"bar", "hbar", "sbar", "line", "area", "mixed",
		"pie", "doughnut", "polar", "radar", "scatter", "hist",
	} {
		if !seen[want] {
			t.Errorf("the %q chart type is gone", want)
		}
	}
}

// A distribution asks one question of one column, so everything else is
// cleared rather than carried -- a control the page does not show must not be
// able to arrive set and change what is drawn.
func TestADistributionKeepsOnlyWhatItUses(t *testing.T) {
	got, err := CleanChart(&ChartSpec{
		Kind: "hist", Values: []string{"WEIGHT"},
		// All of this is meaningless for a histogram.
		Label: "method", Agg: "sum", Bucket: "month", Stacked: true, Bins: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "" || got.Agg != "" || got.Bucket != "" || got.Stacked {
		t.Errorf("a distribution kept settings it cannot use: %+v", got)
	}
	if got.Bins != 20 {
		t.Errorf("the bin count was %d, want the 20 that was asked for", got.Bins)
	}

	// The bin count is clamped rather than refused: it arrives from a number
	// box, and "12" is a better answer to a blank one than an error.
	for _, c := range []struct{ in, want int }{
		{0, binsDefault}, {1, binsMin}, {2, binsMin}, {500, binsMax}, {-5, binsMin},
	} {
		got, err := CleanChart(&ChartSpec{Kind: "hist", Values: []string{"a"}, Bins: c.in})
		if err != nil {
			t.Fatal(err)
		}
		if got.Bins != c.want {
			t.Errorf("%d bins became %d, want %d", c.in, got.Bins, c.want)
		}
	}

	// And exactly one column: a distribution of two columns at once is two
	// charts, and drawing the first under a legend naming both would lie.
	if _, err := CleanChart(&ChartSpec{Kind: "hist", Values: []string{"a", "b"}}); err == nil {
		t.Error("a distribution of two columns was accepted")
	}
	if _, err := CleanChart(&ChartSpec{Kind: "hist"}); err == nil {
		t.Error("a distribution of no column was accepted")
	}
}

// A scatter plots rows as they are: one point per row IS the chart, so folding
// them by the x value would collapse the cloud into a point per x.
func TestAScatterDoesNotAggregate(t *testing.T) {
	got, err := CleanChart(&ChartSpec{
		Kind: "scatter", Label: "WEIGHT", Values: []string{"QTY"},
		Agg: "sum", Bucket: "month", Stacked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agg != "none" {
		t.Errorf("a scatter aggregates with %q", got.Agg)
	}
	if got.Bucket != "" || got.Stacked {
		t.Errorf("a scatter kept a grouping or a stack: %+v", got)
	}

	// It needs both axes, and says which is missing.
	if _, err := CleanChart(&ChartSpec{Kind: "scatter", Values: []string{"QTY"}}); err == nil {
		t.Error("a scatter with no horizontal axis was accepted")
	} else if !strings.Contains(err.Error(), "horizontal") {
		t.Errorf("the message does not say which axis is missing: %v", err)
	}
}

// A logarithmic axis is stored where it is asked for and dropped where it
// means nothing -- a flag that is kept but ignored is one somebody will later
// believe.
func TestLogScaleIsKeptOnlyWhereItApplies(t *testing.T) {
	on, err := CleanChart(&ChartSpec{Kind: "bar", Label: "a", Values: []string{"b"}, LogScale: true})
	if err != nil {
		t.Fatal(err)
	}
	if !on.LogScale {
		t.Error("a bar chart lost its logarithmic axis")
	}
}

func TestCleanChartRefusesWhatCannotBeDrawn(t *testing.T) {
	cases := []struct {
		why  string
		in   ChartSpec
		want string
	}{
		{"a type that does not exist",
			ChartSpec{Kind: "treemap", Label: "a", Values: []string{"b"}}, "treemap"},
		{"nothing to label the bars with",
			ChartSpec{Kind: "bar", Values: []string{"b"}}, "names each bar"},
		{"nothing to plot",
			ChartSpec{Kind: "bar", Label: "a"}, "at least one column"},
		{"only blank columns to plot",
			ChartSpec{Kind: "bar", Label: "a", Values: []string{"", "  "}}, "at least one column"},
		{"the label plotted against itself",
			ChartSpec{Kind: "bar", Label: "a", Values: []string{"a"}}, "both the label"},
		{"a way of combining rows that does not exist",
			ChartSpec{Kind: "bar", Label: "a", Values: []string{"b"}, Agg: "median"}, "median"},

		// The round kinds take one column. Quietly dropping the rest would
		// draw one series under a legend naming three.
		{"a pie of three columns",
			ChartSpec{Kind: "pie", Label: "a", Values: []string{"b", "c", "d"}}, "plots one column"},
		{"a doughnut of two columns",
			ChartSpec{Kind: "doughnut", Label: "a", Values: []string{"b", "c"}}, "plots one column"},
	}
	for _, c := range cases {
		spec := c.in
		_, err := CleanChart(&spec)
		if err == nil {
			t.Errorf("%s: accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the message does not mention %q:\n  %v", c.why, c.want, err)
		}
	}
}

func TestCleanChartNormalises(t *testing.T) {
	// No kind means no chart, which is what most reports are. nil rather than
	// an empty struct, so "has a chart" is one question everywhere else.
	for _, empty := range []*ChartSpec{nil, {}, {Kind: "  "}} {
		got, err := CleanChart(empty)
		if err != nil {
			t.Fatalf("an empty chart spec was an error: %v", err)
		}
		if got != nil {
			t.Errorf("an empty chart spec became %+v, want nil", got)
		}
	}

	got, err := CleanChart(&ChartSpec{
		Kind:  " bar ",
		Label: "  supplier  ",
		Title: "  وزن به ازای فورواردر  ",
		// A repeat and a blank, both of which a form produces easily.
		Values:  []string{" weight ", "weight", "", "cost"},
		Stacked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "bar" || got.Label != "supplier" {
		t.Errorf("not trimmed: %+v", got)
	}
	if got.Title != "وزن به ازای فورواردر" {
		t.Errorf("the title was not trimmed: %q", got.Title)
	}
	if strings.Join(got.Values, ",") != "weight,cost" {
		t.Errorf("values are %v, want the duplicate and the blank gone, order kept", got.Values)
	}
	if !got.Stacked {
		t.Error("stacked was lost")
	}
}

// Counting rows per label reads no numbers, so it is the one combination that
// needs no value column -- and must not keep one it was given, because a count
// of a column is a count of rows.
func TestCountingNeedsNoValueColumn(t *testing.T) {
	got, err := CleanChart(&ChartSpec{Kind: "bar", Label: "supplier", Agg: "count"})
	if err != nil {
		t.Fatalf("counting with no value column was refused: %v", err)
	}
	if len(got.Values) != 0 {
		t.Errorf("counting kept value columns: %v", got.Values)
	}

	given, err := CleanChart(&ChartSpec{Kind: "bar", Label: "supplier",
		Agg: "count", Values: []string{"weight"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(given.Values) != 0 {
		t.Errorf("counting kept a value column it was handed: %v", given.Values)
	}

	// And every other combination still needs one, with a message that names
	// the option which would not.
	_, err = CleanChart(&ChartSpec{Kind: "bar", Label: "supplier", Agg: "sum"})
	if err == nil {
		t.Fatal("summing with nothing to sum was accepted")
	}
	if !strings.Contains(err.Error(), "تعداد ردیف") {
		t.Errorf("the message does not point at the option that needs no column: %v", err)
	}
}

// Date grouping is what the shipped "-- monthly:" directive does, and it is
// the reason a line chart over one of these columns is worth drawing: the
// dates are TEXT, so ungrouped it draws one point per day.
func TestChartDateGroupingIsChecked(t *testing.T) {
	for _, bucket := range []string{"", "day", "month", "year"} {
		got, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"}, Bucket: bucket,
		})
		if err != nil {
			t.Errorf("the %q grouping was refused: %v", bucket, err)
			continue
		}
		if got.Bucket != bucket {
			t.Errorf("the %q grouping came back as %q", bucket, got.Bucket)
		}
	}

	// Anything else is refused rather than silently ignored -- a grouping that
	// does nothing draws a chart of one point per day under a control saying
	// "month".
	for _, bad := range []string{"week", "quarter", "DAYS", "MONTHS"} {
		if _, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"}, Bucket: bad,
		}); err == nil {
			t.Errorf("the grouping %q was accepted", bad)
		}
	}

	// Case and spacing are normalised, since these arrive from a form.
	got, err := CleanChart(&ChartSpec{
		Kind: "line", Label: "d", Values: []string{"n"}, Bucket: "  Month  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bucket != "month" {
		t.Errorf("the grouping was not normalised: %q", got.Bucket)
	}
}

// Sum is the default, because the usual query here is one row per category and
// the usual question is per category.
func TestChartDefaultsToSumming(t *testing.T) {
	got, err := CleanChart(&ChartSpec{Kind: "bar", Label: "a", Values: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agg != "sum" {
		t.Errorf("the default combination is %q, want sum", got.Agg)
	}
}

// Several columns are fine on the kinds that can show them, which is the other
// half of the single-column rule.
func TestCleanChartAllowsSeveralColumnsOnBarsAndLines(t *testing.T) {
	for _, kind := range []string{"bar", "hbar", "line", "area"} {
		got, err := CleanChart(&ChartSpec{
			Kind: kind, Label: "month", Values: []string{"a", "b", "c"},
		})
		if err != nil {
			t.Errorf("%s refused three columns: %v", kind, err)
			continue
		}
		if len(got.Values) != 3 {
			t.Errorf("%s kept %d of three columns", kind, len(got.Values))
		}
	}
}

// A chart survives a save and comes back with the report, because the page
// rebuilds its editor and redraws from exactly this.
func TestChartRoundTripsThroughTheStore(t *testing.T) {
	store, err := OpenSourceStore(tempDir(t) + "/d.json")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src, err := store.Save(DataSource{Name: "db", Driver: DriverMySQL}, "u:p@tcp(127.0.0.1:1)/d")
	if err != nil {
		t.Fatal(err)
	}
	path := tempDir(t) + "/r.json"
	custom, err := OpenCustomStore(path)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := custom.Save(CustomReport{
		SourceID: src.ID, Title: "با نمودار", SQL: "SELECT 1 AS a, 2 AS b",
		Chart: &ChartSpec{Kind: "hbar", Label: "a", Values: []string{"b"},
			Title: "نمودار آزمایشی", Stacked: true},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Chart == nil {
		t.Fatal("the chart was dropped on save")
	}

	// Re-opened from disk, because the JSON tags are what actually carry it.
	again, err := OpenCustomStore(path)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := again.Get(saved.ID)
	if !ok {
		t.Fatal("the report is not there after reloading")
	}
	if back.Chart == nil {
		t.Fatal("the chart did not survive the file")
	}
	if back.Chart.Kind != "hbar" || back.Chart.Label != "a" ||
		strings.Join(back.Chart.Values, ",") != "b" ||
		back.Chart.Title != "نمودار آزمایشی" || !back.Chart.Stacked {
		t.Errorf("the chart came back changed: %+v", back.Chart)
	}

	// And a report saved with a broken chart is refused rather than stored in
	// a state that cannot be drawn.
	if _, err := custom.Save(CustomReport{
		SourceID: src.ID, Title: "bad", SQL: "SELECT 1",
		Chart: &ChartSpec{Kind: "pie", Label: "a", Values: []string{"b", "c"}},
	}, store); err == nil {
		t.Error("a pie of two columns was saved")
	}

	// A period comparison has to survive the file too, since the whole feature
	// is a stored setting the page reads back.
	cmp, err := custom.Save(CustomReport{
		SourceID: src.ID, Title: "مقایسه", SQL: "SELECT 1 AS d, 2 AS n",
		Chart: &ChartSpec{Kind: "line", Label: "d", Values: []string{"n"},
			Bucket: "month", Compare: "year"},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	reread, err := OpenCustomStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reread.Get(cmp.ID)
	if !ok || got.Chart == nil {
		t.Fatal("the comparison report is not there after reloading")
	}
	if got.Chart.Compare != "year" || got.Chart.Bucket != "month" {
		t.Errorf("the comparison came back as compare=%q bucket=%q, want year/month",
			got.Chart.Compare, got.Chart.Bucket)
	}
}

// Each comparison is paired with the grouping that leaves its axis in the
// label, and refused with any other.
//
// This is the bug the feature shipped with and had to be redesigned around: a
// comparison works by cutting a grouped label in two, so month-on-month needs
// the DAY grouping -- under the month grouping "1405/03" has no day left in it
// and the mode could be chosen, saved, and never once draw. Pairing both modes
// with "month" is the version that looked right and could not work.
func TestEachPeriodComparisonNeedsItsOwnGrouping(t *testing.T) {
	// Each mode with the grouping it declares: accepted.
	for _, mode := range ChartCompares() {
		got, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"},
			Bucket: mode.NeedsBucket, Compare: mode.ID,
		})
		if err != nil {
			t.Errorf("%q was refused with the %q grouping it asks for: %v",
				mode.ID, mode.NeedsBucket, err)
			continue
		}
		if got.Compare != mode.ID {
			t.Errorf("the %q comparison came back as %q", mode.ID, got.Compare)
		}
	}

	// Year-on-year needs months, month-on-month needs days, and each is
	// refused with every OTHER grouping -- including the one the other mode
	// wants, which is the pairing that produced a control that never drew.
	for _, mode := range ChartCompares() {
		if mode.ID == "" {
			continue
		}
		for _, bucket := range []string{"", "day", "month", "year"} {
			if bucket == mode.NeedsBucket {
				continue
			}
			_, err := CleanChart(&ChartSpec{
				Kind: "line", Label: "d", Values: []string{"n"},
				Bucket: bucket, Compare: mode.ID,
			})
			if err == nil {
				t.Errorf("the %q comparison was accepted with the %q grouping, but it "+
					"needs %q", mode.ID, bucket, mode.NeedsBucket)
				continue
			}
			// The refusal has to name the grouping to set, or the reader is
			// told no without being told what to do about it.
			want, _ := chartBucket(mode.NeedsBucket)
			if !strings.Contains(err.Error(), want.Label) {
				t.Errorf("the refusal for %q under %q does not name the %q grouping "+
					"it needs: %v", mode.ID, bucket, want.Label, err)
			}
		}
	}

	// Anything else is refused rather than stored and ignored.
	for _, bad := range []string{"week", "quarter", "YEARS", "yoy", "day"} {
		if _, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"},
			Bucket: "month", Compare: bad,
		}); err == nil {
			t.Errorf("the comparison %q was accepted", bad)
		}
	}

	// Case and spacing are normalised; these arrive from a form.
	got, err := CleanChart(&ChartSpec{
		Kind: "line", Label: "d", Values: []string{"n"},
		Bucket: "month", Compare: "  Year  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Compare != "year" {
		t.Errorf("the comparison was not normalised: %q", got.Compare)
	}
}

// Every comparison names a grouping that exists, and every grouping it names
// is one CleanChart accepts.
//
// The pairing is data now, sent to the page so it can offer only the modes the
// current grouping can draw. A mode pointing at a grouping that is not in the
// list would vanish from the dropdown entirely and never be offered at all.
func TestComparisonGroupingsExist(t *testing.T) {
	for _, mode := range ChartCompares() {
		if mode.ID == "" {
			if mode.NeedsBucket != "" {
				t.Errorf("the no-comparison option asks for the %q grouping", mode.NeedsBucket)
			}
			continue
		}
		if mode.NeedsBucket == "" {
			t.Errorf("the %q comparison names no grouping, so the page cannot tell when "+
				"to offer it", mode.ID)
			continue
		}
		if _, ok := chartBucket(mode.NeedsBucket); !ok {
			t.Errorf("the %q comparison needs the grouping %q, which is not one this "+
				"report offers -- the mode can never appear", mode.ID, mode.NeedsBucket)
		}
	}
}

// The day grouping is a real grouping, not just scaffolding for the
// month-on-month comparison, so it has to stand on its own.
func TestTheDayGroupingIsOfferedAndAccepted(t *testing.T) {
	day, ok := chartBucket("day")
	if !ok {
		t.Fatal("there is no day grouping; month-on-month comparison has nothing to " +
			"put on its axis")
	}
	if strings.TrimSpace(day.Label) == "" || strings.TrimSpace(day.Hint) == "" {
		t.Error("the day grouping has no label or no hint, so its option reads blank")
	}
	got, err := CleanChart(&ChartSpec{
		Kind: "line", Label: "d", Values: []string{"n"}, Bucket: "day",
	})
	if err != nil {
		t.Fatalf("the day grouping was refused on its own: %v", err)
	}
	if got.Bucket != "day" {
		t.Errorf("the day grouping came back as %q", got.Bucket)
	}
}

// Every grouping the page offers is one CleanChart accepts, and the validation
// reads the same list the page is sent.
//
// The two were written out separately and had already drifted: the day
// grouping was added to the list and the switch that validated it still named
// only "", month and year, so the new option was offered and then refused.
func TestEveryOfferedGroupingIsAccepted(t *testing.T) {
	for _, bucket := range ChartBuckets() {
		got, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"}, Bucket: bucket.ID,
		})
		if err != nil {
			t.Errorf("the offered grouping %q is refused: %v", bucket.ID, err)
			continue
		}
		if got.Bucket != bucket.ID {
			t.Errorf("the offered grouping %q came back as %q", bucket.ID, got.Bucket)
		}
	}
}

// The kinds that have no periods to compare drop the setting instead of
// carrying one that cannot apply.
//
// A distribution's axis is value ranges and a scatter's is a number; neither
// has a date label to split. Left set, the setting would ride along through a
// save and reappear if the kind were changed back -- pointing at a grouping
// that is no longer there.
func TestPeriodComparisonIsClearedWhereItCannotApply(t *testing.T) {
	for _, kind := range []string{"hist", "scatter"} {
		got, err := CleanChart(&ChartSpec{
			Kind: kind, Label: "d", Values: []string{"n"},
			Bucket: "month", Compare: "year",
		})
		if err != nil {
			t.Errorf("%s refused a spec it should have cleaned: %v", kind, err)
			continue
		}
		if got.Compare != "" {
			t.Errorf("%s kept the comparison %q", kind, got.Compare)
		}
		// And the grouping it was hung off, for the same reason.
		if got.Bucket != "" {
			t.Errorf("%s kept the date grouping %q", kind, got.Bucket)
		}
	}
}

// Every comparison mode the page offers is one CleanChart accepts, and every
// one it accepts is offered.
//
// The list is sent to the page as a dropdown. A mode in the dropdown that the
// server refuses is a control that errors on save; one the server takes but
// the page never offers is dead.
func TestEveryOfferedComparisonIsAccepted(t *testing.T) {
	offered := ChartCompares()
	if len(offered) < 2 {
		t.Fatalf("only %d comparison modes are offered", len(offered))
	}
	for _, mode := range offered {
		if strings.TrimSpace(mode.Label) == "" {
			t.Errorf("the comparison %q has no label, so its option would be blank", mode.ID)
		}
		if strings.TrimSpace(mode.Hint) == "" {
			t.Errorf("the comparison %q has no hint", mode.ID)
		}
		// With the grouping the mode itself declares -- not a fixed one. Each
		// mode needs a different grouping, and this test asserted "month" for
		// all of them until month-on-month turned out to need days.
		got, err := CleanChart(&ChartSpec{
			Kind: "line", Label: "d", Values: []string{"n"},
			Bucket: mode.NeedsBucket, Compare: mode.ID,
		})
		if err != nil {
			t.Errorf("the offered comparison %q is refused: %v", mode.ID, err)
			continue
		}
		if got.Compare != mode.ID {
			t.Errorf("the offered comparison %q came back as %q", mode.ID, got.Compare)
		}
	}
	// The returned slice is a copy: the page's list is not the package's list,
	// or a caller could edit the options every later request is built from.
	offered[0].Label = "tampered"
	if ChartCompares()[0].Label == "tampered" {
		t.Error("ChartCompares hands out its own slice; a caller can rewrite the options")
	}
}
