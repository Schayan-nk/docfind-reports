package auth

import (
	"sort"
	"strings"
)

// Every grantable thing in the application, declared once.
//
// WHAT WAS WRONG WITH THE OLD MODEL. Five permissions mapped from three roles
// in a table in this file, and that table WAS the access model. It cannot say
// "this person may do what a manager does, except this one thing", which is
// the thing that was asked for. Measured at the time: of 133 registered
// routes, 9 consulted a permission and 51 were gated on nothing but "is signed
// in".
//
// THE ID IS THE CONTRACT AND THE LABEL IS DECORATION. Grants are stored
// against the ID, so renaming an entry in Persian never silently revokes
// anybody's access to it -- which is exactly what would happen if the label
// were the key.
//
// ANYTHING NOT IN HERE IS DENIED, to everyone but an administrator. That rule
// is the one that earns its place: it means adding a route cannot accidentally
// grant access to it. Forgetting to declare a permission fails closed and
// somebody complains; the opposite fails open and nobody notices.

// Kind groups the catalogue for the screen that edits it, and nothing else.
type Kind string

const (
	// KindPage is a whole screen.
	KindPage Kind = "page"
	// KindAction is something done rather than seen.
	KindAction Kind = "action"
	// KindReport is one report on the reports page.
	KindReport Kind = "report"
)

// Item is one grantable thing.
type Item struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Group string `json:"group"`
	Label string `json:"label"`
	// AdminOnly marks an item no template but admin's may hold and no
	// override may grant. Reserved for the things that would let somebody
	// grant themselves everything else.
	AdminOnly bool `json:"admin_only,omitempty"`
	// Explicit marks an item NO TEMPLATE GIVES -- not even an
	// administrator's. It is held only by an explicit allow on the account.
	//
	// THIS EXISTS BECAUSE SOME THINGS BELONG TO PEOPLE, NOT TO A ROLE: "these
	// two, and no one else, not even other administrators" is about identity,
	// and a catalogue whose rule is "an administrator holds everything
	// declared" would quietly hand such an item to every administrator.
	//
	// So: Explicit is how the catalogue says "belongs to named people". It
	// composes with the other rules rather than replacing them -- a deny
	// still beats it, and an administrator can still GRANT it to somebody
	// (including themselves, through another administrator), which is the
	// difference between Explicit and AdminOnly.
	Explicit bool `json:"explicit,omitempty"`
}

// The page IDs. Named constants because they are referenced from route
// registrations, and a typo in a string literal there would deny a page to
// everybody -- or, if it were the other way round, grant it.
const (
	PageDashboard = "page:dashboard"
	PageBuilder   = "page:builder"
	PageSettings  = "page:settings"
)

// The actions.
const (
	ActionManageUsers    = "action:manage_users"
	ActionManageSettings = "action:manage_settings"

	// Downloading a report's rows as a file.
	ActionExport = "action:export"
	// Re-reading the folder of .sql reports, so an edited one takes effect
	// without a restart.
	ActionReportsReload = "action:reports_reload"

	// The builder's: running a query somebody typed, saving it as a report,
	// adding or changing a database connection, and exporting from it. All
	// Explicit, with page:builder: they reach any database a connection names,
	// so they are held by named accounts, never by a role.
	ActionBuilderRun    = "action:builder_run"
	ActionBuilderSave   = "action:builder_save"
	ActionSources       = "action:sources"
	ActionBuilderExport = "action:builder_export"
)

// ReportPrefix is how a report ID becomes a permission ID.
const ReportPrefix = "report:"

// ReportPerm is the permission that guards one report.
func ReportPerm(reportID string) string { return ReportPrefix + reportID }

// Persian group headings. The editing screen shows the catalogue grouped.
const (
	groupReports    = "گزارش‌ها"
	groupReportTool = "ابزار گزارش‌سازی"
	groupGeneral    = "عمومی"
	groupAdmin      = "مدیریت"
)

// builtinItems is everything that can be granted besides the reports
// themselves (WithReports). The labels are the words on the screen, so the
// permission for a page is spelled the way the person granting it sees it.
var builtinItems = []Item{
	{PageDashboard, KindPage, groupReports, "گزارش‌ها", false, false},
	{ActionExport, KindAction, groupReports, "خروجی گرفتن", false, false},
	{PageBuilder, KindPage, groupReportTool, "ساختن گزارش", false, true},
	{ActionBuilderRun, KindAction, groupReportTool, "اجرای پرس‌وجوی دلخواه", false, true},
	{ActionBuilderSave, KindAction, groupReportTool, "ذخیره گزارش تازه", false, true},
	{ActionSources, KindAction, groupReportTool, "تغییر اتصال‌های پایگاه داده", false, true},
	{ActionBuilderExport, KindAction, groupReportTool, "خروجی از گزارش‌ساز", false, true},
	{ActionReportsReload, KindAction, groupReportTool, "بازخوانی گزارش‌ها", false, false},
	{PageSettings, KindPage, groupGeneral, "تنظیمات", false, false},
	{ActionManageUsers, KindAction, groupAdmin, "مدیریت کاربران و دسترسی‌ها", true, false},
	{ActionManageSettings, KindAction, groupAdmin, "تغییر تنظیمات سامانه", true, false},
}

// Catalogue is the set of grantable things for this deployment.
//
// Immutable once built, because it is read by every request on every
// goroutine: mutating it in place would be a data race on the access model,
// which is the worst place to have one.
type Catalogue struct {
	items []Item
	byID  map[string]Item
}

// NewCatalogue returns the built-in catalogue, with no reports in it.
func NewCatalogue() *Catalogue { return buildCatalogue(builtinItems) }

func buildCatalogue(items []Item) *Catalogue {
	c := &Catalogue{items: items, byID: make(map[string]Item, len(items))}
	for _, it := range items {
		c.byID[it.ID] = it
	}
	return c
}

// Has reports whether an ID is declared.
func (c *Catalogue) Has(id string) bool {
	if c == nil {
		return false
	}
	_, ok := c.byID[id]
	return ok
}

// Item returns one entry.
func (c *Catalogue) Item(id string) (Item, bool) {
	if c == nil {
		return Item{}, false
	}
	it, ok := c.byID[id]
	return it, ok
}

// IDs returns every declared ID.
func (c *Catalogue) IDs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it.ID)
	}
	return out
}

// Group is one heading and its items, for the editing screen.
type Group struct {
	Name  string `json:"name"`
	Items []Item `json:"items"`
}

// Groups returns the catalogue grouped, in the order groups first appear.
func (c *Catalogue) Groups() []Group {
	if c == nil {
		return nil
	}
	var order []string
	byName := map[string][]Item{}
	for _, it := range c.items {
		if _, seen := byName[it.Group]; !seen {
			order = append(order, it.Group)
		}
		byName[it.Group] = append(byName[it.Group], it)
	}
	out := make([]Group, 0, len(order))
	for _, name := range order {
		out = append(out, Group{Name: name, Items: byName[name]})
	}
	return out
}

// WithReports returns a catalogue that also grants the named reports.
//
// labels maps a report ID to its Persian title; a report with no label gets
// its ID, which is ugly on the screen and still correct -- better than
// dropping it and silently making it ungrantable.
func (c *Catalogue) WithReports(ids []string, labels map[string]string) *Catalogue {
	if c == nil {
		c = NewCatalogue()
	}
	items := make([]Item, 0, len(c.items)+len(ids))
	// Everything that is not already a report, so calling this twice replaces
	// the report set rather than accumulating stale ones.
	for _, it := range c.items {
		if it.Kind != KindReport {
			items = append(items, it)
		}
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		if strings.TrimSpace(id) == "" {
			continue
		}
		label := labels[id]
		if strings.TrimSpace(label) == "" {
			label = id
		}
		items = append(items, Item{
			ID: ReportPerm(id), Kind: KindReport, Group: groupReports, Label: label,
		})
	}
	return buildCatalogue(items)
}

// WithReportGroups files the named reports under a heading of their own
// instead of «گزارش‌ها», keyed by report ID: the accounting reports sit under
// «حسابداری» on the permission screen, as they do in the menu. A separate step
// rather than a third argument to WithReports, so the callers that have no
// groups are untouched. Only the heading changes; the grant is the same.
func (c *Catalogue) WithReportGroups(groups map[string]string) *Catalogue {
	if c == nil || len(groups) == 0 {
		return c
	}
	items := make([]Item, len(c.items))
	copy(items, c.items)
	for i, it := range items {
		if it.Kind != KindReport {
			continue
		}
		if g := strings.TrimSpace(groups[strings.TrimPrefix(it.ID, ReportPrefix)]); g != "" {
			items[i].Group = g
		}
	}
	return buildCatalogue(items)
}
