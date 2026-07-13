package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pg-tui/internal/db"
)

// --- async messages ---

type dashboardMsg struct {
	data db.DashboardData
	err  error
}

type databasesMsg struct {
	rows []db.Database
	err  error
}

type tablesMsg struct {
	dbname string
	rows   []db.Table
	err    error
}

type locksMsg struct {
	rows []db.BlockPair
	err  error
}

type queryMsg struct {
	res db.QueryResult
	err error
}

type tableDataMsg struct {
	token int
	res   db.QueryResult
	err   error
}

type sessionsMsg struct {
	rows []db.Session
	err  error
}

type sessionActionMsg struct {
	action string // "cancel" | "terminate"
	pid    int32
	ok     bool
	err    error
}

type rolesMsg struct {
	rows []db.Role
	err  error
}

type describeMsg struct {
	desc db.TableDescription
	err  error
}

// execMsg is the result of an administrative action (create/drop/grant).
type execMsg struct {
	action string
	tag    string
	err    error
}

// tuningMsg carries the settings advisor result.
type tuningMsg struct {
	in   db.TuningInput
	recs []db.TuningRec
	err  error
}

// hbaMsg carries the parsed pg_hba rules, the file path/content and whether the
// connection can rewrite it.
type hbaMsg struct {
	file     string
	content  string
	writable bool
	rules    []db.HBARule
	err      error
}

// hbaApplyMsg is the result of a guarded pg_hba rewrite.
type hbaApplyMsg struct {
	err error
}

// allSettingsMsg carries the full pg_settings list for the ALTER SYSTEM editor.
type allSettingsMsg struct {
	settings []db.Setting
	err      error
}

// settingApplyMsg is the result of an ALTER SYSTEM SET/RESET + reload.
type settingApplyMsg struct {
	name    string
	restart bool
	err     error
}

type tickMsg time.Time

const (
	clusterTimeout = 12 * time.Second
	queryTimeout   = 60 * time.Second
)

// loadDashboard queries the cluster metrics on the admin database.
func loadDashboard(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return dashboardMsg{err: err}
		}
		d, err := db.LoadDashboard(ctx, p)
		return dashboardMsg{data: d, err: err}
	}
}

func loadDatabases(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return databasesMsg{err: err}
		}
		rows, err := db.ListDatabases(ctx, p)
		return databasesMsg{rows: rows, err: err}
	}
}

func loadTables(mgr *db.Manager, dbname string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, dbname)
		if err != nil {
			return tablesMsg{dbname: dbname, err: err}
		}
		rows, err := db.ListTables(ctx, p)
		return tablesMsg{dbname: dbname, rows: rows, err: err}
	}
}

func loadLocks(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return locksMsg{err: err}
		}
		rows, err := db.ListBlocks(ctx, p)
		return locksMsg{rows: rows, err: err}
	}
}

func runQuery(mgr *db.Manager, dbname, sql string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, dbname)
		if err != nil {
			return queryMsg{err: err}
		}
		res, err := db.RunQuery(ctx, p, sql)
		return queryMsg{res: res, err: err}
	}
}

// loadTableData runs a query (read-only, from the data browser) on a specific
// database. token allows discarding stale results.
func loadTableData(mgr *db.Manager, dbname, sql string, token int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, dbname)
		if err != nil {
			return tableDataMsg{token: token, err: err}
		}
		res, err := db.RunQuery(ctx, p, sql)
		return tableDataMsg{token: token, res: res, err: err}
	}
}

func loadSessions(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return sessionsMsg{err: err}
		}
		rows, err := db.ListSessions(ctx, p)
		return sessionsMsg{rows: rows, err: err}
	}
}

func sessionAction(mgr *db.Manager, action string, pid int32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return sessionActionMsg{action: action, pid: pid, err: err}
		}
		var ok bool
		if action == "terminate" {
			ok, err = db.TerminateBackend(ctx, p, pid)
		} else {
			ok, err = db.CancelBackend(ctx, p, pid)
		}
		return sessionActionMsg{action: action, pid: pid, ok: ok, err: err}
	}
}

func loadRoles(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return rolesMsg{err: err}
		}
		rows, err := db.ListRoles(ctx, p)
		return rolesMsg{rows: rows, err: err}
	}
}

// loadTuning reads the advisor GUCs and computes recommendations using the
// host RAM/cores (ramMB/cpus, 0 = unknown).
func loadTuning(mgr *db.Manager, ramMB, cpus int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return tuningMsg{err: err}
		}
		m, err := db.ReadTuningSettings(ctx, p)
		if err != nil {
			return tuningMsg{err: err}
		}
		in := db.BuildTuningInput(m, int64(ramMB)<<20, cpus)
		return tuningMsg{in: in, recs: db.Recommend(in)}
	}
}

// loadAllSettings reads the full pg_settings list for the editor.
func loadAllSettings(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return allSettingsMsg{err: err}
		}
		s, err := db.ListAllSettings(ctx, p)
		return allSettingsMsg{settings: s, err: err}
	}
}

// applySetting runs ALTER SYSTEM SET (or RESET when reset) then reloads the
// configuration. restart is echoed back so the UI can warn when the change only
// applies after a server restart.
func applySetting(mgr *db.Manager, name, value string, reset, restart bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return settingApplyMsg{name: name, restart: restart, err: err}
		}
		sql := db.BuildAlterSystemSet(name, value)
		if reset {
			sql = db.BuildAlterSystemReset(name)
		}
		if _, err := db.ExecAdmin(ctx, p, sql); err != nil {
			return settingApplyMsg{name: name, restart: restart, err: err}
		}
		if err := db.ReloadConf(ctx, p); err != nil {
			return settingApplyMsg{name: name, restart: restart, err: err}
		}
		return settingApplyMsg{name: name, restart: restart}
	}
}

// loadHBA reads the pg_hba.conf path, content, parsed rules and writability
// (superuser only for the rules/content).
func loadHBA(mgr *db.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, mgr.AdminDB())
		if err != nil {
			return hbaMsg{err: err}
		}
		file, _ := db.HBAFilePath(ctx, p)
		writable := db.HBAWritable(ctx, p)
		content, _ := db.HBAFileContent(ctx, p)
		rules, err := db.ListHBARules(ctx, p)
		return hbaMsg{file: file, content: content, writable: writable, rules: rules, err: err}
	}
}

// applyHBA rewrites pg_hba.conf with the given content through the guarded
// path (backup + validate + reload + verify + auto-rollback).
func applyHBA(mgr *db.Manager, dsn, content string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		return hbaApplyMsg{err: db.ApplyHBAContent(ctx, mgr, dsn, content)}
	}
}

func describeTable(mgr *db.Manager, dbname, schema, table string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, dbname)
		if err != nil {
			return describeMsg{err: err}
		}
		d, err := db.DescribeTable(ctx, p, schema, table)
		return describeMsg{desc: d, err: err}
	}
}

// execStatements runs a sequence of (admin) statements on the given database
// (empty dbname = admin db), stopping at the first error.
func execStatements(mgr *db.Manager, dbname, action string, stmts []string) tea.Cmd {
	return func() tea.Msg {
		if dbname == "" {
			dbname = mgr.AdminDB()
		}
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		p, err := mgr.Pool(ctx, dbname)
		if err != nil {
			return execMsg{action: action, err: err}
		}
		var tag string
		for _, s := range stmts {
			tag, err = db.ExecAdmin(ctx, p, s)
			if err != nil {
				return execMsg{action: action, err: err}
			}
		}
		return execMsg{action: action, tag: tag}
	}
}

// forceDropRole removes a role with dependencies by reassigning ownership of
// objects to the successor (without dropping data) and then running DROP ROLE.
func forceDropRole(mgr *db.Manager, doomed, successor string) tea.Cmd {
	return func() tea.Msg {
		action := "force-drop " + doomed
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		warnings, err := db.ForceDropRole(ctx, mgr, doomed, successor)
		if err != nil {
			msg := "Even after reassigning ownership, DROP ROLE failed:\n\n" + pgErrorText(err)
			if len(warnings) > 0 {
				msg += "\n\nWarnings during the process:\n- " + strings.Join(warnings, "\n- ")
			}
			return execMsg{action: action, err: errors.New(msg)}
		}
		tag := fmt.Sprintf("role %s removed; ownership reassigned to %s", doomed, successor)
		if len(warnings) > 0 {
			tag += fmt.Sprintf(" (%d warnings ignored)", len(warnings))
		}
		return execMsg{action: action, tag: tag}
	}
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
