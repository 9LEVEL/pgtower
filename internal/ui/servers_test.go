package ui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
)

// isolatedStore returns an empty store whose saves land in a temp dir.
func isolatedStore(t *testing.T) *config.Store {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "PGHOST", "PGTUI_CONFIG"} {
		t.Setenv(k, "")
	}
	t.Setenv("PGTUI_CONFIG_DIR", t.TempDir())
	s, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func typeText(tm tea.Model, s string) tea.Model {
	for _, r := range s {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return tm
}

// runCmd executes a command and feeds every resulting message back into the
// model, unpacking batches (a tiny stand-in for the Bubble Tea runtime).
func runCmd(t *testing.T, tm tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return tm
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			tm = runCmd(t, tm, c)
		}
		return tm
	}
	if msg == nil {
		return tm
	}
	tm, next := tm.Update(msg)
	_ = next // don't chase follow-ups (ticks would loop forever)
	return tm
}

func TestScopeTagsAndUnpacks(t *testing.T) {
	type payload struct{ v int }
	one := func() tea.Msg { return payload{1} }

	if got := scope(3, one)(); got != (scopedMsg{gen: 3, msg: payload{1}}) {
		t.Errorf("a plain result must be tagged with the generation, got %#v", got)
	}
	batch, ok := scope(3, tea.Batch(one, one))().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a batch must stay a batch for the runtime, got %#v", batch)
	}
	if got := batch[0](); got != (scopedMsg{gen: 3, msg: payload{1}}) {
		t.Errorf("batch members must be tagged too, got %#v", got)
	}
	if _, ok := scope(3, tea.Quit)().(tea.QuitMsg); !ok {
		t.Error("Bubble Tea control messages must pass through untagged")
	}
	if scope(3, nil) != nil {
		t.Error("nil stays nil")
	}
}

// Results from a previous session must never reach the current one.
func TestStaleSessionResultsAreDropped(t *testing.T) {
	m := New(&config.Store{}, "dev", nil)
	m.gen = 2
	m.sess = &session{gen: 2, cfg: &config.Config{Name: "b"}} // no tabs needed: statusMsg is model-level
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	stale := scopedMsg{gen: m.sess.gen - 1, msg: statusMsg("from server A")}
	tm, _ = tm.Update(stale)
	if m.status == "from server A" {
		t.Error("a result tagged with an old session must be dropped")
	}
	tm, _ = tm.Update(scopedMsg{gen: m.sess.gen, msg: statusMsg("from server B")})
	if m.status != "from server B" {
		t.Error("a result from the current session must be delivered")
	}
}

func TestStartWithoutServersOpensManager(t *testing.T) {
	m := New(isolatedStore(t), "v1.0.0", nil)
	var tm tea.Model = m
	m.Init()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.servers.active {
		t.Fatal("with no server configured the Servers screen must open")
	}
	assertContains(t, tm.View(), "Servers", "No servers yet", "add")

	// esc cannot leave: there is nothing behind it.
	tm, _ = tm.Update(key("esc"))
	if !m.servers.active {
		t.Error("esc must not close the Servers screen while not connected")
	}
}

func TestAddServerSavesAndConnects(t *testing.T) {
	store := isolatedStore(t)
	m := New(store, "dev", nil)
	var tm tea.Model = m
	m.Init()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	tm, _ = tm.Update(key("a"))
	if !m.servers.form.active {
		t.Fatal("'a' should open the new-server form")
	}
	tm = typeText(tm, "local")
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyTab})
	tm = typeText(tm, "127.0.0.1")
	tm, cmd := tm.Update(key("enter"))

	if m.servers.form.active {
		t.Fatalf("a valid form should close, err=%q", m.servers.form.err)
	}
	if len(store.Connections) != 1 || store.Connections[0].Name != "local" {
		t.Fatalf("server not added: %+v", store.Connections)
	}
	b, err := os.ReadFile(filepath.Join(os.Getenv("PGTUI_CONFIG_DIR"), "config.yml"))
	if err != nil || !strings.Contains(string(b), "name: local") {
		t.Errorf("the new server must be saved to config.yml: %v\n%s", err, b)
	}
	if cmd == nil || m.connecting != "local" {
		t.Error("the first server added should be connected right away")
	}
}

func TestServerFormValidation(t *testing.T) {
	m := New(isolatedStore(t), "dev", nil)
	var tm tea.Model = m
	m.Init()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm, _ = tm.Update(key("a"))
	tm, _ = tm.Update(key("enter")) // empty name
	if !m.servers.form.active || m.servers.form.err == "" {
		t.Fatal("an invalid server must keep the form open with an error")
	}
	assertContains(t, tm.View(), "name is required")
}

// The user-reported case: an unreachable server used to kill pgtui with
// `ping "postgres": context deadline exceeded`. Now it explains itself and
// leaves the user on the Servers screen.
func TestConnectFailureIsExplained(t *testing.T) {
	store := isolatedStore(t)
	m := New(store, "dev", nil)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 45})

	cfg := &config.Config{Name: "prod", Host: "192.168.150.171", Port: "5432", User: "juliano"}
	m.attempt = 7
	m.connecting = "prod"
	e := db.ExplainConnect(fmt.Errorf("ping %q: %w", "postgres", errDeadline{}), cfg.Host, cfg.Port)
	tm, _ = tm.Update(connectedMsg{attempt: 7, cfg: cfg, err: e})

	if m.connecting != "" || m.sess != nil {
		t.Error("a failed connect must not leave a half-open session")
	}
	assertContains(t, tm.View(), "Cannot connect to “prod”", "Server did not respond",
		"What to check", "nc -vz 192.168.150.171 5432", "Technical details")

	tm, _ = tm.Update(key("esc"))
	if !m.servers.active {
		t.Error("after the error the Servers screen should be there to pick another server")
	}
}

type errDeadline struct{}

func (errDeadline) Error() string   { return "context deadline exceeded" }
func (errDeadline) Timeout() bool   { return true }
func (errDeadline) Temporary() bool { return true }

// A result for a connect the user already abandoned must be ignored.
func TestSupersededConnectIsIgnored(t *testing.T) {
	m := New(isolatedStore(t), "dev", nil)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.attempt = 2
	tm, _ = tm.Update(connectedMsg{attempt: 1, cfg: &config.Config{Name: "old"},
		err: &db.ConnError{Title: "x", Detail: "y"}})
	if m.notice.active {
		t.Error("an outdated connect result must not raise an alert")
	}
}

// End to end through the real driver: a closed local port.
func TestConnectRefusedEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	store := isolatedStore(t)
	if err := store.Upsert("", config.Connection{Name: "closed", Host: "127.0.0.1", Port: port}); err != nil {
		t.Fatal(err)
	}
	c, _ := store.Find("closed")
	m := New(store, "dev", &c)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	tm = runCmd(t, tm, m.Init())
	assertContains(t, tm.View(), "Connection refused", "nothing is accepting connections")
}

func TestMigrationNoticeShownOnce(t *testing.T) {
	store := isolatedStore(t)
	store.Migration = &config.Migration{From: []string{"config.yml (v1)"}, Path: "/opt/pgtui/config.yml",
		Backups: []string{"/opt/pgtui/config.yml.v1.bak"}, Imported: []string{"10.0.0.5"}}
	m := New(store, "v0.9.0", nil)
	var tm tea.Model = m
	m.Init()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	assertContains(t, tm.View(), "Configuration upgraded", "config.yml.v1.bak", "10.0.0.5")

	tm, _ = tm.Update(key("esc"))
	if m.notice.active {
		t.Error("the notice closes with esc")
	}
	assertContains(t, tm.View(), "Servers")
}

// TestSwitchServersLive connects to one server, switches to another and checks
// the old session's late results are dropped. Both names point at the test
// cluster; what matters is the session swap.
func TestSwitchServersLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping UI test")
	}
	store := isolatedStore(t)
	for _, n := range []string{"alpha", "beta"} {
		if err := store.Upsert("", config.Connection{Name: n, URL: dsn}); err != nil {
			t.Fatal(err)
		}
	}
	alpha, _ := store.Find("alpha")
	m := New(store, "dev", &alpha)
	defer m.Close()
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	tm = runCmd(t, tm, m.Init())
	if m.sess == nil || m.sess.cfg.Name != "alpha" {
		t.Fatalf("should be connected to alpha, notice=%v", m.notice.active)
	}
	assertContains(t, tm.View(), "alpha", "Dashboard")
	oldGen := m.sess.gen

	tm, _ = tm.Update(key("S"))
	tm, _ = tm.Update(key("j")) // → beta
	tm, cmd := tm.Update(key("enter"))
	assertContains(t, tm.View(), "connecting to beta")
	tm = runCmd(t, tm, cmd)
	if m.sess.cfg.Name != "beta" || m.sess.gen == oldGen || m.servers.active {
		t.Fatalf("should have switched to beta and closed the Servers screen: %+v", m.sess.cfg)
	}
	assertContains(t, tm.View(), "beta")

	// A late dashboard result from alpha must not land on beta's dashboard.
	tm, _ = tm.Update(scopedMsg{gen: oldGen, msg: dashboardMsg{err: fmt.Errorf("stale from alpha")}})
	if strings.Contains(tm.View(), "stale from alpha") {
		t.Error("a result from the previous server leaked into the new session")
	}
}

// stubTab renders a fixed number of lines, to test the page layout.
type stubTab struct{ lines int }

func (s stubTab) Title() string          { return "Stub" }
func (s stubTab) Init() tea.Cmd          { return nil }
func (s stubTab) Update(tea.Msg) tea.Cmd { return nil }
func (s stubTab) SetSize(int, int)       {}
func (s stubTab) CapturingInput() bool   { return false }
func (s stubTab) FooterHints() string    { return "" }
func (s stubTab) View() string           { return strings.TrimSuffix(strings.Repeat("row\n", s.lines), "\n") }

// Whatever a tab renders, the page is exactly the terminal height: the header
// stays on the first line and the footer on the last.
func TestPageAlwaysFillsTerminal(t *testing.T) {
	for _, n := range []int{1, 20, 34, 35, 80} {
		m := New(&config.Store{}, "v1.2.3", nil)
		m.gen = 1
		m.sess = &session{gen: 1, cfg: &config.Config{Name: "srv", Tag: config.TagProd}, tabs: []tabView{stubTab{n}}}
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 37})
		page := strings.Split(tm.View(), "\n")
		if len(page) != 37 {
			t.Errorf("body of %d lines: page has %d lines, want 37", n, len(page))
		}
		if !strings.Contains(page[0], "pgtui") {
			t.Errorf("body of %d lines: header scrolled away, first line %q", n, page[0])
		}
	}
}
