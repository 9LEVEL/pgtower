package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

func testManager(t *testing.T) *db.Manager {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL não definido; pulando teste de UI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mgr, err := db.NewManager(ctx, dsn, "postgres")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestModelNavigationRender(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()

	cfg := &config.Config{Host: "192.168.1.242", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = New(cfg, mgr)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Dashboard
	m, _ = m.Update(dashboardMsg{data: db.DashboardData{
		Version: "PostgreSQL 18.4", MaxConns: 50, TotalConns: 41, Active: 3,
		Idle: 30, IdleInTx: 1, CacheHitRatio: 99.9, DBCount: 17, TotalSize: "216 MB",
		Uptime: 2 * time.Hour, StartedAt: time.Now().Add(-2 * time.Hour),
	}})
	assertContains(t, m.View(), "Dashboard", "CONEXÕES", "41 / 50")

	// Aba 2: Bancos
	m, _ = m.Update(key("2"))
	m, _ = m.Update(databasesMsg{rows: []db.Database{
		{Name: "postgres", Owner: "postgres", SizePretty: "8 MB", Connections: 1},
		{Name: "b_fusion", Owner: "us_fusion", SizePretty: "13 MB", Connections: 12},
	}})
	assertContains(t, m.View(), "Databases do cluster", "postgres", "b_fusion")

	// Enter -> tabelas do banco selecionado (postgres)
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(tablesMsg{dbname: "postgres", rows: []db.Table{
		{Schema: "public", Name: "widgets", TotalSize: "1 MB", TableSize: "800 kB", IndexSize: "200 kB", EstRows: 1234},
	}})
	assertContains(t, m.View(), "Tabelas · postgres", "widgets")

	// esc volta para lista
	m, _ = m.Update(key("esc"))
	assertContains(t, m.View(), "Databases do cluster")

	// Aba 4: Locks (sem bloqueios)
	m, _ = m.Update(key("4"))
	m, _ = m.Update(locksMsg{rows: nil})
	assertContains(t, m.View(), "Nenhuma sessão bloqueada")

	// Aba 3: Query runner
	m, _ = m.Update(key("3"))
	assertContains(t, m.View(), "SQL", "alvo:")

	// Ajuda
	m, _ = m.Update(key("?"))
	assertContains(t, m.View(), "Atalhos do teclado", "executar a query")
}

func TestQueryDatabasePicker(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()

	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = New(cfg, mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// vai para a aba Query e alimenta a lista de bancos (broadcast)
	m, _ = m.Update(key("3"))
	m, _ = m.Update(databasesMsg{rows: []db.Database{
		{Name: "postgres"}, {Name: "b_fusion"}, {Name: "db_corely"},
	}})

	// '/' abre o seletor
	m, _ = m.Update(key("/"))
	assertContains(t, m.View(), "Rodar queries em qual database?", "b_fusion", "db_corely", "3/3")

	// filtra por "fus" -> só b_fusion
	m, _ = m.Update(key("fus"))
	assertContains(t, m.View(), "b_fusion", "1/3")

	// enter seleciona -> alvo vira b_fusion
	m, _ = m.Update(key("enter"))
	assertContains(t, m.View(), "alvo:", "b_fusion", "alvo alterado para b_fusion")
}

func assertContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("View() não contém %q\n---\n%s\n---", sub, s)
		}
	}
}
