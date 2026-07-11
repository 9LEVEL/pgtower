package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/db"
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
	th := h - 2
	if th < 3 {
		th = 3
	}
	v.tbl.SetHeight(th)

	qW := clampInt(w-58, 20, 70)
	v.tbl.SetColumns([]table.Column{
		{Title: "BLOQUEADO", Width: 9},
		{Title: "USER", Width: 12},
		{Title: "ESPERA", Width: 9},
		{Title: "BLOQUEIA", Width: 9},
		{Title: "POR USER", Width: 12},
		{Title: "QUERY BLOQUEADA", Width: qW},
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
	return hint("r", "recarregar") + "   " + hint("↑↓", "navegar")
}

func (v *locksView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Erro: "+v.err.Error())
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Árvore de bloqueios")
	if v.loading {
		return "\n" + title + stLabel.Render("  carregando…")
	}
	if v.count == 0 {
		return "\n" + title + "\n\n  " + stGood.Render("✓ Nenhuma sessão bloqueada. Tudo livre.")
	}

	meta := stLabel.Render(fmt.Sprintf("  %d sessão(ões) esperando por lock", v.count))
	detail := v.selectedDetail()
	return "\n" + title + meta + "\n" + v.tbl.View() + detail
}

// selectedDetail mostra a query completa da linha selecionada (bloqueada e
// bloqueadora), já que o grid trunca.
func (v *locksView) selectedDetail() string {
	row := v.tbl.SelectedRow()
	if row == nil {
		return ""
	}
	// row[5] é a query bloqueada já truncada; para o detalhe reusamos os dados
	// via SelectedRow — mostramos o essencial em duas linhas.
	label := stLabel.Render
	return "\n\n" + label("bloqueada: ") + stValue.Render(row[5])
}
