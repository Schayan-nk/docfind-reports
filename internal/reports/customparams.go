package reports

import (
	"errors"
	"fmt"
	"strings"
)

// Filters on a report somebody wrote themselves.
//
// The filter language is the shipped catalogue's, not a second one. An authored
// report writes @name in its query exactly as reports/*.sql do, declares what
// each one is, and Bind does the rest -- so a filter behaves the same whether
// it is on a shipped report or on a query typed in this afternoon. A second
// language would have meant a second set of edge cases around empty values,
// LIKE fragments and list expansion, all of which are already decided and
// tested here.
//
// What is different is who writes it. The catalogue's parameters are declared
// in a file by somebody reading this package; these are declared in a web form
// by somebody who knows SQL and nothing else about the program. So everything
// below is about turning that gap into clear errors at save time rather than a
// driver message at run time.

// KindInfo describes one filter type to the page that offers it.
//
// Label and Hint are Persian because they are interface text, unlike the
// validation messages below, which are diagnostics and match the rest of this
// package. Hint is what the filter type actually does, in the terms somebody
// choosing it needs: not "binds a LIKE pattern" but what they will get back.
type KindInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
	// MySQLOnly marks a type the other engine cannot run, so the page can
	// disable it with a reason instead of letting somebody pick it and be
	// refused on save.
	MySQLOnly bool `json:"mysql_only,omitempty"`
}

// builderKinds are the filter types an authored report may declare, in the
// order the page offers them.
//
// Ordered, not a map: this is a dropdown, and a dropdown whose options move
// between page loads is one nobody can learn. Text first because it is the
// answer most of the time.
var builderKinds = []KindInfo{
	{ID: string(KindText), Label: "متن",
		Hint: "مقدار دقیقاً همان است که نوشته می‌شود."},
	{ID: string(KindLike), Label: "جست‌وجوی بخشی از متن",
		Hint: "هر ردیفی که این متن را در خود داشته باشد. خالی یعنی بدون فیلتر. " +
			"برای مهار کردن می‌توان از % استفاده کرد."},
	{ID: string(KindDate), Label: "تاریخ",
		Hint: "یک تاریخ. اگر ستون تاریخ شمسی را به‌صورت متن نگه می‌دارد، تقویم را شمسی کنید."},
	{ID: string(KindList), Label: "چند مقدار جدا با کاما",
		Hint: "به IN (…) تبدیل می‌شود، پس تطبیق دقیق است. در پرس‌وجو بنویسید: IN (@نام). " +
			// The surprising half, and the reason it is spelled out: unlike the
			// text kinds, empty here is not "no filter" -- it becomes IN (NULL)
			// and matches nothing. That is the right reading of "no options
			// selected" once somebody has chosen, and a trap before they have,
			// so a default is what makes the report work when first opened.
			"خالی بودن یعنی «هیچ‌کدام» و هیچ ردیفی برنمی‌گردد، پس پیش‌فرض بگذارید."},
	{ID: string(KindChoice), Label: "انتخاب از فهرست",
		Hint: "یکی از گزینه‌هایی که خودتان تعریف می‌کنید. مقدار کامل تطبیق می‌یابد، نه بخشی از آن."},
	{ID: string(KindAnyOf), Label: "هر یک از چند مقدار", MySQLOnly: true,
		Hint: "هر ردیفی که هر یک از این مقدارها را در خود داشته باشد؛ هر مقدار در یک خط. " +
			"با REGEXP کار می‌کند و تنها روی MySQL هست. در پرس‌وجو بنویسید: ستون REGEXP @نام."},
}

// BuilderKinds is the list of filter types, for the page that offers them.
func BuilderKinds() []KindInfo {
	out := make([]KindInfo, len(builderKinds))
	copy(out, builderKinds)
	return out
}

// kindLabelOf is the Persian label for a type, for a message that has to name
// the thing the reader picked rather than the internal word for it.
func kindLabelOf(k ParamKind) string {
	if info, ok := kindInfo(k); ok {
		return info.Label
	}
	return string(k)
}

// kindInfo finds one, and says whether it is a type the builder allows at all.
func kindInfo(k ParamKind) (KindInfo, bool) {
	for _, info := range builderKinds {
		if info.ID == string(k) {
			return info, true
		}
	}
	return KindInfo{}, false
}

// DiscoverParams returns the placeholder names a query uses, in the order they
// first appear.
//
// Order of first appearance, not sorted: the filter boxes are laid out in this
// order, and a query's own reading order is the one its author is thinking in.
// Repeats collapse -- using @year twice is one filter used twice, which is the
// whole point of naming them.
//
// Shares findPlaceholders with the binder, so what the builder offers to
// declare is exactly what the binder will demand. Discovering a different set
// would produce the worst version of this feature: a filter box that does
// nothing, or a report that refuses to run with a message about a parameter
// nobody was ever shown.
func DiscoverParams(sql string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sp := range findPlaceholders([]rune(sql)) {
		if seen[sp.Name] {
			continue
		}
		seen[sp.Name] = true
		out = append(out, sp.Name)
	}
	return out
}

// DiscoveredParam is a placeholder the query uses, and the filter type its
// surroundings imply.
type DiscoveredParam struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// SuggestParams returns each placeholder with the filter type its own clause
// implies.
//
// Guessing from the SQL rather than defaulting everything to plain text, and
// the difference is not cosmetic. A placeholder against LIKE with an empty
// value has to bind "%" to mean "no filter"; bound as plain text it binds ""
// and matches only the rows whose column is empty. Measured on the live
// database: a query filtering the shipment method returned 3 rows of 28 --
// the three with an empty column -- which reads exactly like a broken report
// and is the same confusion as an earlier bug in this application where
// cleared filter boxes still queried their defaults.
//
// So the clause decides:
//
//	... LIKE @x        a fragment search, where empty means everything
//	... REGEXP @x      several values at once
//	... IN (@x)        a list
//	anything else      a plain value
//
// Only the word immediately before the placeholder is read, which is enough:
// "NOT LIKE @x" ends in LIKE, and nothing is gained by parsing further into a
// clause this package has decided not to parse.
func SuggestParams(sql string) []DiscoveredParam {
	runes := []rune(sql)
	seen := map[string]bool{}
	var out []DiscoveredParam
	for _, sp := range findPlaceholders(runes) {
		if seen[sp.Name] {
			continue
		}
		seen[sp.Name] = true
		out = append(out, DiscoveredParam{Name: sp.Name, Kind: kindFromClause(runes, sp.From)})
	}
	return out
}

// kindFromClause reads backwards from a placeholder for the operator it sits
// against.
func kindFromClause(runes []rune, at int) string {
	i := skipSpaceBack(runes, at-1)

	// "IN (@x)": step over the bracket and read the word before it.
	if i >= 0 && runes[i] == '(' {
		if w := wordBefore(runes, skipSpaceBack(runes, i-1)); strings.EqualFold(w, "in") {
			return string(KindList)
		}
		return string(KindText)
	}

	switch strings.ToLower(wordBefore(runes, i)) {
	case "like":
		return string(KindLike)
	case "regexp", "rlike":
		return string(KindAnyOf)
	case "in":
		// "IN @x" with no bracket is not valid SQL, but somebody mid-edit will
		// have typed it, and a list is what they mean.
		return string(KindList)
	}
	return string(KindText)
}

func skipSpaceBack(runes []rune, i int) int {
	for i >= 0 && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n' || runes[i] == '\r') {
		i--
	}
	return i
}

// wordBefore returns the identifier ending at i.
func wordBefore(runes []rune, i int) string {
	end := i + 1
	for i >= 0 && isIdentRune(runes[i]) {
		i--
	}
	if i+1 >= end {
		return ""
	}
	return string(runes[i+1 : end])
}

// CleanParams normalises what the browser sent and rejects what cannot work.
//
// Returns the parameters to store, so the caller cannot accidentally keep the
// unvalidated ones: the fields this strips -- Suggest above all -- are fields
// no authored report may set.
func CleanParams(driver Driver, sql string, params []Param) ([]Param, error) {
	declared := make(map[string]bool, len(params))
	out := make([]Param, 0, len(params))

	for i := range params {
		p := params[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Label = strings.TrimSpace(p.Label)
		p.Default = strings.TrimSpace(p.Default)

		if p.Name == "" {
			return nil, errors.New("a filter has no name")
		}
		if !validParamName(p.Name) {
			return nil, fmt.Errorf("«%s» cannot be a filter name: use only English letters, "+
				"digits and _, because that is what @%s in the query can match", p.Name, p.Name)
		}
		if declared[p.Name] {
			return nil, fmt.Errorf("the filter @%s is declared twice", p.Name)
		}
		if strings.EqualFold(p.Name, "id") {
			// The CSV download passes filter values as query keys and uses
			// "id" for the report itself, so a filter by that name would be
			// dropped from the export and only the export -- the table on
			// screen would be filtered and the downloaded file would not.
			// A reserved word with a reason beats a discrepancy nobody spots.
			return nil, errors.New(`"id" cannot be a filter name; it is already how the CSV ` +
				"download names the report. Call it something else, such as case_id")
		}
		if strings.EqualFold(p.Name, detailKeyParam) {
			// @key belongs to the detail query, where it is the clicked row.
			// A filter of the same name would be overwritten by that value the
			// moment somebody opened a row, and only then.
			return nil, fmt.Errorf("%q cannot be a filter name; @%s is reserved for the clicked "+
				"row in a detail query. Call it something else, such as %s_filter",
				p.Name, detailKeyParam, p.Name)
		}
		declared[p.Name] = true

		if p.Kind == "" {
			p.Kind = KindText
		}
		if _, ok := kindInfo(p.Kind); !ok {
			return nil, fmt.Errorf("@%s has an unknown filter type %q", p.Name, p.Kind)
		}
		if err := kindWorksOn(driver, p.Kind); err != nil {
			return nil, fmt.Errorf("@%s: %w", p.Name, err)
		}
		if p.Label == "" {
			// The name is a poor label but an honest one, and an empty heading
			// over a filter box is worse than a technical one.
			p.Label = p.Name
		}

		p.Options = cleanOptions(p.Options)
		if p.Kind == KindChoice && len(p.Options) == 0 {
			return nil, fmt.Errorf("@%s is a list to choose from, so it needs at least one option", p.Name)
		}

		// Suggest itself stays un-settable: it is the shipped catalogue's
		// field, read from a .sql file, and an identifier arriving from a
		// browser in that position is one nobody validated.
		p.Suggest = nil

		// The authored equivalent is allowed, and proven first. safeIdentifier
		// is the same regexp the shipped sources are held to -- letters,
		// digits and underscore -- so nothing that reaches the statement can
		// carry a quote, a space, a semicolon or a comment marker. See
		// customsuggest.go for why an administrator may name a column here.
		p.SuggestTable = strings.TrimSpace(p.SuggestTable)
		p.SuggestColumn = strings.TrimSpace(p.SuggestColumn)
		switch {
		case p.SuggestTable == "" && p.SuggestColumn == "":
			p.SuggestPerson = false
		case p.SuggestTable == "" || p.SuggestColumn == "":
			return nil, fmt.Errorf("@%s: suggestions need both a table and a column", p.Name)
		case !safeIdentifier.MatchString(p.SuggestTable):
			return nil, fmt.Errorf("@%s: %q is not a usable table name for suggestions; "+
				"use only English letters, digits and _", p.Name, p.SuggestTable)
		case !safeIdentifier.MatchString(p.SuggestColumn):
			return nil, fmt.Errorf("@%s: %q is not a usable column name for suggestions; "+
				"use only English letters, digits and _", p.Name, p.SuggestColumn)
		}

		// Suggestable is what the browser is TOLD -- that completions exist,
		// without naming the table they come from. Derived rather than
		// accepted, because claiming completions exist when nothing provides
		// them leaves a box that silently never suggests.
		p.Suggestable = p.SuggestTable != "" && p.SuggestColumn != ""
		if p.Suggestable && (p.Kind == KindChoice || p.Kind == KindDate) {
			// A dropdown already lists its values and a date has a picker;
			// offering completions as well would be two controls for one
			// answer, and the one somebody ignores is the one that is right.
			return nil, fmt.Errorf("@%s: a %q filter cannot also suggest values -- "+
				"it already offers its own", p.Name, kindLabelOf(p.Kind))
		}
		p.Calendar = cleanCalendar(p.Calendar, p.Kind)

		out = append(out, p)
	}

	// Every placeholder in the query must be declared. Bind refuses an
	// undeclared one anyway, but it refuses at the moment somebody runs the
	// report -- and the point of checking here is that the person who can fix
	// it is the person standing in front of it.
	var missing []string
	for _, name := range DiscoverParams(sql) {
		if !declared[name] {
			missing = append(missing, "@"+name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the query uses %s, but no filter is defined for %s. "+
			"Add one, or remove it from the query",
			strings.Join(missing, ", "), pluralIt(len(missing)))
	}
	return out, nil
}

// kindWorksOn refuses a filter type the engine cannot run.
//
// One case, and it is a real one rather than a defensive gesture: KindAnyOf
// binds a MySQL regular expression and the query compares it with REGEXP,
// which SQL Server has no equivalent of. Allowing it would save a report that
// fails with a syntax error the first time anybody opens it, and the error
// would point at the query rather than at the choice that caused it.
func kindWorksOn(driver Driver, kind ParamKind) error {
	if driver != DriverSQLServer {
		return nil
	}
	info, ok := kindInfo(kind)
	if !ok || !info.MySQLOnly {
		return nil
	}
	// Named by the label the dropdown shows rather than by the internal kind,
	// so the message points at the thing the reader picked.
	alt, _ := kindInfo(KindList)
	return fmt.Errorf("%q matches with REGEXP, which SQL Server does not have. "+
		"Use %q on this connection", info.Label, alt.Label)
}

// cleanOptions drops blank entries and fills in a missing label.
//
// A label defaulting to its own value matters for the common case: somebody
// listing the four statuses their column holds should not have to type each
// one twice.
func cleanOptions(in []Option) []Option {
	var out []Option
	for _, o := range in {
		o.Value = strings.TrimSpace(o.Value)
		o.Label = strings.TrimSpace(o.Label)
		if o.Value == "" && o.Label == "" {
			continue
		}
		if o.Label == "" {
			o.Label = o.Value
		}
		out = append(out, o)
	}
	return out
}

// cleanCalendar keeps the calendar marker only where it means something.
//
// Gregorian is the default rather than Jalali, even though many Persian systems
// hold dates as Jalali text. An authored report can
// point at any database, a real DATE column is the ordinary case everywhere
// else, and a date picker offering the wrong calendar produces a filter that
// matches nothing with no hint as to why -- so the safe default is the one the
// column type usually implies, and the author says otherwise when they know
// their column is text.
func cleanCalendar(cal string, kind ParamKind) string {
	if kind != KindDate {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(cal), "jalali") {
		return "jalali"
	}
	return "gregorian"
}

// validParamName says whether a declared name can ever match a placeholder.
//
// Measured against the scanner rather than against a general idea of an
// identifier: findPlaceholders reads a name as a run of isIdentRune, so a
// filter called "نام" or "order-date" could be declared, saved, and never bind
// to anything. Rejecting it here is the difference between an explanation and a
// filter box that does nothing.
func validParamName(s string) bool {
	for _, r := range s {
		if !isIdentRune(r) {
			return false
		}
	}
	// A leading digit is legal for the scanner but reads as a mistake, and
	// nothing is lost by asking for a letter or an underscore first.
	first := rune(s[0])
	return first == '_' || (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}

func pluralIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
