# CLAUDE.md — working in this repo

pgtui is a keyboard-first **PostgreSQL administration TUI** (Go 1.26, Bubble
Tea). Single **static** binary, no CGo. Public repo: `github.com/9level/pgtui`.

## Build · test · run

```bash
make build          # -> ./pgtui  (dev build, version "dev")
make install        # -> /usr/local/bin/pgtui  (dev build)
make vet            # go vet ./...            (must be clean)
gofmt -l .          # must print nothing      (gofmt -w . to fix)
make test           # unit + integration; live tests self-skip without DATABASE_URL
```

Live/integration tests need a throwaway cluster:

```bash
docker compose up -d
export DATABASE_URL='postgres://postgres:pgtui_test@127.0.0.1:5432/postgres?sslmode=disable'
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
- Searched in `./`, the binary's dir, `~/.config/pgtui/`, `/opt/pgtui/`,
  `/etc/pgtui/`; `PGTUI_CONFIG` (file) or `PGTUI_CONFIG_DIR` (dir) replace the
  search (tests rely on this for isolation).
- `DATABASE_URL` / `PG*` add a session-only connection named `env`; it is never
  saved. `PGTUI_*` override the settings.
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
**must** stay named `pgtui-<version>-{linux,darwin}-{amd64,arm64}` and
`SHA256SUMS` — both `install.sh` and the in-app self-updater
(`internal/update`) fetch them by name. Doc command-examples use a `vX.Y.Z`
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
