package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"errors"
	"fmt"
	"strings"
)

// Master and detail: a row you can open.
//
// Click an order, see its line items: a second query, run for one row of the
// first, with that row's key bound into it.
//
// A second QUERY, deliberately, and not a join. A join is the obvious first
// answer and it is the wrong one for this: joining the detail into the master
// multiplies the master rows by their details, so a report of 28 orders with
// 39 line items between them becomes 39 rows in which every order column
// repeats. Every total computed from it is then wrong, every chart over it
// double-counts, and the 500-row page cap starts truncating the master --
// which is why the detail here stays a separate question asked about one row.
//
// The author can still write joins. That is what SQL is for, and both queries
// are their own. What this adds is the relationship between the two.

// detailKeyParam is the placeholder a detail query uses for the row it is
// about.
//
// A fixed name rather than a configured one. The author has already said which
// master column is the key by choosing it; making them also name the
// placeholder would be two ways to say the same thing and one more thing to
// get wrong. "@key" is what they write, every time.
const detailKeyParam = "key"

// DetailSpec is a second query, run for one row of the first.
type DetailSpec struct {
	// SQL must use @key, which binds the value of Key from the clicked row.
	SQL string `json:"sql"`
	// Key names the master column whose value identifies the row. Usually an
	// id, because that is what the detail's WHERE clause can match on.
	Key string `json:"key"`
	// Title heads the expansion. Optional.
	Title string `json:"title,omitempty"`
}

// CleanDetail normalises a detail query and rejects one that cannot work.
func CleanDetail(d *DetailSpec) (*DetailSpec, error) {
	if d == nil {
		return nil, nil
	}
	out := DetailSpec{
		SQL:   strings.TrimSpace(d.SQL),
		Key:   strings.TrimSpace(d.Key),
		Title: strings.TrimSpace(d.Title),
	}
	// No query is how a report says it has no detail, which is most of them.
	if out.SQL == "" {
		return nil, nil
	}
	if out.Key == "" {
		return nil, errors.New("choose which column of the main query identifies a row")
	}
	if err := CheckReadOnly(out.SQL); err != nil {
		return nil, fmt.Errorf("the detail query: %w", err)
	}

	// It has to be ABOUT the row. A detail query with no @key returns the same
	// rows whichever master row is opened, which looks like it works and is
	// never what anybody meant -- so it is refused here rather than shipped as
	// a report that quietly shows the wrong thing under every row.
	uses := false
	for _, name := range DiscoverParams(out.SQL) {
		if name == detailKeyParam {
			uses = true
			break
		}
	}
	if !uses {
		return nil, fmt.Errorf("the detail query has to use @%s, which is the clicked row's %q; "+
			"without it every row would open the same list", detailKeyParam, out.Key)
	}
	return &out, nil
}

// detailParams are the filters a detail query may use: the report's own, plus
// @key.
//
// The report's filters are included because a detail is a view of the same
// data and usually wants the same narrowing -- a date range on the master
// should not silently widen underneath it. @key is appended rather than
// prepended so a report that somehow declares a filter of that name loses to
// the real one; CleanParams refuses that name outright, so this is belt and
// braces rather than a decision.
func detailParams(reportParams []Param) []Param {
	out := make([]Param, 0, len(reportParams)+1)
	for _, p := range reportParams {
		if p.Name == detailKeyParam {
			continue
		}
		out = append(out, p)
	}
	return append(out, Param{Name: detailKeyParam, Kind: KindText})
}

// RunDetail runs a saved report's detail query for one row.
func (c *CustomStore) RunDetail(ctx context.Context, sources *SourceStore, id, keyValue string,
	values map[string]string, page, perPage int) (*CustomResult, error) {
	defer obs.Track(ctx, obs.DB)()

	r, ok := c.Get(id)
	if !ok {
		return nil, errors.New("no such report")
	}
	if r.Detail == nil {
		return nil, errors.New("this report has no detail query")
	}
	if sources == nil || !sources.Has(r.SourceID) {
		return nil, fmt.Errorf("the connection this report used is gone; "+
			"open %q in the builder and choose another", r.Title)
	}
	res, err := runDetailOn(ctx, sources, r.SourceID, r.Detail.SQL, r.Params,
		keyValue, values, page, perPage)
	if err != nil {
		return nil, err
	}
	// The report's own column list applies here too. Most of its entries name
	// master columns the detail does not return, which costs nothing -- what
	// matters is that the report's labels reach the detail's columns, so a
	// column under an opened row is headed the way it is headed in the table
	// above it.
	res.Describe(r.Columns)
	return res, nil
}

// RunDetailPreview runs a detail query that has not been saved yet.
//
// The same binding path as a saved one, on purpose: a detail that works in the
// preview and behaves differently once saved would be worse than one that
// never worked.
func RunDetailPreview(ctx context.Context, sources *SourceStore, sourceID, sql string,
	params []Param, keyValue string, values map[string]string,
	page, perPage int) (*CustomResult, error) {

	if sources == nil {
		return nil, errors.New("no connection")
	}
	return runDetailOn(ctx, sources, sourceID, sql, params, keyValue, values, page, perPage)
}

// runDetailOn is the shared path, so the builder's preview and a saved report
// bind identically. Two code paths here would mean a detail that previews and
// then behaves differently once saved.
func runDetailOn(ctx context.Context, sources *SourceStore, sourceID, sql string,
	params []Param, keyValue string, values map[string]string,
	page, perPage int) (*CustomResult, error) {

	driver, ok := sources.DriverOf(sourceID)
	if !ok {
		return nil, errors.New("that connection no longer exists")
	}
	// The key is a VALUE, bound as a parameter like any other. It arrives from
	// a browser, having arrived there from a database cell, and it is never
	// pasted into the SQL text.
	all := map[string]string{}
	for k, v := range values {
		all[k] = v
	}
	all[detailKeyParam] = keyValue

	bound, args, err := BindFor(driver, sql, detailParams(params), all)
	if err != nil {
		return nil, err
	}
	return sources.RunCustomWith(ctx, sourceID, bound, args, page, perPage)
}
