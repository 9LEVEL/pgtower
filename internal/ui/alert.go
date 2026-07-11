package ui

import (
	"errors"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackc/pgx/v5/pgconn"
)

// alertModal mostra uma mensagem longa (ex.: erro completo do Postgres) num
// box rolável. Fecha com esc/enter/q.
type alertModal struct {
	active bool
	title  string
	danger bool
	vp     viewport.Model
}

func newAlertModal() alertModal {
	return alertModal{vp: viewport.New(60, 10)}
}

func (a *alertModal) show(w, h int, title, body string, danger bool) {
	a.active = true
	a.title = title
	a.danger = danger
	a.vp.Width = clampInt(w-12, 30, 96)
	a.vp.Height = clampInt(h-8, 4, 24)
	a.vp.SetContent(lipgloss.NewStyle().Width(a.vp.Width).Render(body))
	a.vp.GotoTop()
}

// update devolve true quando o alerta é fechado.
func (a *alertModal) update(msg tea.KeyMsg) bool {
	if !a.active {
		return false
	}
	switch msg.String() {
	case "esc", "enter", "q", " ":
		a.active = false
		return true
	}
	a.vp, _ = a.vp.Update(msg)
	return false
}

func (a *alertModal) view(w, h int) string {
	bg := colDanger
	if !a.danger {
		bg = colAccent
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(bg).
		Padding(0, 1).Render(" " + a.title + " ")
	hint := stKeyHint.Render("↑↓ rolar · esc/enter fechar")
	content := title + "\n\n" + a.vp.View() + "\n\n" + hint
	box := stModal.BorderForeground(bg).Render(content)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

// pgErrorText extrai a mensagem completa de um erro Postgres (Message + Detail
// + Hint), que os status de uma linha cortam.
func pgErrorText(err error) string {
	if err == nil {
		return ""
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		s := "ERRO: " + pg.Message
		if pg.Detail != "" {
			s += "\n\nDETALHE: " + pg.Detail
		}
		if pg.Hint != "" {
			s += "\n\nHINT: " + pg.Hint
		}
		return s
	}
	return err.Error()
}
