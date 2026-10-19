package reports

import (
	"strings"
	"testing"
)

// A chosen option is bound whole, not as a fragment.
//
// These sit against LIKE clauses, where a string containing no wildcard is an
// equality test. Wrapping "TO_DO" as "%TO_DO%" would also match any status
// merely containing it, which is not what picking one entry from a list means.
func TestChoiceBindsTheValueWhole(t *testing.T) {
	params := []Param{{Name: "pstatus", Kind: KindChoice, Options: []Option{
		{Value: "TO_DO", Label: "در حال اجرا"},
	}}}
	_, args, err := Bind("SELECT 1 WHERE s LIKE @pstatus", params, map[string]string{"pstatus": "TO_DO"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "TO_DO" {
		t.Errorf("bound %v, want exactly [TO_DO] with no wildcards", args)
	}
}

// An unset choice means "no filter", which against a LIKE clause is "%".
func TestChoiceUnsetMatchesEverything(t *testing.T) {
	params := []Param{{Name: "pstatus", Kind: KindChoice}}
	for _, given := range []map[string]string{nil, {"pstatus": ""}, {"pstatus": "   "}} {
		_, args, err := Bind("SELECT 1 WHERE s LIKE @pstatus", params, given)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 1 || args[0] != "%" {
			t.Errorf("with %v bound %v, want [%%]", given, args)
		}
	}
}

// Every option has to name a parameter the report declares, or the dropdown
// silently comes up empty with nothing explaining why.
func TestOptionMustReferToADeclaredParam(t *testing.T) {
	_, err := parseReport(strings.Join([]string{
		"-- id: r",
		"-- param: pstatus | choice | وضعیت",
		"-- option: pnosuch | X | ایکس",
		"SELECT 1",
	}, "\n"))
	if err == nil {
		t.Error("an option naming an undeclared parameter was accepted")
	}
}

// Options keep the order the file wrote them in, so a report can put the
// common case first.
func TestOptionsKeepFileOrder(t *testing.T) {
	r, err := parseReport(strings.Join([]string{
		"-- id: r",
		"-- param: p | choice | نوع",
		"-- option: p | 1 | یک",
		"-- option: p | 2 | دو",
		"-- option: p | 3 | سه",
		"SELECT 1 WHERE a LIKE @p",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, 3)
	for _, o := range r.Params[0].Options {
		got = append(got, o.Value)
	}
	if strings.Join(got, ",") != "1,2,3" {
		t.Errorf("options came back as %v, want 1,2,3 in file order", got)
	}
}

// A label may legitimately contain a pipe-free Persian phrase with spaces;
// only the first two separators are structural.
func TestOptionLabelKeepsItsText(t *testing.T) {
	r, err := parseReport(strings.Join([]string{
		"-- id: r",
		"-- param: ptype | choice | نوع سفارش",
		"-- option: ptype | سفارش خرید داخلی | خرید داخلی",
		"SELECT 1 WHERE a LIKE @ptype",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	o := r.Params[0].Options[0]
	if o.Value != "سفارش خرید داخلی" || o.Label != "خرید داخلی" {
		t.Errorf("parsed value=%q label=%q", o.Value, o.Label)
	}
}
