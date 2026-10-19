package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reports an administrator wrote, rather than ones shipped as files.
//
// The ten in reports/*.sql are part of the product: they are versioned, they
// are reviewed, and their comments explain why each column is there. These are
// not that. They are somebody's own query, saved so it can be opened again,
// and the difference shows in how they are treated -- kept in a data file
// beside the other state, editable at any time, and never mixed into the
// catalogue the binary ships.
//
// Deliberately NOT written out as .sql files into the catalogue directory,
// which was the first idea and is worse: the catalogue is reloaded wholesale,
// a file written by a web form would sit in a directory under version control
// alongside hand-written ones, and a parse error in an authored report would
// then take the shipped ten down with it. Separate store, separate blast
// radius.

// CustomReport is one saved query.
type CustomReport struct {
	ID string `json:"id"`
	// SourceID names the connection it runs against. A report whose source
	// has been deleted is kept rather than removed -- somebody spent time on
	// the query, and the honest thing is to show it as needing a connection
	// rather than to delete their work on their behalf.
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	SQL         string `json:"sql"`
	// Params are the filters the query declares, in the order they are shown.
	//
	// Stored with the report rather than derived from the SQL each time it is
	// opened: the query says a filter exists and what it is called, but only
	// this says what it is called in Persian, what it defaults to, and whether
	// it is a date or a list -- and none of that can be read out of an @name.
	Params []Param `json:"params,omitempty"`
	// Columns are what this report calls its columns, and which it hides. Only
	// the ones it has something to say about; everything else falls through to
	// the shared list. See customcolumns.go.
	Columns []ColumnSpec `json:"columns,omitempty"`
	// Chart is how the report wants to be drawn, or nil for a table only --
	// which is most of them. See customchart.go for why this stores a choice of
	// columns rather than any data.
	Chart *ChartSpec `json:"chart,omitempty"`
	// Detail is a second query run for one row of the first, or nil. See
	// customdetail.go for why it is a second query rather than a join.
	Detail *DetailSpec `json:"detail,omitempty"`
	// Author is the account that last saved it, so a list of thirty reports
	// can be traced back to whoever is responsible for each.
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CustomStore keeps authored reports in a JSON file.
type CustomStore struct {
	mu      sync.RWMutex
	path    string
	reports map[string]CustomReport
	order   []string
}

// OpenCustomStore loads the file, or starts empty.
func OpenCustomStore(path string) (*CustomStore, error) {
	c := &CustomStore{path: path, reports: map[string]CustomReport{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reports: reading %s: %w", path, err)
	}
	var list []CustomReport
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("reports: %s is not valid JSON: %w", path, err)
	}
	for _, r := range list {
		c.reports[r.ID] = r
		c.order = append(c.order, r.ID)
	}
	c.sortLocked()
	return c, nil
}

// sortLocked orders by title, which is what the list is read in.
func (c *CustomStore) sortLocked() {
	sort.SliceStable(c.order, func(i, j int) bool {
		a, b := c.reports[c.order[i]], c.reports[c.order[j]]
		if !strings.EqualFold(a.Title, b.Title) {
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		}
		return a.ID < b.ID
	})
}

func (c *CustomStore) saveLocked() error {
	list := make([]CustomReport, 0, len(c.order))
	for _, id := range c.order {
		list = append(list, c.reports[id])
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("reports: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("reports: %w", err)
	}
	// Temporary file and rename, so an interrupted write cannot leave half a
	// file where somebody's saved reports used to be.
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("reports: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("reports: replacing %s: %w", c.path, err)
	}
	return nil
}

// List returns every authored report, SQL included.
//
// Unlike the shipped catalogue, the query IS returned: the person who may see
// this list is an administrator, the builder has to show them the SQL in order
// to edit it, and hiding it would make the feature unusable. The access check
// is the one that matters and it is at the route.
func (c *CustomStore) List() []CustomReport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]CustomReport, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.reports[id])
	}
	return out
}

// Get returns one.
func (c *CustomStore) Get(id string) (CustomReport, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.reports[id]
	return r, ok
}

// Count is how many are saved.
func (c *CustomStore) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.reports)
}

// Save adds or replaces a report.
//
// The query is checked here as well as at the moment it runs. Both, because
// they answer different questions: this one stops an unrunnable report being
// saved at all, so nobody discovers it is broken a week later when they open
// it; the one in RunCustom is the guarantee, because a stored report could
// have been edited in the file by hand.
func (c *CustomStore) Save(r CustomReport, sources *SourceStore) (CustomReport, error) {
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	r.SQL = strings.TrimSpace(r.SQL)
	r.SourceID = strings.TrimSpace(r.SourceID)

	if r.Title == "" {
		return CustomReport{}, errors.New("the report needs a title")
	}
	if r.SourceID == "" {
		return CustomReport{}, errors.New("choose which connection this report runs against")
	}
	if sources != nil && !sources.Has(r.SourceID) {
		return CustomReport{}, errors.New("that connection no longer exists")
	}
	if err := CheckReadOnly(r.SQL); err != nil {
		return CustomReport{}, err
	}

	// Filters are validated against the engine the report runs on, which is
	// why this needs the source store rather than only the report.
	driver := DriverMySQL
	if sources != nil {
		if d, ok := sources.DriverOf(r.SourceID); ok {
			driver = d
		}
	}
	params, err := CleanParams(driver, r.SQL, r.Params)
	if err != nil {
		return CustomReport{}, err
	}
	r.Params = params

	columns, err := CleanColumns(r.Columns)
	if err != nil {
		return CustomReport{}, err
	}
	r.Columns = columns

	chart, err := CleanChart(r.Chart)
	if err != nil {
		return CustomReport{}, err
	}
	r.Chart = chart

	detail, err := CleanDetail(r.Detail)
	if err != nil {
		return CustomReport{}, err
	}
	r.Detail = detail
	if detail != nil {
		// The detail query has to bind as well as the main one, and against
		// the same driver -- so a SQL Server report with a detail is checked
		// for the placeholder style here rather than when somebody clicks a
		// row and gets a syntax error about a query they did not write.
		if _, _, err := BindFor(driver, detail.SQL, detailParams(r.Params), nil); err != nil {
			return CustomReport{}, fmt.Errorf("the detail query: %w", err)
		}
	}

	// And the query has to survive binding. A report that saves cleanly but
	// cannot be bound is a report that fails the first time somebody opens it,
	// with an error about a query they may not have written.
	if _, _, err := BindFor(driver, r.SQL, r.Params, nil); err != nil {
		return CustomReport{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UTC()
	existing, isEdit := c.reports[r.ID]
	if r.ID == "" || !isEdit {
		id, err := newCustomID()
		if err != nil {
			return CustomReport{}, err
		}
		r.ID = id
		r.CreatedAt = now
	} else {
		r.CreatedAt = existing.CreatedAt
	}
	r.UpdatedAt = now

	c.reports[r.ID] = r
	if !isEdit {
		c.order = append(c.order, r.ID)
	}
	c.sortLocked()
	if err := c.saveLocked(); err != nil {
		return CustomReport{}, err
	}
	return r, nil
}

// Delete forgets one.
func (c *CustomStore) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.reports[id]; !ok {
		return errors.New("no such report")
	}
	delete(c.reports, id)
	out := c.order[:0]
	for _, x := range c.order {
		if x != id {
			out = append(out, x)
		}
	}
	c.order = out
	return c.saveLocked()
}

// UsesSource reports which saved reports depend on a connection.
//
// Asked before a connection is deleted, so the answer is "three reports use
// this" rather than three reports silently breaking.
func (c *CustomStore) UsesSource(sourceID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for _, id := range c.order {
		if c.reports[id].SourceID == sourceID {
			out = append(out, c.reports[id].Title)
		}
	}
	return out
}

// RunSaved executes a saved report against its own connection, with the filter
// values somebody chose.
//
// values may be nil or partial; every filter not given one falls back to its
// default, which is how the shipped reports behave and what makes an unfiltered
// report the same thing as one whose boxes are empty.
func (c *CustomStore) RunSaved(ctx context.Context, sources *SourceStore, id string,
	values map[string]string, page, perPage int) (*CustomResult, error) {
	defer obs.Track(ctx, obs.DB)()

	r, ok := c.Get(id)
	if !ok {
		return nil, errors.New("no such report")
	}
	if sources == nil || !sources.Has(r.SourceID) {
		return nil, fmt.Errorf("the connection this report used is gone; "+
			"open %q in the builder and choose another", r.Title)
	}
	driver, _ := sources.DriverOf(r.SourceID)
	bound, args, err := BindFor(driver, r.SQL, r.Params, values)
	if err != nil {
		return nil, err
	}
	res, err := sources.RunCustomWith(ctx, r.SourceID, bound, args, page, perPage)
	if err != nil {
		return nil, err
	}
	// What the columns are called is part of the report, not part of the
	// query, so it is applied here rather than in the runner -- which has no
	// business knowing that reports have headings.
	res.Describe(r.Columns)
	return res, nil
}

func newCustomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reports: generating an id: %w", err)
	}
	return "rep_" + hex.EncodeToString(b), nil
}
