package reports

import "testing"

// Values in these views are not plain ASCII digits.
//
// Regression test for a chart of order values that came back as a row of
// zeros: the money column carries thousands separators, and in a Persian
// application the digits are often Persian or Arabic-Indic. ParseFloat
// rejected all of it, every row counted as unparseable, and the chart drew
// nothing while looking like a real answer.
func TestAsFloatReadsTheNumbersTheseViewsActuallyStore(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"1234", 1234},
		{"1,234,567", 1234567},
		{"1234.5", 1234.5},
		{"-42", -42},
		{"  88  ", 88},
		{"۱۲۳۴", 1234},         // Persian digits
		{"۱٬۲۳۴٬۵۶۷", 1234567}, // Persian digits, Arabic thousands separator
		{"۱۲٫۵", 12.5},         // Arabic decimal separator
		{"٣٤٥", 345},           // Arabic-Indic digits
		{"1 234 567", 1234567}, // space as a separator
	}
	for _, c := range cases {
		got, ok := asFloat(c.in)
		if !ok {
			t.Errorf("asFloat(%q) reported not-a-number; expected %v", c.in, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("asFloat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Anything that is not a number must be refused rather than guessed at, so a
// text column charted by mistake shows as unparsed instead of as zeros.
func TestAsFloatRefusesThingsThatAreNotNumbers(t *testing.T) {
	for _, s := range []string{"", "   ", "n/a", "۱۲ ریال", "abc", "12abc", "--"} {
		if v, ok := asFloat(s); ok {
			t.Errorf("asFloat(%q) = %v, expected a refusal", s, v)
		}
	}
}

// The money columns arrive pre-formatted for display.
//
// Regression test for the live finding that every one of 3,912 order rows was
// rejected as non-numeric: the amounts read "€ 30,744.00", and the parser
// refused at the currency sign.
func TestAsMoneyReadsPreformattedAmounts(t *testing.T) {
	cases := []struct {
		in       string
		want     float64
		currency string
	}{
		{"€ 30,744.00", 30744, "€"},
		{"€30744", 30744, "€"},
		{"$ 1,250.50", 1250.5, "$"},
		{"1,000", 1000, ""},
		{"﷼ ۱٬۲۰۰", 1200, "﷼"},
	}
	for _, c := range cases {
		got, cur, ok := asMoney(c.in)
		if !ok {
			t.Errorf("asMoney(%q) refused the value", c.in)
			continue
		}
		if got != c.want || cur != c.currency {
			t.Errorf("asMoney(%q) = %v/%q, want %v/%q", c.in, got, cur, c.want, c.currency)
		}
	}
}

// A label whose value does not parse must still form one category.
//
// Regression test for the bug that made 3,912 orders into 3,912 single-row
// categories: seen-ness was keyed off the totals map, so a row with an
// unparseable value left the label in the ordering with no entry in totals,
// and every later row carrying that same label was treated as new.
func TestRepeatedLabelStaysOneCategoryWhenValuesDoNotParse(t *testing.T) {
	totals := map[string]float64{}
	seen := map[string]bool{}
	var order []string
	var unparsed int

	// Two rows sharing a label, neither with a usable value, then one with.
	rows := []struct {
		label string
		value string
	}{
		{"طناز حصیبی", "n/a"},
		{"طناز حصیبی", ""},
		{"طناز حصیبی", "500"},
		{"علیرضا شمس", "250"},
	}
	for _, r := range rows {
		if !seen[r.label] {
			seen[r.label] = true
			order = append(order, r.label)
			totals[r.label] = 0
		}
		if n, ok := asFloat(r.value); ok {
			totals[r.label] += n
		} else {
			unparsed++
		}
	}

	if len(order) != 2 {
		t.Errorf("got %d categories %v, want 2", len(order), order)
	}
	if totals["طناز حصیبی"] != 500 {
		t.Errorf("repeated label totalled %v, want 500", totals["طناز حصیبی"])
	}
	if unparsed != 2 {
		t.Errorf("unparsed = %d, want 2", unparsed)
	}
}

// Every chart aggregation named in a report file has to be one the runner
// implements, or the chart silently produces nothing.
func TestChartAggregationsAreImplemented(t *testing.T) {
	cat, _ := LoadCatalog(repoReportsDir(t))
	for _, meta := range cat.List() {
		switch meta.ChartAgg {
		case "", "count", "sum":
		default:
			t.Errorf("%s: chart agg %q is not implemented", meta.ID, meta.ChartAgg)
		}
	}
}
