package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// openHelp populates the help viewport and opens it.
func (m *Model) openHelp() {
	m.showHelp = true
	m.helpVP.Width = clampInt(m.width-8, 30, 84)
	m.helpVP.Height = clampInt(m.height-8, 4, 40)
	m.helpVP.SetContent(helpBody())
	m.helpVP.GotoTop()
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

// overlayHelp draws the help: fixed header (version + brand) and footer, with
// the shortcuts in a scrollable viewport to fit any terminal height.
func (m *Model) overlayHelp(bg string) string {
	ver := m.cfg.Version
	if ver == "" {
		ver = "dev"
	}
	header := stBrand.Render("pgtui") + stVersion.Render(" "+ver) +
		stKeyHint.Render("  ·  ") + stBrand.Render(brand)
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render("Keyboard shortcuts")

	footer := stKeyHint.Render("esc close")
	if m.helpVP.TotalLineCount() > m.helpVP.Height {
		footer += stKeyHint.Render(" · ↑↓ scroll")
	}

	content := header + "\n" + title + "\n\n" + m.helpVP.View() + "\n\n" + footer
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
