package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
)

type tuningSection int

const (
	secAdvisor tuningSection = iota
	secSettings
	secHBA
)

// tuningView is tab 7. It hosts the config sections: the settings advisor
// (Unit 2), the ALTER SYSTEM editor (Unit 4) and the pg_hba viewer (Unit 3).
type tuningView struct {
	cfg *config.Config
	mgr *db.Manager

	section tuningSection

	// advisor
	in     db.TuningInput
	recs   []db.TuningRec
	advErr error

	// settings (ALTER SYSTEM editor)
	setList      []db.Setting
	setView      []db.Setting
	setTbl       table.Model
	setErr       error
	setFilter    textinput.Model
	setFiltering bool
	setForm      form
	setEdit      db.Setting
	setEditRst   bool
	setStatus    string

	// pg_hba
	hbaFile     string
	hbaContent  string
	hbaWritable bool
	hbaRules    []db.HBARule
	hbaErr      error
	hbaErrCount int
	hbaTbl      table.Model
	hbaForm     form
	hbaConfirm  confirmModal
	hbaAlert    alertModal
	hbaPending  string // new file content awaiting confirmation
	hbaEditLine int    // line being edited; 0 = append
	hbaStatus   string

	width, height int
}

func newTuningView(cfg *config.Config, mgr *db.Manager) *tuningView {
	fi := textinput.New()
	fi.Placeholder = "filter by name"
	fi.CharLimit = 60
	fi.Width = 30
	return &tuningView{cfg: cfg, mgr: mgr, hbaTbl: newTable(), setTbl: newTable(),
		setFilter: fi, hbaConfirm: newConfirmModal(), hbaAlert: newAlertModal()}
}

func (v *tuningView) Title() string { return "Tuning" }

func (v *tuningView) CapturingInput() bool {
	return v.setForm.active || v.setFiltering ||
		v.hbaForm.active || v.hbaConfirm.active || v.hbaAlert.active
}

func (v *tuningView) SetSize(w, h int) {
	v.width, v.height = w, h
	th := h - 5
	if th < 3 {
		th = 3
	}
	v.hbaTbl.SetHeight(th)
	v.setTbl.SetHeight(th)
	addrW := clampInt(w-60, 12, 30)
	v.hbaTbl.SetColumns([]table.Column{
		{Title: "LINE", Width: 5},
		{Title: "TYPE", Width: 8},
		{Title: "DATABASE", Width: 14},
		{Title: "USER", Width: 14},
		{Title: "ADDRESS", Width: addrW},
		{Title: "METHOD", Width: 14},
	})
	nameW := clampInt(w-42, 20, 44)
	v.setTbl.SetColumns([]table.Column{
		{Title: "NAME", Width: nameW},
		{Title: "VALUE", Width: 14},
		{Title: "UNIT", Width: 6},
		{Title: "CONTEXT", Width: 11},
		{Title: "PEND", Width: 4},
	})
}

func (v *tuningView) FooterHints() string {
	sections := hint("a", "advisor") + "  " + hint("s", "settings") + "  " + hint("h", "pg_hba")
	switch v.section {
	case secSettings:
		return hint("enter", "edit") + "  " + hint("x", "reset") + "  " + hint("/", "filter") + "  " +
			sections + "  " + hint("r", "refresh")
	case secHBA:
		return hint("n", "add") + "  " + hint("e", "edit") + "  " + hint("d", "delete") + "  " +
			sections + "  " + hint("r", "refresh")
	default:
		return sections + "  " + hint("r", "refresh")
	}
}

func (v *tuningView) Init() tea.Cmd {
	v.advErr, v.hbaErr, v.setErr = nil, nil, nil
	return tea.Batch(loadTuning(v.mgr, v.cfg.HostRAMMB, v.cfg.HostCPUs), loadHBA(v.mgr), loadAllSettings(v.mgr))
}

func (v *tuningView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tuningMsg:
		v.advErr = msg.err
		if msg.err == nil {
			v.recs, v.in = msg.recs, msg.in
		}
		return nil
	case hbaMsg:
		v.hbaErr, v.hbaFile = msg.err, msg.file
		v.hbaContent, v.hbaWritable = msg.content, msg.writable
		if msg.err == nil {
			v.hbaRules = msg.rules
			v.rebuildHBATable()
		}
		return nil
	case hbaApplyMsg:
		if msg.err != nil {
			v.hbaAlert.show(v.width, v.height, "pg_hba change failed", pgErrorText(msg.err), true)
			return nil
		}
		v.hbaStatus = stGood.Render("✓ pg_hba updated & reloaded")
		return loadHBA(v.mgr)
	case allSettingsMsg:
		v.setErr = msg.err
		if msg.err == nil {
			v.setList = msg.settings
			v.applySettingsFilter()
		}
		return nil
	case settingApplyMsg:
		if msg.err != nil {
			v.setStatus = stErr.Render("✗ " + msg.name + ": " + oneLineUI(msg.err))
			return nil
		}
		v.setStatus = stGood.Render("✓ " + msg.name + " applied")
		if msg.restart {
			v.setStatus += stWarnV.Render("  — restart required to take effect")
		}
		return loadAllSettings(v.mgr)
	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *tuningView) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Modals first (highest priority).
	if v.hbaAlert.active {
		v.hbaAlert.update(msg)
		return nil
	}
	if v.setForm.active {
		res, cmd := v.setForm.update(msg)
		switch res {
		case formSubmit:
			return v.submitSettingEdit()
		case formCancel:
			return nil
		}
		return cmd
	}
	if v.hbaForm.active {
		res, cmd := v.hbaForm.update(msg)
		switch res {
		case formSubmit:
			return v.submitHBAForm()
		case formCancel:
			return nil
		}
		return cmd
	}
	if v.hbaConfirm.active {
		switch v.hbaConfirm.update(msg) {
		case confirmYes:
			return applyHBA(v.mgr, v.cfg.URL, v.hbaPending)
		case confirmNo:
			v.hbaStatus = stStatus.Render("cancelled")
		}
		return nil
	}
	// Filter capture.
	if v.setFiltering {
		switch msg.String() {
		case "esc", "enter":
			v.setFiltering = false
			v.setFilter.Blur()
			return nil
		}
		var cmd tea.Cmd
		v.setFilter, cmd = v.setFilter.Update(msg)
		v.applySettingsFilter()
		return cmd
	}

	switch msg.String() {
	case "r":
		return v.Init()
	case "a":
		v.section = secAdvisor
		return nil
	case "s":
		v.section = secSettings
		return nil
	case "h":
		v.section = secHBA
		return nil
	}

	var cmd tea.Cmd
	switch v.section {
	case secHBA:
		switch msg.String() {
		case "n":
			return v.openHBAForm(0)
		case "e", "enter":
			return v.openHBAEditForm()
		case "d", "D":
			return v.askHBADelete()
		}
		v.hbaTbl, cmd = v.hbaTbl.Update(msg)
	case secSettings:
		switch msg.String() {
		case "/":
			v.setFiltering = true
			return v.setFilter.Focus()
		case "enter":
			return v.openSettingEdit()
		case "x":
			return v.resetSetting()
		}
		v.setTbl, cmd = v.setTbl.Update(msg)
	}
	return cmd
}

// --- settings section ---

func (v *tuningView) applySettingsFilter() {
	q := strings.ToLower(strings.TrimSpace(v.setFilter.Value()))
	v.setView = v.setView[:0]
	rows := make([]table.Row, 0, len(v.setList))
	for _, s := range v.setList {
		if q != "" && !strings.Contains(strings.ToLower(s.Name), q) {
			continue
		}
		v.setView = append(v.setView, s)
		pend := ""
		if s.PendingRestart {
			pend = "⚠"
		}
		rows = append(rows, table.Row{s.Name, s.Setting, s.Unit, s.Context, pend})
	}
	v.setTbl.SetRows(rows)
	if v.setTbl.Cursor() >= len(rows) {
		v.setTbl.SetCursor(0)
	}
}

func (v *tuningView) selectedSetting() (db.Setting, bool) {
	i := v.setTbl.Cursor()
	if i < 0 || i >= len(v.setView) {
		return db.Setting{}, false
	}
	return v.setView[i], true
}

func (v *tuningView) openSettingEdit() tea.Cmd {
	s, ok := v.selectedSetting()
	if !ok {
		return nil
	}
	if !s.Changeable() {
		v.setStatus = stWarnV.Render(s.Name + " is compile-time (context=internal) — cannot change")
		return nil
	}
	v.setEdit = s
	title := "ALTER SYSTEM · " + s.Name + "  " + stKeyHint.Render("("+settingConstraint(s)+")")
	if s.NeedsRestart() {
		title += "  " + stWarnV.Render("restart required")
	}
	f := textField("value", "New value", s.Setting)
	f.input.SetValue(s.Setting)
	return v.setForm.open(truncate(title, clampInt(v.width-14, 30, 80)), []formField{f})
}

func (v *tuningView) submitSettingEdit() tea.Cmd {
	val := strings.TrimSpace(v.setForm.value("value"))
	if err := db.ValidateSettingValue(v.setEdit, val); err != nil {
		v.setStatus = stWarnV.Render(v.setEdit.Name + ": " + err.Error())
		return nil // keep the form open to fix the value
	}
	name := v.setEdit.Name
	restart := v.setEdit.NeedsRestart()
	v.setForm.close()
	return applySetting(v.mgr, name, val, false, restart)
}

func (v *tuningView) resetSetting() tea.Cmd {
	s, ok := v.selectedSetting()
	if !ok {
		return nil
	}
	if !s.Changeable() {
		v.setStatus = stWarnV.Render(s.Name + " is compile-time — cannot reset")
		return nil
	}
	return applySetting(v.mgr, s.Name, "", true, s.NeedsRestart())
}

func settingConstraint(s db.Setting) string {
	switch s.VarType {
	case "bool":
		return "on/off"
	case "enum":
		return "enum: " + strings.Join(s.EnumVals, "/")
	case "integer", "real":
		u := ""
		if s.Unit != "" {
			u = " " + s.Unit
		}
		return fmt.Sprintf("%s%s, %s..%s", s.VarType, u, s.MinVal, s.MaxVal)
	default:
		return s.VarType
	}
}

// --- pg_hba section ---

func (v *tuningView) selectedHBARule() (db.HBARule, bool) {
	i := v.hbaTbl.Cursor()
	if i < 0 || i >= len(v.hbaRules) {
		return db.HBARule{}, false
	}
	return v.hbaRules[i], true
}

// openHBAForm opens the add/edit rule form. editLine 0 = append a new rule.
func (v *tuningView) openHBAForm(editLine int) tea.Cmd {
	if !v.hbaWritable {
		v.hbaStatus = stWarnV.Render("pg_hba is read-only here — a superuser connection is required to edit")
		return nil
	}
	v.hbaEditLine = editLine
	title := "Add pg_hba rule"
	fields := []formField{
		textField("type", "Type", "host / local / hostssl"),
		textField("database", "Database", "all"),
		textField("user", "User", "all"),
		textField("address", "Address", "0.0.0.0/0 (blank for local)"),
		textField("method", "Method", "scram-sha-256 / trust / reject"),
	}
	return v.hbaForm.open(title, fields)
}

func (v *tuningView) openHBAEditForm() tea.Cmd {
	if !v.hbaWritable {
		v.hbaStatus = stWarnV.Render("pg_hba is read-only here — a superuser connection is required to edit")
		return nil
	}
	r, ok := v.selectedHBARule()
	if !ok {
		return nil
	}
	v.hbaEditLine = r.LineNumber
	pre := func(key, label, val string) formField {
		f := textField(key, label, "")
		f.input.SetValue(val)
		return f
	}
	return v.hbaForm.open(fmt.Sprintf("Edit pg_hba line %d", r.LineNumber), []formField{
		pre("type", "Type", r.Type),
		pre("database", "Database", r.Database),
		pre("user", "User", r.UserName),
		pre("address", "Address", r.Address),
		pre("method", "Method", r.AuthMethod),
	})
}

func (v *tuningView) submitHBAForm() tea.Cmd {
	in := db.HBARuleInput{
		Type:     strings.TrimSpace(v.hbaForm.value("type")),
		Database: strings.TrimSpace(v.hbaForm.value("database")),
		User:     strings.TrimSpace(v.hbaForm.value("user")),
		Address:  strings.TrimSpace(v.hbaForm.value("address")),
		Method:   strings.TrimSpace(v.hbaForm.value("method")),
	}
	if in.Type == "" || in.Database == "" || in.User == "" || in.Method == "" {
		v.hbaStatus = stWarnV.Render("type, database, user and method are required")
		return nil
	}
	line := db.BuildHBALine(in)
	var newContent string
	var err error
	if v.hbaEditLine == 0 {
		newContent = db.HBAAppendLine(v.hbaContent, line)
	} else {
		newContent, err = db.HBAReplaceLine(v.hbaContent, v.hbaEditLine, line)
	}
	if err != nil {
		v.hbaStatus = stErr.Render(err.Error())
		return nil
	}
	v.hbaForm.close()
	v.hbaPending = newContent
	body := "Apply and reload pg_hba with this rule?\n\n  " + stValue.Render(line) +
		"\n\nA backup is kept; the change is auto-rolled-back if admin login breaks."
	return v.hbaConfirm.ask("⚠  Write pg_hba.conf", body)
}

func (v *tuningView) askHBADelete() tea.Cmd {
	if !v.hbaWritable {
		v.hbaStatus = stWarnV.Render("pg_hba is read-only here — a superuser connection is required to edit")
		return nil
	}
	r, ok := v.selectedHBARule()
	if !ok {
		return nil
	}
	newContent, err := db.HBADeleteLine(v.hbaContent, r.LineNumber)
	if err != nil {
		v.hbaStatus = stErr.Render(err.Error())
		return nil
	}
	v.hbaPending = newContent
	body := fmt.Sprintf("Delete pg_hba line %d and reload?\n\n  %s\n\nAuto-rolled-back if admin login breaks.",
		r.LineNumber, stBadV.Render(strings.TrimSpace(fmt.Sprintf("%s %s %s %s %s", r.Type, r.Database, r.UserName, r.Address, r.AuthMethod))))
	return v.hbaConfirm.ask("⚠  Delete pg_hba rule", body)
}

func (v *tuningView) rebuildHBATable() {
	v.hbaErrCount = 0
	rows := make([]table.Row, 0, len(v.hbaRules))
	for _, r := range v.hbaRules {
		method := r.AuthMethod
		if r.Error != "" {
			method = "⚠ error"
			v.hbaErrCount++
		}
		addr := r.Address
		if addr == "" {
			addr = "-"
		}
		rows = append(rows, table.Row{
			strconv.Itoa(r.LineNumber), r.Type, r.Database, r.UserName, addr, method,
		})
	}
	v.hbaTbl.SetRows(rows)
	if v.hbaTbl.Cursor() >= len(rows) {
		v.hbaTbl.SetCursor(0)
	}
}

// --- rendering ---

func (v *tuningView) View() string {
	if v.hbaAlert.active {
		return v.hbaAlert.view(v.width, v.height)
	}
	if v.setForm.active {
		return v.setForm.view(v.width, v.height)
	}
	if v.hbaForm.active {
		return v.hbaForm.view(v.width, v.height)
	}
	if v.hbaConfirm.active {
		return v.hbaConfirm.view(v.width, v.height)
	}
	selector := v.sectionSelector()
	switch v.section {
	case secSettings:
		return selector + "\n" + v.settingsView()
	case secHBA:
		return selector + "\n" + v.hbaView()
	default:
		return selector + "\n" + v.advisorView()
	}
}

func (v *tuningView) sectionSelector() string {
	item := func(active bool, label string) string {
		if active {
			return stTabActive.Render(label)
		}
		return stTabInactive.Render(label)
	}
	return "\n" + item(v.section == secAdvisor, "a · Advisor") +
		item(v.section == secSettings, "s · Settings") +
		item(v.section == secHBA, "h · pg_hba")
}

func (v *tuningView) advisorView() string {
	if v.advErr != nil {
		return "\n" + stErr.Render("Failed to read settings: "+v.advErr.Error())
	}
	if v.recs == nil {
		return "\n  " + stStatus.Render("Reading pg_settings…")
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Configuration advisor")
	var host string
	if v.cfg.HostRAMMB > 0 || v.cfg.HostCPUs > 0 {
		var parts []string
		if v.cfg.HostRAMMB > 0 {
			parts = append(parts, "host RAM "+humanMB(v.cfg.HostRAMMB))
		}
		if v.cfg.HostCPUs > 0 {
			parts = append(parts, fmt.Sprintf("%d CPUs", v.cfg.HostCPUs))
		}
		host = stLabel.Render("  " + strings.Join(parts, " · "))
	} else {
		host = stWarnV.Render("  host RAM/cores unknown") +
			stKeyHint.Render(" — set PGTUI_HOST_RAM_MB and PGTUI_HOST_CPUS for concrete targets")
	}

	header := stLabel.Render(pad("  SETTING", 24)) + stLabel.Render(pad("CURRENT", 13)) +
		stLabel.Render(pad("RECOMMENDED", 15)) + stLabel.Render("NOTE")

	noteW := clampInt(v.width-56, 16, 90)
	var lines []string
	for _, r := range v.recs {
		rec := stKeyHint.Render("—")
		if r.Recommended != "" {
			rec = stKey.Render(r.Recommended)
		}
		lines = append(lines, verdictMark(r.Verdict)+" "+
			stValue.Render(pad(r.Name, 22))+
			stLabel.Render(pad(r.Current, 13))+
			pad(rec, 15)+
			stKeyHint.Render(truncate(r.Note, noteW)))
	}
	return "\n" + title + host + "\n\n" + header + "\n" + strings.Join(lines, "\n")
}

func (v *tuningView) settingsView() string {
	if v.setErr != nil {
		return "\n" + stErr.Render("Failed to read pg_settings: "+v.setErr.Error())
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("ALTER SYSTEM editor")
	head := "\n" + title + stLabel.Render(fmt.Sprintf("  %d settings", len(v.setView)))
	if v.setFiltering || v.setFilter.Value() != "" {
		head += "   " + stKeyHint.Render("filter ") + v.setFilter.View()
	}
	if v.setStatus != "" {
		head += "   " + v.setStatus
	}
	return head + "\n" + v.setTbl.View()
}

func (v *tuningView) hbaView() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Host-based authentication")
	head := "\n" + title
	if v.hbaFile != "" {
		head += stLabel.Render("  " + v.hbaFile)
	}
	if v.hbaErr != nil {
		return head + "\n\n" + stErr.Render("Cannot read pg_hba_file_rules: "+v.hbaErr.Error()) +
			"\n" + stKeyHint.Render("(the view requires a superuser connection)")
	}
	if !v.hbaWritable {
		head += "   " + stWarnV.Render("read-only (superuser required to edit)")
	}
	if v.hbaErrCount > 0 {
		head += "   " + stBadV.Render(fmt.Sprintf("⚠ %d line(s) failed to parse", v.hbaErrCount))
	}
	if v.hbaStatus != "" {
		head += "   " + v.hbaStatus
	}
	return head + "\n" + v.hbaTbl.View()
}

// verdictMark renders a colored bullet for a verdict.
func verdictMark(vd db.Verdict) string {
	switch vd {
	case db.VerdictWarn:
		return stWarnV.Render("●")
	case db.VerdictOK:
		return stGood.Render("●")
	default:
		return stLabel.Render("●")
	}
}

// humanMB renders a megabyte count as MB or GB.
func humanMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

// oneLineUI collapses an error to a single line for status display.
func oneLineUI(err error) string {
	s := strings.ReplaceAll(pgErrorText(err), "\n", " ")
	return truncate(s, 80)
}
