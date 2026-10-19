package reports

import (
	"strings"
	"testing"
)

func chart(labels []string, values []float64) *ChartData {
	return &ChartData{Labels: labels, Values: values, Groups: len(labels), Rows: len(labels)}
}

// A distribution dominated by one entry is the single most useful thing to
// say about a ranked chart, so it has to be said.
func TestAnalyseNamesADominantEntry(t *testing.T) {
	cd := chart([]string{"الف", "ب", "ج", "د"}, []float64{700, 120, 100, 80})
	got := Analyse(cd, "کارشناس", "تعداد")
	if len(got) == 0 {
		t.Fatal("no insights produced for a plainly skewed distribution")
	}
	first := got[0]
	if first.Kind != "concentration" || first.Level != "act" {
		t.Errorf("expected a concentration insight at act level, got %+v", first)
	}
	if !strings.Contains(first.Text, "الف") {
		t.Errorf("the dominant entry is not named: %q", first.Text)
	}
}

// An even distribution has no story, and inventing one would train people to
// ignore the panel.
func TestAnalyseStaysQuietOnAnEvenSpread(t *testing.T) {
	cd := chart([]string{"a", "b", "c", "d", "e"}, []float64{100, 98, 102, 99, 101})
	for _, i := range Analyse(cd, "دسته", "تعداد") {
		if i.Kind == "concentration" || i.Kind == "outlier" {
			t.Errorf("claimed %q about an even distribution: %s", i.Kind, i.Text)
		}
	}
}

// One extreme value must be caught even though it also inflates the standard
// deviation enough to hide itself -- which is why the test uses median
// absolute deviation instead.
func TestAnalyseFindsAnOutlierAStdDevWouldHide(t *testing.T) {
	cd := chart(
		[]string{"a", "b", "c", "d", "e", "f", "g", "spike"},
		[]float64{10, 11, 9, 10, 12, 10, 11, 900})
	var found bool
	for _, i := range Analyse(cd, "دسته", "تعداد") {
		if i.Kind == "outlier" && strings.Contains(i.Text, "spike") {
			found = true
		}
	}
	if !found {
		t.Error("the 900 among tens was not reported as an outlier")
	}
}

// Data problems outrank observations about the data: a total built from a
// capped scan or from rows that failed to parse is not a fact to reason from.
func TestAnalyseReportsDataQuality(t *testing.T) {
	cd := chart([]string{"a", "b", "c"}, []float64{50, 40, 30})
	cd.Capped = true
	cd.Unparsed = 40
	cd.Rows = 100

	var capped, unparsed bool
	for _, i := range Analyse(cd, "دسته", "تعداد") {
		if i.Kind == "quality" && i.Level == "act" {
			if strings.Contains(i.Text, "نمونه") {
				capped = true
			}
			if strings.Contains(i.Text, "۴۰") {
				unparsed = true
			}
		}
	}
	if !capped {
		t.Error("a capped scan was not flagged")
	}
	if !unparsed {
		t.Error("40 unparseable rows out of 100 were not flagged")
	}
}

// Empty input must produce nothing rather than a panic or a sentence about
// nothing.
func TestAnalyseHandlesEmptyAndZeroData(t *testing.T) {
	if got := Analyse(nil, "x", "y"); got != nil {
		t.Errorf("nil data produced %v", got)
	}
	if got := Analyse(chart(nil, nil), "x", "y"); got != nil {
		t.Errorf("empty data produced %v", got)
	}
	if got := Analyse(chart([]string{"a", "b"}, []float64{0, 0}), "x", "y"); got != nil {
		t.Errorf("all-zero data produced %v", got)
	}
}

// The panel is capped: past a handful, nobody reads it.
func TestAnalyseStaysShort(t *testing.T) {
	labels := make([]string, 20)
	values := make([]float64, 20)
	for i := range labels {
		labels[i] = string(rune('a' + i))
		values[i] = float64(1000 / (i + 1))
	}
	cd := chart(labels, values)
	cd.Capped, cd.Unparsed, cd.Rows, cd.Groups = true, 5, 100, 40
	if got := Analyse(cd, "دسته", "تعداد"); len(got) > 5 {
		t.Errorf("produced %d insights; the panel is capped at 5", len(got))
	}
}

// Round numbers must survive formatting.
//
// Regression test: trailing zeros were stripped unconditionally, so 40 became
// "۴" and 100 became "۱" -- every round figure in every insight was wrong.
func TestPersianNumberFormattingKeepsTrailingZeros(t *testing.T) {
	cases := []struct {
		v    float64
		dp   int
		want string
	}{
		{40, 0, "۴۰"}, {100, 0, "۱۰۰"}, {5, 0, "۵"}, {2050, 0, "۲۰۵۰"},
		{1.5, 1, "۱٫۵"}, {2.0, 1, "۲"}, {3.25, 2, "۳٫۲۵"},
	}
	for _, c := range cases {
		if got := fa(c.v, c.dp); got != strings.ReplaceAll(c.want, "٫", ".") {
			t.Errorf("fa(%v, %d) = %q, want %q", c.v, c.dp, got,
				strings.ReplaceAll(c.want, "٫", "."))
		}
	}
	// And the separated form used for totals.
	if got := fmtNum(1234567); got != "۱٬۲۳۴٬۵۶۷" {
		t.Errorf("fmtNum(1234567) = %q", got)
	}
	if got := fmtNum(100); got != "۱۰۰" {
		t.Errorf("fmtNum(100) = %q", got)
	}
}

// Numbers in the text are Persian, matching every other figure on that screen.
func TestInsightNumbersArePersian(t *testing.T) {
	cd := chart([]string{"الف", "ب", "ج", "د"}, []float64{7000, 1200, 1000, 800})
	for _, i := range Analyse(cd, "کارشناس", "تعداد") {
		if strings.ContainsAny(i.Text, "0123456789") {
			t.Errorf("Latin digits in an insight: %q", i.Text)
		}
	}
}
