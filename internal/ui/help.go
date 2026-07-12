package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// openHelp populates the help viewport and opens it.
func (m *Model) openHelp() {
	m.showHelp = true
	m.sizeHelpViewport()
	m.helpVP.SetContent(helpBody())
	m.helpVP.GotoTop()
}

// sizeHelpViewport sizes the shortcuts viewport, leaving vertical room for the
// About block, title and footer so the modal never grows past the screen.
func (m *Model) sizeHelpViewport() {
	m.helpVP.Width = clampInt(m.width-8, 30, 84)
	m.helpVP.Height = clampInt(m.height-16, 4, 40)
}

// helpBody builds the (scrollable) help body.
func helpBody() string {
	rows := [][2]string{
		{"Global navigation", ""},
		{"1 – 6", "switch tab"},
		{"tab / shift+tab", "next / previous tab"},
		{"?", "open/close this help"},
		{"q  /  ctrl+c", "quit"},
		{"", ""},
		{"Dashboard", ""},
		{"r", "refresh now (auto every N s)"},
		{"", ""},
		{"Databases & Tables", ""},
		{"↑/↓  j/k", "navigate list"},
		{"enter", "database → tables → table data"},
		{"esc", "back one level"},
		{"r", "reload"},
		{"", ""},
		{"Table data (read-only)", ""},
		{"←/→  h/l", "navigate between columns (horizontal scroll)"},
		{"/", "search in the active column (ILIKE)"},
		{"e  or  :", "edit the top query (read-only)"},
		{"r", "reset to SELECT *"},
		{"esc", "back to the table list"},
		{"", ""},
		{"Databases: create / drop / describe", ""},
		{"n", "create database (in the database list)"},
		{"D", "drop database (type the name to confirm)"},
		{"d", "describe the table (columns, indexes, constraints)"},
		{"", ""},
		{"Query Runner", ""},
		{"enter / i", "focus the SQL editor"},
		{"/  or  ctrl+t", "switch the target database (filterable list)"},
		{"x", "EXPLAIN (plan, without running)"},
		{"ctrl+r  /  f5", "run the query"},
		{"esc", "leave the editor (focus results)"},
		{"↑/↓ ←/→", "scroll the results grid"},
		{"", ""},
		{"Sessions", ""},
		{"c", "cancel the session's query (pg_cancel_backend)"},
		{"k", "terminate connection (pg_terminate_backend)"},
		{"r", "refresh"},
		{"", ""},
		{"Roles", ""},
		{"enter", "manage role: reset password (generates a random one)"},
		{"n", "create role/user"},
		{"g", "grant to a database"},
		{"D", "drop role (type the name to confirm)"},
		{"F", "force-drop: reassign ownership to another role and remove"},
		{"", ""},
		{"Locks", ""},
		{"r", "reload the blocking tree"},
	}

	var b strings.Builder
	for _, r := range rows {
		switch {
		case r[0] == "" && r[1] == "":
			b.WriteString("\n")
		case r[1] == "":
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render(r[0]))
			b.WriteString("\n")
		default:
			key := stKey.Render(pad(r[0], 16))
			b.WriteString("  " + key + stKeyHint.Render(r[1]) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// overlayHelp draws the About + shortcuts overlay: a fixed identity block
// (name, version, brand, description, connection) above the scrollable list of
// shortcuts. All lines are wrapped to the viewport width so nothing is cut.
func (m *Model) overlayHelp(bg string) string {
	w := m.helpVP.Width
	wrap := lipgloss.NewStyle().Width(w)

	// --- About / identity block ---
	ident := stTitle.Render(" pgtui ") + stHeaderVer.Render(appVersion(m.cfg.Version)) +
		stKeyHint.Render("   ") + stBrand.Render(brand)
	desc := wrap.Foreground(colMuted).Render(
		"PostgreSQL administration TUI — dashboard, databases & tables, query runner, locks, sessions and roles.")
	conn := wrap.Render(
		stLabel.Render("connection  ") + stValue.Render(fmt.Sprintf("%s@%s:%s", m.cfg.User, m.cfg.Host, m.cfg.Port)) +
			stLabel.Render("    admin db  ") + stValue.Render(m.mgr.AdminDB()))
	rule := stKeyHint.Render(strings.Repeat("─", w))
	about := ident + "\n\n" + desc + "\n" + conn

	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render("Keyboard shortcuts")

	footer := stKeyHint.Render("esc close")
	if m.helpVP.TotalLineCount() > m.helpVP.Height {
		footer += stKeyHint.Render(" · ↑↓ scroll")
	}

	content := about + "\n" + rule + "\n\n" + title + "\n\n" + m.helpVP.View() + "\n\n" + footer
	box := stModal.BorderForeground(colAccent).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// pad adjusts a string to a fixed width (left-aligned).
func pad(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// truncate cuts a string (cell width) adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	// Cut by runes until it fits, reserving 1 for "…".
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
