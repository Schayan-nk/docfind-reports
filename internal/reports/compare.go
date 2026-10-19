package reports

import (
	"fmt"
	"sort"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/format"
)

// The period comparison panel drawn above every report.
//
// Three questions, which are the three every manager asks of a number: how
// does this month compare with last month, with the same month a year ago, and
// how does the year as a whole compare with the last one.
//
// All three are answered from the buckets MonthlySeries already built during
// the single scan that draws the bars. Nothing here runs a query, which is the
// property that matters: the comparison describes exactly the rows the reader
// filtered to, and cannot drift from the table underneath it the way a
// separately-fetched figure would.

// thinRows is the point below which a percentage stops meaning anything.
//
// On the live data the warehouse report returns fifteen rows in total. A month
// holding two of them against a month holding one is a 100% rise, and printing
// that beside "orders up 4%" invites the two to be read as comparable facts.
const thinRows = 20

// Delta is one comparison: what the figure is now, what it was then, and by how
// much it moved.
type Delta struct {
	Has  bool    `json:"has"`
	Now  float64 `json:"now"`
	Then float64 `json:"then"`
	// Label names the period Then covers, in Persian, so the figure can never
	// be read against the wrong month.
	Label string  `json:"label"`
	Pct   float64 `json:"pct"`
	// Thin says the two figures together rest on too few rows for the
	// percentage to carry meaning. The figures are still shown; the percentage
	// is what gets held back.
	Thin bool `json:"thin"`
}

// set fills a Delta, leaving Has false when the earlier figure is zero.
//
// A rise from nothing is not a percentage. Zero to five is not "up 500%" and
// not "up infinity"; it is "five, where there were none", and the panel says
// that in words rather than printing a number that cannot be divided.
func (c *Delta) set(now, then float64, label string) {
	c.Now, c.Then, c.Label = now, then, label
	c.Thin = now+then < thinRows
	if then <= 0 {
		return
	}
	c.Pct = (now - then) / then * 100
	c.Has = true
}

// PeriodCompare is the whole panel.
type PeriodCompare struct {
	// Available is false when this report cannot carry a comparison at all.
	// Reason then says why, in Persian, and nothing else is filled in.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`

	// The month everything is anchored on: today's, not the last one with
	// rows in it. A report with nothing recorded this month should say so
	// rather than quietly comparing two older months and labelling the result
	// "this month".
	Month      string `json:"month"`
	MonthLabel string `json:"month_label"`
	// Day is how far into that month today is, and is the cut applied to all
	// three months so none of them is measured over a longer span than the
	// others.
	Day     int  `json:"day"`
	Partial bool `json:"partial"`

	MoM Delta `json:"mom"`
	YoY Delta `json:"yoy"`

	// The year-against-year chart: twelve Jalali months, two series.
	Months        []string  `json:"months"`
	ThisYear      []float64 `json:"this_year"`
	LastYear      []float64 `json:"last_year"`
	ThisYearLabel string    `json:"this_year_label"`
	LastYearLabel string    `json:"last_year_label"`
	Year          Delta     `json:"year"`

	// Caveats are the things that make a figure above mean less than it looks
	// like it means. Shown under the panel, in the reader's language.
	Caveats []string `json:"caveats,omitempty"`
}

// jalaliKey is the "1405/06" bucket a year and month make.
//
// Zero-padded and most-significant-first, which is what lets these keys be
// sorted as plain strings and compared with < and >.
func jalaliKey(y, m int) string { return fmt.Sprintf("%04d/%02d", y, m) }

// nowJalali is today as a Jalali year, month and day.
//
// Separated from comparePeriods so the arithmetic can be tested against a date
// of the caller's choosing. A comparison that can only be exercised on the day
// the test happens to run is a comparison nobody can check.
func nowJalali() (jy, jm, jd int) {
	t := time.Now()
	return format.ToJalali(t.Year(), int(t.Month()), t.Day())
}

// comparePeriods derives the panel from month buckets and their per-day tally.
//
// counts is every month the scan saw, uncapped. byDay[month][day] is the same
// tally split by day of month, and day zero holds the rows whose date named a
// month but no readable day.
func comparePeriods(counts map[string]float64, byDay map[string]map[int]float64, jy, jm, jd int) *PeriodCompare {
	if len(counts) == 0 {
		return &PeriodCompare{
			Reason: "با این فیلترها هیچ ردیفی برای مقایسه وجود ندارد.",
		}
	}

	thisMonth := jalaliKey(jy, jm)

	// toDate counts a month only up to the same day of the month as today, so
	// a month in progress is never set against a finished one. Day zero is
	// excluded deliberately: a row whose date gave no day cannot be placed on
	// either side of the cut, and guessing would put it on the side that
	// happens to flatter the current month.
	toDate := func(k string) float64 {
		var n float64
		for day, c := range byDay[k] {
			if day >= 1 && day <= jd {
				n += c
			}
		}
		return n
	}

	prevY, prevM := jy, jm-1
	if prevM == 0 {
		prevY, prevM = jy-1, 12
	}

	out := &PeriodCompare{
		Available:  true,
		Month:      thisMonth,
		MonthLabel: jalaliMonthLabel(thisMonth),
		Day:        jd,
		// Every Jalali month has at least 29 days, so a cut before the 29th is
		// always mid-month.
		Partial: jd < 29,
	}
	out.MoM.set(toDate(thisMonth), toDate(jalaliKey(prevY, prevM)), jalaliMonthLabel(jalaliKey(prevY, prevM)))
	out.YoY.set(toDate(thisMonth), toDate(jalaliKey(jy-1, jm)), jalaliMonthLabel(jalaliKey(jy-1, jm)))

	// The two years, twelve buckets each. Both series are full length even
	// where the data is not, because a chart with a short series and a long
	// one cannot be read: the months would not line up under each other.
	out.ThisYearLabel = toPersianDigits(fmt.Sprint(jy))
	out.LastYearLabel = toPersianDigits(fmt.Sprint(jy - 1))
	var thisTotal, lastTotal float64
	for m := 1; m <= 12; m++ {
		out.Months = append(out.Months, jalaliMonths[m])
		t, l := counts[jalaliKey(jy, m)], counts[jalaliKey(jy-1, m)]
		out.ThisYear = append(out.ThisYear, t)
		out.LastYear = append(out.LastYear, l)
		thisTotal += t
		// Only the months of last year that have already happened this year
		// count into the year total. Comparing eleven months against twelve is
		// the same mistake as comparing a part-month against a whole one, one
		// scale up.
		if m <= jm {
			lastTotal += l
		}
	}
	out.Year.set(thisTotal, lastTotal, out.LastYearLabel+" تا همین ماه")

	out.Caveats = compareCaveats(counts, jy, jm, out)
	return out
}

// compareCaveats says what would otherwise be read wrongly.
//
// This is the part of the panel that earns it. Every report in this database
// has a short history -- the longest is ten months -- so a year-against-year
// chart drawn without comment shows last year as a row of empty bars, which
// reads as a business that has collapsed rather than as a database that does
// not go back that far. The same mistake, in the same building, already
// reported a blank form field as a sales collapse; see
// sectionSuccessByPerson and successdenominator_test.go.
func compareCaveats(counts map[string]float64, jy, jm int, out *PeriodCompare) []string {
	var caveats []string

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	earliest := keys[0]

	// How much of last year the data actually covers, and which months of it.
	//
	// The span named here has to be last year's own first and last month, not
	// the earliest month in the whole result. Naming the global earliest
	// produced a sentence that was simply false: the warehouse report goes back
	// to Azar 1402 but holds only two months of 1404, and the caveat read
	// "only 2 months of 1404 are in the database (from Azar 1402)" -- two true
	// facts joined into a claim that is not.
	var lastYearMonths int
	var firstOfLast, lastOfLast string
	for m := 1; m <= 12; m++ {
		k := jalaliKey(jy-1, m)
		if counts[k] <= 0 {
			continue
		}
		lastYearMonths++
		if firstOfLast == "" {
			firstOfLast = k
		}
		lastOfLast = k
	}
	if lastYearMonths < 12 {
		switch {
		case lastYearMonths == 0:
			caveats = append(caveats, fmt.Sprintf(
				"داده‌ای از سال %s در این گزارش نیست؛ نخستین ماه ثبت‌شده %s است. "+
					"ستون‌های خالیِ سال گذشته یعنی داده وجود ندارد، نه اینکه کاری انجام نشده.",
				out.LastYearLabel, jalaliMonthLabel(earliest)))
		case firstOfLast == lastOfLast:
			caveats = append(caveats, fmt.Sprintf(
				"از سال %s تنها یک ماه در پایگاه داده هست (%s). مقایسه سال‌به‌سال روی "+
					"ماه‌هایی که داده ندارند معنا نمی‌دهد.",
				out.LastYearLabel, jalaliMonthLabel(firstOfLast)))
		default:
			caveats = append(caveats, fmt.Sprintf(
				"از سال %s تنها %s ماه در پایگاه داده هست (%s تا %s). مقایسه سال‌به‌سال روی "+
					"ماه‌هایی که داده ندارند معنا نمی‌دهد.",
				out.LastYearLabel, toPersianDigits(fmt.Sprint(lastYearMonths)),
				jalaliMonthLabel(firstOfLast), jalaliMonthLabel(lastOfLast)))
		}
	}

	if out.MoM.Thin || out.YoY.Thin {
		caveats = append(caveats, fmt.Sprintf(
			"تعداد ردیف‌های این ماه‌ها کمتر از %s است؛ درصد تغییر روی چنین تعدادی "+
				"گمراه‌کننده است و عدد خام را بخوانید.",
			toPersianDigits(fmt.Sprint(thinRows))))
	}

	if out.Partial {
		caveats = append(caveats, fmt.Sprintf(
			"%s هنوز تمام نشده است. هر سه عدد بالا تا روز %s ماه حساب شده‌اند، "+
				"برای هر سه دوره به یک اندازه.",
			out.MonthLabel, toPersianDigits(fmt.Sprint(out.Day))))
	}

	return caveats
}
