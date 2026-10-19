package reports

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/format"
)

// Date filters whose picker and whose column disagree about the calendar, and
// defaults that move with it.
//
// Most date filters compare against a column in the calendar their picker
// shows: Jalali text against a Jalali text column, or a Gregorian picker
// against a DATETIME. A database that stores real datetimes for readers who
// think in Jalali -- an Iranian fiscal year IS a Jalali year -- needs filters
// that are Jalali on the page and Gregorian in the query:
//
//	-- param: pfrom | date | از تاریخ | default=year-start | bind=gregorian
//
// The value is bound as YYYYMMDD. Not 2026-03-21: SQL Server reads that form
// as year-DAY-month for a datetime under a login whose language is British or
// French, and which language the finance login has is not ours to know.
// YYYYMMDD is read one way under every setting, and by MySQL too.
//
// A default that is a date typed into a file goes stale: "1404/01/01" was this
// year's start when it was written and is last year's now. So a date default
// may name a point instead -- today, month-start or year-start -- in the
// picker's calendar, resolved when the report is listed and when it is bound.

// dateTokens are the defaults that name a point rather than a date.
var dateTokens = map[string]bool{"today": true, "month-start": true, "year-start": true}

// isDateToken reports whether a default names a point in time.
func isDateToken(s string) bool { return dateTokens[strings.ToLower(strings.TrimSpace(s))] }

// resolveDateToken turns a token into a date in the given calendar, as the
// picker writes it: "1405/07/05" or "2026/09/27".
func resolveDateToken(token, calendar string, now time.Time) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if calendar == CalendarGregorian {
		y, m, d := now.Date()
		switch token {
		case "month-start":
			d = 1
		case "year-start":
			m, d = 1, 1
		}
		return fmt.Sprintf("%04d/%02d/%02d", y, int(m), d)
	}
	jy, jm, jd := format.ToJalali(now.Year(), int(now.Month()), now.Day())
	switch token {
	case "month-start":
		jd = 1
	case "year-start":
		jm, jd = 1, 1
	}
	return fmt.Sprintf("%04d/%02d/%02d", jy, jm, jd)
}

// withLiveDefaults is r with every token default replaced by today's date.
//
// A copy of the parameter list is made only when there is something to
// replace: the catalogue's copy is shared by every request and must never be
// edited in place.
func withLiveDefaults(r Report) Report {
	var params []Param
	for i, p := range r.Params {
		if p.Kind != KindDate || !isDateToken(p.Default) {
			continue
		}
		if params == nil {
			params = append([]Param(nil), r.Params...)
		}
		params[i].Default = resolveDateToken(p.Default, p.Calendar, time.Now())
	}
	if params != nil {
		r.Params = params
	}
	return r
}

// bindDate is the value a date filter binds: as typed, unless the parameter
// asks for it in the other calendar. An empty value binds NULL once converted,
// so a query can say "(@pfrom IS NULL OR ...)" rather than comparing a date to
// an empty string, which SQL Server reads as 1900-01-01.
func bindDate(p Param, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	shown := p.Calendar
	if shown == "" {
		shown = CalendarJalali
	}
	if isDateToken(raw) {
		raw = resolveDateToken(raw, shown, time.Now())
	}
	if p.BindCalendar == "" {
		return raw, nil
	}
	if raw == "" {
		return nil, nil
	}
	y, m, d, ok := readYMD(raw)
	if !ok {
		return nil, fmt.Errorf("reports: @%s: %q is not a date", p.Name, raw)
	}
	switch {
	case shown == CalendarJalali && p.BindCalendar == CalendarGregorian:
		y, m, d = format.FromJalali(y, m, d)
	case shown == CalendarGregorian && p.BindCalendar == CalendarJalali:
		y, m, d = format.ToJalali(y, m, d)
		return fmt.Sprintf("%04d/%02d/%02d", y, m, d), nil
	case p.BindCalendar == CalendarJalali:
		return fmt.Sprintf("%04d/%02d/%02d", y, m, d), nil
	}
	return fmt.Sprintf("%04d%02d%02d", y, m, d), nil
}

// readYMD reads a year-first date written with "/", "-" or ".", in Latin or
// Persian digits. Only the ranges are checked; which calendar it is in is the
// parameter's to say.
func readYMD(s string) (y, m, d int, ok bool) {
	s = strings.TrimSpace(latinDigits(s))
	if i := strings.IndexAny(s, " T"); i > 0 {
		s = s[:i]
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
	if len(parts) != 3 || len(parts[0]) != 4 {
		return 0, 0, 0, false
	}
	var err error
	if y, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if m, err = strconv.Atoi(parts[1]); err != nil || m < 1 || m > 12 {
		return 0, 0, 0, false
	}
	if d, err = strconv.Atoi(parts[2]); err != nil || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	if (y < 1300 || y > 1500) && (y < 1900 || y > 2200) {
		return 0, 0, 0, false
	}
	return y, m, d, true
}

// paramCalendar validates a calendar named on a param line.
func paramCalendar(name, what, v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v != CalendarJalali && v != CalendarGregorian {
		return "", fmt.Errorf("param %s has unknown %s calendar %q (use jalali or gregorian)", name, what, v)
	}
	return v, nil
}
