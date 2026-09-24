package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtower/internal/config"
	"github.com/9level/pgtower/internal/db"
	"github.com/9level/pgtower/internal/update"
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
	store   *config.Store
	version string

	// sess is the live connection (nil until the first one succeeds); gen
	// numbers sessions so stale results can be told apart (see session).
	sess *session
	gen  int

	// connecting is the name of the connection being opened ("" = idle);
	// attempt invalidates a connect the user abandoned or superseded.
	start      *config.Connection
	connecting string
	attempt    int

	active int
	width  int
	height int

	showHelp bool
	helpVP   viewport.Model
	status   string
	fatalErr error
	quitting bool

	// servers is the connection manager (S / ctrl+o).
	servers serversModal

	// notices queues model-level alerts (connection errors, the one-time
	// migration notice) so one never hides another.
	notice  alertModal
	notices []pendingNotice

	// update checker (startup "a newer release is available" prompt)
	updateEnabled bool
	updateLatest  string
	updateMenu    actionMenu
	updateAlert   alertModal
	updating      bool

	// quit confirmation: 'q' asks before leaving (guards against a stray press);
	// ctrl+c still hard-quits immediately.
	quitConfirm confirmModal
}

type pendingNotice struct {
	title, body string
	danger      bool
}

// New builds the root model. start is the connection to open right away; nil
// opens the Servers screen so the user can pick or create one.
func New(store *config.Store, version string, start *config.Connection) *Model {
	m := &Model{store: store, version: version, start: start, helpVP: viewport.New(60, 10),
		updateAlert: newAlertModal(), notice: newAlertModal(), quitConfirm: newConfirmModal(),
		servers: newServersModal()}
	// Only offer updates for real release builds the user hasn't opted out of.
	m.updateEnabled = store.UpdateCheck && update.IsRelease(version) && !update.OptedOut()
	if mig := store.Migration; mig != nil && len(mig.From) > 0 {
		m.notify(migrationNotice(mig, version))
	}
	return m
}

// AnnounceRename queues the one-time "pgtui is now pgtower" notice when this
// run finished (or could not finish) moving a pgtui-era install to the new
// name: the binary, the config directories, PGTUI_* variables.
func (m *Model) AnnounceRename(bin update.NameResult) {
	var moved []string
	var moveErr error
	if mig := m.store.Migration; mig != nil {
		moved, moveErr = mig.Moved, mig.MoveErr
	}
	if title, body, danger, ok := renameNotice(bin, moved, moveErr, config.LegacyEnv()); ok {
		m.notify(title, body, danger)
	}
}

// newConnected builds a model around an already-open manager (tests).
func newConnected(cfg *config.Config, mgr *db.Manager) *Model {
	m := New(&config.Store{UpdateCheck: cfg.UpdateCheck}, cfg.Version, nil)
	m.gen = 1
	m.sess = newSession(m.gen, cfg, mgr)
	return m
}

// Close releases the active session's connections.
func (m *Model) Close() {
	if m.sess != nil && m.sess.mgr != nil {
		m.sess.mgr.Close()
	}
}

func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case m.sess != nil:
		for _, t := range m.sess.tabs {
			cmds = append(cmds, m.sess.scope(t.Init()))
		}
	case m.start != nil:
		cmds = append(cmds, m.connect(*m.start))
	default:
		m.openServers()
	}
	if m.updateEnabled {
		cmds = append(cmds, checkUpdateCmd(m.version))
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.sess != nil {
			for _, t := range m.sess.tabs {
				t.SetSize(msg.Width, m.bodyHeight())
			}
		}
		m.sizeHelpViewport()
		m.flushNotices()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Route the mouse only to the active tab.
		if m.sess == nil {
			return m, nil
		}
		return m, m.sess.scope(m.sess.tabs[m.active].Update(msg))

	case scopedMsg:
		if m.sess == nil || msg.gen != m.sess.gen {
			dbg("drop stale %T from session %d", msg.msg, msg.gen)
			return m, nil
		}
		return m.Update(msg.msg)

	case connectedMsg:
		return m, m.handleConnected(msg)

	case probeMsg:
		m.servers.probeDone(msg)
		return m, nil

	case statusMsg:
		m.status = string(msg)
		return m, nil

	case updateCheckedMsg:
		if msg.err == nil && update.IsNewer(msg.latest, m.version) {
			m.updateLatest = msg.latest
			m.updateMenu.open("Update available · "+appVersion(m.version)+" → "+msg.latest,
				[]string{"Update now", "Not now", "Never suggest again"})
		}
		return m, nil

	case updateResultMsg:
		m.updating = false
		if msg.ok {
			m.updateAlert.show(m.width, m.height, "Update complete",
				"Updated to "+m.updateLatest+".\n\nRestart pgtower to run the new version.", false)
		} else {
			m.updateAlert.show(m.width, m.height, "Update", msg.text, msg.danger)
		}
		return m, nil
	}

	// Other messages (DB results, ticks) are broadcast to all tabs — each one
	// ignores what it doesn't recognize.
	if m.sess == nil {
		return m, nil
	}
	var cmds []tea.Cmd
	for _, t := range m.sess.tabs {
		if c := t.Update(msg); c != nil {
			cmds = append(cmds, m.sess.scope(c))
		}
	}
	return m, tea.Batch(cmds...)
}

// statusMsg updates the transient status line.
type statusMsg string

// connectedMsg reports the outcome of opening a connection.
type connectedMsg struct {
	attempt int
	cfg     *config.Config
	mgr     *db.Manager
	err     *db.ConnError
}

// connectTotalTimeout bounds a whole connect (dial + auth + first ping).
const connectTotalTimeout = 20 * time.Second

// connect opens c in the background; the result arrives as connectedMsg. The
// current session (if any) stays usable until the new one is ready.
func (m *Model) connect(c config.Connection) tea.Cmd {
	cfg, err := m.store.Resolve(c, m.version)
	if err != nil {
		m.notify("Invalid connection “"+c.Name+"”", err.Error(), true)
		return nil
	}
	m.attempt++
	attempt := m.attempt
	m.connecting = c.Name
	m.status = ""
	dbg("connect attempt=%d name=%q host=%s", attempt, c.Name, cfg.Host)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTotalTimeout)
		defer cancel()
		mgr, err := db.NewManager(ctx, cfg.URL, cfg.AdminDB)
		if err != nil {
			return connectedMsg{attempt: attempt, cfg: cfg, err: db.ExplainConnect(err, cfg.Host, cfg.Port)}
		}
		return connectedMsg{attempt: attempt, cfg: cfg, mgr: mgr}
	}
}

// cancelConnect abandons the pending connect; its result will be discarded.
func (m *Model) cancelConnect() {
	m.attempt++
	m.connecting = ""
}

func (m *Model) handleConnected(msg connectedMsg) tea.Cmd {
	if msg.attempt != m.attempt {
		// Superseded or cancelled: don't leak its pools.
		if msg.mgr != nil {
			go msg.mgr.Close()
		}
		return nil
	}
	m.connecting = ""
	if msg.err != nil {
		m.notify(connErrorNotice(msg.cfg, msg.err))
		if m.sess == nil {
			m.openServers()
		}
		return nil
	}

	m.sess.close()
	m.gen++
	m.sess = newSession(m.gen, msg.cfg, msg.mgr)
	m.servers.close()
	m.status = "connected to " + msg.cfg.Name
	cmds := make([]tea.Cmd, 0, len(m.sess.tabs))
	for _, t := range m.sess.tabs {
		t.SetSize(m.width, m.bodyHeight())
		cmds = append(cmds, m.sess.scope(t.Init()))
	}
	return tea.Batch(cmds...)
}

// notify shows an alert, or queues it behind the one on screen (or until the
// first window size is known).
func (m *Model) notify(title, body string, danger bool) {
	m.notices = append(m.notices, pendingNotice{title, body, danger})
	m.flushNotices()
}

func (m *Model) flushNotices() {
	if m.notice.active || m.width == 0 || len(m.notices) == 0 {
		return
	}
	n := m.notices[0]
	m.notices = m.notices[1:]
	m.notice.show(m.width, m.height, n.title, n.body, n.danger)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Never log the actual key while a field is capturing text — it could be a
	// password or other secret being typed into a form.
	capturingNow := m.capturing()
	keyStr := msg.String()
	if capturingNow {
		keyStr = "<redacted>"
	}
	dbg("key=%q type=%d active=%d capturing=%v help=%v", keyStr, msg.Type,
		m.active, capturingNow, m.showHelp)
	// ctrl+c always quits.
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}

	// Update prompt (model-level modal) takes priority over everything but quit.
	if m.updateAlert.active {
		m.updateAlert.update(msg)
		return m, nil
	}
	if m.updating {
		return m, nil // ignore keys while the self-update runs
	}
	if m.updateMenu.active {
		if m.updateMenu.update(msg) == menuSelect {
			return m, m.handleUpdateChoice(m.updateMenu.cursor)
		}
		// esc/q closed the menu — treated as "not now".
		return m, nil
	}

	// Connection errors and notices.
	if m.notice.active {
		if m.notice.update(msg) {
			m.flushNotices()
		}
		return m, nil
	}

	// Quit confirmation ('q' opened it; y/enter leaves, n/esc stays).
	if m.quitConfirm.active {
		switch m.quitConfirm.update(msg) {
		case confirmYes:
			m.quitting = true
			return m, tea.Quit
		case confirmNo:
			return m, nil
		}
		return m, nil
	}

	// Servers screen.
	if m.servers.active {
		return m, m.serversKey(msg)
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

	// While connecting without a session there is nothing else to drive.
	if m.sess == nil {
		switch msg.String() {
		case "esc":
			m.cancelConnect()
			m.openServers()
		case "q":
			m.quitConfirm.ask("Quit pgtower?", "Leave pgtower?")
		case "S", "ctrl+o":
			m.openServers()
		}
		return m, nil
	}

	// Global keys only when the tab is not capturing text.
	if !m.capturing() {
		switch msg.String() {
		case "q":
			m.quitConfirm.ask("Quit pgtower?", "Leave pgtower? This closes the app and its database connections.")
			return m, nil
		case "?":
			m.openHelp()
			return m, nil
		case "S", "ctrl+o":
			m.openServers()
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.sess.tabs) {
				return m, m.switchTo(idx)
			}
			return m, nil
		case "tab":
			return m, m.switchTo((m.active + 1) % len(m.sess.tabs))
		case "shift+tab":
			return m, m.switchTo((m.active - 1 + len(m.sess.tabs)) % len(m.sess.tabs))
		}
	}

	m.status = ""
	return m, m.sess.scope(m.sess.tabs[m.active].Update(msg))
}

// capturing reports whether text input currently owns the keyboard.
func (m *Model) capturing() bool {
	if m.servers.capturing() {
		return true
	}
	return m.sess != nil && m.sess.tabs[m.active].CapturingInput()
}

// switchTo switches the active tab and triggers its Init (reloads data).
func (m *Model) switchTo(idx int) tea.Cmd {
	dbg("switchTo(%d) from %d", idx, m.active)
	if idx == m.active {
		return nil
	}
	m.active = idx
	m.status = ""
	m.sess.tabs[idx].SetSize(m.width, m.bodyHeight())
	return m.sess.scope(m.sess.tabs[idx].Init())
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
	body := m.renderBody()

	page := lipgloss.JoinVertical(lipgloss.Left, header, tabbar, body, footer)

	if m.showHelp {
		return m.overlayHelp(page)
	}
	if m.updateAlert.active {
		return m.updateAlert.view(m.width, m.height)
	}
	if m.updating {
		return m.renderUpdating()
	}
	if m.updateMenu.active {
		return m.updateMenu.view(m.width, m.height)
	}
	if m.notice.active {
		return m.notice.view(m.width, m.height)
	}
	if m.quitConfirm.active {
		return m.quitConfirm.view(m.width, m.height)
	}
	if m.servers.active {
		return m.serversView()
	}
	return page
}

func (m *Model) renderBody() string {
	if m.sess != nil {
		return fitHeight(m.sess.tabs[m.active].View(), m.bodyHeight())
	}
	msg := stLabel.Render("Not connected. Press ") + stKey.Render("S") + stLabel.Render(" to choose a server.")
	if m.connecting != "" {
		msg = stLabel.Render("Connecting to ") + stValue.Render(m.connecting) +
			stLabel.Render("…   ") + stKeyHint.Render("esc cancel")
	}
	return lipgloss.Place(m.width, m.bodyHeight(), lipgloss.Center, lipgloss.Center, msg)
}

// fitHeight pins a tab body to exactly h lines. A taller body would scroll the
// header and tab bar off the screen; a shorter one would leave the footer
// floating mid-screen.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
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
	left := stTitle.Render(" pgtower ") + stHeaderVer.Render(appVersion(m.version))

	var conn string
	switch {
	case m.sess != nil:
		c := m.sess.cfg
		conn = tagBadge(c.Tag) + stServer.Render(" "+c.Name+" ") +
			stStatus.Render(fmt.Sprintf(" %s@%s:%s • admin db: %s ", c.User, c.Host, c.Port, c.AdminDB))
	default:
		conn = stStatus.Render(" not connected ")
	}
	if m.connecting != "" {
		conn = stWarnV.Render(" connecting to "+m.connecting+"… ") + conn
	}
	right := conn + stBrand.Render(brand+" ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) renderTabBar() string {
	if m.sess == nil {
		return ""
	}
	var parts []string
	for i, t := range m.sess.tabs {
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
	var tabHints string
	if m.sess != nil {
		tabHints = m.sess.tabs[m.active].FooterHints()
	}
	global := strings.Join([]string{
		hint("1-7", "tabs"),
		hint("S", "servers"),
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
