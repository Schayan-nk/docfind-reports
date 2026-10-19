package find

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/audit"
	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// The report builder's own routes.
//
// Every one of them is behind PermManageSettings, which is the administrator
// permission, and that is not a convenience -- it is the access control for
// the whole feature. What is behind these routes is: connect to an arbitrary
// database with credentials of your choosing, and run SQL against it. Nobody
// below an administrator should be able to reach any of it, so the guard is
// applied in server.go at registration rather than checked inside each
// handler, where a new handler could be added without one.
//
// Kept in a file of its own for the same reason: when somebody adds the next
// route here, the file they are editing is the one whose every line is
// admin-only, rather than reportroutes.go where most routes are not.

// UseBuilder turns the feature on. Without both stores the routes report that
// it is unavailable rather than half-working.
func (s *Server) UseBuilder(sources *reports.SourceStore, custom *reports.CustomStore) {
	s.sources = sources
	s.customReports = custom
	if sources != nil {
		sources.SetGuard(s.builderGuard)
	}
}

// BuilderEnabled reports whether the feature is configured.
func (s *Server) BuilderEnabled() bool {
	return s.sources != nil && s.customReports != nil
}

func (s *Server) builderOff(w http.ResponseWriter) bool {
	if s.BuilderEnabled() {
		return false
	}
	writeJSONError(w, http.StatusNotFound,
		`{"error":"سازندهٔ گزارش روی این سرور تنظیم نشده است."}`)
	return true
}

// builderError answers with a message meant to be read by the person typing.
//
// Deliberately different from the shipped reports, which hide the driver's
// message behind "see the server log". There, the reader is somebody opening a
// report they did not write and the detail is noise that may name tables they
// have no business knowing. Here the reader IS the author, they are mid-edit,
// and the database's own complaint -- unknown column, syntax error at line 3 --
// is the single most useful thing that can be shown to them.
func builderError(w http.ResponseWriter, status int, err error) {
	body, _ := json.Marshal(map[string]string{"error": err.Error()})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

// ---------------------------------------------------------------- sources

func (s *Server) handleBuilderSources(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	writeJSON(w, map[string]any{
		"sources": s.sources.List(),
		"drivers": []map[string]string{
			{"id": string(reports.DriverMySQL), "label": reports.DriverMySQL.Label(),
				"example": "user:password@tcp(db.example.com:3306)/dbname?charset=utf8mb4"},
			{"id": string(reports.DriverSQLServer), "label": reports.DriverSQLServer.Label(),
				"example": "sqlserver://user:password@db.example.com:1433?database=dbname&encrypt=disable"},
		},
	})
}

type sourceSaveRequest struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Note   string `json:"note"`
	DSN    string `json:"dsn"`
}

func (s *Server) handleBuilderSaveSource(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var req sourceSaveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	// The connection's name and driver -- never its DSN, which holds a password.
	audit.Note(r.Context(), "source:"+clipRunes(strings.TrimSpace(req.ID), 80),
		map[string]any{"name": clipRunes(req.Name, 120), "driver": req.Driver, "dsn_changed": req.DSN != ""})
	// Refused at the door as well as at connection time (ssrfguard.go), so a
	// connection that could never be opened is not saved looking usable.
	if dsn := strings.TrimSpace(req.DSN); dsn != "" {
		if err := s.builderGuard(r.Context(), reports.Driver(strings.TrimSpace(req.Driver)), dsn); err != nil {
			builderError(w, http.StatusBadRequest, err)
			return
		}
	}
	pub, err := s.sources.Save(reports.DataSource{
		ID:     strings.TrimSpace(req.ID),
		Name:   req.Name,
		Driver: reports.Driver(strings.TrimSpace(req.Driver)),
		Note:   req.Note,
	}, req.DSN)
	if err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, pub)
}

func (s *Server) handleBuilderDeleteSource(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	audit.Note(r.Context(), "source:"+clipRunes(id, 80), nil)
	// Which reports break if this goes. Answered before deleting rather than
	// after: "three reports use this connection" is something somebody can
	// act on, and three reports that stop working next week is not.
	if used := s.customReports.UsesSource(id); len(used) > 0 &&
		r.URL.Query().Get("force") != "1" {
		writeJSON(w, map[string]any{
			"ok": false, "in_use": used,
			"error": "این اتصال در گزارش‌های زیر استفاده شده است.",
		})
		return
	}
	if err := s.sources.Delete(id); err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

type sourceTestRequest struct {
	Driver string `json:"driver"`
	DSN    string `json:"dsn"`
	// ID lets the button test a SAVED connection whose string the browser
	// does not have -- which is most of them, because the DSN is never sent
	// out. Without this, "test" would only work while typing a new one.
	ID string `json:"id"`
}

func (s *Server) handleBuilderTestSource(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var req sourceTestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	if id := strings.TrimSpace(req.ID); id != "" {
		writeJSON(w, s.sources.TestSaved(r.Context(), id))
		return
	}
	writeJSON(w, s.sources.Test(r.Context(), reports.Driver(strings.TrimSpace(req.Driver)), req.DSN))
}

// ---------------------------------------------------------------- schema

func (s *Server) handleBuilderSchema(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("source"))
	tables, err := s.sources.Schema(r.Context(), id)
	if err != nil {
		builderError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"tables": tables})
}

// ------------------------------------------------------------------- run

type builderRunRequest struct {
	// Source and SQL run something unsaved, which is the builder previewing.
	Source string `json:"source"`
	SQL    string `json:"sql"`
	// Params are the filters a DRAFT declares. Sent with the preview rather
	// than read from a saved report, because the whole point of the preview is
	// to try a filter before committing to it -- and a preview that ignored
	// them would test a different query from the one being written.
	Params []reports.Param `json:"params"`
	// Columns are the headings and hiding a DRAFT declares, for the same
	// reason as Params: the preview has to look like the saved report.
	Columns []reports.ColumnSpec `json:"columns"`
	// Result is the column names the page last got back, sent with a request
	// for suggestions. The query text cannot answer that question -- "SELECT *"
	// names nothing -- so the page reports what it has.
	Result []string `json:"result"`
	// ReportID runs something saved, which is the viewer.
	ReportID string `json:"report_id"`
	// Values are the filter values somebody chose, by parameter name. Missing
	// or blank means "use the default", the same as the shipped reports.
	Values  map[string]string `json:"values"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page"`
}

func (s *Server) handleBuilderRun(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var req builderRunRequest
	// A query can be long -- a join across six tables with a CASE per column
	// runs to pages -- so the limit is higher than the other routes here, and
	// still bounded because this is a text field on the internet.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}

	var res *reports.CustomResult
	var err error
	if id := strings.TrimSpace(req.ReportID); id != "" {
		res, err = s.customReports.RunSaved(r.Context(), s.sources, id, req.Values, req.Page, req.PerPage)
	} else {
		res, err = s.previewDraft(r.Context(), req)
	}
	if err != nil {
		// 400 rather than 502: a query the author got wrong is a bad request,
		// and the page shows the message inline beside the editor instead of
		// as a server failure.
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, res)
}

// previewDraft runs a query that has not been saved, with its draft filters.
//
// The filters go through the same CleanParams the save does, so the preview
// either works or explains itself in exactly the way saving would. The
// alternative -- preview leniently, validate at save -- is the shape where
// somebody gets a working preview and then a refusal, which reads as the
// program changing its mind.
func (s *Server) previewDraft(ctx context.Context, req builderRunRequest) (*reports.CustomResult, error) {
	source := strings.TrimSpace(req.Source)
	driver, ok := s.sources.DriverOf(source)
	if !ok {
		return nil, errors.New("اتصالی برای اجرای این پرس‌وجو انتخاب نشده است.")
	}
	params, err := reports.CleanParams(driver, req.SQL, req.Params)
	if err != nil {
		return nil, err
	}
	bound, args, err := reports.BindFor(driver, req.SQL, params, req.Values)
	if err != nil {
		return nil, err
	}
	res, err := s.sources.RunCustomWith(ctx, source, bound, args, req.Page, req.PerPage)
	if err != nil {
		return nil, err
	}
	// Headings on the preview too, so what the author is looking at is what a
	// reader will see. A preview showing raw column names beside a saved
	// report showing Persian ones is two different reports.
	cols, err := reports.CleanColumns(req.Columns)
	if err != nil {
		return nil, err
	}
	res.Describe(cols)
	return res, nil
}

type builderDetailRequest struct {
	// A saved report's detail, or a draft's.
	ReportID string               `json:"report_id"`
	Source   string               `json:"source"`
	SQL      string               `json:"sql"`
	Params   []reports.Param      `json:"params"`
	Columns  []reports.ColumnSpec `json:"columns"`
	// Key is the clicked row's value, bound as @key. A value from a database
	// cell, arriving back through a browser -- bound as a parameter, never
	// pasted into the query.
	Key     string            `json:"key"`
	Values  map[string]string `json:"values"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page"`
}

// handleBuilderDetail runs a detail query for one row.
func (s *Server) handleBuilderDetail(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var req builderDetailRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}

	var res *reports.CustomResult
	var err error
	if id := strings.TrimSpace(req.ReportID); id != "" {
		res, err = s.customReports.RunDetail(r.Context(), s.sources, id, req.Key,
			req.Values, req.Page, req.PerPage)
	} else {
		// The draft path validates the detail exactly as the save does, so a
		// detail cannot work in the preview and be refused a moment later.
		var spec *reports.DetailSpec
		spec, err = reports.CleanDetail(&reports.DetailSpec{SQL: req.SQL, Key: "row"})
		if err == nil && spec == nil {
			err = errors.New("پرس‌وجوی جزئیات خالی است.")
		}
		if err == nil {
			res, err = reports.RunDetailPreview(r.Context(), s.sources,
				strings.TrimSpace(req.Source), spec.SQL, req.Params, req.Key,
				req.Values, req.Page, req.PerPage)
			if err == nil {
				res.Describe(req.Columns)
			}
		}
	}
	if err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, res)
}

// handleBuilderSuggest completes a filter value from the column behind it.
//
// GET, unlike the other read-in-a-POST routes here: the query text is not
// involved, only a table name, a column name and what somebody has typed so
// far -- short, and none of it secret from the administrator who chose it.
func (s *Server) handleBuilderSuggest(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	q := r.URL.Query()
	values, err := s.sources.SuggestValues(r.Context(),
		strings.TrimSpace(q.Get("source")),
		strings.TrimSpace(q.Get("table")),
		strings.TrimSpace(q.Get("column")),
		q.Get("person") == "1",
		q.Get("q"))
	if err != nil {
		// 200 with an empty list rather than an error: a completion that
		// cannot be offered should leave the box working as a plain text
		// field, not put a red message under something somebody is typing in.
		writeJSON(w, map[string]any{"values": []string{}, "note": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"values": values})
}

// handleBuilderParams reports which filters a draft query asks for.
//
// The browser could look for "@name" itself, and that was the first shape. It
// is wrong for one specific reason: an "@" inside a string literal or a comment
// is not a filter, and a regular expression in the page would find it. The
// scanner that decides this already exists on the server and is the same one
// the binder uses, so asking the server means the list offered is exactly the
// list the binder will demand -- no second opinion to drift.
func (s *Server) handleBuilderParams(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var req builderRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	writeJSON(w, map[string]any{
		// names keeps the query's own order; found carries the filter type each
		// placeholder's clause implies, so a new row arrives already set to
		// what it has to be rather than to plain text.
		"names": reports.DiscoverParams(req.SQL),
		"found": reports.SuggestParams(req.SQL),
		"kinds": reports.BuilderKinds(),
		// The chart types ride along on this call rather than having their own.
		// The page needs both lists before it can render either editor, and
		// two requests to build one panel is two chances to half-load it.
		"charts":        reports.ChartKinds(),
		"chartAggs":     reports.ChartAggs(),
		"chartBuckets":  reports.ChartBuckets(),
		"chartCompares": reports.ChartCompares(),
		// What the columns of the last result will be called, and where each
		// name came from. Sent only when the page says which columns it has,
		// because the query text alone does not say -- "SELECT *" names none
		// of them.
		"columns": reports.SuggestColumns(req.Result, req.Columns),
	})
}

// -------------------------------------------------------- saved reports

func (s *Server) handleBuilderReports(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	writeJSON(w, map[string]any{"reports": s.customReports.List()})
}

func (s *Server) handleBuilderSaveReport(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	var rep reports.CustomReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&rep); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	// The author is taken from the session, never from the request: a field
	// the browser fills in is a field the browser can put somebody else's
	// name in.
	if u := auth.UserFrom(r); u != nil {
		rep.Author = u.Username
	}
	audit.Note(r.Context(), "report:"+clipRunes(rep.ID, 80), map[string]any{"title": clipRunes(rep.Title, 120), "source": rep.SourceID})
	saved, err := s.customReports.Save(rep, s.sources)
	if err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, saved)
}

func (s *Server) handleBuilderDeleteReport(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	audit.Note(r.Context(), "report:"+clipRunes(strings.TrimSpace(r.URL.Query().Get("id")), 80), nil)
	if err := s.customReports.Delete(strings.TrimSpace(r.URL.Query().Get("id"))); err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// -------------------------------------------------------------- export

// handleBuilderExport sends one saved report as CSV.
//
// The same reasoning as the shipped reports' export: a figure somebody has to
// put in a slide is a figure they will otherwise retype, and retyping is where
// numbers change. Capped at the same page depth as the preview, and the cap is
// stated in the file rather than silently applied.
func (s *Server) handleBuilderExport(w http.ResponseWriter, r *http.Request) {
	if s.builderOff(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	rep, ok := s.customReports.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, `{"error":"چنین گزارشی وجود ندارد."}`)
		return
	}
	// Filter values arrive as plain query keys, which is the convention the
	// shipped reports' export already uses. It matters that the download is
	// filtered the same way the table on screen is: a CSV that quietly held
	// more rows than the report somebody was looking at would be worse than no
	// CSV at all.
	values := map[string]string{}
	for k, v := range r.URL.Query() {
		if k != "id" && len(v) > 0 {
			values[k] = v[0]
		}
	}
	res, err := s.customReports.RunSaved(r.Context(), s.sources, id, values, 1, 500)
	if err != nil {
		builderError(w, http.StatusBadRequest, err)
		return
	}
	writeCustomCSV(w, rep, res)
}

// writeCustomCSV sends the rows as a spreadsheet.
//
// A UTF-8 byte-order mark first, and it is not optional: Excel on Windows
// reads a BOM-less UTF-8 CSV as the system codepage, so every Persian column
// heading arrives as mojibake. The same decision, for the same reason, as the
// shipped reports' export.
func writeCustomCSV(w http.ResponseWriter, rep reports.CustomReport, res *reports.CustomResult) {
	name := safeFilename(rep.Title) + "-" + time.Now().Format("20060102") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Write([]byte{0xEF, 0xBB, 0xBF})

	c := csv.NewWriter(w)
	defer c.Flush()

	// The same columns, under the same headings, as the table on screen.
	//
	// Hiding is display-only everywhere else -- the values travel so a chart
	// can plot them and a detail query can key on them -- but a CSV is not a
	// display, it is a copy somebody keeps and sends on. A download holding
	// columns the reader never saw, under raw database names, is not the
	// report they were looking at.
	keep := make([]int, 0, len(res.Columns))
	head := make([]string, 0, len(res.Columns))
	for i, name := range res.Columns {
		if i < len(res.Hidden) && res.Hidden[i] {
			continue
		}
		keep = append(keep, i)
		if i < len(res.Headings) && res.Headings[i] != "" {
			head = append(head, res.Headings[i])
		} else {
			head = append(head, name)
		}
	}
	_ = c.Write(head)

	for _, row := range res.Rows {
		rec := make([]string, 0, len(keep))
		for _, i := range keep {
			if i >= len(row) {
				rec = append(rec, "")
				continue
			}
			switch t := row[i].(type) {
			case nil:
				rec = append(rec, "")
			case string:
				rec = append(rec, t)
			default:
				rec = append(rec, fmt.Sprint(t))
			}
		}
		_ = c.Write(rec)
	}
}

// safeFilename keeps a report title usable as a download name.
//
// Titles are Persian and may contain anything somebody typed, including the
// characters Windows refuses in a filename and the quote that would end the
// Content-Disposition header early.
func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "report"
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"', '\\', '/', ':', '*', '?', '<', '>', '|', '\r', '\n':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > 60 {
		out = string([]rune(out)[:60])
	}
	if out == "" {
		return "report"
	}
	return out
}
