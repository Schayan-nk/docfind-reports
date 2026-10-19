package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Catalog is the set of reports available to the dashboard.
//
// Reports are .sql files on disk rather than Go source, so a new report is a
// file someone drops in a folder instead of a rebuild and a redeploy, and
// the people who write the queries keep writing SQL.
//
// A file carries its own metadata in leading comments, which keeps the
// definition and the query in one place:
//
//	-- id: orders_overview
//	-- title: Orders overview
//	-- chart: bar | label=owner_name | value=order_count
//	-- param: pref | like | Order number
//	-- param: ptype | list | Order type | default=1,2
//	SELECT ...
type Catalog struct {
	mu      sync.RWMutex
	dir     string
	reports map[string]Report
	order   []string
	// files is the id of every .sql file in dir at the last Reload, including
	// the ones that failed to load (see FileIDs).
	files []string
	// hasSource says whether a "-- source:" server is connected. Until it is
	// set, every report on another server is left out: absent, not broken.
	hasSource func(string) bool
}

// UseSources tells the catalogue which other servers can be reached, so a
// report on one this deployment was never given stays off the dashboard, the
// menu and the permission screen instead of failing every time it is opened.
func (c *Catalog) UseSources(has func(name string) bool) {
	c.mu.Lock()
	c.hasSource = has
	c.mu.Unlock()
}

// reachableLocked reports whether r's server is connected. Called under c.mu.
func (c *Catalog) reachableLocked(r Report) bool {
	return r.Source == "" || (c.hasSource != nil && c.hasSource(r.Source))
}

// LoadCatalog reads every .sql file in dir.
//
// A file that cannot be parsed is skipped with an error rather than taking the
// whole dashboard down: one malformed report should not hide the other nine.
func LoadCatalog(dir string) (*Catalog, []error) {
	c := &Catalog{dir: dir, reports: map[string]Report{}}
	errs := c.Reload()
	return c, errs
}

// Reload re-reads the directory, so editing a query takes effect without a
// restart.
func (c *Catalog) Reload() []error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return []error{fmt.Errorf("reports: reading %s: %w", c.dir, err)}
	}

	var errs []error
	loaded := map[string]Report{}
	var order []string
	files := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".sql") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		path := filepath.Join(c.dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			files[stem] = true
			errs = append(errs, fmt.Errorf("reports: %s: %w", e.Name(), err))
			continue
		}
		// The id is settled before the parse is judged, so a file that fails
		// still counts as present under the id it declares.
		r, err := parseReport(string(raw))
		if r.ID == "" {
			r.ID = stem
		}
		files[r.ID] = true
		if err != nil {
			errs = append(errs, fmt.Errorf("reports: %s: %w", e.Name(), err))
			continue
		}
		if _, dup := loaded[r.ID]; dup {
			errs = append(errs, fmt.Errorf("reports: %s: duplicate id %q", e.Name(), r.ID))
			continue
		}
		// Catch a query naming a parameter it never declared now, at load,
		// rather than when somebody opens the report.
		if _, _, err := Bind(r.SQL, r.Params, nil); err != nil {
			errs = append(errs, fmt.Errorf("reports: %s: %w", e.Name(), err))
			continue
		}
		// The file verbatim, so this catalogue can seed a fresh database with
		// exactly what shipped. Never serialised: it contains the SQL.
		r.Body = string(raw)
		loaded[r.ID] = r
		order = append(order, r.ID)
	}
	sort.Strings(order)
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	c.mu.Lock()
	c.reports, c.order, c.files = loaded, order, ids
	c.mu.Unlock()
	return errs
}

// List returns every report, without their SQL.
func (c *Catalog) List() []Report {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Report, 0, len(c.order))
	for _, id := range c.order {
		r := c.reports[id]
		if !c.reachableLocked(r) {
			continue
		}
		r.SQL = "" // never leaves the process
		out = append(out, withLiveDefaults(r))
	}
	return out
}

// Get returns one report, SQL included, for execution.
func (c *Catalog) Get(id string) (Report, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.reports[id]
	if !ok || !c.reachableLocked(r) {
		return Report{}, false
	}
	return withLiveDefaults(r), true
}

// Count reports how many loaded, of those whose server is connected.
func (c *Catalog) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, r := range c.reports {
		if c.reachableLocked(r) {
			n++
		}
	}
	return n
}

// parseReport splits a file's leading metadata comments from its query.
func parseReport(text string) (Report, error) {
	var r Report
	lines := strings.Split(text, "\n")
	body := 0

	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "--") {
			body = i
			break
		}
		directive := strings.TrimSpace(strings.TrimPrefix(t, "--"))
		key, value, found := strings.Cut(directive, ":")
		if !found {
			continue // an ordinary comment
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "id":
			r.ID = value
		case "title":
			r.Title = value
		case "description":
			r.Description = value
		case "source":
			// "-- source: finance": the server this report reads, when it is
			// not the reporting MySQL (extsource.go).
			r.Source = strings.ToLower(value)
		case "group":
			// "-- group: حسابداری": the heading it is filed under.
			r.Group = value
		case "family":
			// "-- family: warehouses | گزارش انبارها | انبار | حسابداری": shown as
			// one report with its members chosen by dropdown (Report.Family);
			// the names after the title label the dropdowns, level by level.
			parts := strings.Split(value, "|")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			r.Family = parts[0]
			if len(parts) > 1 {
				r.FamilyTitle = parts[1]
			}
			if len(parts) > 2 {
				r.FamilyLevels = parts[2:]
			}
		case "variant":
			// "-- variant: انبار مرکزی | رسمی": this member's choice, level by level.
			r.Variant = nil
			for _, v := range strings.Split(value, "|") {
				if v = strings.TrimSpace(v); v != "" {
					r.Variant = append(r.Variant, v)
				}
			}
		case "column":
			// "-- column: order_no | شماره سفارش"
			if key, label, found := strings.Cut(value, "|"); found {
				if r.ColumnLabels == nil {
					r.ColumnLabels = map[string]string{}
				}
				r.ColumnLabels[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(label)
			}
		case "hide":
			r.HideColumns = addNames(r.HideColumns, value)
		case "show":
			r.ShowColumns = addNames(r.ShowColumns, value)
		case "chart":
			parseChart(&r, value)
		case "monthly":
			// "-- monthly: submitted_on | تاریخ ثبت"
			//
			// Names a Jalali date column to build the monthly banner chart
			// from. Grouped in the browser from the rows already returned, so
			// it follows whatever filters are applied rather than needing a
			// second query with its own copy of the WHERE clause.
			//
			// "-- monthly: none | <reason>" declares that this report cannot
			// have one, and why. Three of the ten cannot, for three different
			// reasons, and each reason is worth a sentence on the page.
			//
			// An optional third field names the calendar the column is written
			// in: "-- monthly: تاریخ ورود به انبار | ورود به انبار | calendar=gregorian".
			parts := strings.Split(value, "|")
			col := strings.TrimSpace(parts[0])
			var label string
			if len(parts) > 1 {
				label = strings.TrimSpace(parts[1])
			}
			if strings.EqualFold(col, "none") {
				r.MonthlyWhyNot = label
				break
			}
			r.MonthlyColumn = col
			r.MonthlyLabel = label
			r.MonthlyCalendar = CalendarJalali
			for _, extra := range parts[2:] {
				k, v, found := strings.Cut(strings.TrimSpace(extra), "=")
				if !found || !strings.EqualFold(strings.TrimSpace(k), "calendar") {
					continue
				}
				r.MonthlyCalendar = strings.ToLower(strings.TrimSpace(v))
			}
			if r.MonthlyCalendar != CalendarJalali && r.MonthlyCalendar != CalendarGregorian {
				return r, fmt.Errorf("monthly column %s has unknown calendar %q (use jalali or gregorian)",
					col, r.MonthlyCalendar)
			}
		case "person":
			// "-- person: owner_name | requester_name"
			//
			// Names the result columns that hold a person's name, so the table
			// shows them folded the way the chart and the suggestion list
			// already do. Several columns may be named, separated by "|".
			for _, c := range strings.Split(value, "|") {
				if c = strings.TrimSpace(c); c != "" {
					r.PersonColumns = append(r.PersonColumns, c)
				}
			}
		case "total":
			// "-- total: opening_balance, debit_turnover, balance"
			//
			// The columns a «جمع» row at the foot of the table adds up, in
			// the order written.
			for _, part := range strings.Split(value, ",") {
				if name := strings.ToLower(strings.TrimSpace(part)); name != "" {
					r.TotalColumns = append(r.TotalColumns, name)
				}
			}
		case "param":
			p, err := parseParam(value)
			if err != nil {
				return r, err
			}
			r.Params = append(r.Params, p)
		case "suggest":
			// "-- suggest: powner | staff | full_name | person"
			if err := addSuggest(&r, value); err != nil {
				return r, err
			}
		case "option":
			// "-- option: pstatus | TO_DO | در حال اجرا"
			//
			// A directive of its own rather than more fields on the param
			// line: the labels are Persian phrases and the values are raw
			// database strings, and cramming pairs of them into a
			// pipe-delimited tail made the param line unreadable.
			if err := addOption(&r, value); err != nil {
				return r, err
			}
		}
		body = i + 1
	}

	r.SQL = strings.TrimSpace(strings.Join(lines[body:], "\n"))
	if r.SQL == "" {
		return r, fmt.Errorf("no query found")
	}
	if r.Title == "" {
		r.Title = r.ID
	}
	// A report on another server has rules of its own: see checkSourced.
	if err := checkSourced(r); err != nil {
		return r, err
	}
	return r, nil
}

// addNames folds a comma-separated column list into a set, lower-cased so the
// lookup does not care how the query spelled the column.
func addNames(set map[string]bool, value string) map[string]bool {
	if set == nil {
		set = map[string]bool{}
	}
	for _, part := range strings.Split(value, ",") {
		if name := strings.ToLower(strings.TrimSpace(part)); name != "" {
			set[name] = true
		}
	}
	return set
}

// addOption attaches one dropdown entry to a parameter already declared.
//
// Order matters and is the file's: options appear in the dropdown in the order
// they were written, so a report can put the common case first rather than
// having them sorted into something arbitrary.
func addOption(r *Report, value string) error {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) < 3 {
		return fmt.Errorf("option needs a parameter name, a value and a label, got %q", value)
	}
	name := strings.TrimSpace(parts[0])
	opt := Option{
		Value: strings.TrimSpace(parts[1]),
		Label: strings.TrimSpace(parts[2]),
	}
	for i := range r.Params {
		if r.Params[i].Name == name {
			r.Params[i].Options = append(r.Params[i].Options, opt)
			return nil
		}
	}
	// Caught at load rather than leaving a dropdown mysteriously empty.
	return fmt.Errorf("option refers to @%s, which this report does not declare", name)
}

// addSuggest attaches a completion source to a parameter already declared.
//
// The table and column are checked here, at load, rather than when someone
// first types into the box: an unusable identifier should be a startup problem
// a deploy notices, not a broken filter a user discovers.
//
// An optional fourth field says what kind of values the column holds:
//
//	-- suggest: powner | staff | full_name | person
//
// Only "person" means anything, and it is the difference between offering one
// colleague once and offering them once per job title they have held.
func addSuggest(r *Report, value string) error {
	parts := strings.SplitN(value, "|", 4)
	if len(parts) < 3 {
		return fmt.Errorf("suggest needs a parameter name, a table and a column, got %q", value)
	}
	name := strings.TrimSpace(parts[0])
	src := SuggestSource{
		Table:  strings.TrimSpace(parts[1]),
		Column: strings.TrimSpace(parts[2]),
	}
	if !src.Valid() {
		return fmt.Errorf("suggest source %q is not a plain table.column name", src)
	}
	if len(parts) == 4 {
		// An unknown kind is refused rather than ignored. Reload skips a file it
		// cannot parse, so a report with a misspelled kind disappears from the
		// dashboard and says why -- where ignoring it would leave the filter
		// quietly listing the same person four times, which is the bug this
		// field exists to fix.
		switch kind := strings.ToLower(strings.TrimSpace(parts[3])); kind {
		case "":
			// A trailing pipe with nothing after it declares nothing.
		case "person":
			src.Person = true
		default:
			return fmt.Errorf("suggest source %s has unknown kind %q (the only kind is person)", src, kind)
		}
	}
	for i := range r.Params {
		if r.Params[i].Name == name {
			r.Params[i].Suggest = &src
			r.Params[i].Suggestable = true
			return nil
		}
	}
	return fmt.Errorf("suggest refers to @%s, which this report does not declare", name)
}

// parseChart reads "bar | label=col | value=col | agg=count".
//
// agg says how the bar's height is produced. Without it the query is assumed
// to have aggregated already (one row per bar), which is only true of reports
// written with a GROUP BY; the row-listing reports need "agg=count" or
// "agg=sum" so the server can total them.
func parseChart(r *Report, value string) {
	parts := strings.Split(value, "|")
	if len(parts) == 0 {
		return
	}
	r.Chart = strings.TrimSpace(parts[0])
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		k, v, found := strings.Cut(p, "=")
		if !found {
			// Bare "count" is accepted as shorthand for "agg=count", which is
			// by far the most common case and reads better in the file.
			if lower := strings.ToLower(p); lower == "count" || lower == "sum" {
				r.ChartAgg = lower
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "label":
			r.LabelColumn = strings.TrimSpace(v)
		case "value":
			r.ValueColumn = strings.TrimSpace(v)
		case "agg":
			r.ChartAgg = strings.ToLower(strings.TrimSpace(v))
		case "normalise", "normalize":
			r.ChartNormalise = strings.ToLower(strings.TrimSpace(v))
		case "series":
			r.SeriesColumn = strings.TrimSpace(v)
		case "stack":
			r.StackColumn = strings.TrimSpace(v)
		}
	}
}

// parseParam reads "name | kind | label | default=x".
func parseParam(value string) (Param, error) {
	parts := strings.Split(value, "|")
	if len(parts) < 2 {
		return Param{}, fmt.Errorf("param needs at least a name and a kind, got %q", value)
	}
	p := Param{Name: strings.TrimSpace(parts[0])}
	if p.Name == "" {
		return Param{}, fmt.Errorf("param has no name: %q", value)
	}

	kind := ParamKind(strings.ToLower(strings.TrimSpace(parts[1])))
	switch kind {
	case KindText, KindLike, KindDate, KindList, KindChoice, KindAnyOf:
		p.Kind = kind
	default:
		return Param{}, fmt.Errorf("param %s has unknown kind %q (use text, like, date, list, choice or anyof)", p.Name, kind)
	}

	if len(parts) > 2 {
		p.Label = strings.TrimSpace(parts[2])
	}
	if p.Label == "" {
		p.Label = p.Name
	}
	for _, extra := range parts[3:] {
		k, v, found := strings.Cut(strings.TrimSpace(extra), "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "default":
			p.Default = strings.TrimSpace(v)
		case "calendar":
			p.Calendar = strings.ToLower(strings.TrimSpace(v))
		case "bind":
			// "bind=gregorian": shown in one calendar, bound in the other
			// (datebind.go).
			bc, err := paramCalendar(p.Name, "bind", v)
			if err != nil {
				return Param{}, err
			}
			p.BindCalendar = bc
		case "required":
			// "required=yes": a dropdown with no «همه», for a choice that
			// switches what is shown rather than narrowing it.
			p.Required = strings.EqualFold(strings.TrimSpace(v), "yes") ||
				strings.EqualFold(strings.TrimSpace(v), "true")
		}
	}
	// Date filters here are Jalali unless the file says otherwise: Persian
	// systems commonly keep dates as Jalali text, and a
	// picker offering the wrong calendar writes a value that matches nothing.
	if p.Kind == KindDate && p.Calendar == "" {
		p.Calendar = "jalali"
	}
	if p.Calendar != "" && p.Calendar != "jalali" && p.Calendar != "gregorian" {
		return Param{}, fmt.Errorf("param %s has unknown calendar %q (use jalali or gregorian)", p.Name, p.Calendar)
	}
	return p, nil
}
