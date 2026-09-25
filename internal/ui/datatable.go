package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtower/internal/db"
)

// dataMode is the sub-state of the data browser.
type dataMode int

const (
	dataBrowse dataMode = iota // navigating the grid
	dataSearch                 // typing the column search ('/')
	dataQuery                  // typing in the top query bar
)

// dataBrowser displays a table's data in read-only mode, with horizontal
// column scrolling (←→), copy (y / Y), search on the active column ('/') and a
// query bar at the top for custom queries (read-only).
type dataBrowser struct {
	mgr *db.Manager

	dbname, schema, table string

	grid     resultGrid
	rowCount int
	trunc    bool

	search   textinput.Model
	queryBar textinput.Model

	mode       dataMode
	baseSQL    string // SELECT * … (reset with 'r')
	currentSQL string // query currently displayed

	loading bool
	err     error
	status  string
	token   int

	width, height int
}

func newDataBrowser(mgr *db.Manager) *dataBrowser {
	s := textinput.New()
	s.Placeholder = "term…"
	s.CharLimit = 200

	q := textinput.New()
	q.Placeholder = "SELECT … (read-only)"
	q.CharLimit = 2000

	return &dataBrowser{
		mgr:      mgr,
		grid:     newResultGrid(40),
		search:   s,
		queryBar: q,
	}
}

func (b *dataBrowser) CapturingInput() bool {
	return b.mode == dataSearch || b.mode == dataQuery
}

func (b *dataBrowser) SetSize(w, h int) {
	b.width, b.height = w, h
	gh := h - 5
	if gh < 3 {
		gh = 3
	}
	b.grid.SetHeight(gh)
	b.grid.SetWidth(w)
	b.search.Width = clampInt(w-24, 10, 60)
	b.queryBar.Width = clampInt(w-8, 20, 160)
}

// Open starts the browser on a table and triggers loading the SELECT *.
func (b *dataBrowser) Open(dbname, schema, tbl string) tea.Cmd {
	b.dbname, b.schema, b.table = dbname, schema, tbl
	b.mode = dataBrowse
	b.loading = true
	b.err = nil
	b.status = ""
	b.grid.resetColumns()
	b.baseSQL = "SELECT * FROM " + db.QuoteQualified(schema, tbl) + " LIMIT 1000"
	b.currentSQL = b.baseSQL
	b.grid.Focus()
	return b.run(b.baseSQL)
}

func (b *dataBrowser) run(sql string) tea.Cmd {
	b.token++
	b.currentSQL = sql
	b.loading = true
	b.err = nil
	return loadTableData(b.mgr, b.dbname, sql, b.token)
}

func (b *dataBrowser) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tableDataMsg:
		if msg.token != b.token {
			return nil // stale result
		}
		b.loading = false
		b.err = msg.err
		if msg.err == nil {
			b.grid.SetData(msg.res)
			b.rowCount = msg.res.RowCount
			b.trunc = msg.res.Truncated
			note := ""
			if b.trunc {
				note = " (limited to 1000)"
			}
			b.status = fmt.Sprintf("%d rows%s", b.rowCount, note)
		}
		return nil

	case tea.KeyMsg:
		return b.handleKey(msg)
	}
	return nil
}

func (b *dataBrowser) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch b.mode {
	case dataSearch:
		return b.handleSearchKey(msg)
	case dataQuery:
		return b.handleQueryKey(msg)
	}

	switch msg.String() {
	case "/":
		if len(b.grid.cols) == 0 {
			return nil
		}
		b.mode = dataSearch
		b.search.SetValue("")
		return b.search.Focus()
	case "e", ":":
		b.mode = dataQuery
		b.queryBar.SetValue(b.currentSQL)
		b.queryBar.CursorEnd()
		return b.queryBar.Focus()
	case "r":
		b.grid.resetColumns()
		return b.run(b.baseSQL)
	}
	return b.grid.Update(msg)
}

func (b *dataBrowser) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		b.search.Blur()
		b.mode = dataBrowse
		return nil
	case tea.KeyEnter:
		term := strings.TrimSpace(b.search.Value())
		b.search.Blur()
		b.mode = dataBrowse
		if term == "" {
			return b.run(b.baseSQL)
		}
		col, _ := b.grid.column()
		sql := "SELECT * FROM " + db.QuoteQualified(b.schema, b.table) +
			" WHERE " + db.QuoteIdent(col) + "::text ILIKE " + db.QuoteLiteral("%"+term+"%") +
			" LIMIT 1000"
		b.status = "searching '" + term + "' in " + col + "…"
		return b.run(sql)
	}
	var cmd tea.Cmd
	b.search, cmd = b.search.Update(msg)
	return cmd
}

func (b *dataBrowser) handleQueryKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		b.queryBar.Blur()
		b.mode = dataBrowse
		return nil
	case tea.KeyEnter:
		sql := strings.TrimSpace(b.queryBar.Value())
		if sql == "" {
			return nil
		}
		if db.Classify(sql) != db.Safe {
			b.status = stBadV.Render("read-only here — use the Query tab to write")
			return nil
		}
		b.queryBar.Blur()
		b.mode = dataBrowse
		return b.run(sql)
	}
	var cmd tea.Cmd
	b.queryBar, cmd = b.queryBar.Update(msg)
	return cmd
}

// --- render ---

func (b *dataBrowser) FooterHints() string {
	switch b.mode {
	case dataSearch:
		return hint("enter", "search") + "   " + hint("esc", "cancel")
	case dataQuery:
		return hint("enter", "run (read-only)") + "   " + hint("esc", "cancel")
	default:
		return hint("esc", "back") + "  " + hint("←→", "columns") + "  " + hint("y/Y", "copy cell/row") + "  " +
			hint("/", "search column") + "  " + hint("e", "query") + "  " + hint("r", "reset")
	}
}

func (b *dataBrowser) View() string {
	// Query bar (top).
	var bar string
	if b.mode == dataQuery {
		bar = stKey.Render("SQL›") + " " + b.queryBar.View()
	} else {
		bar = stKeyHint.Render("SQL› ") + stStatus.Render(truncate(b.currentSQL, b.width-8)) +
			stKeyHint.Render("   (e edits)")
	}

	// Title + status.
	loc := lipgloss.NewStyle().Bold(true).Foreground(colAccent).
		Render(fmt.Sprintf("%s · %s.%s", b.dbname, b.schema, b.table))
	meta := ""
	switch {
	case b.err != nil:
		meta = stErr.Render("  " + collapseErr(b.err.Error()))
	case b.loading:
		meta = stLabel.Render("  loading…")
	default:
		colInfo := ""
		if info := b.grid.colInfo(); info != "" {
			colInfo = "  ·  " + info
		}
		meta = stLabel.Render("  "+b.status) + stKeyHint.Render(colInfo)
	}

	body := b.grid.View()
	if b.mode == dataSearch {
		col, _ := b.grid.column()
		searchLine := stKey.Render("search in "+col+": ") + b.search.View()
		return bar + "\n" + loc + meta + "\n" + searchLine + "\n" + body
	}
	if b.err == nil && len(b.grid.rows) == 0 && !b.loading {
		body = "\n  " + stStatus.Render("(0 rows)")
	}
	return bar + "\n" + loc + meta + "\n" + body
}
