package find

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// UseReports attaches the report catalog and reporting database.
//
// Both are optional: docfind without them is the document browser it always
// was, and a failure to reach the reporting database must not stop people
// searching documents.
func (s *Server) UseReports(cat *reports.Catalog, db *reports.DB) {
	s.reports = cat
	s.reportDB = db
}

// ReportsEnabled reports whether the dashboard has anything to show.
func (s *Server) ReportsEnabled() bool { return s.reports != nil && s.reportDB != nil }

// visibleReports filters the catalog to what this request may see.
//
// EVERY ROLE BUT ADMINISTRATOR IS GRANTED REPORTS ONE AT A TIME, managers
// included: "may open the reports page" is a separate question from "may see
// these figures", which is the distinction the whole model exists for.
//
// Filtering happens here rather than in the page, so a report somebody was not
// granted is not merely missing from a menu -- mayRunReport refuses it by id
// as well, and both ask the same function.
func (s *Server) visibleReports(r *http.Request) []reports.Report {
	all := s.reports.List()
	if !s.AuthEnabled() {
		return all
	}
	if auth.UserFrom(r) == nil {
		return nil
	}
	// One permission per report, from the catalogue. This replaced
	// User.CanViewReport, which consulted the old ReportIDs allowlist -- a
	// field the startup migration now empties, so leaving this on it would
	// have shown every viewer an empty dashboard while administrators saw
	// everything and nobody could tell why.
	out := make([]reports.Report, 0, len(all))
	for _, rep := range all {
		if s.can(r, auth.ReportPerm(rep.ID)) {
			out = append(out, rep)
		}
	}
	return out
}

// mayRunReport reports whether this request may execute a given report.
//
// THE LIST AND THE RUN MUST AGREE, and they do because both ask s.can. A
// report hidden from the dashboard but runnable by id would be a permission
// that only hides things, which is the failure the whole model is built to
// avoid.
func (s *Server) mayRunReport(r *http.Request, id string) bool {
	if !s.AuthEnabled() {
		return true
	}
	if auth.UserFrom(r) == nil {
		return false
	}
	return s.can(r, auth.ReportPerm(id))
}

// handleReportList returns the reports this user may open, with their filters
// so the page can build the controls. Never includes the SQL.
func (s *Server) handleReportList(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		writeJSON(w, map[string]any{"enabled": false, "reports": []any{}})
		return
	}
	out := map[string]any{"enabled": true, "reports": s.visibleReports(r)}
	writeJSON(w, out)
}

// reportRunRequest is what the dashboard posts to run one widget.
type reportRunRequest struct {
	ID      string            `json:"id"`
	Params  map[string]string `json:"params"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page"`
}

// handleReportRun executes one report and returns a page of rows.
func (s *Server) handleReportRun(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, `{"error":"گزارش‌گیری روی این سرور تنظیم نشده است."}`, http.StatusNotFound)
		return
	}
	var req reportRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	rep, ok := s.reports.Get(req.ID)
	if !ok {
		http.Error(w, `{"error":"چنین گزارشی وجود ندارد."}`, http.StatusNotFound)
		return
	}
	// Checked after the lookup but before running: an unauthorised user gets
	// the same "forbidden" whether or not the report exists.
	if !s.mayRunReport(r, req.ID) {
		http.Error(w, `{"error":"برای این کار دسترسی ندارید."}`, http.StatusForbidden)
		return
	}

	params := req.Params
	res, src, err := s.snapRun(r.Context(), rep, params, req.Page, req.PerPage)
	if err != nil {
		// The driver's message can name tables and columns; useful in the log,
		// not something to hand to a browser on a public listener.
		logReportError(rep.ID, err)
		http.Error(w, `{"error":"اجرای گزارش انجام نشد؛ شرح خطا در لاگ سرور است."}`, http.StatusBadGateway)
		return
	}
	shown, labels, rows := tableOf(rep, res)

	// «جمع کل»: the whole result's total, when the page on screen is not the
	// whole result (reporttotals.go).
	var totals map[string]string
	if res.HasMore || res.Page > 1 {
		totals = s.reportTotals(r.Context(), rep, params)
	}

	writeJSON(w, map[string]any{
		"totals":  totals,
		"id":      rep.ID,
		"title":   rep.Title,
		"chart":   rep.Chart,
		"label":   rep.LabelColumn,
		"value":   rep.ValueColumn,
		"columns": shown,
		// Persian headings for the columns the query did not already alias.
		"labels":  labels,
		"rows":    rows,
		"page":    res.Page,
		"perPage": res.PerPage,
		"hasMore": res.HasMore,
		"tookMs":  res.TookMs,
		// Live, or the last good copy and how old it is.
		"data_source": dataSource(src),
	})
}

// tableOf is a result as the reader is shown it: the columns they may see,
// with Persian headings, and the rows narrowed to them.
func tableOf(rep reports.Report, res *reports.Result) (shown []string, labels map[string]string, rows [][]any) {
	// Drop the columns the report hides before the result leaves the process.
	// They are selected because the joins need them, not because anyone reads
	// them, and a column of internal ids pushes the useful ones off screen.
	keep, headings := reports.Visible(rep, res.Columns)
	// A column that is MySQL's zero date on every row of the page carries no
	// information: a reminder time nobody filled in is "0000-00-00 00:00:00"
	// on most rows, and a column of impossible dates is one every reader has to
	// learn to ignore. Individual zero dates elsewhere are blanked rather than
	// printed.
	var kept []int
	var keptHeadings []string
	for i, col := range keep {
		if reports.AllZeroDates(res.Rows, col) {
			continue
		}
		kept = append(kept, col)
		keptHeadings = append(keptHeadings, headings[i])
	}
	keep, headings = kept, keptHeadings

	rows = make([][]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		projected := reports.Project(row, keep)
		reports.BlankZeroDates(projected)
		rows = append(rows, projected)
	}
	// The chart addresses its columns by name, so it has to work against the
	// same narrowed set the table shows.
	shown = make([]string, 0, len(keep))
	for _, i := range keep {
		shown = append(shown, res.Columns[i])
	}
	labels = make(map[string]string, len(shown))
	for i, c := range shown {
		labels[c] = headings[i]
	}
	return shown, labels, rows
}

// handleReportChart returns aggregated chart data for one report.
//
// Separate from handleReportRun because the two answer different questions:
// the table shows one page, the chart summarises the whole filtered result.
// Deriving the chart from the page would draw "the first hundred rows" while
// looking exactly like "everything", which is the sort of chart that gets a
// decision made on the wrong number.
func (s *Server) handleReportChart(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, `{"error":"گزارش‌گیری روی این سرور تنظیم نشده است."}`, http.StatusNotFound)
		return
	}
	var req reportRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	rep, ok := s.reports.Get(req.ID)
	if !ok {
		http.Error(w, `{"error":"چنین گزارشی وجود ندارد."}`, http.StatusNotFound)
		return
	}
	if !s.mayRunReport(r, req.ID) {
		http.Error(w, `{"error":"برای این کار دسترسی ندارید."}`, http.StatusForbidden)
		return
	}
	if rep.Chart == "" {
		http.Error(w, `{"error":"این گزارش نمودار ندارد."}`, http.StatusNotFound)
		return
	}

	data, src, err := s.snapChart(r.Context(), rep, req.Params)
	if err != nil {
		logReportError(rep.ID+" (chart)", err)
		http.Error(w, `{"error":"ساخت نمودار انجام نشد؛ شرح خطا در لاگ سرور است."}`, http.StatusBadGateway)
		return
	}
	// Adding euros to rials produces a number that looks authoritative and
	// means nothing. Refuse rather than draw it: a missing chart with a stated
	// reason is recoverable, a confident wrong total on a management dashboard
	// is not.
	if data.MixedCurrency {
		logReportError(rep.ID+" (chart)", fmt.Errorf(
			"value column %q mixes currencies (%s); refusing to sum them",
			rep.ValueColumn, data.Currency))
		writeJSON(w, map[string]any{
			"id":      rep.ID,
			"chart":   rep.Chart,
			"labels":  []string{},
			"values":  []float64{},
			"problem": "مقادیر این ستون با واحدهای پولی متفاوت ثبت شده‌اند و جمع آنها معتبر نیست.",
		})
		return
	}

	category := reports.LabelFor(rep, rep.LabelColumn)
	writeJSON(w, map[string]any{
		"id":          rep.ID,
		"chart":       rep.Chart,
		"label":       category,
		"value":       chartValueName(rep),
		"labels":      data.Labels,
		"values":      data.Values,
		"rows":        data.Rows,
		"groups":      data.Groups,
		"other":       data.Other,
		"capped":      data.Capped,
		"unparsed":    data.Unparsed,
		"currency":    data.Currency,
		"data_source": dataSource(src),
		// Absent unless the report asked to be broken down, in which case the
		// browser draws these instead of the flat Values -- which stay, because
		// the bar totals and the "other" fold are still computed from them.
		"series": data.Series,
		// Computed from the aggregate that was just built, so it costs one
		// pass over at most twenty numbers -- no second query, no round trip.
		"insights": reports.Analyse(data, category, chartValueName(rep)),
		"tookMs":   data.TookMs,
	})
}

// chartValueName is what the value axis is called.
//
// A counted chart has no value column to take a name from, so it is described
// by what it counts rather than left blank.
func chartValueName(rep reports.Report) string {
	if strings.EqualFold(rep.ChartAgg, "count") {
		return "تعداد"
	}
	return reports.LabelFor(rep, rep.ValueColumn)
}

// handleReportMonthly returns the monthly summary drawn above a report.
//
// Takes the same parameters as running the report, and applies them, so the
// banner always describes the rows the reader is actually looking at rather
// than the unfiltered table.
func (s *Server) handleReportMonthly(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, `{"error":"گزارش‌گیری روی این سرور تنظیم نشده است."}`, http.StatusNotFound)
		return
	}
	var req reportRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	rep, ok := s.reports.Get(req.ID)
	if !ok {
		http.Error(w, `{"error":"چنین گزارشی وجود ندارد."}`, http.StatusNotFound)
		return
	}
	if !s.mayRunReport(r, req.ID) {
		http.Error(w, `{"error":"برای این کار دسترسی ندارید."}`, http.StatusForbidden)
		return
	}
	// A report with no date per row still answers, with the reason it cannot be
	// compared rather than a 404 the page would render as a blank strip.
	//
	// Three of the ten cannot, for three different reasons, and the reasons are
	// worth reading: one returns totals per expert with no case date in them,
	// one has a date column filled on 41 rows out of 306, and one was measured
	// and found to have no date column at all. A reader who sees nothing
	// assumes the feature is broken; a reader who is told why can go and fix
	// the underlying gap.
	if rep.MonthlyColumn == "" {
		reason := rep.MonthlyWhyNot
		if reason == "" {
			reason = "این گزارش ستون تاریخی ندارد که بتوان ماه‌به‌ماه مقایسه‌اش کرد."
		}
		writeJSON(w, &reports.MonthlyData{
			ID:      rep.ID,
			Compare: &reports.PeriodCompare{Reason: reason},
		})
		return
	}
	data, src, err := s.snapMonthly(r.Context(), rep, req.Params)
	if err != nil {
		logReportError(rep.ID+" (monthly)", err)
		http.Error(w, `{"error":"نمودار ماهانه ساخته نشد"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, struct {
		*reports.MonthlyData
		DataSource map[string]any `json:"data_source"`
	}{data, dataSource(src)})
}

// handleReportSuggest returns completions for one filter box.
//
// Gated exactly like running the report: the values it returns are staff and
// customer names out of the same tables, so anyone who may not see the report
// may not mine it for names either.
func (s *Server) handleReportSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, `{"error":"گزارش‌گیری روی این سرور تنظیم نشده است."}`, http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	rep, ok := s.reports.Get(q.Get("id"))
	if !ok {
		http.Error(w, `{"error":"چنین گزارشی وجود ندارد."}`, http.StatusNotFound)
		return
	}
	if !s.mayRunReport(r, rep.ID) {
		http.Error(w, `{"error":"برای این کار دسترسی ندارید."}`, http.StatusForbidden)
		return
	}

	name := q.Get("param")
	var src *reports.SuggestSource
	for _, p := range rep.Params {
		if p.Name == name {
			src = p.Suggest
			break
		}
	}
	// The source is looked up from the report's own declaration, never taken
	// from the request: a caller may name a parameter, not a table.
	if src == nil {
		http.Error(w, `{"error":"برای این فیلتر پیشنهادی وجود ندارد."}`, http.StatusNotFound)
		return
	}

	typed := q.Get("q")
	if len(typed) > 128 {
		typed = typed[:128]
	}
	values, err := s.reportDB.SuggestWhere(r.Context(), *src, typed, nil)
	if err != nil {
		logReportError(rep.ID+" (suggest)", err)
		writeJSON(w, map[string]any{"values": []string{}})
		return
	}
	writeJSON(w, map[string]any{"values": values})
}

// exportFailedPage is what a browser that navigated to an export is shown when
// the query fails before its first row: the page it came from is one click
// away, which a line of JSON in its place would not be.
var (
	exportFailedPage = buildErrorPage("502", "خروجی گرفته نشد",
		"اجرای این گزارش به نتیجه نرسید. شرح خطا در لاگ سرور ثبت شد؛ چند لحظه بعد دوباره تلاش کنید.")
	exportFailedCSP = errorPageCSP(exportFailedPage)
)

// handleReportExport streams a report as CSV.
//
// Streamed rather than assembled: an export is the one place where the whole
// result set is wanted, and holding tens of thousands of rows in memory to
// build a string would be the one thing that makes this process fall over.
func (s *Server) handleReportExport(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, "reporting is not configured", http.StatusNotFound)
		return
	}
	id := r.URL.Query().Get("id")
	rep, ok := s.reports.Get(id)
	if !ok {
		http.Error(w, "no such report", http.StatusNotFound)
		return
	}
	if !s.mayRunReport(r, id) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	values := map[string]string{}
	for k, v := range r.URL.Query() {
		if k != "id" && len(v) > 0 {
			values[k] = v[0]
		}
	}

	// The file starts with its first row rather than before the query, so a
	// database that cannot be reached at all is still an error response of its
	// own instead of a download with nothing in it.
	started := false
	start := func() {
		filename := fmt.Sprintf("%s-%s.csv", rep.ID, time.Now().Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))
		// Excel opens a UTF-8 CSV as mojibake unless it sees a byte order
		// mark, and these reports are mostly Persian.
		w.Write([]byte{0xEF, 0xBB, 0xBF})
		started = true
	}

	cw := csv.NewWriter(w)
	var keep []int
	wroteHeader := false
	err := s.reportDB.Stream(r.Context(), rep, values, func(cols []string, row []any) error {
		if !started {
			start()
		}
		if !wroteHeader {
			// Same columns the screen shows. Exporting the ones it hides
			// would quietly put internal identifiers into a spreadsheet
			// somebody emails on.
			var headings []string
			keep, headings = reports.Visible(rep, cols)
			if err := cw.Write(headings); err != nil {
				return err
			}
			wroteHeader = true
		}
		shown := reports.Project(row, keep)
		text := make([]string, len(shown))
		for i, v := range shown {
			if v == nil {
				continue
			}
			text[i] = fmt.Sprint(v)
		}
		if err := cw.Write(text); err != nil {
			return err
		}
		// Flush periodically so a long export starts arriving immediately
		// rather than sitting in a buffer until the query finishes.
		cw.Flush()
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return cw.Error()
	})
	if err != nil {
		logReportError(rep.ID, err)
		if !started {
			writeErrorPage(w, r, http.StatusBadGateway, exportFailedPage, exportFailedCSP,
				`{"error":"اجرای گزارش انجام نشد؛ شرح خطا در لاگ سرور است."}`)
			return
		}
		// Rows are already sent, so the only honest signal left is to write
		// the problem into the file itself rather than pretend it completed.
		cw.Write([]string{"ERROR", "the export stopped early; see the server log"})
	}
	if !started {
		// No rows: an empty file, still a CSV.
		start()
	}
	cw.Flush()
}

// handleReportReload re-reads the report directory, so an edited query takes
// effect without restarting the server.
func (s *Server) handleReportReload(w http.ResponseWriter, r *http.Request) {
	if !s.ReportsEnabled() {
		http.Error(w, `{"error":"گزارش‌گیری روی این سرور تنظیم نشده است."}`, http.StatusNotFound)
		return
	}
	errs := s.reports.Reload()
	// Names cached from before the reload may come from a table a rewritten
	// report no longer uses, so a reload drops them too.
	reports.ResetSuggestCache()
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	writeJSON(w, map[string]any{"count": s.reports.Count(), "problems": msgs})
}

func logReportError(id string, err error) {
	fmt.Printf("report %s failed: %v\n", id, strings.TrimSpace(err.Error()))
}
