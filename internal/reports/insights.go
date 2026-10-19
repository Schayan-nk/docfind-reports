package reports

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Automatic analysis of a report's chart data.
//
// Computed here rather than sent to an external model. Two reasons, in the
// order they matter: a report's rows are often live commercial data --
// customers, order values, who is behind on what -- and they should not leave
// the building to be analysed; and the questions worth asking of a
// single-series aggregate are known in advance, so a language model would be
// an expensive way to compute a share and a median.
//
// What this cannot do is explain *why* a number is what it is. It points at
// the row worth looking into. The judgement stays with the reader, which is
// the honest division of labour.

// Insight is one observation about a report's data.
type Insight struct {
	// Kind drives the icon and colour: "concentration", "outlier", "spread",
	// "gap", "scale", "quality".
	Kind string `json:"kind"`
	// Level is how much attention it deserves: "info", "watch", "act".
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Analyse turns aggregated chart data into a short list of observations.
//
// Deliberately short. Ten observations is a wall of text nobody reads; the
// aim is the two or three things a person would have noticed if they had time
// to study the chart, surfaced in the time they actually have.
func Analyse(cd *ChartData, category, value string) []Insight {
	if cd == nil || len(cd.Values) == 0 {
		return nil
	}
	var out []Insight

	total := 0.0
	for _, v := range cd.Values {
		total += v
	}
	total += cd.Other
	if total <= 0 {
		return nil
	}

	// Values arrive sorted largest first from Aggregate, but this must not
	// depend on that.
	pairs := make([]struct {
		Label string
		Value float64
	}, len(cd.Values))
	for i := range cd.Values {
		pairs[i].Label, pairs[i].Value = cd.Labels[i], cd.Values[i]
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].Value > pairs[j].Value })

	top := pairs[0]
	share := top.Value / total * 100

	// --- concentration -----------------------------------------------
	//
	// The single most useful thing to know about a ranked distribution is
	// whether it is a distribution at all, or one entry and a tail.
	switch {
	case share >= 50 && cd.Groups > 2:
		out = append(out, Insight{"concentration", "act", fmt.Sprintf(
			"«%s» به تنهایی %s٪ از کل را در بر دارد. تمرکز در یک %s معمولاً یعنی گلوگاه یا وابستگی؛ ارزش بررسی دارد.",
			top.Label, fa(share, 0), category)})
	case share >= 30 && cd.Groups > 3:
		out = append(out, Insight{"concentration", "watch", fmt.Sprintf(
			"«%s» با %s٪ بیشترین سهم را دارد، حدود %s برابر میانگین بقیه.",
			top.Label, fa(share, 0), fa(top.Value/mean(pairs[1:]), 1))})
	}

	// --- how much of the whole sits in the first few ------------------
	//
	// Measured against what an even spread would give, not against a fixed
	// percentage. "The top three hold 60%" is remarkable across twenty
	// categories and completely unremarkable across five, where three
	// categories *are* 60% of them -- and saying it anyway teaches people
	// that this panel states the obvious.
	if cd.Groups >= 5 {
		head := 0.0
		for i := 0; i < 3 && i < len(pairs); i++ {
			head += pairs[i].Value
		}
		p := head / total * 100
		evenShare := 3.0 / float64(cd.Groups) * 100
		if p >= 60 && p >= 2*evenShare {
			out = append(out, Insight{"concentration", "watch", fmt.Sprintf(
				"سه مورد نخست %s٪ از کل را تشکیل می‌دهند، در حالی که %s %s وجود دارد؛ بیش از دو برابر سهم یک توزیع یکنواخت.",
				fa(p, 0), fa(float64(cd.Groups), 0), category)})
		}
	}

	// --- outliers, by median absolute deviation ----------------------
	//
	// Not the standard deviation: one very large value inflates it enough to
	// hide itself, which is exactly the case worth catching here.
	if len(pairs) >= 5 {
		if o := outliers(pairs); len(o) > 0 {
			names := make([]string, 0, 3)
			for i, p := range o {
				if i == 3 {
					break
				}
				names = append(names, "«"+p.Label+"»")
			}
			more := ""
			if len(o) > 3 {
				more = fmt.Sprintf(" و %s مورد دیگر", fa(float64(len(o)-3), 0))
			}
			out = append(out, Insight{"outlier", "watch", fmt.Sprintf(
				"%s%s به وضوح از بقیه فاصله دارند و بررسی جداگانه می‌خواهند.",
				strings.Join(names, "، "), more)})
		}
	}

	// --- the quiet end ------------------------------------------------
	if cd.Groups >= 6 {
		low := 0
		med := median(pairs)
		for _, p := range pairs {
			if p.Value < med*0.25 {
				low++
			}
		}
		if low >= 2 && float64(low)/float64(len(pairs)) >= 0.3 {
			out = append(out, Insight{"spread", "info", fmt.Sprintf(
				"%s مورد کمتر از یک‌چهارم میانه هستند؛ یا واقعاً کم‌کار هستند یا داده‌شان ناقص ثبت شده.",
				fa(float64(low), 0))})
		}
	}

	// --- what the chart is not showing --------------------------------
	if cd.Groups > len(cd.Labels) {
		out = append(out, Insight{"scale", "info", fmt.Sprintf(
			"نمودار %s دستهٔ نخست از %s دسته را نشان می‌دهد؛ %s باقی‌مانده روی هم %s هستند.",
			fa(float64(len(cd.Labels)), 0), fa(float64(cd.Groups), 0),
			fa(float64(cd.Groups-len(cd.Labels)), 0), fmtNum(cd.Other))})
	}
	if cd.Capped {
		out = append(out, Insight{"quality", "act",
			"این تحلیل بر پایهٔ نمونه‌ای محدود از داده‌هاست، نه کل نتیجه. فیلترها را محدودتر کنید."})
	}
	if cd.Unparsed > 0 {
		p := float64(cd.Unparsed) / float64(max(cd.Rows, 1)) * 100
		lvl := "info"
		if p >= 10 {
			lvl = "act"
		}
		out = append(out, Insight{"quality", lvl, fmt.Sprintf(
			"%s ردیف (%s٪) مقدار عددی معتبری نداشتند و در محاسبه نیامده‌اند.",
			fa(float64(cd.Unparsed), 0), fa(p, 0))})
	}

	// --- the plain summary, always last -------------------------------
	out = append(out, Insight{"info", "info", fmt.Sprintf(
		"%s در %s دسته، مجموع %s؛ میانه %s و میانگین %s.",
		value, fa(float64(cd.Groups), 0), fmtNum(total),
		fmtNum(median(pairs)), fmtNum(total/float64(cd.Groups)))})

	// Ordered before it is cut, and cut from the bottom.
	//
	// The naive version appended data-quality warnings last and truncated the
	// tail, so a report built from a capped scan or from rows that failed to
	// parse could report confident observations while silently dropping the
	// warning that they were computed from incomplete data. Quality comes
	// first, then anything needing action, then the rest.
	rank := func(i Insight) int {
		switch {
		case i.Kind == "quality":
			return 0
		case i.Level == "act":
			return 1
		case i.Level == "watch":
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return rank(out[a]) < rank(out[b]) })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

type pair = struct {
	Label string
	Value float64
}

func mean(p []pair) float64 {
	if len(p) == 0 {
		return 1
	}
	t := 0.0
	for _, x := range p {
		t += x.Value
	}
	if t == 0 {
		return 1
	}
	return t / float64(len(p))
}

func median(p []pair) float64 {
	if len(p) == 0 {
		return 0
	}
	v := make([]float64, len(p))
	for i, x := range p {
		v[i] = x.Value
	}
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// outliers finds values far from the median, measured in median absolute
// deviations. The 3.5 threshold is the usual convention for this test.
func outliers(p []pair) []pair {
	med := median(p)
	dev := make([]float64, len(p))
	for i, x := range p {
		dev[i] = math.Abs(x.Value - med)
	}
	sort.Float64s(dev)
	mad := dev[len(dev)/2]
	if mad == 0 {
		return nil // too uniform for this test to say anything
	}
	var out []pair
	for _, x := range p {
		if 0.6745*math.Abs(x.Value-med)/mad > 3.5 {
			out = append(out, x)
		}
	}
	return out
}

// fa renders a number with Persian digits, to the given number of decimals.
//
// Trailing zeros are only stripped after a decimal point. Trimming them
// unconditionally turned 40 into 4 and 100 into 1 -- every round number in
// every insight was silently wrong, which is worse than no analysis at all.
func fa(v float64, dp int) string {
	s := fmt.Sprintf("%.*f", dp, v)
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return toPersianDigits(s)
}

// fmtNum renders a total with thousands separators and Persian digits.
func fmtNum(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune('٬')
		}
		b.WriteRune(r)
	}
	out := toPersianDigits(b.String())
	if neg {
		return "-" + out
	}
	return out
}

func toPersianDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune('۰' + (r - '0'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
