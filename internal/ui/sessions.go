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

	pendingAction string
	pendingPID    int32

	loading bool
	err     error
	status  string

	width, height int
}

func newSessionsView(mgr *db.Manager) *sessionsView {
	return &sessionsView{mgr: mgr, tbl: newTable(), confirm: newConfirmModal()}
}

func (v *sessionsView) Title() string        { return "Sessões" }
func (v *sessionsView) CapturingInput() bool { return v.confirm.active }

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
		{Title: "ESTADO", Width: 10},
		{Title: "ESPERA", Width: 12},
		{Title: "DURAÇÃO", Width: 9},
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
			v.status = stErr.Render(fmt.Sprintf("✗ %s pid %d: %s", msg.action, msg.pid, collapseErr(msg.err.Error())))
		} else if msg.ok {
			v.status = stGood.Render(fmt.Sprintf("✓ %s enviado ao pid %d", msg.action, msg.pid))
		} else {
			v.status = stWarnV.Render(fmt.Sprintf("pid %d não encontrado (já terminou?)", msg.pid))
		}
		return loadSessions(v.mgr)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *sessionsView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return sessionAction(v.mgr, v.pendingAction, v.pendingPID)
		case confirmNo:
			v.status = stStatus.Render("cancelado")
		}
		return nil
	}

	switch msg.String() {
	case "r":
		return v.Init()
	case "c":
		return v.confirmOn("cancelar", "Cancelar a query em execução (pg_cancel_backend)?")
	case "k":
		return v.confirmOn("encerrar", "Encerrar a conexão (pg_terminate_backend)? A sessão será derrubada.")
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
	if action == "encerrar" {
		return "Encerrar conexão"
	}
	return "Cancelar query"
}

func (v *sessionsView) FooterHints() string {
	return hint("c", "cancelar query") + "   " + hint("k", "encerrar conexão") + "   " +
		hint("r", "atualizar") + "   " + hint("↑↓", "navegar")
}

func (v *sessionsView) View() string {
	body := v.viewBody()
	if v.confirm.active {
		return v.confirm.view(v.width, v.height)
	}
	return body
}

func (v *sessionsView) viewBody() string {
	if v.err != nil {
		return "\n" + stErr.Render("Erro: "+v.err.Error())
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Sessões ativas")
	meta := stLabel.Render(fmt.Sprintf("  %d conexões de cliente", len(v.sessions)))
	if v.loading {
		meta = stLabel.Render("  carregando…")
	}
	head := "\n" + title + meta
	if v.status != "" {
		head += "   " + v.status
	}
	if len(v.sessions) == 0 && !v.loading {
		return head + "\n\n  " + stStatus.Render("Nenhuma sessão de cliente além do pgtui.")
	}
	return head + "\n" + v.tbl.View()
}
