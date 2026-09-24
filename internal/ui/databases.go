package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtower/internal/db"
)

type dbMode int

const (
	modeDBList dbMode = iota
	modeTables
	modeTableData
	modeDescribe
)

type databasesView struct {
	mgr *db.Manager

	mode     dbMode
	dbTable  table.Model
	tblTable table.Model
	browser  *dataBrowser

	dbs        []db.Database
	tables     []db.Table
	selectedDB string
	tblCount   int
	tblTotal   string

	// describe (\d) of the selected table
	descVP      viewport.Model
	descTitle   string
	descLoading bool

	// create database / drop database
	form          form
	confirm       confirmModal
	alert         alertModal
	finder        finder
	pendingDropDB string
	status        string

	loading bool
	err     error

	width, height int
}

func newDatabasesView(mgr *db.Manager) *databasesView {
	v := &databasesView{mgr: mgr}
	v.dbTable = newTable()
	v.tblTable = newTable()
	v.browser = newDataBrowser(mgr)
	v.confirm = newConfirmModal()
	v.alert = newAlertModal()
	v.finder = newFinder()
	v.descVP = viewport.New(80, 20)
	return v
}

func (v *databasesView) Title() string { return "Databases" }

func (v *databasesView) CapturingInput() bool {
	if v.finder.active || v.form.active || v.confirm.active || v.alert.active {
		return true
	}
	return v.mode == modeTableData && v.browser.CapturingInput()
}

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
	v.browser.SetSize(w, h)
	v.descVP.Width = w - 2
	v.descVP.Height = h - 2
	v.layoutColumns()
}

func (v *databasesView) layoutColumns() {
	w := v.width
	if w < 40 {
		w = 40
	}
	nameW := clampInt(w-40, 18, 48)
	v.dbTable.SetColumns([]table.Column{
		{Title: "DATABASE", Width: nameW},
		{Title: "OWNER", Width: 16},
		{Title: "SIZE", Width: 12},
		{Title: "CONNS", Width: 9},
	})
	tnameW := clampInt(w-56, 16, 44)
	v.tblTable.SetColumns([]table.Column{
		{Title: "SCHEMA", Width: 12},
		{Title: "TABLE", Width: tnameW},
		{Title: "TOTAL", Width: 10},
		{Title: "HEAP", Width: 10},
		{Title: "INDEXES", Width: 10},
		{Title: "ROWS~", Width: 12},
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
			if v.dbTable.Cursor() >= len(rows) {
				v.dbTable.SetCursor(0)
			}
		}
		return nil

	case tablesMsg:
		if msg.dbname != v.selectedDB {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.tables = msg.rows
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

	case describeMsg:
		v.descLoading = false
		if msg.err != nil {
			v.descVP.SetContent(stErr.Render("Error describing: " + msg.err.Error()))
			return nil
		}
		v.descTitle = msg.desc.Schema + "." + msg.desc.Table
		v.descVP.SetContent(buildDescribe(msg.desc))
		v.descVP.GotoTop()
		return nil

	case tableDataMsg:
		return v.browser.Update(msg)

	case execMsg:
		if msg.err != nil {
			v.status = stErr.Render("✗ " + msg.action)
			body := pgErrorText(msg.err)
			if strings.HasPrefix(msg.action, "drop database") {
				body += "\n\nHINT: if there are active connections on this database, terminate them " +
					"first in the Sessions tab (key 'k')."
			}
			v.alert.show(v.width, v.height, "Failed to "+msg.action, body, true)
			return nil
		}
		v.status = stGood.Render("✓ " + msg.action + " ok")
		return loadDatabases(v.mgr)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *databasesView) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Overlays take priority.
	if v.alert.active {
		v.alert.update(msg)
		return nil
	}
	if v.finder.active {
		res, cmd := v.finder.update(msg)
		if res == finderSelect {
			v.applyFinderSelection(v.finder.selectedIndex())
			v.finder.close()
		}
		return cmd
	}
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return execStatements(v.mgr, "", "drop database "+v.pendingDropDB, []string{db.BuildDropDatabase(v.pendingDropDB)})
		case confirmNo:
			v.status = stStatus.Render("cancelled")
		}
		return nil
	}
	if v.form.active {
		res, cmd := v.form.update(msg)
		switch res {
		case formSubmit:
			return v.submitCreateDB()
		case formCancel:
		}
		return cmd
	}

	if v.mode == modeTableData {
		if msg.String() == "esc" && v.browser.mode == dataBrowse {
			v.mode = modeTables
			v.browser.grid.Blur()
			return nil
		}
		return v.browser.Update(msg)
	}
	if v.mode == modeDescribe {
		if msg.String() == "esc" {
			v.mode = modeTables
			return nil
		}
		var cmd tea.Cmd
		v.descVP, cmd = v.descVP.Update(msg)
		return cmd
	}

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
			v.tables = nil
			v.tblTable.SetRows(nil)
			return loadTables(v.mgr, v.selectedDB)
		case "n":
			return v.openCreateDB()
		case "D":
			return v.askDropDB()
		case "/":
			return v.openFinder()
		case "r":
			return v.Init()
		}
		var cmd tea.Cmd
		v.dbTable, cmd = v.dbTable.Update(msg)
		return cmd

	case modeTables:
		switch msg.String() {
		case "enter":
			row := v.tblTable.SelectedRow()
			if row == nil {
				return nil
			}
			v.mode = modeTableData
			return v.browser.Open(v.selectedDB, row[0], row[1])
		case "d":
			row := v.tblTable.SelectedRow()
			if row == nil {
				return nil
			}
			v.mode = modeDescribe
			v.descLoading = true
			v.descTitle = row[0] + "." + row[1]
			v.descVP.SetContent("loading…")
			return describeTable(v.mgr, v.selectedDB, row[0], row[1])
		case "/":
			return v.openFinder()
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

// openFinder opens the quick-find overlay for whichever list is on screen: the
// database list or the table list.
func (v *databasesView) openFinder() tea.Cmd {
	switch v.mode {
	case modeDBList:
		if len(v.dbs) == 0 {
			return nil
		}
		items := make([]finderItem, len(v.dbs))
		for i, d := range v.dbs {
			items[i] = finderItem{index: i, label: d.Name}
		}
		return v.finder.open("Find database", items)
	case modeTables:
		if len(v.tables) == 0 {
			return nil
		}
		items := make([]finderItem, len(v.tables))
		for i, t := range v.tables {
			items[i] = finderItem{index: i, label: t.Schema + "." + t.Name}
		}
		return v.finder.open("Find table", items)
	}
	return nil
}

// applyFinderSelection moves the active list's cursor to the chosen row.
func (v *databasesView) applyFinderSelection(idx int) {
	if idx < 0 {
		return
	}
	switch v.mode {
	case modeDBList:
		if idx < len(v.dbs) {
			v.dbTable.SetCursor(idx)
		}
	case modeTables:
		if idx < len(v.tables) {
			v.tblTable.SetCursor(idx)
		}
	}
}

func (v *databasesView) openCreateDB() tea.Cmd {
	return v.form.open("Create database", []formField{
		textField("name", "Name", "e.g. app_prod"),
		textField("owner", "Owner", "owner role (optional)"),
	})
}

func (v *databasesView) submitCreateDB() tea.Cmd {
	name := strings.TrimSpace(v.form.value("name"))
	if name == "" {
		v.status = stWarnV.Render("name is required")
		return nil
	}
	owner := strings.TrimSpace(v.form.value("owner"))
	v.form.close()
	return execStatements(v.mgr, "", "create database "+name, []string{db.BuildCreateDatabase(name, owner)})
}

func (v *databasesView) askDropDB() tea.Cmd {
	row := v.dbTable.SelectedRow()
	if row == nil {
		return nil
	}
	name := row[0]
	v.pendingDropDB = name
	body := fmt.Sprintf("This drops database %s and ALL its data.\nType the name to confirm:",
		stBadV.Render(name))
	return v.confirm.askCritical("⚠  DROP DATABASE", body, name)
}

func (v *databasesView) FooterHints() string {
	switch v.mode {
	case modeTableData:
		return v.browser.FooterHints()
	case modeDescribe:
		return hint("esc", "back") + "   " + hint("↑↓", "scroll")
	case modeTables:
		return hint("enter", "view data") + "  " + hint("d", "describe") + "  " + hint("/", "find") + "  " + hint("esc", "back") + "  " + hint("↑↓", "navigate") + "  " + hint("r", "reload")
	default:
		return hint("enter", "tables") + "  " + hint("/", "find") + "  " + hint("n", "create db") + "  " + hint("D", "drop db") + "  " + hint("↑↓", "navigate") + "  " + hint("r", "reload")
	}
}

func (v *databasesView) View() string {
	if v.finder.active {
		return v.finder.view(v.width, v.height)
	}
	if v.alert.active {
		return v.alert.view(v.width, v.height)
	}
	if v.form.active {
		return v.form.view(v.width, v.height)
	}
	if v.confirm.active {
		return v.confirm.view(v.width, v.height)
	}
	if v.mode == modeTableData {
		return "\n" + v.browser.View()
	}
	if v.mode == modeDescribe {
		title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Structure · " + v.descTitle)
		return "\n" + title + "\n" + v.descVP.View()
	}
	if v.err != nil {
		return "\n" + stErr.Render("Error: "+v.err.Error())
	}

	switch v.mode {
	case modeTables:
		title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).
			Render(fmt.Sprintf("Tables · %s", v.selectedDB))
		meta := stLabel.Render(fmt.Sprintf("  %d tables · %s", v.tblCount, v.tblTotal))
		if v.loading {
			meta = stLabel.Render("  loading…")
		}
		body := v.tblTable.View()
		if v.tblCount == 0 && !v.loading {
			body = "\n  " + stStatus.Render("No tables in user schemas.")
		}
		return "\n" + title + meta + "\n" + body

	default:
		title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Cluster databases")
		meta := stLabel.Render(fmt.Sprintf("  %d databases", len(v.dbs)))
		if v.loading {
			meta = stLabel.Render("  loading…")
		}
		head := "\n" + title + meta
		if v.status != "" {
			head += "   " + v.status
		}
		return head + "\n" + v.dbTable.View()
	}
}

// buildDescribe builds the "\d" text of the table.
func buildDescribe(d db.TableDescription) string {
	var b strings.Builder
	sec := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render

	b.WriteString(sec("Columns"))
	b.WriteString("\n")
	for _, c := range d.Columns {
		nn := ""
		if !c.Nullable {
			nn = stWarnV.Render(" not null")
		}
		def := ""
		if c.Default != "" {
			def = stKeyHint.Render(" default " + c.Default)
		}
		b.WriteString(fmt.Sprintf("  %s  %s%s%s\n",
			stValue.Render(pad(c.Name, 24)), stLabel.Render(c.Type), nn, def))
	}

	if len(d.Indexes) > 0 {
		b.WriteString("\n" + sec("Indexes") + "\n")
		for _, i := range d.Indexes {
			b.WriteString("  " + stValue.Render(i.Name) + stKeyHint.Render("  "+i.Def) + "\n")
		}
	}
	if len(d.Constraints) > 0 {
		b.WriteString("\n" + sec("Constraints") + "\n")
		for _, c := range d.Constraints {
			b.WriteString(fmt.Sprintf("  %s %s %s\n",
				stLabel.Render("["+constraintKind(c.Type)+"]"), stValue.Render(c.Name), stKeyHint.Render(c.Def)))
		}
	}
	return b.String()
}

func constraintKind(t string) string {
	switch t {
	case "p":
		return "PK"
	case "f":
		return "FK"
	case "u":
		return "UNIQUE"
	case "c":
		return "CHECK"
	default:
		return t
	}
}

// --- table helpers ---

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
