# pgtui

[![CI](https://github.com/9level/pgtui/actions/workflows/ci.yml/badge.svg)](https://github.com/9level/pgtui/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/9level/pgtui?sort=semver)](https://github.com/9level/pgtui/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A keyboard-first **PostgreSQL administration TUI** for sysadmins — think *k9s,
but for Postgres*. Where most database TUIs are data browsers, pgtui leans into
**operations**: watch and kill sessions, manage roles and grants, create/drop
databases, and inspect cluster health — all with strong guards against
destructive mistakes.

Single static binary, no dependencies, no container required.

<!-- TODO: add a demo GIF here -->

```
 pgtui  v0.6.1                         postgres@db:5432 • admin db: postgres  9level.dev
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
  replication, longest active query, plus a **connection advisor** that flags
  when you're near the limit or drowning in idle connections.
- **Tuning** — a read-only settings advisor, an `ALTER SYSTEM` editor, and a
  `pg_hba` viewer/editor with a lockout-proof safety net.

Core management (roles, databases, sessions, queries) works **without a
superuser** — a role with `CREATEROLE`/`CREATEDB` is enough. The **Tuning**
tab's `ALTER SYSTEM` and `pg_hba` editing are the exception and require a
superuser (see [Permissions](#permissions)).

## Features

| Tab | What it does |
|-----|--------------|
| **1 · Dashboard** | Cluster health: connections vs `max_connections` (with reserved slots), cache hit ratio, uptime, total size, commits/rollbacks, version, longest active query, replication. A **connection advisor** flags near-limit/idle-dominated/idle-in-transaction situations and tells you what to do. Auto-refresh. |
| **2 · Databases** | Databases (owner, size, connections) → tables → **read-only data browser** (horizontal column scroll `←→`, per-column search `/`, top query bar `e`). Create (`n`) / drop (`D`) databases, `d` for a table's structure (columns, indexes, constraints), and `/` for a **fuzzy quick-find** in the database/table list. |
| **3 · Query** | SQL editor with a paged result grid. `x` runs **EXPLAIN** (plan only). Writes require confirmation; destructive statements (`DROP`/`TRUNCATE`/`DELETE`/`UPDATE` without `WHERE`) require typing `yes`. |
| **4 · Locks** | Blocking tree: which session waits on which. |
| **5 · Sessions** | `pg_stat_activity` with state/wait/duration/query. `c` cancels the query, `k` terminates the connection, `/` **fuzzy quick-find** (by PID, user, database, state or query text). |
| **6 · Roles** | Roles with login/super/createdb attributes and their connection limit (`CONN`, ∞ = unlimited). `enter` **manage** the selected role (reset password — generates a random 32-char one, shown once; or set the connection limit), `n` create, `g` grant to a database, `D` drop, `F` **force-drop** (reassign ownership to a successor, then drop — no data loss), `/` **fuzzy quick-find** by name. |
| **7 · Tuning** | Config sections (switch with `a` / `s` / `h`): a read-only **configuration advisor** (`shared_buffers`, `effective_cache_size`, `work_mem`, `maintenance_work_mem`, `max_connections` — current vs recommended with a verdict; concrete targets need `PGTUI_HOST_RAM_MB` / `PGTUI_HOST_CPUS`); an **ALTER SYSTEM editor** (`enter` edit / `x` reset any GUC — validated against type & bounds, applied with `pg_reload_conf`, `/` filters, restart-required settings are flagged); and a **pg_hba editor** (`pg_hba_file_rules` with parse-error flags; `n`/`e`/`d` add/edit/delete a rule — superuser only, each write is backed up, validated, reloaded and **auto-rolled-back if admin login breaks**). `r` refresh. |

## Install

### One line (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/9level/pgtui/master/install.sh | sh
```

It detects your OS/arch, downloads the latest **static** binary (verifying its
SHA-256) and installs it to `/usr/local/bin` — **no compiler, no runtime
dependencies**. The binary runs on any Linux distro (Debian, Ubuntu, Alpine, …)
and macOS; only the CPU architecture matters:

| | amd64 (x86_64) | arm64 (aarch64) |
|---|:---:|:---:|
| **Linux** | ✅ | ✅ |
| **macOS** | ✅ | ✅ |
| **Windows** | build from source / WSL | — |

Tweak the install: `PGTUI_INSTALL_DIR="$HOME/.local/bin"` or `PGTUI_VERSION=vX.Y.Z`.
Prefer to read before you pipe to a shell? It's just [`install.sh`](install.sh).
Prebuilt binaries are also attached to each
[GitHub Release](https://github.com/9level/pgtui/releases).

### From source (Go 1.26+)

```bash
git clone https://github.com/9level/pgtui.git
cd pgtui
make build          # -> ./pgtui
make install        # -> /usr/local/bin/pgtui (sudo)
```

### Get running in 30 seconds

```bash
export DATABASE_URL='postgres://user:pass@host:5432/postgres?sslmode=disable'
pgtui        # opens on the Dashboard — press ? for shortcuts, q to quit
```

## Upgrading

pgtui checks GitHub for a newer release **on startup** and, if there is one,
asks what to do:

- **Update now** — downloads the right binary for your OS/arch, verifies its
  SHA-256 and replaces the running binary **in place**. If the install directory
  needs root (e.g. `/usr/local/bin`), it shows the exact command to finish.
  Restart pgtui afterwards.
- **Not now** — dismiss for this run.
- **Never suggest again** — stop asking for good (a marker in your config dir);
  re-enable with `update_check: true` in `config.yml`.

Disable the check entirely with `update_check: false` (config.yml) or
`PGTUI_UPDATE_CHECK=0`. Source/dev builds are never nagged.

### Lazy upgrade (one line)

Don't want the prompt at all? Just re-run the installer — it always grabs the
**latest** release, verifies the checksum and replaces your binary (your
`config.yml` is left untouched):

```bash
curl -fsSL https://raw.githubusercontent.com/9level/pgtui/master/install.sh | sh
```

Pin a version with `PGTUI_VERSION=vX.Y.Z`. Installed from source instead?
`cd pgtui && git pull && make install`.

## Configuration

pgtui is configured, in order of precedence, by **environment variables** → a
**`config.yml`** file → built-in defaults. The installer creates
`/opt/pgtui/config.yml` for you; you can also drop one next to the binary, in
`~/.config/pgtui/`, or the working directory (full template:
[`config.yml.example`](config.yml.example)).

```yaml
# /opt/pgtui/config.yml
database_url: "postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=disable"
refresh_seconds: 5
# scram_iterations: 15000   # PBKDF2 rounds for password-reset hashing
# host_ram_mb: 8192         # host RAM for the Tuning advisor
# host_cpus: 4              # host cores for the Tuning advisor
# update_check: true        # startup "newer release available" prompt
```

Prefer environment variables? They still work and **override** the file:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=disable'
```

- The database in the URL is the **admin db** — where cluster-level queries run
  (`pg_stat_activity`, `pg_database`, replication, locks). pgtui opens
  additional connections on demand when you browse another database's tables or
  run a query against a different target (switch it with `/`).
- Standard `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE`/`PGSSLMODE`
  variables are used if neither `DATABASE_URL` nor `database_url` is set.
- Precedence: environment variables > `config.yml` > defaults.
- Search locations: `PGTUI_CONFIG` (an explicit file) or `PGTUI_CONFIG_DIR`
  override the search; otherwise `./`, the binary's directory, `~/.config/pgtui/`,
  `/opt/pgtui/`, `/etc/pgtui/` (first hit wins).

### Permissions

- Read access to `pg_stat_*` / `pg_database` is enough for the read-only tabs.
  To see other sessions' query text on the dashboard/sessions view, connect as a
  superuser or a member of `pg_monitor`.
- Management actions need the usual Postgres privileges: `CREATEROLE` to
  create/drop roles, `CREATEDB` to create databases, and ownership/`WITH ADMIN`
  to grant. No superuser required.
- The **Tuning** tab is the exception. The settings advisor works for any role,
  but `pg_hba_file_rules` (the pg_hba viewer) is superuser-only, `ALTER SYSTEM`
  needs a superuser, and editing `pg_hba.conf` needs a superuser (it writes the
  file via `COPY … TO PROGRAM`). Without those, the affected sections show as
  read-only.

## Keyboard shortcuts

Press `?` in the app for the full, scrollable list.

| Key | Action |
|-----|--------|
| `1`–`7` | switch tab |
| `tab` / `shift+tab` | next / previous tab |
| `?` | help (all shortcuts) |
| `q` / `ctrl+c` | quit |
| **Databases** | |
| `enter` | database → tables → read-only data |
| `/` | fuzzy quick-find in the database / table list |
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
| `/` | fuzzy quick-find (PID, user, database, state, query) |
| `c` | cancel the session's query (`pg_cancel_backend`) |
| `k` | terminate the connection (`pg_terminate_backend`) |
| **Roles** | |
| `/` | fuzzy quick-find a role by name |
| `enter` | manage role: reset password (random 32-char, shown once) / set connection limit |
| `n` | create role/user |
| `g` | grant to a database (CONNECT / ALL / owner / public schema) |
| `D` | drop role (asks for the name) |
| `F` | force-drop: reassign ownership to a successor, then drop — no data loss |
| **Tuning** | |
| `a` / `s` / `h` | switch section: advisor / settings / pg_hba |
| `enter` / `x` | settings: edit (`ALTER SYSTEM`) / reset a GUC (`/` filters) |
| `n` / `e` / `d` | pg_hba: add / edit / delete a rule (superuser) |
| `r` | re-read `pg_settings` / `pg_hba` |

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
- **Editing `pg_hba.conf`** (Tuning tab) never leaves you locked out: pgtui
  backs up the file, writes the change, checks `pg_hba_file_rules` for parse
  errors, reloads, then opens a **fresh admin connection** to confirm login
  still works — any failure restores the backup and reloads. It needs a
  superuser connection and writes via `COPY … TO PROGRAM` (no adminpack needed).
- Admin statements run over the pgx simple protocol (required for
  `CREATE`/`DROP DATABASE`), with quoted identifiers.
- There is no code path that drops a database on its own.

## Development

```bash
make test     # unit + integration (integration self-skips without DATABASE_URL)
make vet
gofmt -l .    # should be empty
```

Need a throwaway cluster for the integration tests? A `docker-compose.yml`
(PostgreSQL 18, superuser) ships with the repo:

```bash
docker compose up -d
export DATABASE_URL='postgres://postgres:pgtui_test@127.0.0.1:5432/postgres?sslmode=disable'
make test                                   # now runs the live tests too
PGTUI_HBA_LIVE_TEST=1 go test ./...          # also the guarded pg_hba write test
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow. To debug the UI,
log to a file: `PGTUI_DEBUG=/tmp/pgtui.log ./pgtui`.

## Distribution & releases

```bash
make build-all VERSION=vX.Y.Z   # cross-compile to dist/ (linux/darwin, amd64/arm64)
make release VERSION=vX.Y.Z     # validate semver + clean tree, tag, push
```

Pushing a `v*.*.*` tag runs the CI (`.github/workflows/ci.yml`): tests, then
`build-all`, then the binaries are attached to a GitHub Release.

## License & trademarks

The **source code** is licensed under the [MIT License](LICENSE) © 2026 9Level.

The MIT license covers the code only — it does **not** grant rights to the
project's brand. **"pgtui", "9Level", and the 9Level logo are trademarks of
9Level**; see [TRADEMARKS.md](TRADEMARKS.md). If you fork it, please use a
different name.

· [9level.dev](https://9level.dev)
