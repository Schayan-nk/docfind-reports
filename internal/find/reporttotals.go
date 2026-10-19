package find

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// A report's «جمع» over every row, not only the page on screen.
//
// The table adds up the columns a report names with "-- total:" over the rows
// it shows, which is the whole answer while the report fits on one page of a
// hundred. «مانده بدهی شرکت‌ها به ما» can run past that, and a sum of the
// first hundred companies under a «جمع» is a wrong figure on the one row
// people read first. So when the result has more than one page the server
// adds up every row -- the report run again, a thousand rows a page -- and
// the table shows that as «جمع کل».

// totalsPages bounds the run: twenty thousand rows, far past any report here.
const totalsPages = 20

// reportTotals is the declared total columns summed over the whole result,
// keyed by the lower-cased column name; nil when the report declares none or
// the run fails (the table then sums its page, and says so).
func (s *Server) reportTotals(ctx context.Context, rep reports.Report, params map[string]string) map[string]string {
	if len(rep.TotalColumns) == 0 {
		return nil
	}
	var columns []string
	var rows [][]any
	for page := 1; page <= totalsPages; page++ {
		res, _, err := s.snapRun(ctx, rep, params, page, 1000)
		if err != nil {
			logReportError(rep.ID+" totals", err)
			return nil
		}
		columns = res.Columns
		rows = append(rows, res.Rows...)
		if !res.HasMore {
			break
		}
		if page == totalsPages {
			return nil // too many to add up honestly
		}
	}
	return sumColumns(columns, rows, rep.TotalColumns)
}

// sumColumns adds up the named columns, cells written as the reports write
// them ("1,234,567", "-1,234").
func sumColumns(columns []string, rows [][]any, names []string) map[string]string {
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	out := map[string]string{}
	for i, c := range columns {
		key := strings.ToLower(c)
		if !want[key] {
			continue
		}
		var sum float64
		for _, row := range rows {
			if i < len(row) {
				sum += cellNumber(row[i])
			}
		}
		out[key] = groupRials(int64(math.Round(sum)))
	}
	return out
}

func cellNumber(v any) float64 {
	s := strings.NewReplacer(",", "", "٬", "", " ", "").Replace(cellString(v))
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
