package ui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
)

func TestPgErrorText(t *testing.T) {
	pg := &pgconn.PgError{Message: "boom", Detail: "because X", Hint: "do Y"}
	got := pgErrorText(pg)
	for _, want := range []string{"boom", "because X", "do Y"} {
		if !strings.Contains(got, want) {
			t.Errorf("pgErrorText does not contain %q: %q", want, got)
		}
	}
	if pgErrorText(errors.New("plain")) != "plain" {
		t.Error("pgErrorText of a simple error should return the message")
	}
	if pgErrorText(nil) != "" {
		t.Error("pgErrorText(nil) should be empty")
	}
}

func testManager(t *testing.T) *db.Manager {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping UI test")
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

	cfg := &config.Config{Host: "192.0.2.10", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5, Version: "v9.9.9"}
	var m tea.Model = newConnected(cfg, mgr)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Dashboard — the header shows version and brand.
	m, _ = m.Update(dashboardMsg{data: db.DashboardData{
		Version: "PostgreSQL 18.4", MaxConns: 50, TotalConns: 41, Active: 3,
		Idle: 30, IdleInTx: 1, CacheHitRatio: 99.9, DBCount: 17, TotalSize: "216 MB",
		Uptime: 2 * time.Hour, StartedAt: time.Now().Add(-2 * time.Hour),
	}})
	assertContains(t, m.View(), "Dashboard", "CONNECTIONS", "41 / 50", "v9.9.9", "9level.dev", "7 Tuning")

	// Tab 2: Databases
	m, _ = m.Update(key("2"))
	m, _ = m.Update(databasesMsg{rows: []db.Database{
		{Name: "postgres", Owner: "postgres", SizePretty: "8 MB", Connections: 1},
		{Name: "b_fusion", Owner: "us_fusion", SizePretty: "13 MB", Connections: 12},
	}})
	assertContains(t, m.View(), "Cluster databases", "postgres", "b_fusion")

	// Enter -> tables of the selected database (postgres)
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(tablesMsg{dbname: "postgres", rows: []db.Table{
		{Schema: "public", Name: "widgets", TotalSize: "1 MB", TableSize: "800 kB", IndexSize: "200 kB", EstRows: 1234},
	}})
	assertContains(t, m.View(), "Tables · postgres", "widgets")

	// esc goes back to the list
	m, _ = m.Update(key("esc"))
	assertContains(t, m.View(), "Cluster databases")

	// Tab 4: Locks (no blocks)
	m, _ = m.Update(key("4"))
	m, _ = m.Update(locksMsg{rows: nil})
	assertContains(t, m.View(), "No blocked sessions")

	// Tab 3: Query runner
	m, _ = m.Update(key("3"))
	assertContains(t, m.View(), "SQL", "target:")

	// Help (about): fixed title + brand at the top; scrollable shortcuts.
	m, _ = m.Update(key("?"))
	assertContains(t, m.View(), "Keyboard shortcuts", "9level.dev", "Global navigation")
}

func TestQueryDatabasePicker(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()

	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = newConnected(cfg, mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// go to the Query tab and feed the database list (broadcast)
	m, _ = m.Update(key("3"))
	m, _ = m.Update(databasesMsg{rows: []db.Database{
		{Name: "postgres"}, {Name: "b_fusion"}, {Name: "db_corely"},
	}})

	// '/' opens the selector
	m, _ = m.Update(key("/"))
	assertContains(t, m.View(), "Run queries on which database?", "b_fusion", "db_corely", "3/3")

	// filter by "fus" -> only b_fusion
	m, _ = m.Update(key("fus"))
	assertContains(t, m.View(), "b_fusion", "1/3")

	// enter selects -> target becomes b_fusion
	m, _ = m.Update(key("enter"))
	assertContains(t, m.View(), "target:", "b_fusion", "target changed to b_fusion")
}

func TestDataBrowserLive(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()

	b := newDataBrowser(mgr)
	b.SetSize(120, 30)

	// pg_catalog.pg_class exists in any database and has many columns.
	msg := b.Open("postgres", "pg_catalog", "pg_class")()
	b.Update(msg)
	if b.err != nil {
		t.Fatalf("Open: %v", b.err)
	}
	if len(b.allCols) < 5 || len(b.allRows) == 0 {
		t.Fatalf("expected several columns and rows, got cols=%d rows=%d", len(b.allCols), len(b.allRows))
	}

	// column navigation: →→ advances the cursor and keeps it within bounds.
	b.Update(key("right"))
	b.Update(key("right"))
	if b.colCursor != 2 {
		t.Errorf("colCursor after 2×→ = %d, want 2", b.colCursor)
	}
	b.Update(key("left"))
	if b.colCursor != 1 {
		t.Errorf("colCursor after ← = %d, want 1", b.colCursor)
	}

	if !strings.Contains(b.View(), "pg_catalog.pg_class") {
		t.Errorf("View() does not show the table location:\n%s", b.View())
	}

	// Regression: walking through ALL the columns crosses the horizontal window
	// boundaries (where the count of visible columns changes) — this used to
	// overflow an index in the bubbles table render. View() forces the render.
	b.SetSize(80, 24) // smaller width => more window swaps
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
		t.Errorf("after going back through all columns, colCursor=%d, want 0", b.colCursor)
	}
	b.SetSize(120, 30)

	// search in column 'relname' for the table's own name -> >=1 row.
	relname := -1
	for i, c := range b.allCols {
		if c == "relname" {
			relname = i
		}
	}
	if relname < 0 {
		t.Fatal("relname column not found")
	}
	b.colCursor = relname
	b.mode = dataSearch
	b.search.SetValue("pg_class")
	rmsg := b.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})()
	b.Update(rmsg)
	if b.err != nil {
		t.Fatalf("column search: %v", b.err)
	}
	if b.rowCount < 1 {
		t.Errorf("search 'pg_class' in relname returned %d rows, expected >=1", b.rowCount)
	}

	// query bar refuses writes (read-only).
	b.mode = dataQuery
	b.queryBar.SetValue("drop table foo")
	b.handleQueryKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(b.status, "read-only") {
		t.Errorf("query bar should refuse writes, status=%q", b.status)
	}
}

func TestSessionsAndRolesRender(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()
	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = newConnected(cfg, mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})

	// Tab 5: Sessions
	m, _ = m.Update(key("5"))
	m, _ = m.Update(sessionsMsg{rows: []db.Session{
		{PID: 123, User: "app", DB: "prod", State: "active", Duration: "00:00:05", Query: "select pg_sleep(9)"},
	}})
	assertContains(t, m.View(), "Active sessions", "123", "select pg_sleep")

	// 'c' opens the cancel confirmation
	m, _ = m.Update(key("c"))
	assertContains(t, m.View(), "Cancel query", "pid 123")
	m, _ = m.Update(key("n")) // cancels the modal

	// Tab 6: Roles
	m, _ = m.Update(key("6"))
	m, _ = m.Update(rolesMsg{rows: []db.Role{
		{Name: "postgres", Super: true, CanLogin: true},
		{Name: "app_user", CanLogin: true},
	}})
	m, _ = m.Update(databasesMsg{rows: []db.Database{{Name: "prod"}, {Name: "stage"}}})
	assertContains(t, m.View(), "Cluster roles", "postgres", "app_user")

	// 'n' opens the creation form
	m, _ = m.Update(key("n"))
	assertContains(t, m.View(), "Create role", "Name", "Password")
	m, _ = m.Update(key("esc"))

	// 'g' starts the grant flow: pick the database in the fuzzy finder first
	m, _ = m.Update(key("g"))
	assertContains(t, m.View(), "Grant", "pick database", "prod", "stage")
	m, _ = m.Update(key("esc"))

	// An admin action error opens an alert with the FULL message (no truncation).
	pgErr := &pgconn.PgError{
		Message: "role \"app_user\" cannot be dropped because some objects depend on it",
		Detail:  "owner of database prod",
	}
	m, _ = m.Update(execMsg{action: "drop role app_user", err: pgErr})
	assertContains(t, m.View(), "Failed to drop role", "cannot be dropped",
		"owner of database prod", "HOW TO FIX")
	m, _ = m.Update(key("esc")) // closes the alert
	assertContains(t, m.View(), "Cluster roles")
}

// TestRolesResetPasswordFlow drives Enter → menu → reset password → confirm and
// checks the generated password is shown. It needs no DB: the async ALTER ROLE
// command is not executed; instead its success is simulated with an execMsg.
func TestRolesResetPasswordFlow(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(rolesMsg{rows: []db.Role{
		{Name: "postgres", Super: true, CanLogin: true},
		{Name: "app_user", CanLogin: true},
	}})

	// move the cursor to app_user, then Enter opens the manage menu.
	v.Update(key("down"))
	v.Update(key("enter"))
	assertContains(t, v.View(), "Manage role", "app_user", "Reset password")

	// Enter selects "Reset password" -> confirmation dialog.
	v.Update(key("enter"))
	assertContains(t, v.View(), "Reset password", "32-character", "app_user")

	// 'y' confirms: a password is generated (the async ALTER cmd is ignored here).
	v.Update(key("y"))
	if len(v.newPassword) != newPasswordLen {
		t.Fatalf("generated password length = %d, want %d", len(v.newPassword), newPasswordLen)
	}
	pwd := v.newPassword

	// Simulate the successful ALTER ROLE result -> the password modal appears,
	// showing the password and the SCRAM hashing detail (default 15000 rounds).
	v.Update(execMsg{action: "reset password app_user"})
	assertContains(t, v.View(), "Password reset", pwd, "cannot be shown again",
		"SCRAM-SHA-256", "15000 rounds")

	// esc closes the modal, back to the roles table.
	v.Update(key("esc"))
	assertContains(t, v.View(), "Cluster roles")
}

// TestDashboardConnAdvisor checks the Unit 1 connection advisor renders on the
// dashboard for a near-limit, idle-dominated cluster (the 47/50 scenario).
func TestDashboardConnAdvisor(t *testing.T) {
	d := newDashboardView(&config.Config{}, nil)
	d.SetSize(120, 40)
	d.Update(dashboardMsg{data: db.DashboardData{
		Version: "PostgreSQL 18.4", MaxConns: 50, Reserved: 3,
		TotalConns: 47, Active: 5, Idle: 40, IdleInTx: 2,
		TotalSize: "1 MB", StartedAt: time.Now(),
	}})
	view := d.View()
	assertContains(t, view, "resv 3", "CONNECTION ADVISOR",
		"at the limit", "PgBouncer", "idle in transaction")

	// A healthy cluster shows only the info line, no scary findings.
	d.Update(dashboardMsg{data: db.DashboardData{
		Version: "PostgreSQL 18.4", MaxConns: 100, Reserved: 3,
		TotalConns: 12, Active: 3, Idle: 9, TotalSize: "1 MB", StartedAt: time.Now(),
	}})
	healthy := d.View()
	assertContains(t, healthy, "healthy headroom")
	if strings.Contains(healthy, "PgBouncer") {
		t.Error("healthy cluster should not nag about pooling")
	}
}

// TestRolesConnLimitFlow drives Enter → menu → set connection limit and checks
// the CONN column renders (∞ for unlimited).
func TestRolesConnLimitFlow(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(rolesMsg{rows: []db.Role{
		{Name: "app_user", CanLogin: true, ConnLimit: -1},
		{Name: "svc", CanLogin: true, ConnLimit: 30},
	}})
	assertContains(t, v.View(), "CONN", "∞", "30")

	v.Update(key("enter")) // open manage menu
	assertContains(t, v.View(), "Manage role", "Set connection limit")

	v.Update(key("down"))  // move to "Set connection limit"
	v.Update(key("enter")) // select it -> opens the limit form
	assertContains(t, v.View(), "Connection limit", "app_user")

	v.Update(key("enter")) // submit the prefilled -1 (valid) -> exec cmd (ignored)
	if v.form.active {
		t.Error("form should close after submitting a valid connection limit")
	}
}

// TestTuningView renders the settings advisor with known input.
func TestTuningView(t *testing.T) {
	in := db.TuningInput{
		RAMBytes: 8 << 30, CPUs: 4, MaxConnections: 100,
		SharedBuffers: 128 << 20, EffectiveCache: 4 << 30, WorkMem: 4 << 20, MaintWorkMem: 64 << 20,
	}
	v := newTuningView(&config.Config{HostRAMMB: 8192, HostCPUs: 4}, nil)
	v.SetSize(120, 40)
	v.Update(tuningMsg{in: in, recs: db.Recommend(in)})
	assertContains(t, v.View(), "Configuration advisor", "host RAM 8.0 GB",
		"shared_buffers", "2.0 GB", "effective_cache_size", "work_mem", "max_connections")

	// Unknown host RAM/cores -> hint to set the env vars.
	v2 := newTuningView(&config.Config{}, nil)
	v2.SetSize(120, 40)
	v2.Update(tuningMsg{recs: db.Recommend(db.TuningInput{MaxConnections: 100, SharedBuffers: 128 << 20})})
	assertContains(t, v2.View(), "host RAM/cores unknown", "PGTUI_HOST_RAM_MB")
}

// TestTuningSettingsSection drives the ALTER SYSTEM editor: list, filter, edit
// form with constraints, and the compile-time guardrail.
func TestTuningSettingsSection(t *testing.T) {
	v := newTuningView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(allSettingsMsg{settings: []db.Setting{
		{Name: "work_mem", Setting: "4096", Unit: "kB", Context: "user", VarType: "integer", MinVal: "64", MaxVal: "2147483647"},
		{Name: "max_connections", Setting: "100", Context: "postmaster", VarType: "integer", MinVal: "1", MaxVal: "262143"},
		{Name: "block_size", Setting: "8192", Context: "internal", VarType: "integer"},
	}})
	v.Update(key("s")) // settings section
	assertContains(t, v.View(), "ALTER SYSTEM editor", "3 settings", "work_mem", "max_connections")

	// filter down to work_mem
	v.Update(key("/"))
	v.Update(key("work"))
	assertContains(t, v.View(), "1 settings", "work_mem")
	v.Update(key("esc"))

	// open the edit form; title carries the type/bounds constraint
	v.Update(key("enter"))
	assertContains(t, v.View(), "ALTER SYSTEM · work_mem", "New value", "64..2147483647")
	v.Update(key("esc")) // close the form

	// select the compile-time setting -> cannot change
	v.setFilter.SetValue("")
	v.applySettingsFilter()
	v.setTbl.SetCursor(2) // block_size (internal)
	v.Update(key("enter"))
	assertContains(t, v.View(), "compile-time")
}

// TestTuningHBASection checks the pg_hba viewer renders rules, the file path and
// a parse-error banner, and that the section selector switches with 'h'.
func TestTuningHBASection(t *testing.T) {
	v := newTuningView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(hbaMsg{
		file: "/etc/postgresql/pg_hba.conf",
		rules: []db.HBARule{
			{LineNumber: 90, Type: "local", Database: "all", UserName: "all", AuthMethod: "trust"},
			{LineNumber: 95, Type: "host", Database: "all", UserName: "all", Address: "0.0.0.0/0", AuthMethod: "scram-sha-256"},
			{LineNumber: 99, Type: "host", Database: "all", UserName: "all", Address: "::1", AuthMethod: "md5", Error: "bad line"},
		},
	})
	v.Update(key("h")) // switch to the pg_hba section
	assertContains(t, v.View(), "pg_hba", "Host-based authentication",
		"/etc/postgresql/pg_hba.conf", "scram-sha-256", "1 line(s) failed to parse")
}

// TestTuningHBAEdit drives adding a pg_hba rule: n -> fill form -> submit ->
// confirmation shows the built line. Also checks the read-only guardrail.
func TestTuningHBAEdit(t *testing.T) {
	tab := tea.KeyMsg{Type: tea.KeyTab}
	v := newTuningView(&config.Config{URL: "postgres://x"}, nil)
	v.SetSize(120, 40)
	v.Update(hbaMsg{
		file: "/x/pg_hba.conf", content: "host all all all trust\n", writable: true,
		rules: []db.HBARule{{LineNumber: 1, Type: "host", Database: "all", UserName: "all", Address: "all", AuthMethod: "trust"}},
	})
	v.Update(key("h")) // pg_hba section
	v.Update(key("n")) // add rule
	assertContains(t, v.View(), "Add pg_hba rule", "Type")

	v.Update(key("host"))
	v.Update(tab)
	v.Update(key("all"))
	v.Update(tab)
	v.Update(key("all"))
	v.Update(tab)
	v.Update(key("10.0.0.0/8"))
	v.Update(tab)
	v.Update(key("scram-sha-256"))
	v.Update(key("enter")) // submit -> confirmation

	assertContains(t, v.View(), "Write pg_hba.conf", "host all all 10.0.0.0/8 scram-sha-256")

	v.Update(key("y")) // confirm -> applyHBA cmd (not executed with nil mgr)
	if v.hbaConfirm.active {
		t.Error("confirm should close after y")
	}

	// read-only guardrail: no superuser -> editing is refused.
	v2 := newTuningView(&config.Config{}, nil)
	v2.SetSize(120, 40)
	v2.Update(hbaMsg{writable: false, rules: []db.HBARule{{LineNumber: 1, Type: "host", AuthMethod: "trust"}}})
	v2.Update(key("h"))
	v2.Update(key("n"))
	assertContains(t, v2.View(), "read-only")
}

// TestDashboardCardsUniform proves every dashboard card renders at the same
// height (padded, never clipped) even with very different content lengths.
func TestDashboardCardsUniform(t *testing.T) {
	v := newDashboardView(&config.Config{}, nil)
	v.SetSize(120, 40)
	cards := []dashCard{
		{"Short", []string{"one"}},
		{"Tall", []string{"one", "two", "three", "four"}},
		{"Mid", []string{"a", "b"}},
	}
	h := 0
	for _, c := range cards {
		if ch := v.cardHeight(c); ch > h {
			h = ch
		}
	}
	lines := func(s string) int { return strings.Count(s, "\n") + 1 }
	want := -1
	for _, c := range cards {
		got := lines(stCardBorder.Width(v.cardWidth()).Height(h).Render(v.cardContent(c)))
		if want == -1 {
			want = got
		} else if got != want {
			t.Errorf("card %q renders %d lines, want %d — cards are not uniform", c.title, got, want)
		}
	}
}

// TestRolesMenuCancel checks that esc dismisses the manage menu without action.
func TestRolesMenuCancel(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(rolesMsg{rows: []db.Role{{Name: "app_user", CanLogin: true}}})
	v.Update(key("enter"))
	assertContains(t, v.View(), "Manage role")
	v.Update(key("esc"))
	if v.menu.active {
		t.Error("menu should be closed after esc")
	}
	assertContains(t, v.View(), "Cluster roles")
}

// TestDashboardTickStartsOnce ensures that reopening the Dashboard tab does not
// create multiple auto-refresh loops (no DB needed — Init only builds commands).
func TestDashboardTickStartsOnce(t *testing.T) {
	d := newDashboardView(&config.Config{RefreshSeconds: 5}, nil)
	if d.started {
		t.Fatal("started should begin false")
	}
	d.Init()
	if !d.started {
		t.Fatal("first Init() should set started=true (starts the tick)")
	}
	// Subsequent reopens must not reset the flag (they don't duplicate the tick).
	for i := 0; i < 5; i++ {
		d.Init()
	}
	if !d.started {
		t.Fatal("started should remain true")
	}
}

// TestFuzzyMatch pins the matcher contract: subsequence (not just substring),
// order-preserving, case-insensitive, empty-query-matches-all, and a ranking
// that prefers an earlier / word-boundary hit.
func TestFuzzyMatch(t *testing.T) {
	// subsequence: 'apu' hits a,p,…,u in order inside "app_user".
	if _, pos, ok := fuzzyMatch("apu", "app_user"); !ok || len(pos) != 3 {
		t.Errorf("fuzzyMatch(apu, app_user) = ok=%v pos=%v, want ok with 3 positions", ok, pos)
	}
	// case-insensitive.
	if _, _, ok := fuzzyMatch("APP", "app_user"); !ok {
		t.Error("fuzzyMatch should be case-insensitive")
	}
	// no match when a rune is missing / out of order.
	if _, _, ok := fuzzyMatch("xyz", "app_user"); ok {
		t.Error("fuzzyMatch(xyz, app_user) should not match")
	}
	if _, _, ok := fuzzyMatch("resu", "app_user"); ok {
		t.Error("fuzzyMatch should require in-order runes (resu is out of order)")
	}
	// empty query matches everything with score 0.
	if s, _, ok := fuzzyMatch("", "whatever"); !ok || s != 0 {
		t.Errorf("empty query: ok=%v score=%d, want ok, 0", ok, s)
	}
	// ranking: a word-boundary / earlier hit outranks a mid-word one.
	early, _, _ := fuzzyMatch("user", "user_data")
	late, _, _ := fuzzyMatch("user", "app_user")
	if early <= late {
		t.Errorf("ranking: 'user_data'=%d should outrank 'app_user'=%d", early, late)
	}
}

// TestFinder drives the reusable finder: filter narrows the count, ranking puts
// the best match first, arrows move, and selectedIndex maps back to caller data.
func TestFinder(t *testing.T) {
	f := newFinder()
	f.open("Find role", []finderItem{
		{index: 0, label: "postgres"},
		{index: 1, label: "app_user"},
		{index: 2, label: "app_admin"},
	})
	if !f.active {
		t.Fatal("finder should be active after open")
	}
	assertContains(t, f.view(80, 30), "Find role", "3/3")

	// type "app" -> only the two app_* roles remain; postgres drops out.
	f.update(key("app"))
	assertContains(t, f.view(80, 30), "2/3")
	if got := f.selectedIndex(); got != 1 && got != 2 {
		t.Errorf("selectedIndex after 'app' = %d, want an app_* role (1 or 2)", got)
	}

	// ctrl+n moves down to the second match; enter would pick it.
	first := f.selectedIndex()
	f.update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if f.selectedIndex() == first {
		t.Error("ctrl+n should move the cursor to the next match")
	}
	res, _ := f.update(key("enter"))
	if res != finderSelect {
		t.Errorf("enter on a non-empty list should return finderSelect, got %v", res)
	}

	// esc cancels and closes.
	f.update(key("app"))
	res, _ = f.update(key("esc"))
	if res != finderCancel || f.active {
		t.Errorf("esc should cancel and close; res=%v active=%v", res, f.active)
	}

	// no-match: enter does nothing, count reads 0.
	f.open("t", []finderItem{{index: 0, label: "abc"}})
	f.update(key("zzz"))
	assertContains(t, f.view(80, 30), "0/1", "(no match)")
	if res, _ := f.update(key("enter")); res != finderNone {
		t.Errorf("enter with no match should be a no-op, got %v", res)
	}
}

// TestRolesFinderJump: '/' opens the finder, typing filters, enter jumps the
// table cursor to the matched role (no DB needed).
func TestRolesFinderJump(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(120, 40)
	v.Update(rolesMsg{rows: []db.Role{
		{Name: "postgres", Super: true, CanLogin: true},
		{Name: "app_user", CanLogin: true},
		{Name: "reporting", CanLogin: true},
	}})
	if v.tbl.Cursor() != 0 {
		t.Fatalf("cursor should start at 0, got %d", v.tbl.Cursor())
	}

	v.Update(key("/"))
	assertContains(t, v.View(), "Find role", "3/3")
	if !v.CapturingInput() {
		t.Error("roles must report CapturingInput while the finder is open")
	}

	v.Update(key("app"))   // narrows to app_user (index 1)
	v.Update(key("enter")) // jump
	if v.finder.active {
		t.Error("finder should close after selecting")
	}
	if v.tbl.Cursor() != 1 {
		t.Errorf("after finding 'app', cursor = %d, want 1 (app_user)", v.tbl.Cursor())
	}
	assertContains(t, v.View(), "Cluster roles")
}

// TestSessionsFinderJump: '/' finds a session by any column (here the user).
func TestSessionsFinderJump(t *testing.T) {
	v := newSessionsView(nil)
	v.SetSize(130, 40)
	v.Update(sessionsMsg{rows: []db.Session{
		{PID: 100, User: "alice", DB: "prod", State: "active", Query: "select 1"},
		{PID: 200, User: "bob", DB: "stage", State: "idle", Query: "update t"},
		{PID: 300, User: "carol", DB: "prod", State: "active", Query: "vacuum"},
	}})

	v.Update(key("/"))
	assertContains(t, v.View(), "Find session", "3/3")
	v.Update(key("bob"))
	assertContains(t, v.View(), "1/3")
	v.Update(key("enter"))
	if v.tbl.Cursor() != 1 {
		t.Errorf("after finding 'bob', cursor = %d, want 1", v.tbl.Cursor())
	}
}

// TestDatabasesFinderJump: '/' finds a database in the list, and a table in the
// table list.
func TestDatabasesFinderJump(t *testing.T) {
	v := newDatabasesView(nil)
	v.SetSize(120, 40)
	v.Update(databasesMsg{rows: []db.Database{
		{Name: "postgres"}, {Name: "b_fusion"}, {Name: "db_corely"},
	}})

	v.Update(key("/"))
	assertContains(t, v.View(), "Find database", "3/3")
	v.Update(key("corely"))
	v.Update(key("enter"))
	if v.dbTable.Cursor() != 2 {
		t.Errorf("after finding 'corely', db cursor = %d, want 2", v.dbTable.Cursor())
	}

	// table list: seed tables for the selected db and find one by name.
	v.selectedDB = "postgres"
	v.mode = modeTables
	v.Update(tablesMsg{dbname: "postgres", rows: []db.Table{
		{Schema: "public", Name: "widgets"},
		{Schema: "public", Name: "orders"},
		{Schema: "audit", Name: "log"},
	}})
	v.Update(key("/"))
	assertContains(t, v.View(), "Find table", "3/3")
	v.Update(key("orders"))
	v.Update(key("enter"))
	if v.tblTable.Cursor() != 1 {
		t.Errorf("after finding 'orders', table cursor = %d, want 1", v.tblTable.Cursor())
	}
}

// TestUpdatePrompt: when a newer release is reported, a 3-choice modal opens;
// an equal/older release stays silent. (Uses a DB-backed model, so it skips
// without DATABASE_URL — the update package's own logic is covered by pure
// tests in internal/update.)
func TestUpdatePrompt(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()
	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres",
		RefreshSeconds: 5, Version: "v0.1.0"}
	m := newConnected(cfg, mgr)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// A newer release arrives -> the prompt opens with all three choices.
	tm, _ = tm.Update(updateCheckedMsg{latest: "v9.9.9"})
	assertContains(t, tm.View(), "Update available", "v0.1.0", "v9.9.9",
		"Update now", "Not now", "Never suggest again")

	// esc = "not now": the modal closes, back to the app.
	tm, _ = tm.Update(key("esc"))
	if m.updateMenu.active {
		t.Error("esc should dismiss the update prompt")
	}
	assertContains(t, tm.View(), "Dashboard")

	// An equal (or older) release must not prompt.
	m2 := newConnected(cfg, mgr)
	var tm2 tea.Model = m2
	tm2, _ = tm2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm2, _ = tm2.Update(updateCheckedMsg{latest: "v0.1.0"})
	if m2.updateMenu.active {
		t.Error("an equal version should not open the update prompt")
	}
}

// TestQuitConfirm: 'q' asks before leaving; 'n' stays, 'q' then 'y' quits.
// ctrl+c is the immediate hard-quit and skips the prompt.
func TestQuitConfirm(t *testing.T) {
	mgr := testManager(t)
	defer mgr.Close()
	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	m := newConnected(cfg, mgr)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// 'q' opens the confirmation instead of quitting.
	tm, _ = tm.Update(key("q"))
	if !m.quitConfirm.active || m.quitting {
		t.Fatal("'q' should open the quit confirmation, not quit")
	}
	assertContains(t, tm.View(), "Quit pgtui?")

	// 'n' dismisses; the app keeps running.
	tm, _ = tm.Update(key("n"))
	if m.quitConfirm.active || m.quitting {
		t.Error("'n' should dismiss the quit confirmation and keep running")
	}

	// 'q' then 'y' quits for real.
	tm, _ = tm.Update(key("q"))
	tm, cmd := tm.Update(key("y"))
	if !m.quitting {
		t.Error("'q' then 'y' should quit")
	}
	if cmd == nil {
		t.Error("quitting should return a command (tea.Quit)")
	}

	// ctrl+c hard-quits without a prompt.
	m2 := newConnected(cfg, mgr)
	var tm2 tea.Model = m2
	tm2, _ = tm2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm2, _ = tm2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m2.quitConfirm.active || !m2.quitting {
		t.Error("ctrl+c should quit immediately, no confirmation")
	}
}

// TestDashboardAutoRefresh guards the refresh loop: a tick must reschedule the
// next reload (nil would freeze the dashboard), and the Connections card must
// disclose how many backends are pgtui's own.
func TestDashboardAutoRefresh(t *testing.T) {
	d := newDashboardView(&config.Config{RefreshSeconds: 5}, nil)
	d.SetSize(120, 40)

	if cmd := d.Update(tickMsg(time.Now())); cmd == nil {
		t.Fatal("tickMsg must return a command (reload + reschedule) or the dashboard freezes")
	}

	d.Update(dashboardMsg{data: db.DashboardData{
		Version: "PostgreSQL 18.4", MaxConns: 100, TotalConns: 40, PgtuiConns: 18,
		Active: 3, Idle: 30, TotalSize: "1 MB", StartedAt: time.Now(),
	}})
	assertContains(t, d.View(), "40 / 100", "incl. pgtui 18", "live", "auto-refresh every 5s")
}

func assertContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("View() does not contain %q\n---\n%s\n---", sub, s)
		}
	}
}

// TestRolesAttributesFlow drives the attribute editor and the guard that stands
// between an operator and an accidental privilege escalation.
func TestRolesAttributesFlow(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(140, 40)
	v.Update(rolesMsg{rows: []db.Role{
		{Name: "app_user", CanLogin: true, CreateDB: true},
		{Name: "auditor", CanLogin: true, BypassRLS: true},
	}})

	// The listing has to expose BYPASSRLS, and mark it: a role that defeats
	// row-level security must not read like an ordinary one.
	assertContains(t, v.View(), "BYPASSRLS", "⚠ yes")

	v.Update(key("enter")) // manage menu
	v.Update(key("down"))
	v.Update(key("down")) // "Edit attributes"
	v.Update(key("enter"))
	assertContains(t, v.View(), "Attributes", "app_user", "CREATEDB", "BYPASSRLS")

	// Submitting untouched must not produce a statement.
	v.Update(key("enter"))
	if v.form.active {
		t.Error("form should close after submit")
	}
	assertContains(t, v.View(), "nothing changed")
}

// TestRolesEscalationIsGuarded is the important one: turning SUPERUSER on must
// stop at a typed confirmation, like a drop does, instead of running straight
// away.
func TestRolesEscalationIsGuarded(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(140, 40)
	v.Update(rolesMsg{rows: []db.Role{{Name: "app_user", CanLogin: true}}})

	v.Update(key("enter"))
	v.Update(key("down"))
	v.Update(key("down"))
	v.Update(key("enter")) // attributes form

	// Move to SUPERUSER (4th field) and switch it to "yes".
	for i := 0; i < 3; i++ {
		v.Update(key("down"))
	}
	v.Update(key("right"))
	v.Update(key("enter")) // submit

	if !v.confirm.active {
		t.Fatal("enabling SUPERUSER ran without asking; it must be guarded like a drop")
	}
	assertContains(t, v.View(), "PRIVILEGE ESCALATION", "app_user", "SUPERUSER")
	if v.pendingAttrsSQL == "" {
		t.Error("no statement held for the confirmation")
	}
}

// TestRolesRevokeFormOffersNoOwnership: ownership is a transfer, not a
// privilege, so it must not appear in a revoke menu that cannot undo it.
// TestRolesRevokeScopeOffersNoOwnership drives R → pick database → privilege and
// checks the scope step never offers ownership (a transfer, not a privilege).
func TestRolesRevokeScopeOffersNoOwnership(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(140, 40)
	v.Update(rolesMsg{rows: []db.Role{{Name: "app_user", CanLogin: true}}})
	v.Update(databasesMsg{rows: []db.Database{{Name: "app_db"}}})

	v.Update(key("R"))
	assertContains(t, v.View(), "Revoke", "pick database", "app_db")
	v.Update(key("enter")) // pick app_db -> privilege form
	view := v.View()
	assertContains(t, view, "Revoke", "app_user", "app_db", "Privilege")
	if strings.Contains(view, db.GrantOwner.Label()) {
		t.Errorf("revoke scope offers ownership, which it cannot undo:\n%s", view)
	}
}

// TestRolesGrantFlowConfirms proves the grant path asks for confirmation (with
// the exact SQL) before applying — the guard for "enter on the wrong role".
func TestRolesGrantFlowConfirms(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(140, 40)
	v.Update(rolesMsg{rows: []db.Role{{Name: "app_user", CanLogin: true}}})
	v.Update(databasesMsg{rows: []db.Database{{Name: "app_db"}, {Name: "other"}}})

	v.Update(key("g"))
	assertContains(t, v.View(), "Grant", "pick database", "app_db")
	v.Update(key("app"))   // fuzzy-filter to app_db
	v.Update(key("enter")) // pick it -> privilege form
	assertContains(t, v.View(), "Grant", "app_user", "app_db", "Privilege")

	v.Update(key("enter")) // submit the first privilege (CONNECT) -> confirmation
	if !v.confirm.active {
		t.Fatal("grant must ask for confirmation before applying")
	}
	assertContains(t, v.View(), "Grant privileges?", "app_db", "app_user", "GRANT CONNECT ON DATABASE")
	if len(v.pendingGrantStmts) == 0 {
		t.Error("no statements staged for the confirmation")
	}

	v.Update(key("y")) // confirm (exec cmd not run with nil mgr)
	if v.confirm.active {
		t.Error("confirm should close after y")
	}
}

// TestRolesAccessReport renders the inspector's per-database report from a
// synthetic result (no DB needed).
func TestRolesAccessReport(t *testing.T) {
	v := newRolesView(&config.Config{}, nil)
	v.SetSize(140, 40)
	v.Update(rolesMsg{rows: []db.Role{{Name: "app_user", CanLogin: true}}})
	v.Update(roleAccessMsg{role: "app_user", rows: []db.DBAccess{
		{Database: "app_db", IsOwner: true, DBPrivs: []string{"CONNECT"},
			Schema: []string{"USAGE"}, Tables: []db.TablePriv{{Privilege: "SELECT", Count: 12}}},
	}})
	assertContains(t, v.View(), "Access · app_user", "app_db", "(owner)",
		"database: CONNECT", "schema public: USAGE", "SELECT×12")

	// Empty result explains PUBLIC/CONNECT instead of looking broken.
	v.Update(roleAccessMsg{role: "lonely", rows: nil})
	assertContains(t, v.View(), "Access · lonely", "no explicit", "PUBLIC")
}
