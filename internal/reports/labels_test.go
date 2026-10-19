package reports

import "testing"

// A column the query already aliased into Persian is the author's wording and
// beats a label the report declares for it.
func TestPersianAliasesAreLeftAlone(t *testing.T) {
	r := Report{ColumnLabels: map[string]string{"نام فرآیند": "something else"}}
	if got := LabelFor(r, "نام فرآیند"); got != "نام فرآیند" {
		t.Errorf("LabelFor rewrote a Persian alias: %q", got)
	}
}

// "-- column:" labels a column however the query spelled it.
func TestColumnLabelsMatchCaseInsensitively(t *testing.T) {
	r := Report{ColumnLabels: map[string]string{"order_no": "شماره سفارش"}}
	if got := LabelFor(r, "ORDER_NO"); got != "شماره سفارش" {
		t.Errorf("LabelFor(ORDER_NO) = %q, want the report's label", got)
	}
}

// An unlabelled column keeps its own name rather than becoming blank.
func TestUnknownColumnKeepsItsName(t *testing.T) {
	if got := LabelFor(Report{}, "SOME_NEW_FIELD"); got != "SOME_NEW_FIELD" {
		t.Errorf("LabelFor(%q) = %q", "SOME_NEW_FIELD", got)
	}
}

// Nothing is hidden unless the report says so: a column the author selected
// is a column they meant to show.
func TestEveryColumnIsShownByDefault(t *testing.T) {
	keep, headings := Visible(Report{}, []string{"id", "order_no", "customer"})
	if len(keep) != 3 || len(headings) != 3 {
		t.Errorf("kept %v %v, want all three", keep, headings)
	}
}

// "-- hide:" keeps a column off the screen and out of the export, and Project
// narrows a row to the columns that are left.
func TestHideDropsANamedColumn(t *testing.T) {
	r := Report{
		HideColumns:  map[string]bool{"id": true},
		ColumnLabels: map[string]string{"order_no": "شماره سفارش"},
	}
	keep, headings := Visible(r, []string{"ID", "order_no", "customer"})
	if len(keep) != 2 || headings[0] != "شماره سفارش" || headings[1] != "customer" {
		t.Fatalf("headings = %v, want the labelled order number and the customer", headings)
	}
	got := Project([]any{7, "ORD-1001", "بازرگانی آلفا"}, keep)
	if len(got) != 2 || got[0] != "ORD-1001" || got[1] != "بازرگانی آلفا" {
		t.Errorf("Project = %v, want the two visible values", got)
	}
}

// "-- show:" beats "-- hide:" for the same column.
func TestShowBeatsHide(t *testing.T) {
	r := Report{HideColumns: map[string]bool{"id": true}, ShowColumns: map[string]bool{"id": true}}
	if _, headings := Visible(r, []string{"id"}); len(headings) != 1 {
		t.Errorf("an explicit show was ignored: %v", headings)
	}
}

// MySQL's zero date is blanked where it appears, and a column holding nothing
// else is one the page may drop.
func TestZeroDates(t *testing.T) {
	for _, v := range []any{"0000-00-00", "0000-00-00 00:00:00", "0000/00/00", "", nil} {
		if !IsZeroDate(v) {
			t.Errorf("IsZeroDate(%v) = false", v)
		}
	}
	for _, v := range []any{"1405/01/01", "2026-03-21", "0000-01-01", 5} {
		if IsZeroDate(v) {
			t.Errorf("IsZeroDate(%v) = true", v)
		}
	}
	row := []any{"0000-00-00", 5}
	BlankZeroDates(row)
	if row[0] != "" || row[1] != 5 {
		t.Errorf("BlankZeroDates = %v", row)
	}
	if !AllZeroDates([][]any{{"0000-00-00"}, {nil}, {""}}, 0) {
		t.Error("a column of zero dates was not recognised")
	}
	if AllZeroDates([][]any{{"0000-00-00"}, {"1405/02/03"}}, 0) {
		t.Error("one real date makes it a date column")
	}
	if AllZeroDates(nil, 0) {
		t.Error("no rows is not a column of zero dates")
	}
}
