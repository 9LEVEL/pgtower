package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

// tabView is the contract for each TUI tab.
type tabView interface {
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
	SetSize(w, h int)
	// CapturingInput indicates the tab is capturing text (e.g. the query
	// editor focused). While true, the root does not intercept global keys
	// other than ctrl+c.
	CapturingInput() bool
	// FooterHints returns the tab-specific shortcut hints.
	FooterHints() string
}

// Model is the root Bubble Tea model.
type Model struct {
	cfg  *config.Config
	mgr  *db.Manager
	tabs []tabView

	active int
	width  int
	height int

	showHelp bool
	helpVP   viewport.Model
	status   string
	fatalErr error
	quitting bool
}

// New builds the root model with all tabs.
func New(cfg *config.Config, mgr *db.Manager) *Model {
	m := &Model{cfg: cfg, mgr: mgr, helpVP: viewport.New(60, 10)}
	m.tabs = []tabView{
		newDashboardView(cfg, mgr),
		newDatabasesView(mgr),
		newQueryView(cfg, mgr),
		newLocksView(mgr),
		newSessionsView(mgr),
		newRolesView(cfg, mgr),
		newTuningView(cfg, mgr),
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
		m.sizeHelpViewport()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Route the mouse only to the active tab.
		return m, m.tabs[m.active].Update(msg)

	case statusMsg:
		m.status = string(msg)
		return m, nil
	}

	// Non-key messages (DB results, tick) are broadcast to all tabs — each one
	// ignores what it doesn't recognize.
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.Update(msg); c != nil {
			cmds = append(cmds, c)
		}
	}
	return m, tea.Batch(cmds...)
}

// statusMsg updates the transient status line.
type statusMsg string

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Never log the actual key while a field is capturing text — it could be a
	// password or other secret being typed into a form.
	capturingNow := m.tabs[m.active].CapturingInput()
	keyStr := msg.String()
	if capturingNow {
		keyStr = "<redacted>"
	}
	dbg("key=%q type=%d active=%d(%s) capturing=%v help=%v", keyStr, msg.Type,
		m.active, m.tabs[m.active].Title(), capturingNow, m.showHelp)
	// ctrl+c always quits.
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}

	// Help overlay: ↑↓ scroll; ?/esc/q close.
	if m.showHelp {
		if msg.String() == "?" || msg.Type == tea.KeyEsc || msg.String() == "q" {
			m.showHelp = false
			return m, nil
		}
		var cmd tea.Cmd
		m.helpVP, cmd = m.helpVP.Update(msg)
		return m, cmd
	}

	capturing := m.tabs[m.active].CapturingInput()

	// Global keys only when the tab is not capturing text.
	if !capturing {
		switch msg.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "?":
			m.openHelp()
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

// switchTo switches the active tab and triggers its Init (reloads data).
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
		return "Loading…"
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

// bodyHeight computes the available height for the tab body.
func (m *Model) bodyHeight() int {
	// header(1) + tabbar(1) + footer(1) = 3 lines of chrome.
	h := m.height - 3
	if h < 3 {
		h = 3
	}
	return h
}

// appVersion returns the running version for display, never empty.
func appVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func (m *Model) renderHeader() string {
	left := stTitle.Render(" pgtui ") + stHeaderVer.Render(appVersion(m.cfg.Version))

	conn := stStatus.Render(fmt.Sprintf(" %s@%s:%s • admin db: %s ",
		m.cfg.User, m.cfg.Host, m.cfg.Port, m.mgr.AdminDB()))
	right := conn + stBrand.Render(brand+" ")

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
		return stErr.Render("error: " + m.fatalErr.Error())
	}
	tabHints := m.tabs[m.active].FooterHints()
	global := strings.Join([]string{
		hint("1-7", "tabs"),
		hint("?", "help"),
		hint("q", "quit"),
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
