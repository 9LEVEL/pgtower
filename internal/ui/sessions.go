package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/db"
)

type sessionsView struct {
	mgr *db.Manager

	tbl      table.Model
	sessions []db.Session
	confirm  confirmModal
	alert    alertModal

	pendingAction string
	pendingPID    int32

	loading bool
	err     error
	status  string

	width, height int
}

func newSessionsView(mgr *db.Manager) *sessionsView {
	return &sessionsView{mgr: mgr, tbl: newTable(), confirm: newConfirmModal(), alert: newAlertModal()}
}

func (v *sessionsView) Title() string        { return "Sessions" }
func (v *sessionsView) CapturingInput() bool { return v.confirm.active || v.alert.active }

func (v *sessionsView) Init() tea.Cmd {
	v.loading = true
	v.err = nil
	return loadSessions(v.mgr)
}

func (v *sessionsView) SetSize(w, h int) {
	v.width, v.height = w, h
	th := h - 2
	if th < 3 {
		th = 3
	}
	v.tbl.SetHeight(th)
	qW := clampInt(w-64, 16, 80)
	v.tbl.SetColumns([]table.Column{
		{Title: "PID", Width: 8},
		{Title: "USER", Width: 12},
		{Title: "DATABASE", Width: 14},
		{Title: "STATE", Width: 10},
		{Title: "WAITING", Width: 12},
		{Title: "DURATION", Width: 9},
		{Title: "QUERY", Width: qW},
	})
}

func (v *sessionsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionsMsg:
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.sessions = msg.rows
			rows := make([]table.Row, 0, len(msg.rows))
			for _, s := range msg.rows {
				rows = append(rows, table.Row{
					fmt.Sprintf("%d", s.PID), s.User, s.DB, s.State, s.Wait, s.Duration, s.Query,
				})
			}
			v.tbl.SetRows(rows)
			if v.tbl.Cursor() >= len(rows) {
				v.tbl.SetCursor(0)
			}
		}
		return nil

	case sessionActionMsg:
		if msg.err != nil {
			v.status = stErr.Render(fmt.Sprintf("✗ %s pid %d", msg.action, msg.pid))
			v.alert.show(v.width, v.height, fmt.Sprintf("Failed to %s pid %d", msg.action, msg.pid), pgErrorText(msg.err), true)
		} else if msg.ok {
			v.status = stGood.Render(fmt.Sprintf("✓ %s sent to pid %d", msg.action, msg.pid))
		} else {
			v.status = stWarnV.Render(fmt.Sprintf("pid %d not found (already gone?)", msg.pid))
		}
		return loadSessions(v.mgr)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *sessionsView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.alert.active {
		v.alert.update(msg)
		return nil
	}
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return sessionAction(v.mgr, v.pendingAction, v.pendingPID)
		case confirmNo:
			v.status = stStatus.Render("cancelled")
		}
		return nil
	}

	switch msg.String() {
	case "r":
		return v.Init()
	case "c":
		return v.confirmOn("cancel", "Cancel the running query (pg_cancel_backend)?")
	case "k":
		return v.confirmOn("terminate", "Terminate the connection (pg_terminate_backend)? The session will be dropped.")
	}
	var cmd tea.Cmd
	v.tbl, cmd = v.tbl.Update(msg)
	return cmd
}

func (v *sessionsView) confirmOn(action, question string) tea.Cmd {
	i := v.tbl.Cursor()
	if i < 0 || i >= len(v.sessions) {
		return nil
	}
	s := v.sessions[i]
	v.pendingAction, v.pendingPID = action, s.PID
	body := fmt.Sprintf("%s\n\npid %d · %s · %s\n%s",
		question, s.PID, s.User, s.DB, stLabel.Render(truncate(s.Query, v.width-16)))
	return v.confirm.ask(actionTitle(action), body)
}

func actionTitle(action string) string {
	if action == "terminate" {
		return "Terminate connection"
	}
	return "Cancel query"
}

func (v *sessionsView) FooterHints() string {
	return hint("c", "cancel query") + "   " + hint("k", "terminate connection") + "   " +
		hint("r", "refresh") + "   " + hint("↑↓", "navigate")
}

func (v *sessionsView) View() string {
	if v.alert.active {
		return v.alert.view(v.width, v.height)
	}
	body := v.viewBody()
	if v.confirm.active {
		return v.confirm.view(v.width, v.height)
	}
	return body
}

func (v *sessionsView) viewBody() string {
	if v.err != nil {
		return "\n" + stErr.Render("Error: "+v.err.Error())
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Active sessions")
	meta := stLabel.Render(fmt.Sprintf("  %d client connections", len(v.sessions)))
	if v.loading {
		meta = stLabel.Render("  loading…")
	}
	head := "\n" + title + meta
	if v.status != "" {
		head += "   " + v.status
	}
	if len(v.sessions) == 0 && !v.loading {
		return head + "\n\n  " + stStatus.Render("No client sessions other than pgtui.")
	}
	return head + "\n" + v.tbl.View()
}
