package reports

import (
	"errors"
	"fmt"
	"strings"
)

// A chart on a report somebody wrote themselves.
//
// Held as a choice of columns, not as data. The server stores "draw the
// supplier column against the weight column as bars" and nothing more; the
// page draws it from the rows it already has. That is deliberate and it is the
// whole design:
//
//   - The chart and the table cannot disagree. They are the same rows. A
//     server-side aggregate would be a second query, and a second query is a
//     second answer -- run a moment later, against a database somebody else is
//     writing to.
//   - No query is composed on the server from column names that arrived from a
//     browser. Identifiers cannot be bound as parameters, so a "GROUP BY the
//     column they picked" feature would mean building SQL out of user input in
//     the one package that exists to avoid exactly that.
//
// The cost is that a chart covers the rows on screen rather than the whole
// result. That is stated in the page rather than hidden, and the answer for
// somebody who wants the whole thing is the one they already know: put a
// GROUP BY in the query. A person whose only skill here is SQL is well served
// by being told to use it.

// ChartSpec is how a saved report wants to be drawn.
//
// A zero value means no chart, which is the normal case -- most queries are
// tables. So this is a pointer on CustomReport rather than a struct with an
// "enabled" flag: there is no such thing as a chart with no kind.
type ChartSpec struct {
	Kind string `json:"kind"`
	// Label names the column whose values name each bar, slice or point.
	Label string `json:"label"`
	// Values name the columns plotted. More than one is a grouped or stacked
	// comparison; the round kinds take only the first and say so.
	//
	// Empty is legal for Agg "count", which counts rows per label and so has
	// nothing to read a number out of.
	Values []string `json:"values"`
	// Agg folds the rows that share a label.
	//
	// "sum", "count" and "" mean here what they mean in the shipped
	// catalogue's chart directives -- one vocabulary, so somebody who has read
	// a .sql file is not learning a second set of words. "avg" and "none" are
	// additions the authored charts need and the catalogue never wanted.
	//
	// Folding happens in the browser, over rows it already has. That is
	// arithmetic, not SQL: it composes no query and touches no identifier, so
	// it stays on the safe side of the line this package exists to draw.
	Agg string `json:"agg,omitempty"`
	// Bucket groups a date label before the rows are folded: "month" or
	// "year", or empty for one point per distinct value.
	//
	// This is what the shipped reports' "-- monthly:" directive does, and it
	// exists because the date columns in this database are TEXT holding a
	// Jalali date ("1404/03/12"). A line chart over the raw column draws one
	// point per day -- twenty-eight points that happen to be in three months,
	// which answers no question anybody asked. Truncating to "1404/03" is the
	// whole trick, and it needs no calendar conversion at all: the Jalali
	// month is the first seven characters, which is why the shipped reports
	// group these columns as strings too.
	Bucket string `json:"bucket,omitempty"`
	// Compare turns a date-bucketed chart into a period-over-period one.
	//
	// "year" puts the month on the axis and each YEAR on its own line, so
	// 1404 and 1405 sit side by side. "month" does the same with the day of
	// the month across months. Empty is one continuous series.
	//
	// This is the question management actually asks -- "how does this month
	// compare with last" -- and no report here could answer it. It is done by
	// regrouping the rows already fetched rather than by running the query
	// twice: two runs are two answers, taken a moment apart against a
	// database somebody else is writing to, and the totals would not add up.
	// The cost is that the query has to cover both periods, which the page
	// says when it cannot find two.
	Compare string `json:"compare,omitempty"`
	// Stacked applies to the bar kinds: totals rather than side-by-side.
	Stacked bool `json:"stacked,omitempty"`
	// LogScale puts the value axis on a logarithmic scale.
	//
	// Needed by this data rather than offered for completeness. The live
	// WEIGHT column runs from 10.45 to 52,689 in twenty-eight rows: on a
	// linear axis the small consignments are a flat line along the bottom and
	// the chart says only "one of these is big", which is the one thing
	// anybody already knew.
	LogScale bool `json:"log_scale,omitempty"`
	// Bins is how many buckets a distribution chart divides its column into.
	// Ignored by every other kind.
	Bins int `json:"bins,omitempty"`
	// Title is shown above the chart. Optional -- the report's own title is
	// right there, and repeating it adds nothing.
	Title string `json:"title,omitempty"`
}

// ChartKind describes one chart type to the page that offers it.
//
// The flags are capabilities rather than decoration: the page reads them to
// decide which controls to show, and CleanChart reads the same ones to decide
// what to refuse. One list, so a control that appears is a control that saves.
type ChartKind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
	// Single marks a kind that plots one column only, so the page can say so
	// instead of silently ignoring the others.
	Single bool `json:"single,omitempty"`
	// NumericLabel marks a kind whose label column is a NUMBER, not a
	// category -- the horizontal position of a point rather than the name of
	// a bar.
	NumericLabel bool `json:"numeric_label,omitempty"`
	// NoAgg marks a kind that plots rows as they are, so folding them by
	// label would destroy the thing being shown. A scatter of one point per
	// shipment is the point of a scatter.
	NoAgg bool `json:"no_agg,omitempty"`
	// Bins marks a kind that divides a column into buckets and counts them,
	// so it needs a bin count and no label column at all.
	Bins bool `json:"bins,omitempty"`
	// Stackable marks a kind where stacking means something.
	Stackable bool `json:"stackable,omitempty"`
	// MinValues is how many value columns the kind needs to say anything. Two
	// for a scatter (x comes from the label) is one; for a mixed chart it is
	// two, because a bar series with no line is just a bar chart.
	MinValues int `json:"min_values,omitempty"`
	// NeedsBucket is the date grouping a comparison mode requires, and is set
	// only on the comparison modes.
	//
	// A comparison works by cutting a grouped label in two, so it can only
	// compare what the grouping left in the label: comparing YEARS needs the
	// month grouping, because "1405/03" still says which month it is, and
	// comparing MONTHS needs the day grouping, because "1405/03" no longer
	// says which day. Pairing them the other way round produces a control
	// that is offered, saved, and can never draw.
	NeedsBucket string `json:"needs_bucket,omitempty"`
}

// chartKinds are the chart types an authored report may ask for, in the order
// the page offers them.
//
// Bar first: it is the answer for "how much per category", which is what
// nearly every report here is. The round kinds are last and carry a warning in
// their hint, because a pie of thirty suppliers is a legend, not a chart.
var chartKinds = []ChartKind{
	// --- how much per category. The answer for most reports here.
	{ID: "bar", Label: "میله‌ای", Stackable: true,
		Hint: "مقدار هر دسته. برای «چقدر به ازای هر …» همین درست است."},
	{ID: "hbar", Label: "میله‌ای افقی", Stackable: true,
		Hint: "مثل میله‌ای، اما نام دسته‌ها در سمت راست می‌نشیند؛ برای نام‌های بلند فارسی خواناتر است."},
	{ID: "sbar", Label: "انباشتهٔ درصدی", MinValues: 2,
		Hint: "سهم هر ستون از کل هر دسته، روی صد درصد. برای «ترکیب هر ماه چه بود»، " +
			"اندازهٔ کل را پنهان می‌کند و فقط نسبت را نشان می‌دهد."},

	// --- change over time.
	{ID: "line", Label: "خطی",
		Hint: "روند در طول زمان. ردیف‌ها باید به ترتیب تاریخ مرتب باشند، یعنی ORDER BY در پرس‌وجو."},
	{ID: "area", Label: "خطی پُر",
		Hint: "مثل خطی، با زیر خط پُر شده."},
	{ID: "mixed", Label: "میله‌ای + خطی", MinValues: 2,
		Hint: "ستون اول میله و بقیه خط. برای دو چیز با واحدهای مختلف، مثل تعداد مرسوله " +
			"در برابر میانگین وزن."},

	// --- share of a whole. Last, and each hint says when not to.
	{ID: "pie", Label: "دایره‌ای", Single: true,
		Hint: "سهم هر دسته از کل. فقط یک ستون مقدار، و تا حدود هشت دسته؛ بیشتر از آن خوانا نیست."},
	{ID: "doughnut", Label: "حلقه‌ای", Single: true,
		Hint: "مثل دایره‌ای، با میان خالی. فقط یک ستون مقدار."},
	{ID: "polar", Label: "گل‌سرخی", Single: true,
		Hint: "مثل دایره‌ای، اما همهٔ قطاع‌ها هم‌زاویه‌اند و مقدار را با شعاع نشان می‌دهند؛ " +
			"اختلاف‌های کوچک را بهتر از دایره‌ای نشان می‌دهد."},
	{ID: "radar", Label: "راداری",
		Hint: "چند سنجه برای چند دسته، روی یک تور. برای مقایسهٔ سه تا شش دسته در " +
			"سه تا هشت سنجه؛ بیشتر از آن به هم می‌ریزد."},

	// --- the shape of the data itself, rather than a total per category.
	{ID: "scatter", Label: "پراکندگی", NumericLabel: true, NoAgg: true, Single: true,
		Hint: "هر ردیف یک نقطه: «ستون دسته‌ها» محور افقی است و باید عدد باشد. " +
			"برای دیدن رابطهٔ دو عدد، مثل وزن در برابر هزینه."},
	{ID: "hist", Label: "توزیع (هیستوگرام)", Bins: true, NoAgg: true, Single: true,
		Hint: "یک ستون عددی را به بازه‌های مساوی تقسیم می‌کند و تعداد هر بازه را می‌شمارد. " +
			"برای «بیشتر مرسوله‌ها چه وزنی دارند»؛ به ستون دسته‌ها نیازی نیست."},
}

// ChartKinds is the list of chart types, for the page that offers them.
func ChartKinds() []ChartKind {
	out := make([]ChartKind, len(chartKinds))
	copy(out, chartKinds)
	return out
}

// chartAggs are the ways rows sharing a label may be folded.
//
// Sum is the default rather than "none", because the shape of nearly every
// query is one row per record and the question asked of it is per category:
// a chart of 28 unfolded rows against 8 suppliers draws the same supplier
// several times and reads as noise. Where a query already has its own GROUP BY,
// summing one row per label is that same row -- so the default is right for
// both shapes.
var chartAggs = []ChartKind{
	{ID: "sum", Label: "جمع", Hint: "مقدارهای هر دسته با هم جمع می‌شوند."},
	{ID: "avg", Label: "میانگین", Hint: "میانگین مقدارهای هر دسته."},
	{ID: "count", Label: "تعداد ردیف", Single: true,
		Hint: "تعداد ردیف‌های هر دسته شمرده می‌شود؛ به ستون مقدار نیازی نیست."},
	{ID: "none", Label: "بدون جمع‌بندی",
		Hint: "هر ردیف یک نقطه. اگر پرس‌وجو خودش GROUP BY دارد، همین را بخواهید."},
}

// ChartAggs is the list of aggregations, for the page that offers them.
func ChartAggs() []ChartKind {
	out := make([]ChartKind, len(chartAggs))
	copy(out, chartAggs)
	return out
}

// chartBuckets are the ways a date label may be grouped.
var chartBuckets = []ChartKind{
	{ID: "", Label: "بدون گروه‌بندی", Hint: "هر مقدار یک نقطه."},
	{ID: "day", Label: "روز",
		Hint: "یک نقطه برای هر روز؛ تاریخ‌های «۱۴۰۴/۳/۵» و «۱۴۰۴/۰۳/۰۵» یکی شمرده می‌شوند."},
	{ID: "month", Label: "ماه",
		Hint: "تاریخ‌های «۱۴۰۴/۰۳/۱۲» به «۱۴۰۴/۰۳» تبدیل می‌شوند: روند ماه‌به‌ماه."},
	{ID: "year", Label: "سال",
		Hint: "فقط سال؛ برای داده‌های چندساله."},
}

// ChartBuckets is the list of date groupings, for the page that offers them.
func ChartBuckets() []ChartKind {
	out := make([]ChartKind, len(chartBuckets))
	copy(out, chartBuckets)
	return out
}

// chartCompares are the period-over-period modes.
//
// Only two, and each needs the grouping that leaves its axis in the label.
// Comparing years puts the MONTH on the axis, which the month grouping keeps;
// comparing months puts the DAY on it, which the month grouping has already
// thrown away, so it needs the day grouping instead. Pairing them the other
// way is not a matter of taste: "1405/03" has no day in it, and the mode would
// be offered, saved, and never able to draw.
var chartCompares = []ChartKind{
	{ID: "", Label: "بدون مقایسه", Hint: "یک خط پیوسته در طول زمان."},
	{ID: "year", Label: "سال با سال", NeedsBucket: "month",
		Hint: "ماه روی محور و هر سال یک خط: «امسال در برابر پارسال». " +
			"با گروه‌بندی «ماه» کار می‌کند."},
	{ID: "month", Label: "ماه با ماه", NeedsBucket: "day",
		Hint: "روز روی محور و هر ماه یک خط: «این ماه در برابر ماه قبل». " +
			"با گروه‌بندی «روز» کار می‌کند، چون مقایسهٔ ماه‌ها به روز نیاز دارد."},
}

// ChartCompares is the list of comparison modes, for the page that offers them.
func ChartCompares() []ChartKind {
	out := make([]ChartKind, len(chartCompares))
	copy(out, chartCompares)
	return out
}

func chartAgg(id string) (ChartKind, bool) {
	for _, a := range chartAggs {
		if a.ID == id {
			return a, true
		}
	}
	return ChartKind{}, false
}

func chartKind(id string) (ChartKind, bool) {
	for _, k := range chartKinds {
		if k.ID == id {
			return k, true
		}
	}
	return ChartKind{}, false
}

func chartBucket(id string) (ChartKind, bool) {
	for _, b := range chartBuckets {
		if b.ID == id {
			return b, true
		}
	}
	return ChartKind{}, false
}

func chartCompare(id string) (ChartKind, bool) {
	for _, c := range chartCompares {
		if c.ID == id {
			return c, true
		}
	}
	return ChartKind{}, false
}

// CleanChart normalises a chart request and rejects one that cannot be drawn.
//
// Column names are NOT checked against the query here, and cannot be: knowing
// what columns a query returns means running it, and a report must be savable
// while its database is unreachable. The page checks them against the result it
// has in front of it and says which column is missing -- which is both the
// earlier warning and the more useful one, because it can offer the list of
// columns that do exist.
func CleanChart(c *ChartSpec) (*ChartSpec, error) {
	if c == nil {
		return nil, nil
	}
	out := ChartSpec{
		Kind:     strings.TrimSpace(c.Kind),
		Label:    strings.TrimSpace(c.Label),
		Agg:      strings.ToLower(strings.TrimSpace(c.Agg)),
		Bucket:   strings.ToLower(strings.TrimSpace(c.Bucket)),
		Compare:  strings.ToLower(strings.TrimSpace(c.Compare)),
		Stacked:  c.Stacked,
		LogScale: c.LogScale,
		Bins:     c.Bins,
		Title:    strings.TrimSpace(c.Title),
	}
	// No kind is how a report says it has no chart. Returning nil rather than
	// an empty struct keeps "has a chart" a single question everywhere else.
	if out.Kind == "" {
		return nil, nil
	}
	kind, ok := chartKind(out.Kind)
	if !ok {
		return nil, fmt.Errorf("%q is not a chart type this report can draw", out.Kind)
	}

	seen := map[string]bool{}
	for _, v := range c.Values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out.Values = append(out.Values, v)
	}

	// --- the kinds that describe one column rather than a total per category.
	//
	// A distribution divides ONE column into buckets and counts them. It has
	// no label column, nothing to aggregate and nothing to stack, so those are
	// cleared rather than validated: a control the page does not show must not
	// be able to arrive set and change what is drawn.
	if kind.Bins {
		if len(out.Values) != 1 {
			return nil, fmt.Errorf("a %s chart divides ONE column into ranges; %d were chosen",
				kind.Label, len(out.Values))
		}
		out.Label, out.Agg, out.Bucket, out.Stacked = "", "", "", false
		out.Compare = ""
		out.Bins = clampBins(out.Bins)
		return &out, nil
	}

	if out.Label == "" {
		if kind.NumericLabel {
			return nil, errors.New("choose which column is the horizontal axis; " +
				"it has to be a number")
		}
		return nil, errors.New("choose which column names each bar or slice")
	}

	// A scatter plots rows as they are -- one point per row IS the chart -- so
	// folding them by label would collapse the thing being shown into a single
	// point per x value.
	if kind.NoAgg {
		out.Agg = "none"
		out.Bucket = ""
		out.Compare = ""
		out.Stacked = false
	}

	if out.Agg == "" {
		out.Agg = "sum"
	}
	if _, ok := chartAgg(out.Agg); !ok {
		return nil, fmt.Errorf("%q is not a way of combining rows this report can do", out.Agg)
	}
	// Checked against the list the page is offered rather than a list written
	// out here: two lists of the same groupings drift, and this one had
	// already fallen behind by one when the day grouping was added.
	if _, ok := chartBucket(out.Bucket); !ok {
		return nil, fmt.Errorf("%q is not a way of grouping dates this report can do", out.Bucket)
	}

	// A comparison needs the date grouping that leaves its axis in the label.
	//
	// Each mode declares which one (NeedsBucket) rather than sharing a single
	// rule, because they do not need the same grouping: comparing years splits
	// "1405/03" into the year and the month, while comparing months needs the
	// day -- which the month grouping has already truncated away. A mode
	// checked against the wrong grouping is one that saves and never draws.
	if out.Compare != "" {
		mode, ok := chartCompare(out.Compare)
		if !ok {
			return nil, fmt.Errorf("%q is not a comparison this report can draw", out.Compare)
		}
		if out.Bucket != mode.NeedsBucket {
			grouping, _ := chartBucket(mode.NeedsBucket)
			return nil, fmt.Errorf("%q needs the date grouping set to %q, because that is "+
				"the grouping that leaves what it puts on the axis in the label",
				mode.Label, grouping.Label)
		}
	}
	if !kind.Stackable {
		// Stacking a pie or a radar means nothing, and a flag that is stored
		// but ignored is one somebody will later believe.
		out.Stacked = false
	}
	out.Bins = 0

	// Counting rows per label reads no numbers, so it needs no value column --
	// and must not be given one, because a count of a column is a count of
	// rows and offering both invites the reader to think otherwise. This
	// mirrors the shipped catalogue, where agg=count is the one directive that
	// does not require a value column.
	if out.Agg == "count" {
		if kind.MinValues > 1 {
			return nil, fmt.Errorf("a %s chart needs %d columns to compare, so it cannot "+
				"just count rows", kind.Label, kind.MinValues)
		}
		out.Values = nil
		return &out, nil
	}

	if len(out.Values) == 0 {
		// Naming the one option that needs no value column, rather than the
		// one currently chosen -- an error is more use when it says what to do.
		counting, _ := chartAgg("count")
		return nil, fmt.Errorf("choose at least one column to plot, or set the combination to %q, "+
			"which needs none", counting.Label)
	}
	if seen[out.Label] {
		// Plotting a column against itself draws a straight diagonal and
		// teaches nobody anything; far more likely it is a mis-click.
		return nil, fmt.Errorf("%q is both the label and a plotted value; pick a different column to plot",
			out.Label)
	}
	if kind.Single && len(out.Values) > 1 {
		// Truncating silently would draw a chart of the first column under a
		// legend naming three, which is a chart that lies about itself.
		return nil, fmt.Errorf("a %s chart plots one column; %d were chosen",
			kind.Label, len(out.Values))
	}
	if kind.MinValues > 1 && len(out.Values) < kind.MinValues {
		return nil, fmt.Errorf("a %s chart needs at least %d columns to compare; %d was chosen",
			kind.Label, kind.MinValues, len(out.Values))
	}
	return &out, nil
}

// binsDefault and the bounds around it.
//
// Twelve because the queries here return tens of rows, not thousands: more
// buckets than that over twenty-eight shipments gives mostly empty ones and a
// chart of gaps. The floor and ceiling exist because the number arrives from a
// form -- one bucket is not a distribution and two hundred over a small result
// is a comb.
const (
	binsDefault = 12
	binsMin     = 3
	binsMax     = 60
)

func clampBins(n int) int {
	switch {
	case n == 0:
		return binsDefault
	case n < binsMin:
		return binsMin
	case n > binsMax:
		return binsMax
	}
	return n
}
