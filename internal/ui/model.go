package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

// tabView é o contrato de cada aba do TUI.
type tabView interface {
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
	SetSize(w, h int)
	// CapturingInput indica que a aba está capturando texto (ex.: editor de
	// query focado). Enquanto true, o root não intercepta teclas globais
	// além de ctrl+c.
	CapturingInput() bool
	// FooterHints retorna as dicas de atalho específicas da aba.
	FooterHints() string
}

// Model é o modelo Bubble Tea raiz.
type Model struct {
	cfg  *config.Config
	mgr  *db.Manager
	tabs []tabView

	active int
	width  int
	height int

	showHelp bool
	status   string
	fatalErr error
	quitting bool
}

// New monta o modelo raiz com todas as abas.
func New(cfg *config.Config, mgr *db.Manager) *Model {
	m := &Model{cfg: cfg, mgr: mgr}
	m.tabs = []tabView{
		newDashboardView(cfg, mgr),
		newDatabasesView(mgr),
		newQueryView(cfg, mgr),
		newLocksView(mgr),
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.tabs))
	for _, t := range m.tabs {
		if c := t.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		bodyH := m.bodyHeight()
		for _, t := range m.tabs {
			t.SetSize(msg.Width, bodyH)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Roteia mouse apenas para a aba ativa.
		return m, m.tabs[m.active].Update(msg)

	case statusMsg:
		m.status = string(msg)
		return m, nil
	}

	// Mensagens não-tecla (resultados de DB, tick) são transmitidas a todas as
	// abas — cada uma ignora o que não reconhece.
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.Update(msg); c != nil {
			cmds = append(cmds, c)
		}
	}
	return m, tea.Batch(cmds...)
}

// statusMsg atualiza a linha de status transitória.
type statusMsg string

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dbg("key=%q type=%d active=%d(%s) capturing=%v help=%v", msg.String(), msg.Type,
		m.active, m.tabs[m.active].Title(), m.tabs[m.active].CapturingInput(), m.showHelp)
	// ctrl+c encerra sempre.
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}

	// Help overlay: qualquer tecla fecha.
	if m.showHelp {
		if msg.String() == "?" || msg.Type == tea.KeyEsc || msg.String() == "q" {
			m.showHelp = false
		}
		return m, nil
	}

	capturing := m.tabs[m.active].CapturingInput()

	// Teclas globais só quando a aba não está capturando texto.
	if !capturing {
		switch msg.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "?":
			m.showHelp = true
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.tabs) {
				return m, m.switchTo(idx)
			}
			return m, nil
		case "tab":
			return m, m.switchTo((m.active + 1) % len(m.tabs))
		case "shift+tab":
			return m, m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs))
		}
	}

	m.status = ""
	return m, m.tabs[m.active].Update(msg)
}

// switchTo troca a aba ativa e dispara o Init dela (recarrega dados).
func (m *Model) switchTo(idx int) tea.Cmd {
	dbg("switchTo(%d) from %d", idx, m.active)
	if idx == m.active {
		return nil
	}
	m.active = idx
	m.status = ""
	m.tabs[idx].SetSize(m.width, m.bodyHeight())
	return m.tabs[idx].Init()
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Carregando…"
	}

	header := m.renderHeader()
	tabbar := m.renderTabBar()
	footer := m.renderFooter()
	body := m.tabs[m.active].View()

	page := lipgloss.JoinVertical(lipgloss.Left, header, tabbar, body, footer)

	if m.showHelp {
		return m.overlayHelp(page)
	}
	return page
}

// bodyHeight calcula a altura disponível para o corpo da aba.
func (m *Model) bodyHeight() int {
	// header(1) + tabbar(1) + footer(1) = 3 linhas de cromo.
	h := m.height - 3
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) renderHeader() string {
	left := stTitle.Render(" pgtui ")
	conn := fmt.Sprintf(" %s@%s:%s  •  admin db: %s ",
		m.cfg.User, m.cfg.Host, m.cfg.Port, m.mgr.AdminDB())
	right := stStatus.Render(conn)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) renderTabBar() string {
	var parts []string
	for i, t := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.Title())
		if i == m.active {
			parts = append(parts, stTabActive.Render(label))
		} else {
			parts = append(parts, stTabInactive.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m *Model) renderFooter() string {
	if m.fatalErr != nil {
		return stErr.Render("erro: " + m.fatalErr.Error())
	}
	tabHints := m.tabs[m.active].FooterHints()
	global := strings.Join([]string{
		hint("1-4", "abas"),
		hint("?", "ajuda"),
		hint("q", "sair"),
	}, "   ")

	line := global
	if tabHints != "" {
		line = tabHints + stKeyHint.Render("   │   ") + global
	}
	if m.status != "" {
		line = stStatus.Render(m.status)
	}
	if lipgloss.Width(line) > m.width {
		line = truncate(line, m.width)
	}
	return line
}
