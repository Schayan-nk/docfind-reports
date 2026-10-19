package find

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/reports"
	snap "github.com/Schayan-nk/docfind-reports/internal/snapshot"
)

// The reporting snapshot: the last good answer to every report page, chart
// and monthly series anyone has asked for, served at once and refreshed
// behind the answer (internal/snapshot). A dashboard opens in the time it
// takes to read a file, whether the database is fast, slow or down;
// what it shows says how old it is.
//
// Off -- every request goes to the database -- until
// UseReportSnapshots is called.
type reportSnapshots struct {
	run     *snap.Store[*reports.Result]
	chart   *snap.Store[*reports.ChartData]
	monthly *snap.Store[*reports.MonthlyData]
}

// UseReportSnapshots keeps the snapshots in dir, refreshing any answer older
// than fresh, and writes them to disk every half minute until ctx ends.
func (s *Server) UseReportSnapshots(ctx context.Context, dir string, fresh time.Duration) {
	rs := &reportSnapshots{
		run:     snap.New[*reports.Result](filepath.Join(dir, "report-pages.json"), fresh, 400),
		chart:   snap.New[*reports.ChartData](filepath.Join(dir, "report-charts.json"), fresh, 200),
		monthly: snap.New[*reports.MonthlyData](filepath.Join(dir, "report-monthly.json"), fresh, 200),
	}
	rs.run.KeepFlushed(ctx, 30*time.Second)
	rs.chart.KeepFlushed(ctx, 30*time.Second)
	rs.monthly.KeepFlushed(ctx, 30*time.Second)
	s.snaps = rs
}

// snapKey is a report and every parameter that changes its answer, in a
// fixed order.
func snapKey(id string, params map[string]string, extra ...any) string {
	v := url.Values{}
	for k, p := range params {
		v.Set(k, p)
	}
	return fmt.Sprint(id, "|", v.Encode(), "|", extra)
}

// dataSource is what a response says about where its figures came from.
func dataSource(m snap.Meta) map[string]any {
	return map[string]any{"source": m.Source, "at": m.At, "stale": m.Stale, "unreachable": m.Unreachable,
		"state": freshState(m)}
}

// liveOnly says a report is not kept in the snapshots: one reading another
// server ("-- source: finance"). A figure from another system, a balance
// above all, is one the reader should see as it is now or not at all.
func liveOnly(rep reports.Report) bool { return rep.Source != "" }

func (s *Server) snapRun(ctx context.Context, rep reports.Report, params map[string]string, page, perPage int) (*reports.Result, snap.Meta, error) {
	fetch := func(ctx context.Context) (*reports.Result, error) {
		return s.reportDB.Run(ctx, rep, params, page, perPage)
	}
	if s.snaps == nil || liveOnly(rep) {
		res, err := fetch(ctx)
		return res, snap.Meta{Source: "live", At: time.Now()}, err
	}
	return s.snaps.run.Get(ctx, snapKey(rep.ID, params, page, perPage), fetch)
}

func (s *Server) snapChart(ctx context.Context, rep reports.Report, params map[string]string) (*reports.ChartData, snap.Meta, error) {
	fetch := func(ctx context.Context) (*reports.ChartData, error) { return s.reportDB.Aggregate(ctx, rep, params) }
	if s.snaps == nil || liveOnly(rep) {
		d, err := fetch(ctx)
		return d, snap.Meta{Source: "live", At: time.Now()}, err
	}
	return s.snaps.chart.Get(ctx, snapKey(rep.ID, params), fetch)
}

func (s *Server) snapMonthly(ctx context.Context, rep reports.Report, params map[string]string) (*reports.MonthlyData, snap.Meta, error) {
	fetch := func(ctx context.Context) (*reports.MonthlyData, error) {
		return s.reportDB.MonthlySeries(ctx, rep, params)
	}
	if s.snaps == nil || liveOnly(rep) {
		d, err := fetch(ctx)
		return d, snap.Meta{Source: "live", At: time.Now()}, err
	}
	return s.snaps.monthly.Get(ctx, snapKey(rep.ID, params), fetch)
}
