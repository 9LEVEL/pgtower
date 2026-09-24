package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
)

// serversModal is the connection manager (S / ctrl+o): list the configured
// servers, connect, add/edit/delete them, test reachability and pick the
// default. It is a Model-level modal because it outlives any one session.
type serversModal struct {
	active  bool
	cursor  int
	form    form
	editing string // name being edited ("" = new) while the form is open
	confirm confirmModal
	probes  map[string]probeState
}

type probeState struct {
	running bool
	res     db.ProbeResult
	err     *db.ConnError
}

// probeMsg reports a "test connection" result. It is not session-scoped: a
// probe is about a server, not about the active session.
type probeMsg struct {
	name string
	res  db.ProbeResult
	err  *db.ConnError
}

var sslModes = []string{"disable", "prefer", "require", "verify-ca", "verify-full"}

func newServersModal() serversModal {
	return serversModal{confirm: newConfirmModal(), probes: map[string]probeState{}}
}

func (s *serversModal) close() {
	s.active = false
	s.form.close()
	s.confirm.close()
}

func (s *serversModal) capturing() bool { return s.active && (s.form.active || s.confirm.active) }

func (s *serversModal) probeDone(msg probeMsg) {
	s.probes[msg.name] = probeState{res: msg.res, err: msg.err}
}

// openServers shows the Servers screen with the active (or default)
// connection under the cursor.
func (m *Model) openServers() {
	s := &m.servers
	s.active = true
	want := m.store.Default
	if m.sess != nil {
		want = m.sess.cfg.Name
	}
	for i, n := range m.store.Names() {
		if n == want {
			s.cursor = i
		}
	}
	s.clampCursor(len(m.store.Names()))
}

func (s *serversModal) clampCursor(n int) {
	if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// selected returns the connection under the cursor.
func (m *Model) selectedServer() (config.Connection, bool) {
	names := m.store.Names()
	if len(names) == 0 {
		return config.Connection{}, false
	}
	return m.store.Find(names[m.servers.cursor])
}

func (m *Model) serversKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.servers
	if s.form.active {
		return m.serversFormKey(msg)
	}
	if s.confirm.active {
		if s.confirm.update(msg) == confirmYes {
			m.store.Remove(s.editing)
			if err := m.store.Save(); err != nil {
				m.notify("Could not save config.yml", err.Error(), true)
			}
			delete(s.probes, s.editing)
			s.clampCursor(len(m.store.Names()))
		}
		return nil
	}

	names := m.store.Names()
	c, ok := m.selectedServer()
	isEnv := ok && m.store.Env != nil && c.Name == m.store.Env.Name
	switch msg.String() {
	case "esc":
		if m.connecting != "" {
			m.cancelConnect()
		} else if m.sess != nil {
			s.close()
		}
	case "q":
		m.quitConfirm.ask("Quit pgtui?", "Leave pgtui? This closes the app and its database connections.")
	case "up", "k":
		if len(names) > 0 {
			s.cursor = (s.cursor - 1 + len(names)) % len(names)
		}
	case "down", "j":
		if len(names) > 0 {
			s.cursor = (s.cursor + 1) % len(names)
		}
	case "enter":
		if !ok {
			return nil
		}
		if m.sess != nil && m.sess.cfg.Name == c.Name && m.connecting == "" {
			s.close() // already on it
			return nil
		}
		return m.connect(c)
	case "a", "n":
		s.editing = ""
		return s.form.open("New server", serverFields(config.Connection{Port: 5432, SSLMode: "prefer"}))
	case "e":
		if !ok {
			return nil
		}
		if isEnv {
			m.notify("Read-only connection", "“"+c.Name+"” comes from DATABASE_URL / PG* variables and "+
				"exists only for this run. Change the environment to change it.", false)
			return nil
		}
		s.editing = c.Name
		return s.form.open("Edit server · "+c.Name, serverFields(c.Parts()))
	case "d", "delete":
		if !ok || isEnv {
			return nil
		}
		s.editing = c.Name
		body := "Remove “" + c.Name + "” from config.yml?"
		if m.sess != nil && m.sess.cfg.Name == c.Name {
			body += "\n\nYou stay connected until you switch or quit."
		}
		return s.confirm.ask("Delete server", body)
	case "*":
		if !ok || isEnv {
			return nil
		}
		m.store.Default = c.Name
		if err := m.store.Save(); err != nil {
			m.notify("Could not save config.yml", err.Error(), true)
		}
	case "t":
		if !ok {
			return nil
		}
		return m.probe(c)
	}
	return nil
}

func (m *Model) serversFormKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.servers
	res, cmd := s.form.update(msg)
	if res != formSubmit {
		return cmd
	}
	c, err := serverFromForm(&s.form)
	if err == nil {
		if old, ok := m.store.Find(s.editing); ok {
			// Keep what the form doesn't show.
			c.PasswordEnv, c.HostRAMMB, c.HostCPUs = old.PasswordEnv, old.HostRAMMB, old.HostCPUs
		}
		err = m.store.Upsert(s.editing, c)
	}
	if err != nil {
		s.form.err = err.Error()
		return nil
	}
	if err := m.store.Save(); err != nil {
		s.form.err = "saved for this run only — " + err.Error()
	}
	s.form.close()
	delete(s.probes, s.editing)
	for i, n := range m.store.Names() {
		if n == c.Name {
			s.cursor = i
		}
	}
	// The very first server: connect right away.
	if m.sess == nil && m.connecting == "" {
		return m.connect(c)
	}
	return nil
}

func (m *Model) probe(c config.Connection) tea.Cmd {
	cfg, err := m.store.Resolve(c, m.version)
	if err != nil {
		m.servers.probes[c.Name] = probeState{err: &db.ConnError{Title: "Invalid settings", Detail: err.Error()}}
		return nil
	}
	m.servers.probes[c.Name] = probeState{running: true}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTotalTimeout)
		defer cancel()
		res, err := db.Probe(ctx, cfg.URL, cfg.Host, cfg.Port)
		pm := probeMsg{name: c.Name, res: res}
		if err != nil {
			pm.err = db.ExplainConnect(err, cfg.Host, cfg.Port)
		}
		return pm
	}
}

func serverFields(c config.Connection) []formField {
	text := func(key, label, placeholder, value string) formField {
		f := textField(key, label, placeholder)
		f.input.SetValue(value)
		return f
	}
	port := ""
	if c.Port > 0 {
		port = strconv.Itoa(c.Port)
	}
	pw := secretField("password", "Password")
	pw.input.SetValue(c.Password)
	pw.input.Placeholder = "empty = none / ~/.pgpass"
	return []formField{
		text("name", "Name", "e.g. prod-db1", c.Name),
		text("host", "Host", "IP, hostname or /var/run/postgresql", c.Host),
		text("port", "Port", "5432", port),
		text("user", "User", "postgres", c.User),
		pw,
		text("database", "Admin DB", "postgres", c.Database),
		selectOf("sslmode", "SSL mode", sslModes, orStr(c.SSLMode, "prefer")),
		selectOf("tag", "Tag", []string{"none", config.TagDev, config.TagStaging, config.TagProd}, orStr(c.Tag, "none")),
	}
}

func selectOf(key, label string, opts []string, current string) formField {
	f := selectField(key, label, opts)
	for i, o := range opts {
		if o == current {
			f.sel = i
		}
	}
	return f
}

func serverFromForm(f *form) (config.Connection, error) {
	c := config.Connection{
		Name:     strings.TrimSpace(f.value("name")),
		Host:     strings.TrimSpace(f.value("host")),
		User:     strings.TrimSpace(f.value("user")),
		Password: f.value("password"),
		Database: strings.TrimSpace(f.value("database")),
	}
	if p := strings.TrimSpace(f.value("port")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("port must be a number between 1 and 65535")
		}
		c.Port = n
	}
	_, c.SSLMode = f.selected("sslmode")
	if _, tag := f.selected("tag"); tag != "none" {
		c.Tag = tag
	}
	return c, nil
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (m *Model) serversView() string {
	s := &m.servers
	if s.form.active {
		return s.form.view(m.width, m.height)
	}
	if s.confirm.active {
		return s.confirm.view(m.width, m.height)
	}

	boxW := clampInt(m.width-8, 50, 110)
	inner := boxW - 6
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render(" Servers ")

	var rows []string
	names := m.store.Names()
	if len(names) == 0 {
		rows = append(rows, stLabel.Render("No servers yet. Press ")+stKey.Render("a")+
			stLabel.Render(" to add your first one."))
	}
	for i, n := range names {
		c, _ := m.store.Find(n)
		rows = append(rows, m.serverRow(i, c, inner))
	}

	var detail string
	if c, ok := m.selectedServer(); ok {
		if p, ok := s.probes[c.Name]; ok && p.err != nil {
			detail = "\n" + lipgloss.NewStyle().Width(inner).Render(
				stBadV.Render(p.err.Title)+stLabel.Render(" — "+p.err.Detail)+
					"\n"+stKeyHint.Render(p.err.Hint))
		}
	}

	if m.connecting != "" {
		detail += "\n" + stWarnV.Render("connecting to "+m.connecting+"…") + stKeyHint.Render("   esc cancel")
	}

	where := "not saved yet"
	if m.store.Path != "" {
		where = m.store.Path
	}
	hints := []string{hint("enter", "connect"), hint("a", "add"), hint("e", "edit"), hint("d", "delete"),
		hint("t", "test"), hint("*", "default")}
	if m.sess != nil {
		hints = append(hints, hint("esc", "close"))
	} else {
		hints = append(hints, hint("q", "quit"))
	}
	content := title + "\n\n" + strings.Join(rows, "\n") + detail + "\n\n" +
		lipgloss.NewStyle().Width(inner).Render(strings.Join(hints, "  ")) + "\n" +
		stKeyHint.Render(truncate("config: "+where, inner))
	box := stModal.BorderForeground(colAccent).Width(boxW).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) serverRow(i int, c config.Connection, w int) string {
	s := &m.servers
	cursor := "  "
	if i == s.cursor {
		cursor = stKey.Render("› ")
	}
	mark := " "
	switch {
	case m.connecting == c.Name:
		mark = stWarnV.Render("…")
	case m.sess != nil && m.sess.cfg.Name == c.Name:
		mark = stGood.Render("●")
	}
	def := " "
	if c.Name == m.store.Default {
		def = stKey.Render("*")
	}
	name := stValue.Render(pad(truncate(c.Name, 18), 18))
	if i != s.cursor {
		name = stLabel.Render(pad(truncate(c.Name, 18), 18))
	}
	tag := c.Tag
	if m.store.Env != nil && c.Name == m.store.Env.Name {
		tag = "env"
	}
	badge := pad(tagBadge(tag), 9)

	target := "?"
	if cfg, err := m.store.Resolve(c, ""); err == nil {
		target = fmt.Sprintf("%s@%s:%s", cfg.User, cfg.Host, cfg.Port)
	}

	var probe string
	if p, ok := s.probes[c.Name]; ok {
		switch {
		case p.running:
			probe = stWarnV.Render("testing…")
		case p.err != nil:
			probe = stBadV.Render("✗ " + p.err.Title)
		default:
			probe = stGood.Render(fmt.Sprintf("✓ %s · PG %s", p.res.Latency.Round(time.Millisecond), p.res.Version))
		}
	}
	line := cursor + mark + def + " " + name + " " + badge + " " + stStatus.Render(target)
	if probe != "" {
		line += "  " + probe
	}
	return truncate(line, w)
}

// connErrorNotice explains a failed connect for the notice modal.
func connErrorNotice(cfg *config.Config, e *db.ConnError) (string, string, bool) {
	var b strings.Builder
	b.WriteString(e.Detail + "\n\n")
	if e.Hint != "" {
		b.WriteString("What to check: " + e.Hint + "\n\n")
	}
	fmt.Fprintf(&b, "Server: %s (%s@%s:%s)\n", cfg.Name, cfg.User, cfg.Host, cfg.Port)
	if cause := e.Cause(); cause != "" {
		b.WriteString("\nTechnical details: " + cause)
	}
	b.WriteString("\n\nPress S to pick another server or fix this one.")
	return "Cannot connect to “" + cfg.Name + "” — " + e.Title, b.String(), true
}

// migrationNotice is the one-time explanation shown on the run that upgraded
// a legacy configuration.
func migrationNotice(mig *config.Migration, version string) (string, string, bool) {
	var b strings.Builder
	if mig.Err != nil {
		b.WriteString("pgtui " + appVersion(version) + " manages several servers, and your old configuration " +
			"was converted — but the new file could not be written:\n\n  " + mig.Err.Error() + "\n\n" +
			"pgtui is running with the converted settings for now. Fix the permission and save " +
			"any change from the Servers screen (S) to finish the upgrade. Your original files were not touched.")
		return "Configuration upgrade incomplete", b.String(), true
	}
	b.WriteString("pgtui " + appVersion(version) + " can manage several PostgreSQL servers (press S). " +
		"Your configuration was upgraded automatically:\n\n")
	b.WriteString("• Migrated from: " + strings.Join(mig.From, " + ") + "\n")
	if len(mig.Imported) > 0 {
		b.WriteString("• Server created: " + strings.Join(mig.Imported, ", ") + " (default)\n")
	}
	b.WriteString("• Now using: " + mig.Path + "\n")
	for _, bak := range mig.Backups {
		b.WriteString("• Original kept as: " + bak + "\n")
	}
	legacy := "The old top-level keys (database_url, host, …) are"
	if strings.Contains(strings.Join(mig.From, " "), ".env") {
		legacy = "The old top-level keys (database_url, host, …) and the .env file are"
	}
	b.WriteString("\n" + legacy + " no longer read. " +
		"The backups are only needed to downgrade; delete them once you're happy.")
	return "Configuration upgraded", b.String(), false
}
