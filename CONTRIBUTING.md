# Contributing to pgtui

Thanks for your interest in improving pgtui! This document explains how to get
set up, the conventions we follow, and how to propose changes.

## Ground rules

- Be respectful and constructive.
- Keep the UI **keyboard-first** and the code **dependency-light** (single
  static binary, no CGo).
- Destructive actions must stay **guarded**: anything that drops a role or a
  database requires typing the object's exact name; nothing is deleted
  automatically. Don't add code paths that bypass this.
- The project language is **English** — code, comments, UI strings, and docs.

## Getting set up

You need **Go 1.26+** and a reachable PostgreSQL instance.

```bash
git clone https://github.com/9level/pgtui.git
cd pgtui
cp .env.example .env      # point DATABASE_URL at a dev database
make build                # builds ./pgtui
./pgtui
```

## Development workflow

```bash
make build     # compile
make run       # build + run against ./.env
make vet       # go vet
make test      # unit + integration tests
gofmt -l .     # must print nothing (run `gofmt -w .` to fix)
```

Before opening a PR, make sure **all three are clean**: `gofmt -l .` prints
nothing, `go vet ./...` passes, and `go test ./...` is green.

### Tests

- Unit tests run with no external dependencies.
- Integration tests run **only** when `DATABASE_URL` is set; otherwise they
  self-skip. They are read-only against your data and only ever create/drop
  throwaway objects named `pgtui_*` / `aaa_pgtui_*`, which they clean up.

```bash
DATABASE_URL='postgres://user:pass@host:5432/postgres?sslmode=disable' go test ./...
```

> Never point the integration tests at a production cluster you care about.
> They create and drop test roles/databases (always cleaned up), but a shared
> box is a shared box.

### Debugging the TUI

The alt-screen owns stdout, so log to a file instead:

```bash
PGTUI_DEBUG=/tmp/pgtui.log ./pgtui   # then: tail -f /tmp/pgtui.log
```

## Project layout

```
main.go                 entrypoint, flags, version
internal/config         .env / environment loading
internal/db             pgx pools + all SQL (queries, admin, describe, safety)
internal/ui             Bubble Tea model, tabs, reusable modals/forms
```

Each tab implements the `tabView` interface in `internal/ui/model.go`. Reusable
pieces live in `confirm.go` (confirmation modal), `form.go` (text/select
forms), and `alert.go` (scrollable message box).

## Making a change

1. Open an issue first for anything non-trivial, so we can agree on the approach.
2. Branch off `master`.
3. Keep commits focused; write clear messages (present tense, imperative).
4. Match the surrounding style: same naming, comment density, and idioms.
5. Add or update tests for behavior changes. For a new SQL query, prefer a
   read-only integration test guarded by `DATABASE_URL`.
6. Run the checks above and open the PR with a short description of the change
   and how you verified it.

## Reporting bugs

Include: the pgtui version (shown in the header, or `pgtui --version`), the
PostgreSQL version, what you did, what you expected, and what happened. A
`PGTUI_DEBUG` log excerpt helps a lot for UI issues.

## Security

If you find a security issue (e.g. a way to run unconfirmed destructive SQL, or
a credential-handling problem), please report it privately rather than opening a
public issue.
