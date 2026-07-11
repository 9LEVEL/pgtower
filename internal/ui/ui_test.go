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

func TestDataBrowserLive(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()

	b := newDataBrowser(mgr)
	b.SetSize(120, 30)

	// pg_catalog.pg_class existe em qualquer database e tem muitas colunas.
	msg := b.Open("postgres", "pg_catalog", "pg_class")()
	b.Update(msg)
	if b.err != nil {
		t.Fatalf("Open: %v", b.err)
	}
	if len(b.allCols) < 5 || len(b.allRows) == 0 {
		t.Fatalf("esperava várias colunas e linhas, veio cols=%d rows=%d", len(b.allCols), len(b.allRows))
	}

	// navegação de colunas: →→ avança o cursor e mantém dentro dos limites.
	b.Update(key("right"))
	b.Update(key("right"))
	if b.colCursor != 2 {
		t.Errorf("colCursor após 2×→ = %d, quero 2", b.colCursor)
	}
	b.Update(key("left"))
	if b.colCursor != 1 {
		t.Errorf("colCursor após ←  = %d, quero 1", b.colCursor)
	}

	if !strings.Contains(b.View(), "pg_catalog.pg_class") {
		t.Errorf("View() não mostra o local da tabela:\n%s", b.View())
	}

	// Regressão: percorrer TODAS as colunas cruza as fronteiras da janela
	// horizontal (onde a contagem de colunas visíveis muda) — antes isso
	// estourava um índice no render do bubbles table. View() força o render.
	b.SetSize(80, 24) // largura menor => mais trocas de janela
	b.colCursor, b.colOffset = 0, 0
	b.buildGrid()
	for i := 0; i < len(b.allCols)+3; i++ {
		b.Update(key("right"))
		_ = b.View()
	}
	for i := 0; i < len(b.allCols)+3; i++ {
		b.Update(key("left"))
		_ = b.View()
	}
	if b.colCursor != 0 {
		t.Errorf("após voltar todas as colunas, colCursor=%d, quero 0", b.colCursor)
	}
	b.SetSize(120, 30)

	// busca na coluna 'relname' pelo próprio nome da tabela -> >=1 linha.
	relname := -1
	for i, c := range b.allCols {
		if c == "relname" {
			relname = i
		}
	}
	if relname < 0 {
		t.Fatal("coluna relname não encontrada")
	}
	b.colCursor = relname
	b.mode = dataSearch
	b.search.SetValue("pg_class")
	rmsg := b.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})()
	b.Update(rmsg)
	if b.err != nil {
		t.Fatalf("busca de coluna: %v", b.err)
	}
	if b.rowCount < 1 {
		t.Errorf("busca 'pg_class' em relname trouxe %d linhas, esperava >=1", b.rowCount)
	}

	// barra de query recusa escrita (somente leitura).
	b.mode = dataQuery
	b.queryBar.SetValue("drop table foo")
	b.handleQueryKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(b.status, "somente leitura") {
		t.Errorf("query bar deveria recusar escrita, status=%q", b.status)
	}
}

func TestSessionsAndRolesRender(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()
	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = New(cfg, mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})

	// Aba 5: Sessões
	m, _ = m.Update(key("5"))
	m, _ = m.Update(sessionsMsg{rows: []db.Session{
		{PID: 123, User: "app", DB: "prod", State: "active", Duration: "00:00:05", Query: "select pg_sleep(9)"},
	}})
	assertContains(t, m.View(), "Sessões ativas", "123", "select pg_sleep")

	// 'c' abre confirmação de cancelamento
	m, _ = m.Update(key("c"))
	assertContains(t, m.View(), "Cancelar query", "pid 123")
	m, _ = m.Update(key("n")) // cancela o modal

	// Aba 6: Roles
	m, _ = m.Update(key("6"))
	m, _ = m.Update(rolesMsg{rows: []db.Role{
		{Name: "postgres", Super: true, CanLogin: true},
		{Name: "app_user", CanLogin: true},
	}})
	m, _ = m.Update(databasesMsg{rows: []db.Database{{Name: "prod"}, {Name: "stage"}}})
	assertContains(t, m.View(), "Roles do cluster", "postgres", "app_user")

	// 'n' abre o formulário de criação
	m, _ = m.Update(key("n"))
	assertContains(t, m.View(), "Criar role", "Nome", "Senha")
	m, _ = m.Update(key("esc"))

	// 'g' abre o formulário de grant (com database selecionável)
	m, _ = m.Update(key("g"))
	assertContains(t, m.View(), "Grant", "Database", "Privilégio")
	m, _ = m.Update(key("esc"))
}

// TestDashboardTickStartsOnce garante que reabrir a aba Dashboard não cria
// múltiplos loops de auto-refresh (não precisa de DB — Init só monta comandos).
func TestDashboardTickStartsOnce(t *testing.T) {
	d := newDashboardView(&config.Config{RefreshSeconds: 5}, nil)
	if d.started {
		t.Fatal("started deveria começar false")
	}
	d.Init()
	if !d.started {
		t.Fatal("primeira Init() deveria marcar started=true (inicia o tick)")
	}
	// Reaberturas seguintes não devem reiniciar o flag (não duplicam o tick).
	for i := 0; i < 5; i++ {
		d.Init()
	}
	if !d.started {
		t.Fatal("started deveria permanecer true")
	}
}

func assertContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("View() não contém %q\n---\n%s\n---", sub, s)
		}
	}
}
