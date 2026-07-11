package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pg-tui/internal/db"
)

// --- mensagens assíncronas ---

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
	action string // "cancelar" | "encerrar"
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

// execMsg é o resultado de uma ação administrativa (create/drop/grant).
type execMsg struct {
	action string
	tag    string
	err    error
}

type tickMsg time.Time

const (
	clusterTimeout = 12 * time.Second
	queryTimeout   = 60 * time.Second
)

// loadDashboard consulta as métricas de cluster no database admin.
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

// loadTableData roda uma query (read-only, do navegador de dados) num database
// específico. token permite descartar resultados obsoletos.
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
		if action == "encerrar" {
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

// execStatements roda uma sequência de statements (admin) no database indicado
// (dbname vazio = admin db), parando no primeiro erro.
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

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
