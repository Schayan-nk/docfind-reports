package find

import "testing"

// The whole result's «جمع»: the declared columns added up over every row,
// written as the reports write amounts; a column not declared is left out,
// and a blank or unreadable cell counts as nothing.
func TestTheTotalAddsUpEveryRow(t *testing.T) {
	columns := []string{"customer_name", "Receivable", "last_movement"}
	rows := [][]any{
		{"الف", "1,200,000", "1405/07/01"},
		{"ب", []byte("-200,000"), ""},
		{"پ", "", "1405/06/30"},
		{"ت", "3٬000", nil},
	}
	got := sumColumns(columns, rows, []string{"receivable"})
	if len(got) != 1 || got["receivable"] != "1,003,000" {
		t.Errorf("got %v", got)
	}
	if got := sumColumns(columns, rows, nil); len(got) != 0 {
		t.Errorf("no total declared, got %v", got)
	}
}
