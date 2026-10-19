package reports

import (
	"strings"
	"testing"
)

// A month in progress is measured against the same part of the months it is
// compared with.
//
// This is the whole reason the day is carried through the scan. Anchored on
// the 10th of Shahrivar: this month has 12 rows but only 4 of them fall on or
// before the 10th, and last month had 40 spread over the whole month with 8 of
// them by the 10th. The honest comparison is 4 against 8 -- down by half. The
// comparison that ignores the day is 12 against 40, down 70%, which is mostly
// the twenty days of Mordad that Shahrivar has not reached yet.
func TestAPartFinishedMonthIsComparedWithTheSamePartOfTheOthers(t *testing.T) {
	counts := map[string]float64{
		"1405/06": 12,
		"1405/05": 40,
		"1404/06": 20,
	}
	byDay := map[string]map[int]float64{
		"1405/06": {2: 1, 5: 2, 9: 1, 14: 3, 20: 5},  // 4 by the 10th
		"1405/05": {1: 3, 7: 5, 18: 12, 25: 20},      // 8 by the 10th
		"1404/06": {3: 2, 8: 8, 12: 4, 19: 3, 28: 3}, // 10 by the 10th
	}

	c := comparePeriods(counts, byDay, 1405, 6, 10)
	if !c.Available {
		t.Fatalf("comparison unavailable: %s", c.Reason)
	}
	if c.MoM.Now != 4 || c.MoM.Then != 8 {
		t.Errorf("month on month is %v against %v, want 4 against 8 -- the figures must be "+
			"cut at the same day of the month or the comparison reports the calendar "+
			"rather than the business", c.MoM.Now, c.MoM.Then)
	}
	if c.MoM.Pct != -50 {
		t.Errorf("month-on-month change is %v%%, want -50%%", c.MoM.Pct)
	}
	if c.YoY.Now != 4 || c.YoY.Then != 10 {
		t.Errorf("year on year is %v against %v, want 4 against 10", c.YoY.Now, c.YoY.Then)
	}
	if !c.Partial {
		t.Error("the 10th of the month is not the end of it; Partial must be set so the " +
			"page can say the figures are to date")
	}
}

// A whole month is not treated as partial.
func TestTheLastDayOfAMonthIsNotPartial(t *testing.T) {
	c := comparePeriods(
		map[string]float64{"1405/06": 5},
		map[string]map[int]float64{"1405/06": {1: 5}},
		1405, 6, 31)
	if c.Partial {
		t.Error("the 31st is past every Jalali month's length; the month is over")
	}
}

// Rows whose date named a month but no day are in the bars and out of the
// comparison.
//
// They cannot be placed on either side of the cut. Counting them in would
// inflate whichever month they happen to fall in; dropping them from the
// series as well would make the bars disagree with the table.
func TestARowWithNoReadableDayIsChartedButNotCompared(t *testing.T) {
	counts := map[string]float64{"1405/06": 10, "1405/05": 10}
	byDay := map[string]map[int]float64{
		"1405/06": {0: 7, 3: 3},
		"1405/05": {0: 4, 3: 6},
	}
	c := comparePeriods(counts, byDay, 1405, 6, 15)
	if c.MoM.Now != 3 || c.MoM.Then != 6 {
		t.Errorf("compared %v against %v, want 3 against 6: the day-zero rows carry no day "+
			"and cannot be placed inside or outside the cut", c.MoM.Now, c.MoM.Then)
	}
}

// Growth from nothing is words, not a percentage.
func TestARiseFromZeroReportsNoPercentage(t *testing.T) {
	c := comparePeriods(
		map[string]float64{"1405/06": 30},
		map[string]map[int]float64{"1405/06": {1: 30}},
		1405, 6, 20)
	if c.MoM.Has {
		t.Errorf("month-on-month claims a percentage of %v against a base of zero; "+
			"zero to thirty is not a percentage", c.MoM.Pct)
	}
	if c.MoM.Now != 30 {
		t.Errorf("the current figure is %v, want 30 -- it is still a fact worth printing", c.MoM.Now)
	}
}

// The year chart is always twelve months on both series.
//
// Two series of different lengths cannot be read against each other: the
// months stop lining up under one another, and Mehr of one year sits above
// Aban of the other.
func TestBothYearSeriesAreTwelveMonthsLong(t *testing.T) {
	c := comparePeriods(
		map[string]float64{"1405/06": 3, "1404/11": 9},
		map[string]map[int]float64{"1405/06": {1: 3}, "1404/11": {1: 9}},
		1405, 6, 20)
	if len(c.Months) != 12 || len(c.ThisYear) != 12 || len(c.LastYear) != 12 {
		t.Fatalf("series lengths are months=%d this=%d last=%d, want 12 each",
			len(c.Months), len(c.ThisYear), len(c.LastYear))
	}
	if c.LastYear[10] != 9 {
		t.Errorf("Bahman of last year holds %v, want 9 -- month 11 is index 10", c.LastYear[10])
	}
}

// The year total compares like with like: last year is counted only as far
// into the year as this year has reached.
func TestTheYearTotalStopsLastYearAtTheCurrentMonth(t *testing.T) {
	counts := map[string]float64{}
	for m := 1; m <= 12; m++ {
		counts[keyFor(1404, m)] = 10 // 120 over the whole of last year
	}
	counts[keyFor(1405, 1)] = 10
	counts[keyFor(1405, 2)] = 10
	counts[keyFor(1405, 3)] = 10 // 30 so far this year

	c := comparePeriods(counts, map[string]map[int]float64{}, 1405, 3, 15)
	if c.Year.Now != 30 {
		t.Errorf("this year totals %v, want 30", c.Year.Now)
	}
	if c.Year.Then != 30 {
		t.Errorf("last year totals %v, want 30 -- three months against three months. "+
			"Counting all 120 would report the business as down 75%% every spring",
			c.Year.Then)
	}
	if c.Year.Pct != 0 {
		t.Errorf("year on year moved %v%%, want 0", c.Year.Pct)
	}
}

// A short history is stated, not drawn as a collapse.
//
// Every report in this database has less than a year of history -- the longest
// measured is ten months. Drawn without comment, last year is a row of empty
// bars beside a full one, which reads as a business that has fallen off a
// cliff. The same misreading already happened once here, when an unfilled form
// field made the sales section look like a collapse.
func TestAMissingPreviousYearIsSaidOutLoud(t *testing.T) {
	counts := map[string]float64{"1405/04": 50, "1405/05": 60, "1405/06": 55}
	c := comparePeriods(counts, map[string]map[int]float64{"1405/06": {1: 55}}, 1405, 6, 20)

	var found bool
	for _, s := range c.Caveats {
		if strings.Contains(s, "۱۴۰۴") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no caveat names the missing year. Caveats were %v. Last year is empty on "+
			"every one of these reports, and a chart that does not say so reports the "+
			"database's age as a fall in business", c.Caveats)
	}
}

// A percentage built on a handful of rows says so.
func TestAPercentageOnVeryFewRowsIsMarkedThin(t *testing.T) {
	c := comparePeriods(
		map[string]float64{"1405/06": 2, "1405/05": 1},
		map[string]map[int]float64{"1405/06": {1: 2}, "1405/05": {1: 1}},
		1405, 6, 20)
	if !c.MoM.Thin {
		t.Error("two rows against one is marked as a solid 100% rise; on this many rows the " +
			"percentage is noise and the page must say so")
	}
}

// Nothing at all is a sentence, not an empty panel.
func TestNoRowsGivesAReasonRatherThanABlank(t *testing.T) {
	c := comparePeriods(map[string]float64{}, map[string]map[int]float64{}, 1405, 6, 20)
	if c.Available {
		t.Fatal("a comparison over no rows claims to be available")
	}
	if c.Reason == "" {
		t.Error("no reason given; the panel would render empty and read as broken")
	}
}

// The comparison is anchored on today, not on the last month that has rows.
//
// A report with nothing recorded this month must say "nothing this month",
// not silently slide back to Mordad and label it Shahrivar.
func TestTheAnchorIsTodayNotTheLastMonthWithData(t *testing.T) {
	counts := map[string]float64{"1405/04": 40, "1405/05": 30}
	byDay := map[string]map[int]float64{
		"1405/04": {5: 40},
		"1405/05": {5: 30},
	}
	c := comparePeriods(counts, byDay, 1405, 6, 20)
	if c.Month != "1405/06" {
		t.Errorf("anchored on %s, want 1405/06 -- today's month", c.Month)
	}
	if c.MoM.Now != 0 || c.MoM.Then != 30 {
		t.Errorf("compared %v against %v, want 0 against 30", c.MoM.Now, c.MoM.Then)
	}
	if !c.MoM.Has || c.MoM.Pct != -100 {
		t.Errorf("a month with nothing in it against a month with thirty is -100%%, got has=%v pct=%v",
			c.MoM.Has, c.MoM.Pct)
	}
}

// Farvardin looks back to Esfand of the previous year, not to month zero.
func TestTheFirstMonthOfTheYearComparesWithTheLastOfThePrevious(t *testing.T) {
	counts := map[string]float64{"1405/01": 10, "1404/12": 20}
	byDay := map[string]map[int]float64{
		"1405/01": {2: 10},
		"1404/12": {2: 20},
	}
	c := comparePeriods(counts, byDay, 1405, 1, 15)
	if c.MoM.Then != 20 {
		t.Errorf("Farvardin's previous month holds %v, want 20 (Esfand 1404)", c.MoM.Then)
	}
	if !strings.Contains(c.MoM.Label, "اسفند") {
		t.Errorf("the previous month is labelled %q, want it to name Esfand", c.MoM.Label)
	}
}

func keyFor(y, m int) string {
	return jalaliKey(y, m)
}
