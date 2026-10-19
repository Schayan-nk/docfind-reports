package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// What is in there.
//
// "No coding, only queries" still needs an answer to "queries against what".
// Nobody writes a join without knowing the column is called OWNER_NAME and
// not owner, and the alternative to showing them is a support request or a
// guess.
//
// Read from information_schema, which both engines have and which needs no
// special privilege beyond being able to see the objects. That is deliberate:
// a read-only reporting account will have it, where SHOW TABLES on a
// restricted MySQL user or sys.tables on a locked-down SQL Server may not.

// SchemaTable is one table or view and its columns.
type SchemaTable struct {
	Schema  string         `json:"schema,omitempty"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"` // "table" or "view"
	Rows    int64          `json:"rows,omitempty"`
	Columns []SchemaColumn `json:"columns"`
}

// SchemaColumn is one column.
type SchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

const schemaTimeout = 20 * time.Second

// Schema lists the tables and views a source can see.
//
// Everything in one pair of queries rather than a query per table: a database
// with four hundred tables would otherwise be four hundred round trips, and
// this is opened from a dropdown that has to feel instant.
func (s *SourceStore) Schema(ctx context.Context, sourceID string) ([]SchemaTable, error) {
	defer obs.Track(ctx, obs.DB)()
	db, driver, err := s.pool(sourceID)
	if err != nil {
		return nil, err
	}

	c, cancel := context.WithTimeout(ctx, schemaTimeout)
	defer cancel()

	// The tables. TABLE_SCHEMA is compared against the connection's own
	// database so a MySQL account that can see other schemas does not return
	// every table on the server -- which on a shared MySQL is hundreds of
	// tables from applications this report has nothing to do with.
	tableQ := `
		SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME`
	// TABLE_SCHEMA is selected in the column query as well, and that is not
	// decoration. SQL Server puts tables in several schemas -- this
	// installation has Office.Appointment and BPM.BpmCaseExtraInfo in the same
	// database -- so two tables can share a name. Keying the columns on the
	// name alone merged them: whichever table was indexed last received the
	// other's columns, and the report writer was shown a column list for a
	// table that does not have those columns.
	colQ := `
		SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, ORDINAL_POSITION`
	if driver == DriverSQLServer {
		// SQL Server has no DATABASE(); the connection is already scoped to
		// one database, and the system schemas are excluded by name because
		// they are not what anybody is reporting on.
		tableQ = `
			SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
			FROM information_schema.TABLES
			WHERE TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA')
			ORDER BY TABLE_SCHEMA, TABLE_NAME`
		colQ = `
			SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA')
			ORDER BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION`
	}

	rows, err := db.QueryContext(c, tableQ)
	if err != nil {
		return nil, fmt.Errorf("the table list could not be read: %w", err)
	}
	var out []SchemaTable
	byName := map[string]int{}
	for rows.Next() {
		var sch, name, kind any
		if err := rows.Scan(&sch, &name, &kind); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading the table list: %w", err)
		}
		t := SchemaTable{
			Schema: asString(sch),
			Name:   asString(name),
			Kind:   "table",
		}
		if strings.Contains(strings.ToUpper(asString(kind)), "VIEW") {
			t.Kind = "view"
		}
		byName[t.Schema+"."+t.Name] = len(out)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("reading the table list: %w", err)
	}
	rows.Close()

	if len(out) == 0 {
		return out, nil
	}

	// The columns, attached to the tables above.
	//
	// Read after the table rows are closed, not during: the pool for a source
	// is four connections wide and shared, and holding one open while asking
	// for another is how a dashboard with several sources deadlocks itself.
	// The same mistake was made and fixed in the calendar reader.
	crows, err := db.QueryContext(c, colQ)
	if err != nil {
		// The tables are still worth returning: a list of names with no
		// columns is less useful than both, and much more useful than an
		// error page.
		return out, nil
	}
	defer crows.Close()
	for crows.Next() {
		var sch, tbl, col, typ, nullable any
		if err := crows.Scan(&sch, &tbl, &col, &typ, &nullable); err != nil {
			return out, nil
		}
		i, ok := byName[asString(sch)+"."+asString(tbl)]
		if !ok {
			continue
		}
		out[i].Columns = append(out[i].Columns, SchemaColumn{
			Name:     asString(col),
			Type:     asString(typ),
			Nullable: strings.EqualFold(asString(nullable), "YES"),
		})
	}

	// Tables with columns first, then the rest: a table the account cannot
	// see the columns of is a table it probably cannot read either, and it
	// belongs at the bottom of the list rather than interleaved.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := len(out[i].Columns) > 0, len(out[j].Columns) > 0
		if a != b {
			return a
		}
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
