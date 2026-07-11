package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/db"
)

type dbMode int

const (
	modeDBList dbMode = iota
	modeTables
)

type databasesView struct {
	mgr *db.Manager

	mode     dbMode
	dbTable  table.Model
	tblTable table.Model

	dbs        []db.Database
	selectedDB string
	tblCount   int
	tblTotal   string

	loading bool
	err     error

	width, height int
}

func newDatabasesView(mgr *db.Manager) *databasesView {
	v := &databasesView{mgr: mgr}
	v.dbTable = newTable()
	v.tblTable = newTable()
	return v
}

func (v *databasesView) Title() string       { return "Bancos" }
func (v *databasesView) CapturingInput() bool { return false }

func (v *databasesView) Init() tea.Cmd {
	v.loading = true
	v.err = nil
	return loadDatabases(v.mgr)
}

func (v *databasesView) SetSize(w, h int) {
	v.width, v.height = w, h
	tblH := h - 2
	if tblH < 3 {
		tblH = 3
	}
	v.dbTable.SetHeight(tblH)
	v.tblTable.SetHeight(tblH)
	v.layoutColumns()
}

func (v *databasesView) layoutColumns() {
	w := v.width
	if w < 40 {
		w = 40
	}
	// Bancos: nome | owner | tamanho | conexões
	nameW := clampInt(w-40, 18, 48)
	v.dbTable.SetColumns([]table.Column{
		{Title: "DATABASE", Width: nameW},
		{Title: "OWNER", Width: 16},
		{Title: "TAMANHO", Width: 12},
		{Title: "CONEXÕES", Width: 9},
	})
	// Tabelas: schema | tabela | total | heap | índices | linhas (est.)
	tnameW := clampInt(w-56, 16, 44)
	v.tblTable.SetColumns([]table.Column{
		{Title: "SCHEMA", Width: 12},
		{Title: "TABELA", Width: tnameW},
		{Title: "TOTAL", Width: 10},
		{Title: "HEAP", Width: 10},
		{Title: "ÍNDICES", Width: 10},
		{Title: "LINHAS~", Width: 12},
	})
}

func (v *databasesView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case databasesMsg:
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.dbs = msg.rows
			rows := make([]table.Row, 0, len(msg.rows))
			for _, d := range msg.rows {
				rows = append(rows, table.Row{d.Name, d.Owner, d.SizePretty, fmt.Sprintf("%d", d.Connections)})
			}
			v.dbTable.SetRows(rows)
		}
		return nil

	case tablesMsg:
		if msg.dbname != v.selectedDB {
			return nil // resultado de outro banco; ignora
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.tblCount = len(msg.rows)
			var total int64
			rows := make([]table.Row, 0, len(msg.rows))
			for _, t := range msg.rows {
				total += t.TotalBytes
				rows = append(rows, table.Row{
					t.Schema, t.Name, t.TotalSize, t.TableSize, t.IndexSize, estRows(t.EstRows),
				})
			}
			v.tblTotal = prettyBytes(total)
			v.tblTable.SetRows(rows)
			v.tblTable.SetCursor(0)
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *databasesView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch v.mode {
	case modeDBList:
		switch msg.String() {
		case "enter":
			row := v.dbTable.SelectedRow()
			if row == nil {
				return nil
			}
			v.selectedDB = row[0]
			v.mode = modeTables
			v.loading = true
			v.err = nil
			v.tblTable.SetRows(nil)
			return loadTables(v.mgr, v.selectedDB)
		case "r":
			return v.Init()
		}
		var cmd tea.Cmd
		v.dbTable, cmd = v.dbTable.Update(msg)
		return cmd

	case modeTables:
		switch msg.String() {
		case "esc":
			v.mode = modeDBList
			v.err = nil
			return nil
		case "r":
			v.loading = true
			v.err = nil
			return loadTables(v.mgr, v.selectedDB)
		}
		var cmd tea.Cmd
		v.tblTable, cmd = v.tblTable.Update(msg)
		return cmd
	}
	return nil
}

func (v *databasesView) FooterHints() string {
	switch v.mode {
	case modeTables:
		return hint("esc", "voltar") + "   " + hint("↑↓", "navegar") + "   " + hint("r", "recarregar")
	default:
		return hint("enter", "abrir tabelas") + "   " + hint("↑↓", "navegar") + "   " + hint("r", "recarregar")
	}
}

func (v *databasesView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Erro: "+v.err.Error())
	}

	switch v.mode {
	case modeTables:
		title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).
			Render(fmt.Sprintf("Tabelas · %s", v.selectedDB))
		meta := stLabel.Render(fmt.Sprintf("  %d tabelas · %s", v.tblCount, v.tblTotal))
		if v.loading {
			meta = stLabel.Render("  carregando…")
		}
		body := v.tblTable.View()
		if v.tblCount == 0 && !v.loading {
			body = "\n  " + stStatus.Render("Nenhuma tabela em schemas de usuário.")
		}
		return "\n" + title + meta + "\n" + body

	default:
		title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Databases do cluster")
		meta := stLabel.Render(fmt.Sprintf("  %d bancos", len(v.dbs)))
		if v.loading {
			meta = stLabel.Render("  carregando…")
		}
		return "\n" + title + meta + "\n" + v.dbTable.View()
	}
}

// --- helpers de tabela ---

func newTable() table.Model {
	t := table.New(table.WithFocused(true), table.WithHeight(10))
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		BorderBottom(true).
		Bold(true).
		Foreground(colMuted)
	s.Selected = s.Selected.
		Foreground(colOnDark).
		Background(colAccent).
		Bold(true)
	s.Cell = s.Cell.Foreground(colFg)
	t.SetStyles(s)
	return t
}

// estRows formata a estimativa de linhas; -1 significa "nunca analisada".
func estRows(n int64) string {
	if n < 0 {
		return "?"
	}
	return human(n)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func prettyBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}
