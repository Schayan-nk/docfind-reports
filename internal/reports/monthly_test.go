package reports

import (
	"sort"
	"testing"
)

// The keys are sorted as plain strings. That is only correct while they are
// fixed-width and zero-padded, so the property is worth pinning down.
func TestMonthKeysSortChronologically(t *testing.T) {
	in := []string{"1404/10", "1403/12", "1404/02", "1404/01", "1405/01", "1404/09"}
	want := []string{"1403/12", "1404/01", "1404/02", "1404/09", "1404/10", "1405/01"}
	got := append([]string(nil), in...)
	sort.Strings(got)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted %v, want %v", got, want)
		}
	}
}

func TestJalaliMonthLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1404/01", "فروردین ۱۴۰۴"},
		{"1404/03", "خرداد ۱۴۰۴"},
		{"1404/12", "اسفند ۱۴۰۴"},
		{"1403/07", "مهر ۱۴۰۳"},
	}
	for _, c := range cases {
		if got := jalaliMonthLabel(c.in); got != c.want {
			t.Errorf("jalaliMonthLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Anything unparseable comes back untouched rather than panicking on a
	// month index out of range.
	for _, in := range []string{"", "1404", "1404/99", "junk"} {
		if got := jalaliMonthLabel(in); got != in {
			t.Errorf("jalaliMonthLabel(%q) = %q, want it returned unchanged", in, got)
		}
	}
}

// The month filter a click applies uses /01../31 as its bounds. That works
// only because the comparison is lexicographic on a fixed-width format: the
// upper bound must cover a thirty-day month completely and still fall short
// of the next month's first day.
func TestMonthBoundsCannotLeakIntoTheNextMonth(t *testing.T) {
	const upper = "1404/07/31" // Mehr has 30 days
	for _, inside := range []string{"1404/07/01", "1404/07/15", "1404/07/30"} {
		if inside > upper {
			t.Errorf("%q sorts after the month's upper bound %q", inside, upper)
		}
	}
	for _, outside := range []string{"1404/08/01", "1404/12/29", "1405/01/01"} {
		if outside <= upper {
			t.Errorf("%q sorts within the month bounded by %q", outside, upper)
		}
	}
}
