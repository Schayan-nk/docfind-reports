package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Chart data for one report.
//
// Computed over the whole filtered result rather than the page on screen.
// Charting only the visible page was the obvious cheaper option and it is
// wrong: page one of a hundred-page result drawn as a bar chart looks like the
// answer to "how many per expert" while actually answering "how many per
// expert among the first hundred rows", and nothing on screen says so.
type ChartData struct {
	Labels []string  `json:"labels"`
	Values []float64 `json:"values"`
	// Rows is how many result rows went into this, so the page can say what
	// the chart is actually summarising.
	Rows int `json:"rows"`
	// Groups is how many distinct labels existed before the top-N cut, and
	// Other is the total the remainder folded into.
	Groups int     `json:"groups"`
	Other  float64 `json:"other,omitempty"`
	// Capped reports that the scan stopped early, so the page can say the
	// chart is based on a sample rather than quietly implying completeness.
	Capped bool `json:"capped,omitempty"`
	// Unparsed counts rows whose value column held something that is not a
	// number. Surfaced rather than swallowed: a summed chart quietly reading
	// most of its column as zero looks like a real answer.
	Unparsed int `json:"unparsed,omitempty"`
	// A few of the offending values, so a diagnostic can say what the column
	// actually contains instead of only that it was rejected.
	UnparsedSamples []string `json:"unparsed_samples,omitempty"`
	// Currency is the symbol the amounts were written in, when they all agree.
	// MixedCurrency says they did not, in which case the total is a sum of
	// different currencies and means nothing -- the page has to say so rather
	// than print a confident wrong number.
	Currency      string `json:"currency,omitempty"`
	MixedCurrency bool   `json:"mixed_currency,omitempty"`
	TookMs        int64  `json:"took_ms"`
	// Series is the breakdown inside each bar, present only when the report
	// asked for one. Values always align with Labels, index for index, with an
	// explicit zero where a combination does not occur -- the browser draws
	// these as parallel datasets and a short array would silently shift a
	// category's figures onto the wrong person.
	Series []ChartSeries `json:"series,omitempty"`
}

// ChartSeries is one coloured slice of a broken-down chart.
//
// Name is what the legend shows and what decides the colour; Stack puts the
// slice on a bar of its own beside the others under the same label. So a
// report broken down by order type and stacked by case status draws one bar
// per status for each expert, each bar split by type in its own colour.
type ChartSeries struct {
	Name   string    `json:"name"`
	Stack  string    `json:"stack,omitempty"`
	Values []float64 `json:"values"`
}

const (
	// maxChartGroups is how many bars survive; the rest fold into "other".
	// Beyond roughly this many, a bar chart stops being readable and a table
	// is the better tool.
	maxChartGroups = 20
	// maxChartScan bounds the work one chart can cause. A report that somehow
	// matches millions of rows should degrade to a capped sample rather than
	// hold the database connection for minutes.
	maxChartScan = 250000
)

// partKey identifies one slice of a broken-down chart. A struct rather than a
// joined string so a value containing the separator cannot merge two slices.
type partKey struct{ stack, series string }

// Aggregate builds chart data by scanning the report's full result.
//
// Three shapes are supported, chosen by the report's "-- chart:" line:
//
//	agg=count  tally rows per label       (a list of cases -> cases per status)
//	agg=sum    add ValueColumn per label  (order lines -> total value per expert)
//	(neither)  the query already aggregated; take the value column as given
//
// Behind the same ~15s single-flight report cache as Run (see runner.go):
// nobody downstream mutates the returned *ChartData, so sharing it across
// concurrent readers of the same report and filters is safe.
func (d *DB) Aggregate(ctx context.Context, r Report, values map[string]string) (*ChartData, error) {
	defer obs.Track(ctx, obs.DB)()
	key := fmt.Sprintf("chart|%s|%s", r.ID, filterKey(values))
	v, _, err := d.reportCache.do(key, func() (any, error) {
		return d.aggregateQuery(ctx, r, values)
	})
	if err != nil {
		return nil, err
	}
	data, _ := v.(*ChartData)
	return data, nil
}

// aggregateQuery is Aggregate without the cache, doing the actual scan.
func (d *DB) aggregateQuery(ctx context.Context, r Report, values map[string]string) (*ChartData, error) {
	if r.LabelColumn == "" {
		return nil, fmt.Errorf("reports: %s has no chart label column", r.ID)
	}
	agg := strings.ToLower(strings.TrimSpace(r.ChartAgg))
	normalisePeople := strings.EqualFold(r.ChartNormalise, "person")
	if agg != "count" && r.ValueColumn == "" {
		return nil, fmt.Errorf("reports: %s needs a chart value column for agg=%q", r.ID, agg)
	}

	var (
		labelIdx, valueIdx = -1, -1
		resolved           bool
		totals             = map[string]float64{}
		seen               = map[string]bool{}
		order              []string // first-seen order, to break ties predictably
		rows               int
		unparsed           int
		samples            []string
		currencies         = map[string]bool{}
		capped             bool
	)
	// The breakdown, kept alongside the plain totals rather than instead of
	// them: the bar heights and the "other" fold still come from totals, so a
	// report that asks for a breakdown draws the same chart it always did with
	// the slices laid into it.
	breaking := r.SeriesColumn != "" || r.StackColumn != ""
	var (
		seriesIdx, stackIdx = -1, -1
		parts               = map[partKey]map[string]float64{}
		partTotal           = map[partKey]float64{}
		partOrder           []partKey
		stackSeen           = map[string]int{}
	)

	started := nowMs()
	err := d.streamLimited(ctx, r, values, maxChartScan, func(cols []string, row []any) error {
		if !resolved {
			labelIdx = indexOfFold(cols, r.LabelColumn)
			if labelIdx < 0 {
				return fmt.Errorf("reports: %s: chart label column %q is not in the result (have: %s)",
					r.ID, r.LabelColumn, strings.Join(cols, ", "))
			}
			if agg != "count" {
				valueIdx = indexOfFold(cols, r.ValueColumn)
				if valueIdx < 0 {
					return fmt.Errorf("reports: %s: chart value column %q is not in the result (have: %s)",
						r.ID, r.ValueColumn, strings.Join(cols, ", "))
				}
			}
			if r.SeriesColumn != "" {
				if seriesIdx = indexOfFold(cols, r.SeriesColumn); seriesIdx < 0 {
					return fmt.Errorf("reports: %s: chart series column %q is not in the result (have: %s)",
						r.ID, r.SeriesColumn, strings.Join(cols, ", "))
				}
			}
			if r.StackColumn != "" {
				if stackIdx = indexOfFold(cols, r.StackColumn); stackIdx < 0 {
					return fmt.Errorf("reports: %s: chart stack column %q is not in the result (have: %s)",
						r.ID, r.StackColumn, strings.Join(cols, ", "))
				}
			}
			resolved = true
		}
		rows++

		label := strings.TrimSpace(fmt.Sprint(row[labelIdx]))
		// A person's name in this database carries their job title, so one
		// person who has held two roles arrives as two labels and gets two
		// bars. The report says when its label column is a person.
		if normalisePeople {
			label = NormalisePerson(label)
		}
		if label == "" || label == "<nil>" {
			label = "بدون مقدار" // a null category is still a category worth seeing
		}
		// Seen-ness is tracked separately from the totals map, and the entry is
		// created before the value is parsed.
		//
		// Keying "have I seen this label" off the totals map looked equivalent
		// and was not: a row whose value failed to parse left the label in the
		// order list with no entry in totals, so the next row carrying the
		// same label was treated as new and appended again. Against the live
		// data that turned 3,912 orders into 3,912 one-row "categories", each
		// worth zero -- a chart that was wrong rather than merely empty.
		if !seen[label] {
			seen[label] = true
			order = append(order, label)
			totals[label] = 0
		}
		// The contribution is worked out once and then applied to both the bar
		// total and its breakdown. Adding it in two places was the obvious
		// shape and would drift: every early return below -- a blank amount, an
		// unparsable one -- has to skip the slice as well as the total, or the
		// slices would add up to more than the bar they sit in.
		var add float64
		if agg == "count" {
			add = 1
		} else {
			raw := row[valueIdx]
			// A NULL or blank amount means "nothing recorded", which
			// contributes zero and is not a data problem worth reporting.
			if raw == nil || strings.TrimSpace(fmt.Sprint(raw)) == "" {
				return nil
			}
			n, cur, ok := asMoney(raw)
			if !ok {
				// A value that is neither blank nor a number is a data problem,
				// but not a reason to fail the whole chart; it contributes
				// nothing, and the category still exists and is still counted.
				unparsed++
				if len(samples) < 3 {
					samples = append(samples, fmt.Sprintf("%q", fmt.Sprint(raw)))
				}
				return nil
			}
			if cur != "" {
				currencies[cur] = true
			}
			add = n
		}
		totals[label] += add
		if breaking {
			k := partKey{stack: chartCell(row, stackIdx), series: chartCell(row, seriesIdx)}
			m := parts[k]
			if m == nil {
				m = map[string]float64{}
				parts[k] = m
				partOrder = append(partOrder, k)
				if _, ok := stackSeen[k.stack]; !ok {
					stackSeen[k.stack] = len(stackSeen)
				}
			}
			m[label] += add
			partTotal[k] += add
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if rows >= maxChartScan {
		capped = true
	}

	// Largest first, ties broken by first appearance so repeated loads of the
	// same data draw the same chart.
	pos := make(map[string]int, len(order))
	for i, l := range order {
		pos[l] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := totals[order[i]], totals[order[j]]
		if a != b {
			return a > b
		}
		return pos[order[i]] < pos[order[j]]
	})

	out := &ChartData{
		Rows: rows, Groups: len(order), Capped: capped,
		Unparsed: unparsed, UnparsedSamples: samples, TookMs: nowMs() - started,
	}
	// One currency means the axis can be labelled with it. More than one means
	// the total is the sum of different currencies, which is not a number that
	// means anything -- say so rather than print it as if it were.
	switch len(currencies) {
	case 1:
		for c := range currencies {
			out.Currency = c
		}
	default:
		if len(currencies) > 1 {
			out.MixedCurrency = true
			for c := range currencies {
				out.Currency += c
			}
		}
	}
	for i, l := range order {
		if i >= maxChartGroups {
			out.Other += totals[l]
			continue
		}
		out.Labels = append(out.Labels, l)
		out.Values = append(out.Values, totals[l])
	}

	// Slices are emitted stack by stack, in the order the stacks first
	// appeared, and within a stack largest first -- so the legend reads the
	// same way on every load rather than following map iteration order.
	if breaking && len(out.Labels) > 0 {
		sort.SliceStable(partOrder, func(i, j int) bool {
			a, b := partOrder[i], partOrder[j]
			if a.stack != b.stack {
				return stackSeen[a.stack] < stackSeen[b.stack]
			}
			return partTotal[a] > partTotal[b]
		})
		for _, k := range partOrder {
			s := ChartSeries{Name: k.series, Stack: k.stack, Values: make([]float64, len(out.Labels))}
			// Only the labels that survived the top-N cut: a slice carrying a
			// value for a label with no bar would be drawn against whichever
			// bar happened to share its index.
			for i, l := range out.Labels {
				s.Values[i] = parts[k][l]
			}
			out.Series = append(out.Series, s)
		}
	}
	return out, nil
}

// chartCell reads one column as a category name.
//
// A missing column, a NULL and an empty string all mean the same thing to a
// reader -- the fact was never recorded -- and all three have to become the
// same category, or one real group is drawn as three.
func chartCell(row []any, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	v := row[idx]
	if v == nil {
		return "نامشخص"
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return "نامشخص"
	}
	return s
}

// streamLimited runs the report and stops after limit rows.
//
// Separate from Stream because an export wants everything and a chart wants a
// bounded amount of work; sharing one path would mean either an unbounded
// chart or a truncated export.
func (d *DB) streamLimited(ctx context.Context, r Report, values map[string]string, limit int,
	fn func(cols []string, row []any) error) error {
	n := 0
	stop := fmt.Errorf("chart row limit reached")
	err := d.streamWithTimeout(ctx, r, values, d.timeout, func(cols []string, row []any) error {
		if n >= limit {
			return stop
		}
		n++
		return fn(cols, row)
	})
	if err == stop {
		return nil
	}
	return err
}

// indexOfFold finds a column by name, ignoring case.
//
// Case-insensitive because queries mix conventions freely -- the same column
// is referenced as OrderNo, ORDERNO and orderno across reports,
// and a chart should not break because of it.
func indexOfFold(cols []string, want string) int {
	for i, c := range cols {
		if strings.EqualFold(strings.TrimSpace(c), strings.TrimSpace(want)) {
			return i
		}
	}
	return -1
}

// asMoney is asFloat plus the currency the value was written in, for the
// summed charts where mixing currencies would be a real error.
func asMoney(v any) (float64, string, bool) {
	if s, ok := v.(string); ok {
		digits, cur := normaliseNumber(s)
		if digits == "" {
			return 0, "", false
		}
		f, err := strconv.ParseFloat(digits, 64)
		return f, cur, err == nil
	}
	if b, ok := v.([]byte); ok {
		return asMoney(string(b))
	}
	f, ok := asFloat(v)
	return f, "", ok
}

// asFloat converts a scanned value to a number.
//
// Everything arrives from this driver as a string, so the string case is the
// one that actually runs; the others are there so the function stays correct
// if the driver or a column type changes.
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case string:
		// A unit or currency makes it an amount, which only asMoney reads:
		// a plain number column with «۱۲ ریال» in it is text charted by
		// mistake, and is refused rather than summed.
		s, cur := normaliseNumber(t)
		if s == "" || cur != "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	case []byte:
		return asFloat(string(t))
	}
	return 0, false
}

// normaliseNumber turns a number as these views store it into something
// strconv understands.
//
// The money columns do not come back as plain ASCII digits. They carry
// thousands separators, and in a Persian application the digits themselves
// are frequently Persian (۰-۹) or Arabic-Indic (٠-٩) with their own separator
// characters. Handing those straight to ParseFloat fails silently, which is
// how a chart of money values comes back as a row of zeros.
func normaliseNumber(s string) (string, string) {
	var b strings.Builder
	var cur string
	s = strings.TrimSpace(s)

	// A currency's Persian name, as the accounting reports write it after an
	// amount -- «1,250,000,000.00 ریال» -- read as its code, so a sum still
	// refuses to add euros to rials. Without this every such amount failed to
	// parse and the proformas' chart drew nothing but zeros.
	for word, code := range persianUnits {
		if rest, ok := strings.CutSuffix(s, word); ok && strings.TrimSpace(rest) != "" {
			cur, s = code, strings.TrimSpace(rest)
			break
		}
		if rest, ok := strings.CutPrefix(s, word); ok && strings.TrimSpace(rest) != "" {
			cur, s = code, strings.TrimSpace(rest)
			break
		}
	}
	// A three-letter code is as common as a symbol in these views -- the same
	// column holds both "€ 30,744.00" and "360,000,000 IRR" -- and the letters
	// would otherwise be rejected as not-a-number.
	for _, code := range currencyCodes {
		if cur != "" {
			break
		}
		if len(s) > len(code) && strings.EqualFold(s[len(s)-len(code):], code) {
			cur, s = strings.ToUpper(code), strings.TrimSpace(s[:len(s)-len(code)])
			break
		}
		if len(s) > len(code) && strings.EqualFold(s[:len(code)], code) {
			cur, s = strings.ToUpper(code), strings.TrimSpace(s[len(code):])
			break
		}
	}
	b.Grow(len(s))
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹': // Persian ۰-۹
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩': // Arabic-Indic ٠-٩
			b.WriteRune('0' + (r - '٠'))
		case r == '.' || r == '٫': // decimal point, Arabic decimal separator
			b.WriteRune('.')
		case r == '-' || r == '+':
			b.WriteRune(r)
		case r == ',' || r == '٬' || r == ' ' || r == ' ' || r == '\'':
			// Thousands separators in their various spellings: dropped.
		case strings.ContainsRune(currencySigns, r):
			// These columns arrive pre-formatted for display, so a money value
			// reads "€ 30,744.00". The sign is recorded rather than merely
			// skipped, because adding euros to rials would be silently wrong
			// and somebody has to be able to notice.
			cur = string(r)
		default:
			// Anything else -- a letter, a currency word, punctuation that is
			// not a separator -- means this is not a number, and guessing at
			// it would invent data.
			return "", ""
		}
	}
	return b.String(), cur
}

// currencySigns are the symbols these views prefix an amount with.
const currencySigns = "€$£¥₽₺﷼"

// currencyCodes are the written codes seen in the same columns.
var currencyCodes = []string{"IRR", "IRT", "EUR", "USD", "GBP", "AED", "TRY", "CNY"}

// persianUnits are the Persian names written after an amount, and their
// codes. Both spellings of ریال: the finance database stores Arabic ي.
var persianUnits = map[string]string{
	"ریال": "IRR", "ريال": "IRR", "تومان": "IRT", "یورو": "EUR", "دلار": "USD",
	"درهم": "AED", "یوان": "CNY", "پوند": "GBP", "لیر": "TRY",
}
