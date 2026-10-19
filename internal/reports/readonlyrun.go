package reports

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Making "the report builder only reads" true on the database, not only in
// the text check.
//
// sqlguard.go decides whether a query's TEXT may run: one statement, opening
// with SELECT or WITH, no denied word, no procedure. That is the first line
// and it is a good one, but it is text inspection, and text inspection is one
// clever query away from being wrong. The accounts behind the builder's
// connections are whatever somebody typed into the form -- on this site one of
// them is sa -- so a gap in the text check would be a write, or worse, with
// that account's rights.
//
// So every builder query now also runs INSIDE A TRANSACTION THAT NEVER
// COMMITS:
//
//   - MySQL: START TRANSACTION READ ONLY. The server itself refuses any write
//     inside it, and max_execution_time makes the server stop a runaway
//     SELECT rather than only the client giving up on it.
//   - SQL Server has no read-only transaction, so the query runs in an
//     ordinary one that is ALWAYS rolled back. Whatever slipped past the text
//     check is undone -- rows, and the DDL SQL Server treats as transactional.
//
// It does not make a powerful account safe -- nothing rolls back xp_cmdshell,
// which is why the text check refuses every procedure -- so the builder also
// says, when a connection is tested, whether its account can write
// (AccountCanWrite), and a read-only account is the real fix.

// queryReadOnly runs query on a connection of its own, inside a transaction
// that can never commit. done must be called when the rows have been read.
func queryReadOnly(ctx context.Context, db *sql.DB, driver Driver, query string, args ...any) (*sql.Rows, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: driver == DriverMySQL})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if driver == DriverMySQL {
		if dl, ok := ctx.Deadline(); ok {
			ms := max(int64(1000), time.Until(dl).Milliseconds())
			_, _ = tx.ExecContext(ctx, fmt.Sprintf("SET SESSION max_execution_time = %d", ms))
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		conn.Close()
		return nil, nil, err
	}
	done := func() {
		rows.Close()
		_ = tx.Rollback() // never Commit: this is the point
		conn.Close()
	}
	return rows, done, nil
}

// AccountCanWrite reports whether the account a connection signs in with can
// change data, and why -- so the builder can say "use a read-only account"
// at the moment somebody tests one.
func AccountCanWrite(ctx context.Context, db *sql.DB, driver Driver) (bool, string) {
	switch driver {
	case DriverSQLServer:
		var sysadmin, owner, writer, alter sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT IS_SRVROLEMEMBER('sysadmin'),
			IS_ROLEMEMBER('db_owner'), IS_ROLEMEMBER('db_datawriter'),
			HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'ALTER')`).Scan(&sysadmin, &owner, &writer, &alter)
		if err != nil {
			return false, ""
		}
		switch {
		case sysadmin.Int64 == 1:
			return true, "sysadmin on the whole server"
		case owner.Int64 == 1:
			return true, "db_owner of this database"
		case writer.Int64 == 1:
			return true, "db_datawriter of this database"
		case alter.Int64 == 1:
			return true, "may ALTER this database"
		}
	case DriverMySQL:
		rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
		if err != nil {
			return false, ""
		}
		defer rows.Close()
		for rows.Next() {
			var g string
			if rows.Scan(&g) != nil {
				continue
			}
			up := strings.ToUpper(g)
			for _, p := range []string{"ALL PRIVILEGES", "SUPER", " INSERT", " UPDATE", " DELETE", " DROP", " ALTER", " CREATE"} {
				if strings.Contains(up, p) {
					return true, strings.TrimSpace(p) + " granted"
				}
			}
		}
	}
	return false, ""
}
