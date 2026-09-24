package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtower/internal/db"
)

type locksView struct {
	mgr *db.Manager

	tbl     table.Model
	count   int
	loading bool
	err     error

	width, height int
}

func newLocksView(mgr *db.Manager) *locksView {
	v := &locksView{mgr: mgr}
	v.tbl = newTable()
	return v
}

func (v *locksView) Title() string        { return "Locks" }
func (v *locksView) CapturingInput() bool { return false }

func (v *locksView) Init() tea.Cmd {
	v.loading = true
	v.err = nil
	return loadLocks(v.mgr)
}

func (v *locksView) SetSize(w, h int) {
	v.width, v.height = w, h
	// blank line + title above the table, blank line + "blocked:" detail below.
	th := h - 4
	if th < 3 {
		th = 3
	}
	v.tbl.SetHeight(th)

	qW := clampInt(w-58, 20, 70)
	v.tbl.SetColumns([]table.Column{
		{Title: "BLOCKED", Width: 9},
		{Title: "USER", Width: 12},
		{Title: "WAITING", Width: 9},
		{Title: "BLOCKED BY", Width: 10},
		{Title: "BY USER", Width: 12},
		{Title: "BLOCKED QUERY", Width: qW},
	})
}

func (v *locksView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case locksMsg:
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.count = len(msg.rows)
			rows := make([]table.Row, 0, len(msg.rows))
			for _, b := range msg.rows {
				rows = append(rows, table.Row{
					fmt.Sprintf("%d", b.BlockedPID),
					b.BlockedUser,
					b.BlockedFor,
					fmt.Sprintf("%d", b.BlockingPID),
					b.BlockingUser,
					b.BlockedQuery,
				})
			}
			v.tbl.SetRows(rows)
			v.tbl.SetCursor(0)
		}
		return nil

	case tea.KeyMsg:
		if msg.String() == "r" {
			return v.Init()
		}
		var cmd tea.Cmd
		v.tbl, cmd = v.tbl.Update(msg)
		return cmd
	}
	return nil
}

func (v *locksView) FooterHints() string {
	return hint("r", "reload") + "   " + hint("↑↓", "navigate")
}

func (v *locksView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Error: "+v.err.Error())
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Blocking tree")
	if v.loading {
		return "\n" + title + stLabel.Render("  loading…")
	}
	if v.count == 0 {
		return "\n" + title + "\n\n  " + stGood.Render("✓ No blocked sessions. All clear.")
	}

	meta := stLabel.Render(fmt.Sprintf("  %d session(s) waiting on a lock", v.count))
	detail := v.selectedDetail()
	return "\n" + title + meta + "\n" + v.tbl.View() + detail
}

// selectedDetail shows the full query of the selected row (blocked and
// blocking), since the grid truncates.
func (v *locksView) selectedDetail() string {
	row := v.tbl.SelectedRow()
	if row == nil {
		return ""
	}
	// row[5] is the blocked query already truncated; for the detail we reuse
	// the data via SelectedRow — we show the essentials in two lines.
	label := stLabel.Render
	return "\n\n" + label("blocked: ") + stValue.Render(row[5])
}
