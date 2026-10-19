package reports

import (
	"strings"
	"testing"
)

// The "how much of last year is here" caveat names last year's own months.
//
// It did not, at first. The warehouse report reaches back to Azar 1402 and
// holds two months of 1404, and the sentence read "only 2 months of 1404 are
// in the database (from Azar 1402)" -- both halves true, the sentence itself
// false. It was caught by reading what the live test printed rather than by
// any assertion, which is the argument for printing these strings at all.
func TestTheShortYearCaveatNamesLastYearsOwnSpan(t *testing.T) {
	counts := map[string]float64{
		"1402/09": 4, // years earlier, and not what the sentence is about
		"1402/10": 2,
		"1404/09": 3, // the only two months of last year
		"1404/11": 1,
		"1405/06": 1,
	}
	c := comparePeriods(counts, map[string]map[int]float64{"1405/06": {1: 1}}, 1405, 6, 20)

	var line string
	for _, s := range c.Caveats {
		if strings.Contains(s, "۱۴۰۴") {
			line = s
		}
	}
	if line == "" {
		t.Fatalf("no caveat about last year. Caveats: %v", c.Caveats)
	}
	if strings.Contains(line, "۱۴۰۲") {
		t.Errorf("the caveat about 1404 names 1402: %q. The span in the brackets must be "+
			"last year's own first and last month, not the earliest month in the whole "+
			"result -- otherwise it reads as a claim about when 1404 starts", line)
	}
	if !strings.Contains(line, "آذر") || !strings.Contains(line, "بهمن") {
		t.Errorf("the caveat should name Azar and Bahman, the two months of 1404 that "+
			"carry rows; got %q", line)
	}
}

// One month of last year is not "from X to X".
func TestASingleMonthOfLastYearReadsAsOneMonth(t *testing.T) {
	counts := map[string]float64{"1404/07": 5, "1405/06": 5}
	c := comparePeriods(counts, map[string]map[int]float64{"1405/06": {1: 5}}, 1405, 6, 20)
	for _, s := range c.Caveats {
		if strings.Contains(s, "۱۴۰۴") && strings.Contains(s, "تا") {
			t.Errorf("a single month is written as a range: %q", s)
		}
	}
}

// A full previous year draws no caveat at all.
//
// A banner that is always on is a banner nobody reads; the same argument that
// keeps the decision-coverage warning quiet when the field is filled in.
func TestAFullPreviousYearSaysNothingAboutIt(t *testing.T) {
	counts := map[string]float64{}
	for m := 1; m <= 12; m++ {
		counts[jalaliKey(1404, m)] = 50
	}
	for m := 1; m <= 6; m++ {
		counts[jalaliKey(1405, m)] = 50
	}
	byDay := map[string]map[int]float64{
		"1405/06": {1: 50}, "1405/05": {1: 50}, "1404/06": {1: 50},
	}
	c := comparePeriods(counts, byDay, 1405, 6, 29)
	for _, s := range c.Caveats {
		if strings.Contains(s, "۱۴۰۴") {
			t.Errorf("a complete previous year still draws a caveat: %q", s)
		}
	}
}
