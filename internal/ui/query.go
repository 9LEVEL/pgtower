package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

type queryMode int

const (
	modeEdit queryMode = iota
	modeResults
	modeConfirm
	modeTarget
)

type queryView struct {
	cfg *config.Config
	mgr *db.Manager

	editor  textarea.Model
	results table.Model
	confirm textinput.Model
	target  textinput.Model

	mode     queryMode
	targetDB string

	// seletor de database (aberto com '/' ou ctrl+t)
	dbNames    []string
	dbFiltered []string
	dbCursor   int

	pendingSQL    string
	pendingDanger db.Danger

	running bool
	res     db.QueryResult
	hasRes  bool
	command string
	err     error
	status  string

	width, height int
}

func newQueryView(cfg *config.Config, mgr *db.Manager) *queryView {
	ta := textarea.New()
	ta.Placeholder = "SELECT * FROM pg_stat_activity LIMIT 20;   (i / enter para editar)"
	ta.ShowLineNumbers = true
	ta.CharLimit = 100000

	ci := textinput.New()
	ci.Placeholder = "digite: sim"
	ci.CharLimit = 16

	ti := textinput.New()
	ti.Placeholder = "filtrar…"
	ti.CharLimit = 63

	v := &queryView{
		cfg:      cfg,
		mgr:      mgr,
		editor:   ta,
		results:  newTable(),
		confirm:  ci,
		target:   ti,
		mode:     modeResults, // inicia em navegação; 'i'/enter foca o editor
		targetDB: mgr.AdminDB(),
	}
	return v
}

func (v *queryView) Title() string { return "Query" }

func (v *queryView) CapturingInput() bool {
	return v.mode == modeEdit || v.mode == modeConfirm || v.mode == modeTarget
}

// Init não precisa devolver comando: a aba entra em modo navegação (editor
// desfocado) e o textarea cuida do próprio blink ao ser focado.
func (v *queryView) Init() tea.Cmd { return nil }

func (v *queryView) SetSize(w, h int) {
	v.width, v.height = w, h
	edH := clampInt(h/3, 4, 8)
	v.editor.SetWidth(w - 2)
	v.editor.SetHeight(edH)
	resH := h - edH - 4
	if resH < 3 {
		resH = 3
	}
	v.results.SetHeight(resH)
	v.confirm.Width = 20
	v.target.Width = 30
}

func (v *queryView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case queryMsg:
		v.running = false
		v.err = msg.err
		v.hasRes = false
		v.command = ""
		if msg.err == nil {
			v.res = msg.res
			if len(msg.res.Columns) == 0 {
				v.command = msg.res.Command
				v.status = fmt.Sprintf("OK · %s · %s", msg.res.Command, msg.res.Elapsed.Round(1e6))
			} else {
				v.hasRes = true
				v.buildResults()
				trunc := ""
				if msg.res.Truncated {
					trunc = fmt.Sprintf(" (truncado em %d)", len(msg.res.Rows))
				}
				v.status = fmt.Sprintf("%d linha(s)%s · %s", msg.res.RowCount, trunc, msg.res.Elapsed.Round(1e6))
				v.mode = modeResults
				v.editor.Blur()
			}
		} else {
			v.status = ""
		}
		return nil

	case databasesMsg:
		// Alimenta o seletor de database ('/' ou ctrl+t). É broadcast.
		if msg.err == nil {
			v.dbNames = v.dbNames[:0]
			for _, d := range msg.rows {
				v.dbNames = append(v.dbNames, d.Name)
			}
			if v.mode == modeTarget {
				v.filterDBs()
			}
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *queryView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch v.mode {
	case modeConfirm:
		return v.handleConfirmKey(msg)
	case modeTarget:
		return v.handleTargetKey(msg)
	}

	// Teclas comuns a modeEdit e modeResults.
	switch msg.String() {
	case "ctrl+r", "f5":
		return v.submit()
	case "ctrl+t":
		return v.openTarget()
	}

	if v.mode == modeEdit {
		if msg.Type == tea.KeyEsc {
			v.mode = modeResults
			v.editor.Blur()
			return nil
		}
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}

	// modeResults (navegação): teclas únicas são comandos.
	switch msg.String() {
	case "/":
		return v.openTarget()
	case "x":
		return v.explain()
	case "enter", "i", "e", "a":
		v.mode = modeEdit
		return v.editor.Focus()
	}
	var cmd tea.Cmd
	v.results, cmd = v.results.Update(msg)
	return cmd
}

// openTarget abre o seletor de database: recarrega a lista e foca o filtro.
func (v *queryView) openTarget() tea.Cmd {
	v.mode = modeTarget
	v.editor.Blur()
	v.target.SetValue("")
	v.dbCursor = 0
	v.filterDBs()
	return tea.Batch(loadDatabases(v.mgr), v.target.Focus())
}

// filterDBs recomputa a lista visível a partir do texto do filtro (substring,
// case-insensitive) e mantém o cursor dentro dos limites.
func (v *queryView) filterDBs() {
	q := strings.ToLower(strings.TrimSpace(v.target.Value()))
	v.dbFiltered = v.dbFiltered[:0]
	for _, name := range v.dbNames {
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			v.dbFiltered = append(v.dbFiltered, name)
		}
	}
	if v.dbCursor >= len(v.dbFiltered) {
		v.dbCursor = len(v.dbFiltered) - 1
	}
	if v.dbCursor < 0 {
		v.dbCursor = 0
	}
}

func (v *queryView) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	if v.pendingDanger == db.Critical {
		switch msg.Type {
		case tea.KeyEsc:
			v.cancelPending()
			return nil
		case tea.KeyEnter:
			if strings.EqualFold(strings.TrimSpace(v.confirm.Value()), "sim") {
				return v.execPending()
			}
			v.status = stBadV.Render("confirmação incorreta — digite 'sim' para prosseguir")
			return nil
		}
		var cmd tea.Cmd
		v.confirm, cmd = v.confirm.Update(msg)
		return cmd
	}

	// Write: y/n
	switch strings.ToLower(msg.String()) {
	case "y", "s":
		return v.execPending()
	case "n", "esc":
		v.cancelPending()
	}
	return nil
}

func (v *queryView) handleTargetKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.target.Blur()
		v.mode = modeResults
		return nil
	case "up", "ctrl+p":
		if v.dbCursor > 0 {
			v.dbCursor--
		}
		return nil
	case "down", "ctrl+n":
		if v.dbCursor < len(v.dbFiltered)-1 {
			v.dbCursor++
		}
		return nil
	case "enter":
		name := ""
		if v.dbCursor >= 0 && v.dbCursor < len(v.dbFiltered) {
			name = v.dbFiltered[v.dbCursor]
		} else {
			// Sem item na lista: usa o texto digitado como nome literal.
			name = strings.TrimSpace(v.target.Value())
		}
		v.target.Blur()
		if name == "" {
			v.mode = modeResults
			return nil
		}
		if name != v.targetDB {
			v.targetDB = name
			v.status = "alvo alterado para " + name
		}
		v.mode = modeEdit
		return v.editor.Focus()
	}
	// Demais teclas atualizam o filtro.
	var cmd tea.Cmd
	v.target, cmd = v.target.Update(msg)
	v.filterDBs()
	return cmd
}

// submit valida o SQL e decide entre executar direto ou pedir confirmação.
func (v *queryView) submit() tea.Cmd {
	sql := strings.TrimSpace(v.editor.Value())
	if sql == "" {
		v.status = stWarnV.Render("query vazia")
		return nil
	}
	v.err = nil
	danger := db.Classify(sql)
	if danger == db.Safe {
		return v.run(sql)
	}

	v.pendingSQL = sql
	v.pendingDanger = danger
	v.mode = modeConfirm
	v.editor.Blur()
	if danger == db.Critical {
		v.confirm.SetValue("")
		return v.confirm.Focus()
	}
	return nil
}

// explain roda EXPLAIN (plano, sem executar) da query do editor. EXPLAIN é
// read-only, então dispensa confirmação.
func (v *queryView) explain() tea.Cmd {
	sql := strings.TrimSpace(v.editor.Value())
	if sql == "" {
		v.status = stWarnV.Render("query vazia")
		return nil
	}
	v.err = nil
	return v.run("EXPLAIN " + sql)
}

func (v *queryView) execPending() tea.Cmd {
	sql := v.pendingSQL
	v.confirm.Blur()
	v.confirm.SetValue("")
	v.mode = modeEdit
	cmd := v.run(sql)
	return tea.Batch(cmd, v.editor.Focus())
}

func (v *queryView) cancelPending() {
	v.pendingSQL = ""
	v.confirm.Blur()
	v.confirm.SetValue("")
	v.mode = modeEdit
	v.status = stStatus.Render("execução cancelada")
	_ = v.editor.Focus()
}

func (v *queryView) run(sql string) tea.Cmd {
	v.running = true
	v.status = "executando em " + v.targetDB + "…"
	return runQuery(v.mgr, v.targetDB, sql)
}

func (v *queryView) buildResults() {
	res := v.res
	n := len(res.Columns)
	widths := make([]int, n)
	for i, c := range res.Columns {
		widths[i] = len([]rune(c))
	}
	for _, row := range res.Rows {
		for i, cell := range row {
			if l := len([]rune(cell)); l > widths[i] {
				widths[i] = l
			}
		}
	}
	cols := make([]table.Column, n)
	for i := range widths {
		cols[i] = table.Column{Title: strings.ToUpper(res.Columns[i]), Width: clampInt(widths[i]+1, 4, 48)}
	}
	rows := make([]table.Row, len(res.Rows))
	for i, r := range res.Rows {
		rows[i] = table.Row(r)
	}
	v.results.SetColumns(cols)
	v.results.SetRows(rows)
	v.results.SetCursor(0)
}

func (v *queryView) FooterHints() string {
	switch v.mode {
	case modeConfirm:
		return hint("y/n", "confirmar/cancelar")
	case modeTarget:
		return hint("↑↓", "escolher") + "   " + hint("enter", "confirmar") + "   " + hint("esc", "cancelar")
	case modeEdit:
		return hint("ctrl+r", "executar") + "   " + hint("ctrl+t", "trocar db") + "   " + hint("esc", "resultados")
	default:
		return hint("i", "editar") + "  " + hint("/", "trocar db") + "  " + hint("x", "explain") + "  " + hint("ctrl+r", "executar") + "  " + hint("↑↓", "rolar")
	}
}

func (v *queryView) View() string {
	// Cabeçalho do editor.
	label := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("SQL")
	tgt := stLabel.Render("  alvo: ") + stValue.Render(v.targetDB)
	editorTitle := label + tgt

	borderColor := colBorder
	if v.mode == modeEdit {
		borderColor = colAccent
	}
	editorBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(v.editor.View())

	var b strings.Builder
	b.WriteString(editorTitle + "\n")
	b.WriteString(editorBox + "\n")
	b.WriteString(v.statusLine() + "\n")
	b.WriteString(v.resultsArea())

	page := b.String()

	switch v.mode {
	case modeConfirm:
		return v.overlayConfirm(page)
	case modeTarget:
		return v.overlayTarget(page)
	}
	return page
}

func (v *queryView) statusLine() string {
	if v.running {
		return stWarnV.Render("⏳ " + v.status)
	}
	if v.err != nil {
		return stErr.Render("✗ " + collapseErr(v.err.Error()))
	}
	if v.status != "" {
		return stStatus.Render(v.status)
	}
	return stKeyHint.Render("dica: ctrl+r executa · statements de escrita pedem confirmação")
}

func (v *queryView) resultsArea() string {
	if v.command != "" {
		return "\n  " + stGood.Render("✓ "+v.command)
	}
	if v.hasRes {
		return v.results.View()
	}
	return "\n  " + stKeyHint.Render("(sem resultados ainda)")
}

func (v *queryView) overlayConfirm(bg string) string {
	var title, body string
	if v.pendingDanger == db.Critical {
		title = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colDanger).
			Padding(0, 1).Render(" ⚠  OPERAÇÃO CRÍTICA ")
		body = fmt.Sprintf("Este comando pode causar perda de dados:\n\n%s\n\nDigite %s e pressione Enter para executar:\n\n%s",
			stBadV.Render(previewSQL(v.pendingSQL)),
			stKey.Render("sim"),
			v.confirm.View())
	} else {
		title = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colWarn).
			Padding(0, 1).Render(" Confirmar escrita ")
		body = fmt.Sprintf("Statement de escrita:\n\n%s\n\n%s executar    %s cancelar",
			stWarnV.Render(previewSQL(v.pendingSQL)),
			stKey.Render("y"), stKey.Render("n"))
	}
	box := stModal.BorderForeground(colDanger).Render(title + "\n\n" + body)
	return lipgloss.Place(v.width, v.height, lipgloss.Center, lipgloss.Center, box)
}

func (v *queryView) overlayTarget(bg string) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render(" Rodar queries em qual database? ")

	// Lista filtrada (janela de até 12 itens ao redor do cursor).
	const window = 12
	start := 0
	if v.dbCursor >= window {
		start = v.dbCursor - window + 1
	}
	end := start + window
	if end > len(v.dbFiltered) {
		end = len(v.dbFiltered)
	}

	var list strings.Builder
	if len(v.dbFiltered) == 0 {
		list.WriteString(stKeyHint.Render("  (nenhum database bate com o filtro)"))
	}
	for i := start; i < end; i++ {
		name := v.dbFiltered[i]
		marker := "  "
		if name == v.targetDB {
			marker = stGood.Render("● ")
		}
		line := marker + name
		if i == v.dbCursor {
			line = lipgloss.NewStyle().Foreground(colOnDark).Background(colAccent).Bold(true).
				Render(" " + pad(marker+name, 30) + " ")
		}
		list.WriteString(line + "\n")
	}

	count := fmt.Sprintf("%d/%d", len(v.dbFiltered), len(v.dbNames))
	hints := stKeyHint.Render("↑↓ selecionar · enter confirmar · esc cancelar · " + count)
	body := "filtro: " + v.target.View() + "\n\n" + strings.TrimRight(list.String(), "\n") + "\n\n" + hints

	box := stModal.BorderForeground(colAccent).Width(46).Render(title + "\n\n" + body)
	return lipgloss.Place(v.width, v.height, lipgloss.Center, lipgloss.Center, box)
}

func previewSQL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + " …"
	}
	return s
}

func collapseErr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
