# CLAUDE.md — working in this repo

pgtower is a keyboard-first **PostgreSQL administration TUI** (Go 1.26, Bubble
Tea). Single **static** binary, no CGo. Public repo: `github.com/9level/pgtower`.

## Build · test · run

```bash
make build          # -> ./pgtower  (dev build, version "dev")
make install        # -> /usr/local/bin/pgtower  (dev build)
make vet            # go vet ./...            (must be clean)
gofmt -l .          # must print nothing      (gofmt -w . to fix)
make test           # unit + integration; live tests self-skip without DATABASE_URL
```

Live/integration tests need a throwaway cluster:

```bash
docker compose up -d
export DATABASE_URL='postgres://postgres:pgtower_test@127.0.0.1:5432/postgres?sslmode=disable'
make test
```

Before any commit: `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` green.

## Configuration (how the app itself is configured)

Resolution order, highest first: **environment variables → `config.yml` →
defaults**. There is **no `.env`-file support** — env vars only, plus the file.

- `config.yml` (format `version: 2`) holds a list of named `connections` plus
  global settings, and is **written by the app** (Servers screen, `S`) — keep
  `config.Store.Save` the only writer; it is atomic and `0600`. Template and key
  reference: `config.yml.example`.
- It lives in `config.UserDir()` = `~/.config/pgtower` (`$XDG_CONFIG_HOME`,
  macOS too) and **pgtower always saves there**. Searched in `./`, the binary's
  dir, `~/.config/pgtower/`, then the system dirs `/opt/pgtower/`,
  `/etc/pgtower/` (read-only, admin-managed: a config read from there is saved
  to the user dir). `PGTOWER_CONFIG` (file) or `PGTOWER_CONFIG_DIR` (dir) replace
  the search (tests rely on this for isolation; `isolateSearch` in
  `location_test.go` covers the default search). The default search skips files
  the user may not read (`Store.Skipped`) and never saves over them; an
  explicit `PGTOWER_CONFIG`/`_DIR` file must be readable.
- Up to v0.11 the config lived in `/opt/pgtower`: `internal/config/location.go`
  moves the user's own private (`0600`, owned) file there to the user dir on
  load (backup `*.moved.bak`, one-time notice); others' files are only read.
  `install.sh` sets up the user dir for the invoking user (`$SUDO_USER` under
  sudo) and never seeds a config that would hide an older one.
- `DATABASE_URL` / `PG*` add a session-only connection named `env`; it is never
  saved. `PGTOWER_*` override the settings.
- pgtower was **pgtui** up to v0.9. `internal/config/legacy.go` still reads
  `PGTUI_*` (after `PGTOWER_*`, via `config.Env`) and moves the pgtui config
  dirs on load; `internal/update/rename.go` renames a binary started as `pgtui`
  and leaves a `pgtui` symlink. Always read settings through `config.Env`.
- Legacy (v0.8-) single-connection files and pre-v0.8 `.env` files are migrated
  on load by `internal/config/migrate.go` (backup `*.v1.bak`, then rewrite).
  Any future format change must follow the same pattern: bump `FileVersion`,
  migrate + back up, and surface a one-time notice (`config.Migration`).

## Cutting a release — do exactly this

1. Land everything on `master`; tree clean; `gofmt`/`vet`/`test` green.
2. `make release VERSION=vX.Y.Z`  — validates semver + clean tree, tags, pushes the **tag**.
3. `git push origin master`  — `make release` pushes only the tag; sync the branch.
4. CI (`.github/workflows/ci.yml`, on `v*.*.*` tags) runs `make build-all` and
   attaches `dist/*` to a GitHub Release with auto-generated notes.
5. Verify: `gh release view vX.Y.Z` lists **4 binaries + `SHA256SUMS`**.

The version exists **only** in the git tag (injected via `-ldflags
main.version`); nothing in the source needs editing to bump it. Release assets
**must** stay named `pgtower-<version>-{linux,darwin}-{amd64,arm64}` and
`SHA256SUMS` — both `install.sh` and the in-app self-updater
(`internal/update`) fetch them by name. Releases no longer carry `pgtui-*`
copies (dropped after v0.12.0), so pgtui v0.9 cannot self-update to pgtower;
it reinstalls with `install.sh`. Doc command-examples use a `vX.Y.Z`
placeholder so they never go stale.

## Commit rules

- **Do NOT add a `Co-Authored-By: Claude` trailer** (nor "Generated with Claude
  Code" in PR bodies). This repo is public and the owner wants no such
  attribution. Messages: imperative, present tense, Conventional-Commits style
  (`feat:`, `fix:`, `docs:` …).
- Keep it **dependency-light** and **destructive-action-guarded**: dropping a
  role/database always requires typing the exact name; never add a bypass.
- Match the surrounding style (naming, comment density, idioms).

## Layout

```
main.go            entrypoint, flags, version
internal/config    config.yml (connections + settings), env, legacy migration
internal/db        pgx pools + all SQL (queries, admin, describe, safety, scram, hba, settings),
                   connection-error diagnosis (connerr.go)
internal/ui        Bubble Tea model, sessions, Servers screen, tabs, reusable modals
                   (confirm/form/alert/menu/finder)
internal/update    GitHub release check + in-place self-update
```

Each tab implements `tabView` (`internal/ui/model.go`). Tabs belong to a
`session` (`internal/ui/session.go`) — one per connected server, rebuilt on
every switch. **Every command a tab returns must go through `session.scope`**
so results arriving after a switch are dropped instead of rendering under the
wrong server. Model-level modals (help, Servers, notices, update prompt, quit
confirmation) live on `Model`; tab-level ones are fields on each tab. `q` asks
to confirm before quitting; `ctrl+c` hard-quits.
