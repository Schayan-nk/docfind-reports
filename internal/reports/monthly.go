package reports

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Schayan-nk/docfind-reports/internal/format"
	"github.com/Schayan-nk/docfind-reports/internal/obs"
)

// monthsShown caps the banner at a year and a half. Beyond that the bars are
// too thin to read and the older months are not what anyone is looking at.
const monthsShown = 18

// jalaliMonths are the Persian month names, indexed from one.
var jalaliMonths = [...]string{"",
	"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور",
	"مهر", "آبان", "آذر", "دی", "بهمن", "اسفند",
}

// MonthlyData is the monthly summary drawn above a report's table.
type MonthlyData struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Labels []string  `json:"labels"`
	Values []float64 `json:"values"`
	// Keys are the raw "1404/03" buckets, so the browser can filter the
	// report by a month the reader clicks.
	Keys  []string `json:"keys"`
	Total float64  `json:"total"`
	// Peak and Average give the banner its two supporting figures.
	Peak      string  `json:"peak"`
	PeakValue float64 `json:"peak_value"`
	Average   float64 `json:"average"`
	// ChangePct compares the last complete month with the one before it.
	// Reported separately from the series so the sign can be styled.
	ChangePct float64 `json:"change_pct"`
	HasChange bool    `json:"has_change"`
	// Skipped counts rows whose date could not be read, and Capped says the
	// scan stopped early. Both are shown: a chart drawn from part of the data
	// must say so.
	Skipped int   `json:"skipped"`
	Capped  bool  `json:"capped"`
	Rows    int   `json:"rows"`
	TookMs  int64 `json:"took_ms"`
	// Compare is the period panel: this month against last month, against the
	// same month a year ago, and the two Jalali years side by side.
	//
	// Built from the buckets this scan already produced rather than from
	// queries of its own, which is what keeps it following the reader's
	// filters. A comparison fetched separately would quietly describe a
	// different set of rows from the table underneath it.
	Compare *PeriodCompare `json:"compare,omitempty"`
}

// MonthlySeries groups a report's rows by the Jalali month in its configured
// date column.
//
// The whole result is scanned, not the page the reader is looking at: a
// monthly total built from fifty visible rows would be wrong in a way nobody
// could see. The dates are Jalali text ("1404/03/12"), so the first seven
// characters are the month and no calendar conversion is involved -- and
// because that form is zero-padded, sorting the keys as strings puts the
// months in chronological order.
//
// Behind the same ~15s single-flight report cache as Run and Aggregate (see
// runner.go): nobody downstream mutates the returned *MonthlyData.
func (d *DB) MonthlySeries(ctx context.Context, r Report, values map[string]string) (*MonthlyData, error) {
	defer obs.Track(ctx, obs.DB)()
	key := fmt.Sprintf("monthly|%s|%s", r.ID, filterKey(values))
	v, _, err := d.reportCache.do(key, func() (any, error) {
		return d.monthlySeriesQuery(ctx, r, values)
	})
	if err != nil {
		return nil, err
	}
	data, _ := v.(*MonthlyData)
	return data, nil
}

// monthlySeriesQuery is MonthlySeries without the cache, doing the actual scan.
func (d *DB) monthlySeriesQuery(ctx context.Context, r Report, values map[string]string) (*MonthlyData, error) {
	if r.MonthlyColumn == "" {
		return nil, fmt.Errorf("reports: %s has no monthly column", r.ID)
	}

	var (
		idx      = -1
		resolved bool
		counts   = map[string]float64{}
		// byDay is the same tally kept per day, and only the comparison reads
		// it. It is what lets a part-finished month be measured against the
		// same part of an earlier one.
		byDay   = map[string]map[int]float64{}
		rows    int
		skipped int
	)

	started := nowMs()
	err := d.streamLimited(ctx, r, values, maxChartScan, func(cols []string, row []any) error {
		if !resolved {
			idx = indexOfFold(cols, r.MonthlyColumn)
			if idx < 0 {
				return fmt.Errorf("reports: %s: monthly column %q is not in the result (have: %s)",
					r.ID, r.MonthlyColumn, strings.Join(cols, ", "))
			}
			resolved = true
		}
		rows++
		p, ok := monthPoint(fmt.Sprint(row[idx]), r.MonthlyCalendar)
		if !ok {
			skipped++
			return nil
		}
		counts[p.key]++
		if byDay[p.key] == nil {
			byDay[p.key] = map[int]float64{}
		}
		byDay[p.key][p.day]++
		return nil
	})
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > monthsShown {
		keys = keys[len(keys)-monthsShown:]
	}

	out := &MonthlyData{
		ID: r.ID, Title: r.MonthlyLabel,
		Rows: rows, Skipped: skipped, Capped: rows >= maxChartScan,
		TookMs: nowMs() - started,
	}
	for _, k := range keys {
		out.Keys = append(out.Keys, k)
		out.Labels = append(out.Labels, jalaliMonthLabel(k))
		out.Values = append(out.Values, counts[k])
		out.Total += counts[k]
		if counts[k] > out.PeakValue {
			out.PeakValue, out.Peak = counts[k], jalaliMonthLabel(k)
		}
	}
	if n := len(out.Values); n > 0 {
		out.Average = out.Total / float64(n)
	}
	// Month-on-month change, from the last two buckets present. Only
	// meaningful when the earlier one is non-zero.
	if n := len(out.Values); n >= 2 && out.Values[n-2] > 0 {
		out.ChangePct = (out.Values[n-1] - out.Values[n-2]) / out.Values[n-2] * 100
		out.HasChange = true
	}
	// Built from counts rather than from out.Values, which has been cut to the
	// last eighteen months for the bars. The year-against-year chart needs
	// twenty-four, and taking it from the trimmed series would have shown the
	// first six months of last year as zero on every report.
	jy, jm, jd := nowJalali()
	out.Compare = comparePeriods(counts, byDay, jy, jm, jd)
	return out, nil
}

// The two calendars a report's date column can be written in.
const (
	CalendarJalali    = "jalali"
	CalendarGregorian = "gregorian"
)

// datePoint is a row's date reduced to the two things a comparison needs: the
// Jalali month it falls in, and the day within that month.
//
// The day is what makes "this month against last month" honest. Today is
// almost never the last day of a month, so comparing a part-month against a
// whole one reports a fall that is nothing but the calendar -- on the 10th of
// the month every report in the building would show business down by two
// thirds. Keeping the day lets both sides be cut at the same point.
type datePoint struct {
	key string // "1405/06"
	day int    // 1..31, within that Jalali month
}

// monthPoint reads one cell in the calendar the report declared.
func monthPoint(s, calendar string) (datePoint, bool) {
	if calendar == CalendarGregorian {
		return gregorianPoint(s)
	}
	return jalaliPoint(s)
}

// jalaliPoint reads a Jalali date string.
//
// The separator varies in this data -- slash and hyphen both appear -- and
// some values carry a time after the date. The year and month must both be
// plausible: a stray "0000/00/00" or a free-text note in a date column must be
// skipped rather than becoming a bucket of its own.
//
// A missing or unreadable day is not a reason to reject the row: the month is
// still good, and the row still belongs in the series. It is reported as day
// zero, which sorts before every real day and so is never counted into a
// month-to-date figure -- present in the bars, absent from the comparison,
// which is the truthful place for a date that does not say which day it was.
func jalaliPoint(s string) (datePoint, bool) {
	s = strings.TrimSpace(latinDigits(s))
	if len(s) < 7 {
		return datePoint{}, false
	}
	s = strings.ReplaceAll(s, "-", "/")
	parts := strings.SplitN(s, "/", 3)
	if len(parts) < 2 {
		return datePoint{}, false
	}
	year, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || year < 1300 || year > 1500 {
		return datePoint{}, false
	}
	month, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || month < 1 || month > 12 {
		return datePoint{}, false
	}
	p := datePoint{key: fmt.Sprintf("%04d/%02d", year, month)}
	if len(parts) == 3 {
		// The day may be followed by a time; read only the leading digits.
		tail := strings.TrimSpace(parts[2])
		end := 0
		for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
			end++
		}
		if d, err := strconv.Atoi(tail[:end]); err == nil && d >= 1 && d <= 31 {
			p.day = d
		}
	}
	return p, true
}

// gregorianPoint reads a Gregorian date and returns the Jalali month it falls
// in, together with the Jalali day of that month.
//
// The day is converted rather than carried across: the first of March is the
// tenth or eleventh of Esfand, and a month-to-date cut taken on the Gregorian
// day number would compare two different spans.
func gregorianPoint(s string) (datePoint, bool) {
	s = strings.TrimSpace(latinDigits(s))
	if len(s) < 8 {
		return datePoint{}, false
	}
	// A datetime carries a time after the date; only the date is read.
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, 'T'); i > 0 {
		s = s[:i]
	}
	iso := strings.Count(s, "-") == 2
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
	if len(parts) != 3 {
		return datePoint{}, false
	}

	var ys, ms, ds string
	if iso && len(parts[0]) == 4 {
		ys, ms, ds = parts[0], parts[1], parts[2]
	} else if len(parts[2]) == 4 {
		ds, ms, ys = parts[0], parts[1], parts[2]
	} else {
		return datePoint{}, false
	}

	year, err := strconv.Atoi(ys)
	if err != nil || year < 1900 || year > 2100 {
		return datePoint{}, false
	}
	month, err := strconv.Atoi(ms)
	if err != nil || month < 1 || month > 12 {
		return datePoint{}, false
	}
	day, err := strconv.Atoi(ds)
	if err != nil || day < 1 || day > 31 {
		return datePoint{}, false
	}
	jy, jm, jd := format.ToJalali(year, month, day)
	if jy < 1300 || jy > 1500 || jm < 1 || jm > 12 {
		return datePoint{}, false
	}
	return datePoint{key: fmt.Sprintf("%04d/%02d", jy, jm), day: jd}, true
}

// latinDigits rewrites Persian and Arabic-Indic digits as ASCII.
//
// Dates in this database arrive in all three forms depending on which form
// wrote them, and strconv only reads the ASCII one.
func latinDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹': // Persian
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩': // Arabic-Indic
			b.WriteRune('0' + (r - '٠'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jalaliMonthLabel turns "1404/03" into "خرداد ۱۴۰۴".
func jalaliMonthLabel(key string) string {
	year, month, found := strings.Cut(key, "/")
	if !found {
		return key
	}
	m, err := strconv.Atoi(month)
	if err != nil || m < 1 || m > 12 {
		return key
	}
	return jalaliMonths[m] + " " + toPersianDigits(year)
}
