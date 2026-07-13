# pgtui

A keyboard-first **PostgreSQL administration TUI** for sysadmins — think *k9s,
but for Postgres*. Where most database TUIs are data browsers, pgtui leans into
**operations**: watch and kill sessions, manage roles and grants, create/drop
databases, and inspect cluster health — all with strong guards against
destructive mistakes.

Single static binary, no dependencies, no container required.

<!-- TODO: add a demo GIF here -->

```
 pgtui  v0.4.0                         postgres@db:5432 • admin db: postgres  9level.dev
 1 Dashboard  2 Databases  3 Query  4 Locks  5 Sessions  6 Roles  7 Tuning
 ╭ CONNECTIONS ╮ ╭ CACHE HIT ╮ ╭ STORAGE ─╮ ╭ UPTIME ─╮
 │ 40 / 50     │ │ 100.00%   │ │ 217 MB   │ │ 2h 27m  │
 ╰─────────────╯ ╰───────────╯ ╰──────────╯ ╰─────────╯
```

## Why pgtui?

Tools like `pgcli`, `lazysql` and `rainfrog` are great for **browsing data and
running queries**. pgtui overlaps there (it has a query runner and a read-only
data browser), but its focus is **cluster administration**:

- **Sessions** — see every client backend and `pg_cancel_backend` /
  `pg_terminate_backend` a runaway one.
- **Roles** — create users, grant privileges to databases, and drop roles
  (including a safe *force-drop* that reassigns ownership instead of deleting
  data).
- **Databases** — create and drop databases, browse tables and sizes, and get a
  `\d`-style structure view.
- **Locks** — a blocking tree showing who is waiting on whom.
- **Dashboard** — connections vs `max_connections`, cache hit ratio, uptime,
  replication, longest active query.

It works **without a superuser**: a role with `CREATEROLE`/`CREATEDB` is enough
for the management actions.

## Features

| Tab | What it does |
|-----|--------------|
| **1 · Dashboard** | Cluster health: connections vs `max_connections` (with reserved slots), cache hit ratio, uptime, total size, commits/rollbacks, version, longest active query, replication. A **connection advisor** flags near-limit/idle-dominated/idle-in-transaction situations and tells you what to do. Auto-refresh. |
| **2 · Databases** | Databases (owner, size, connections) → tables → **read-only data browser** (horizontal column scroll `←→`, per-column search `/`, top query bar `e`). Create (`n`) / drop (`D`) databases and `d` for a table's structure (columns, indexes, constraints). |
| **3 · Query** | SQL editor with a paged result grid. `x` runs **EXPLAIN** (plan only). Writes require confirmation; destructive statements (`DROP`/`TRUNCATE`/`DELETE`/`UPDATE` without `WHERE`) require typing `yes`. |
| **4 · Locks** | Blocking tree: which session waits on which. |
| **5 · Sessions** | `pg_stat_activity` with state/wait/duration/query. `c` cancels the query, `k` terminates the connection. |
| **6 · Roles** | Roles with login/super/createdb attributes and their connection limit (`CONN`, ∞ = unlimited). `enter` **manage** the selected role (reset password — generates a random 32-char one, shown once; or set the connection limit), `n` create, `g` grant to a database, `D` drop, `F` **force-drop** (reassign ownership to a successor, then drop — no data loss). |
| **7 · Tuning** | Config sections (switch with `a` / `h`): a read-only **configuration advisor** (`shared_buffers`, `effective_cache_size`, `work_mem`, `maintenance_work_mem`, `max_connections` — current vs recommended with a verdict; concrete targets need `PGTUI_HOST_RAM_MB` / `PGTUI_HOST_CPUS`) and a **pg_hba viewer** (`pg_hba_file_rules`, flagging lines that fail to parse). `r` refresh. |

## Install

pgtui is a compiled Go utility — a single static binary.

```bash
# from source (Go 1.26+)
git clone https://github.com/9level/pg-tui.git
cd pg-tui
make build          # -> ./pgtui
make install        # -> /usr/local/bin/pgtui (sudo)
```

Prebuilt binaries for Linux/macOS (amd64/arm64) are attached to each
[GitHub Release](https://github.com/9level/pg-tui/releases).

## Configuration

pgtui reads a `.env` file from the working directory (or next to the binary):

```bash
cp .env.example .env
$EDITOR .env
```

```dotenv
DATABASE_URL=postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=disable
PGTUI_REFRESH_SECONDS=5
# PGTUI_SCRAM_ITERATIONS=15000   # PBKDF2 rounds for password-reset hashing
# PGTUI_HOST_RAM_MB=8192         # host RAM for the Tuning advisor
# PGTUI_HOST_CPUS=4              # host cores for the Tuning advisor
```

- The database in the URL is the **admin db** — where cluster-level queries run
  (`pg_stat_activity`, `pg_database`, replication, locks). pgtui opens
  additional connections on demand when you browse another database's tables or
  run a query against a different target (switch it with `/`).
- Standard `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE`/`PGSSLMODE`
  variables are used if `DATABASE_URL` is unset.
- Environment variables take precedence over `.env`.

### Permissions

- Read access to `pg_stat_*` / `pg_database` is enough for the read-only tabs.
  To see other sessions' query text on the dashboard/sessions view, connect as a
  superuser or a member of `pg_monitor`.
- Management actions need the usual Postgres privileges: `CREATEROLE` to
  create/drop roles, `CREATEDB` to create databases, and ownership/`WITH ADMIN`
  to grant. No superuser required.

## Keyboard shortcuts

Press `?` in the app for the full, scrollable list.

| Key | Action |
|-----|--------|
| `1`–`6` | switch tab |
| `tab` / `shift+tab` | next / previous tab |
| `?` | help (all shortcuts) |
| `q` / `ctrl+c` | quit |
| **Databases** | |
| `enter` | database → tables → read-only data |
| `d` | describe table (columns, types, indexes, constraints) |
| `n` / `D` | create / drop database (drop asks for the name) |
| **Table data** | |
| `←`/`→` `h`/`l` | move between columns (horizontal scroll) |
| `/` | search the active column (`ILIKE '%term%'`) |
| `e` | edit the top query bar (read-only) |
| **Query** | |
| `i` / `enter` | focus the SQL editor |
| `/` or `ctrl+t` | switch the target database (filterable list) |
| `x` | EXPLAIN (plan, without executing) |
| `ctrl+r` / `f5` | run |
| **Sessions** | |
| `c` | cancel the session's query (`pg_cancel_backend`) |
| `k` | terminate the connection (`pg_terminate_backend`) |
| **Roles** | |
| `enter` | manage role: reset password (random 32-char, shown once) / set connection limit |
| `n` | create role/user |
| `g` | grant to a database (CONNECT / ALL / owner / public schema) |
| `D` | drop role (asks for the name) |
| `F` | force-drop: reassign ownership to a successor, then drop — no data loss |

## Safety model

pgtui is built so you can't lose data by accident:

- The query runner classifies every statement: **read-only** runs immediately,
  **writes** confirm with `y`, and **critical** statements (`DROP DATABASE` /
  `DROP TABLE` / `TRUNCATE`, or `DELETE`/`UPDATE` without a `WHERE`) require
  typing `yes`.
- Dropping a role or a database opens a confirmation that only proceeds when you
  **type the object's exact name**.
- **Force-drop** never deletes data: it `REASSIGN OWNED` / `ALTER DATABASE
  OWNER` to a successor role and revokes privileges before `DROP ROLE`.
- **Passwords are hashed client-side.** Both **create role** and **reset
  password** compute the **SCRAM-SHA-256** verifier locally and send only that
  in `CREATE`/`ALTER ROLE`, so the plaintext never reaches the server or its
  logs. Reset generates a random 32-char password (letters and digits only,
  safe in any terminal) and shows it once, on screen. A non-ASCII typed password
  is sent as-is so the server can SASLprep it correctly. The PBKDF2 round count
  defaults to 15000 (stronger than Postgres' 4096) and is configurable via
  `PGTUI_SCRAM_ITERATIONS`.
- Admin statements run over the pgx simple protocol (required for
  `CREATE`/`DROP DATABASE`), with quoted identifiers.
- There is no code path that drops a database on its own.

## Development

```bash
make test     # unit + integration (integration self-skips without DATABASE_URL)
make vet
gofmt -l .    # should be empty
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow. To debug the UI,
log to a file: `PGTUI_DEBUG=/tmp/pgtui.log ./pgtui`.

## Distribution & releases

```bash
make build-all VERSION=v0.4.0   # cross-compile to dist/ (linux/darwin, amd64/arm64)
make release VERSION=v0.4.0     # validate semver + clean tree, tag, push
```

Pushing a `v*.*.*` tag runs the CI (`.github/workflows/ci.yml`): tests, then
`build-all`, then the binaries are attached to a GitHub Release.

## License

[MIT](LICENSE) © 9Level · [9level.dev](https://9level.dev)
