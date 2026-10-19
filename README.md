# docfind-reports

A self-hosted reporting server written in Go. Write a report as a SQL file and
drop it in a folder: the people you grant it to get it on a Persian dashboard,
with filters and suggestions, a sortable table with totals, a chart, a monthly
summary and a CSV export. The accounts you name also get a report builder in
the browser: connect a MySQL or SQL Server database, write a query, give it
filters, headings, a chart and a detail query, and save it.

It began as the reporting side of DocFind, a document and workflow system, and
runs here on its own: one binary, a folder of `.sql` files and the database
they read. PostgreSQL is optional.

## What it does

**Reports from .sql files**
- One file per report: the query, and in its leading comments its title,
  filters, chart, headings and totals (see [Writing a report](#writing-a-report))
- Filter values are bound as real parameters, never pasted into the SQL: text,
  LIKE fragments, dates in the Jalali or Gregorian calendar, lists, a choice
  from fixed options, and several values at once, with suggestions read from a
  column you name
- Read-only twice over: a report must be a single SELECT to load at all, and it
  runs on an account you give nothing but SELECT
- Re-read from the folder on request, so an edited report takes effect without
  a restart
- A report can read another server instead (`-- source: finance`), a SQL
  Server attached with `DOCFIND_SOURCE_FINANCE_DSN`

**The dashboard**
- Filters, a sortable table with column choice and resizing, a total row, a
  chart (bar, horizontal bar, line, pie, doughnut), a monthly summary and a
  comparison with an earlier period
- Findings under a chart (concentration, outliers, gaps), computed on the
  server and sent nowhere
- People written with their job title after the name folded to one person, in
  the table, the chart and the suggestions alike
- Report families: several reports shown as one, chosen with dropdowns
- A CSV of the whole filtered result, with a byte order mark so Excel reads the
  Persian, and a print layout
- Answers served from a copy on disk while they refresh behind it, so a slow
  database does not make a slow dashboard, and every page says how old its
  figures are

**The builder**
- Connections to MySQL and SQL Server, sealed at rest with a key kept beside
  them. A connection string never reaches the browser, not even for the person
  who typed it
- The tables and columns of the database beside the query, read from
  `information_schema`
- Filters, column headings, a chart, and a detail query run for one row of the
  result
- A connection may reach the network but not this machine itself or a
  link-local address (where cloud metadata lives), unless its host is named in
  `DOCFIND_ALLOWED_HOSTS`; one query at a time per person, and a rate limit on
  every route that runs typed SQL
- Reports saved in the builder stay in the builder, for whoever holds it. To
  give one to everybody, save its query as a `.sql` file in the reports folder

**Accounts and security**
- Roles (administrator, manager, viewer) with per-account allow and deny
  overrides, and one permission per report
- Two-step sign-in with an authenticator app, a code by email or by SMS, and
  passkeys (WebAuthn)
- A sign-in policy (password rules, lockouts, two steps for everyone), the list
  of active sessions, and an audit trail of every change
- A Content-Security-Policy that runs scripts from this server only (the
  pages' own inline scripts included), CSRF and origin checks, and rate limits

## Writing a report

A report is a `.sql` file. Its leading `--` lines describe it; the first line
that is not a comment starts the query, which uses each filter as `@name`:

```sql
-- id: orders
-- title: سفارش‌ها
-- chart: bar | label=owner_name | value=amount | agg=sum
-- monthly: submitted_on | تاریخ ثبت
-- total: amount
-- column: order_no | شماره سفارش
-- param: pstatus | choice | وضعیت
-- option: pstatus | open | باز
-- option: pstatus | shipped | ارسال‌شده
-- param: pfrom | date | از تاریخ | default=year-start
-- param: pto | date | تا تاریخ | default=today
SELECT o.order_no AS order_no, o.owner_name AS owner_name,
       o.submitted_on AS submitted_on, o.amount AS amount
FROM orders o
WHERE o.status LIKE @pstatus
  AND o.submitted_on BETWEEN @pfrom AND @pto
ORDER BY o.submitted_on DESC
```

| Directive | |
|---|---|
| `id`, `title`, `description` | The id is also the permission, `report:<id>`. Default: the file name. |
| `group` | The heading the report is filed under on the dashboard and the permission screen. |
| `param: <name> \| <kind> \| <label>` | A filter. Kinds: `text`, `like` (a fragment; empty means everything), `date`, `list` (written `IN (@name)`), `choice` (one of the options) and `anyof` (several values, compared with `REGEXP`). Options after the label: `default=`, `required=yes`, `calendar=jalali\|gregorian`, and `bind=gregorian` for a date shown in Jalali but stored as a real date. Date defaults may be `today`, `month-start` or `year-start`. |
| `option: <param> \| <value> \| <label>` | One entry of a `choice` or `list` dropdown, in the order written. |
| `suggest: <param> \| <table> \| <column> [\| person]` | Completions for a filter, read from one column. `person` folds job titles out of the names offered. |
| `chart: <kind> \| label=<col> \| value=<col>` | `bar`, `hbar`, `line`, `pie` or `doughnut`. `agg=count` or `agg=sum` when the query returns one row per record rather than one per bar; `normalise=person` to fold names first. |
| `monthly: <col> \| <label> [\| calendar=gregorian]` | The date column the monthly summary counts by. `monthly: none \| <why>` says why a report has none. |
| `column: <col> \| <heading>` | A column's heading. A column the query aliased into Persian keeps its own. |
| `hide: <col>, ...`, `show: <col>, ...` | Columns kept off the table and the CSV, or forced back on. |
| `total: <col>, ...` | The columns a total row adds up, over the whole result. |
| `person: <col> [\| <col>]` | Columns holding people's names, folded in the table too. |
| `family: <id> \| <title> \| <label>` and `variant: <choice>` | Several reports shown as one, chosen with a dropdown labelled `<label>`. |
| `source: <name>` | The server the report reads, when it is not the reporting database (SQL Server only, and its query must end in an `ORDER BY`). |

[examples/reports](examples/reports) has four reports that use most of these,
over the invented data in [examples/demo-schema.sql](examples/demo-schema.sql).

## Requirements

- Go 1.26 to build
- MySQL 8 or MariaDB 10.6 or later for the reports in the folder
  (`DOCFIND_DB_*`), on an account that can only read. The builder brings its own
  connections and works without it
- PostgreSQL (optional) for accounts, the searchable audit trail, an editable
  sign-in policy and role templates. Without it accounts are kept in the
  accounts file and the defaults apply
- HTTPS in front of it for anything beyond this machine: passkeys and the
  installed app need a secure page. Use a reverse proxy, or give the server a
  certificate with `DOCFIND_TLS_CERT` and `DOCFIND_TLS_KEY`

## Quick start

```sh
go build -o docfind-reports ./cmd/reports

cp .env.example .env        # then set DOCFIND_DB_* at least

# The first administrator, typed at the terminal (the password is not echoed):
./docfind-reports -users data/users.json -add-user admin -role admin

# The builder is given to named accounts, an administrator's included:
./docfind-reports -users data/users.json -set-grant admin -perm builder -state allow

# On this machine only, to try it with the examples:
./docfind-reports -users data/users.json -reports examples/reports

# For the network, once an account exists:
./docfind-reports -users data/users.json -reports /srv/reports -bind 0.0.0.0 -port 8320
```

To try the examples, load `examples/demo-schema.sql` into an empty database
(its header shows how) and point `DOCFIND_DB_*` at an account that can read
it.

The server refuses to listen beyond this machine while the accounts file is
empty, so the first administrator cannot be created over the network by
whoever gets there first. A missing reports folder or an unreachable database
does not stop it starting: the dashboard says what is not configured.

## Permissions

| Permission | Who has it by default |
|---|---|
| `page:dashboard`, `action:export` | everyone |
| `report:<id>` (one report) | administrators; everyone else is granted reports one at a time |
| `action:reports_reload` (re-read the folder) | managers and administrators |
| `page:builder`, `action:builder_run`, `action:builder_save`, `action:sources`, `action:builder_export` | nobody: granted per account |
| `action:manage_users`, `action:manage_settings` | administrators |

The builder reaches any database a connection names, so no role carries it,
not even an administrator's. Grant it in Settings (nobody may change their own
permissions, so that takes a second administrator), or from the command line
with the server stopped, which works with or without PostgreSQL. `-perm`
takes a comma-separated list, and `builder` stands for the builder's five.

## Configuration

Everything is read from the environment, or from a `.env` file beside the
binary (`-env-file` to name another). Nothing secret belongs in the code or in
a config file under version control. [.env.example](.env.example) lists every
variable with what it does; the main ones:

| Variable | Purpose |
|---|---|
| `DOCFIND_DB_HOST`, `DOCFIND_DB_USER`, `DOCFIND_DB_PASSWORD`, `DOCFIND_DB_NAME` | The reporting database, or `DOCFIND_DB_DSN` for a whole MySQL connection string. |
| `DOCFIND_SOURCE_<NAME>_DSN` | Another server reports may read with `-- source: <name>`, as a `sqlserver://` URL. |
| `DOCFIND_ALLOWED_HOSTS` | Hosts the builder's connections may reach whatever they resolve to, such as a database on this same machine. |
| `DOCFIND_REPORT_REFRESH_SECONDS` | How old an answer may be before it is refreshed, 60 to 300. Default 120. |
| `DOCFIND_PG_HOST`, `DOCFIND_PG_PORT`, `DOCFIND_PG_USER`, `DOCFIND_PG_PASSWORD`, `DOCFIND_PG_DATABASE`, `DOCFIND_PG_SSLMODE` | The optional database. The schema is created and upgraded on start. |
| `DOCFIND_PASSKEY_RPID`, `DOCFIND_PASSKEY_ORIGINS` | The host name passkeys are bound to, and the origins they are used from. |

Files the server keeps beside the accounts file (`-users`): the session
signing key, the active sessions, profile pictures, the key that seals
two-step secrets, the builder's connections (`datasources.json`) and the key
that seals them (`datasources.json.key`), its saved reports
(`customreports.json`), the report snapshots, and by default the logs. Back
that folder up, with the database.

## Command line

| Flag | |
|---|---|
| `-users <file>` | The accounts file. Required. |
| `-reports <folder>` | The `.sql` reports. Default: `reports`. |
| `-bind <address>`, `-port <n>` | Where to listen. Default 127.0.0.1:8320. |
| `-trust-proxy-headers` | Take the client address from X-Forwarded-For. Only behind a proxy you control. |
| `-add-user <name> -role admin\|manager\|viewer` | Create an account, or reset its password, then exit. |
| `-set-grant <name> -perm <id>[,<id>...] -state allow\|deny\|inherit` | Set permission overrides, with the server stopped. `builder` names the builder's five. |
| `-set-phone <name> -phone <number>` | Set an account's mobile number in PostgreSQL, with the server stopped. Without a database, set it in Settings. |
| `-env-file <path>`, `-env-file-wins` | Which `.env` to read, and whether it overrides the real environment. |
| `-version` | Print the version. |

## Layout

| Path | |
|---|---|
| `cmd/reports` | The binary: options, start-up, the reporting database and the builder's stores |
| `internal/reports` | The catalogue, binding, running, charts, the monthly summary, the builder's connections and reports |
| `internal/find` | The HTTP server and its pages |
| `internal/snapshot` | The answers kept on disk |
| `internal/auth` | Accounts, sessions, roles, permissions, the sign-in policy |
| `internal/pgstore` | PostgreSQL access and the numbered migrations |
| `internal/webauthn` | Passkeys |
| `internal/audit`, `internal/obs` | The audit trail, request logs and metrics |
| `internal/format` | The Jalali calendar and Persian digits |
| `examples` | Example reports and the demo data they read |

## Development

```sh
go vet ./...
go test ./...
```

The tests run the real router, and the builder's tests the real stores, in
temporary folders. The tests that need a database run when one is configured
and skip otherwise: `DOCFIND_PG_*` for PostgreSQL (use one you can throw
away, and run them one package at a time, `go test -p 1 ./...`), and
`DOCFIND_DB_*` or `DOCFIND_DB_DSN` for a MySQL holding the demo data.

## License

MIT. See [LICENSE](LICENSE). The bundled third-party files keep their own
licences; see [THIRD-PARTY.md](THIRD-PARTY.md).
