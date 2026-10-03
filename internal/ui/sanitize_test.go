package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/9level/pgtower/internal/config"
	"github.com/9level/pgtower/internal/db"
)

func TestSafeView(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"text and line breaks", "a ñ 日本\nb", "a ñ 日本\nb"},
		{"SGR styling kept", "\x1b[1;38;2;1;2;3mx\x1b[0m\x1b[4:3my", "\x1b[1;38;2;1;2;3mx\x1b[0m\x1b[4:3my"},
		{"OSC: window title, clipboard", "a\x1b]0;t\x07\x1b]52;c;aGk=\x07b", "a�]0;t��]52;c;aGk=�b"},
		{"cursor move, clear screen", "\x1b[2J\x1b[H", "�[2J�[H"},
		{"tab and carriage return", "a\tb\rc", "a b c"},
		{"8-bit CSI (C1 control)", "a\u009b2Jb", "a�2Jb"},
		{"invalid byte", "a\x9b2Jb", "a�2Jb"},
		{"other C0 and DEL", "a\x00\x08\x7fb", "a���b"},
		{"lone ESC at the end", "a\x1b", "a�"},
	}
	for _, c := range cases {
		if got := safeView(c.in); got != c.want {
			t.Errorf("%s: safeView(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func trueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// Any database user controls the text of their query and their
// application_name, and pgtower shows both to the administrator. They are
// shown, but can't drive the administrator's terminal.
func TestSessionsCannotDriveTerminal(t *testing.T) {
	trueColor(t)
	var m tea.Model = newConnected(&config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres"}, new(db.Manager))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m, _ = m.Update(key("5"))
	m, _ = m.Update(sessionsMsg{rows: []db.Session{{PID: 42, User: "app", DB: "postgres",
		AppName: "evil\x1b[2J", State: "active", Duration: "00:00:01",
		Query: "select 1 /*\x1b]0;pwned\x07\x1b]52;c;ZXZpbA==\x07*/"}}})

	view := m.View()
	for _, bad := range []string{"\x1b]", "\x07", "\x1b[2J"} {
		if strings.Contains(view, bad) {
			t.Errorf("the frame lets %q through to the terminal", bad)
		}
	}
	assertContains(t, view, "select 1 /*", "\x1b[") // shown, and styled
}

// The UI's own frames pass through untouched: only what came from the data
// can change.
func TestSafeViewKeepsOwnFrames(t *testing.T) {
	trueColor(t)
	mm := newConnected(&config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres",
		Version: "v9.9.9", RefreshSeconds: 5}, new(db.Manager))
	var m tea.Model = mm
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = m.Update(dashboardMsg{data: db.DashboardData{Version: "PostgreSQL 18.4", MaxConns: 50,
		TotalConns: 41, Active: 3, Idle: 30, CacheHitRatio: 99.9, DBCount: 2, TotalSize: "216 MB",
		Uptime: time.Hour, StartedAt: time.Now().Add(-time.Hour)}})
	check := func(where string) {
		t.Helper()
		if v := mm.view(); safeView(v) != v {
			t.Errorf("%s: safeView changed the UI's own frame", where)
		}
	}
	check("dashboard")
	for _, k := range []string{"2", "3", "4", "5", "6", "7"} {
		m, _ = m.Update(key(k))
		check("tab " + k)
	}
	m, _ = m.Update(key("?"))
	check("help")
	m, _ = m.Update(key("?"))
	m, _ = m.Update(key("S"))
	check("servers")
}
