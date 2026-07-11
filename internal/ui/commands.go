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

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
