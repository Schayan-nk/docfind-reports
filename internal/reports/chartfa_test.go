package reports

import "testing"

// The accounting reports write an amount with its Persian unit after it --
// «3,891,000,000.00 ریال» -- and the chart read every one as unparsable, so
// the sales proformas' chart was all zeros. The unit is kept as its code, so
// a sum still refuses to mix currencies.
func TestAnAmountWithAPersianUnitIsANumber(t *testing.T) {
	for in, want := range map[string]struct {
		n   float64
		cur string
	}{
		"3,891,000,000.00 ریال": {3891000000, "IRR"},
		"3,891,000,000.00 ريال": {3891000000, "IRR"}, // Arabic ي, as the finance database stores it
		"98,261.00 یورو":        {98261, "EUR"},
		"12,500.50 دلار":        {12500.5, "USD"},
		"360,000,000 IRR":       {360000000, "IRR"},
		"€ 30,744.00":           {30744, "€"},
	} {
		n, cur, ok := asMoney(in)
		if !ok || n != want.n || cur != want.cur {
			t.Errorf("asMoney(%q) = %v %q %v, want %v %q", in, n, cur, ok, want.n, want.cur)
		}
	}
	if _, _, ok := asMoney("ریال"); ok {
		t.Error("a unit with no amount read as a number")
	}
}
