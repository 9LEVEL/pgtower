package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtower/internal/db"
)

// resultGrid is a read-only result table with a column cursor, shared by the
// data browser and the Query tab. Only the columns that fit the width are
// handed to the bubbles table, so ←→ scroll horizontally through results
// wider than the screen; y / Y copy the active cell / the selected row.
type resultGrid struct {
	table  table.Model
	cols   []string
	rows   [][]string // display text (one line, NULL as ∅)
	raw    [][]string // full text of each cell, for copying
	widths []int
	maxCol int // widest a column renders

	colCursor int   // active column (absolute index)
	colOffset int   // first visible column (horizontal scroll)
	visible   []int // columns on screen (absolute indexes), set by build

	width int
}

// cellPad is the horizontal padding the table adds around every cell (one
// space each side): a column takes its width plus this on screen.
const cellPad = 2

// Header cells, as newTable draws them (minus the bottom border); the active
// column is painted like the selected row, so the two cross at the active cell.
var (
	stGridHead       = lipgloss.NewStyle().Bold(true).Foreground(colMuted).Padding(0, 1)
	stGridHeadActive = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).Padding(0, 1)
)

func newResultGrid(maxCol int) resultGrid {
	return resultGrid{table: newTable(), maxCol: maxCol}
}

func (g *resultGrid) Focus()          { g.table.Focus() }
func (g *resultGrid) Blur()           { g.table.Blur() }
func (g *resultGrid) SetHeight(h int) { g.table.SetHeight(h) }

// View is the table with its header line redrawn by headerLine: the bubbles
// table measures titles with their color codes included, so a styled title
// can't go through it.
func (g *resultGrid) View() string {
	out := g.table.View()
	if len(g.visible) == 0 {
		return out
	}
	_, body, _ := strings.Cut(out, "\n")
	return g.headerLine() + "\n" + body
}

// headerLine renders the column titles cell by cell, at the table's widths.
func (g *resultGrid) headerLine() string {
	cells := make([]string, len(g.visible))
	for j, i := range g.visible {
		title, st := g.cols[i], stGridHead
		if i == g.colCursor {
			title, st = "›"+title, stGridHeadActive // the › survives without colors
		}
		w := g.widths[i]
		cell := lipgloss.NewStyle().Width(w).MaxWidth(w).Inline(true).Render(truncate(strings.ToUpper(title), w))
		cells[j] = st.Render(cell)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

func (g *resultGrid) SetWidth(w int) {
	g.width = w
	if len(g.cols) > 0 {
		g.build()
	}
}

// SetData shows a new result. The active column survives when the column count
// is unchanged (re-running or filtering the same query).
func (g *resultGrid) SetData(res db.QueryResult) {
	if len(res.Columns) != len(g.cols) {
		g.colCursor, g.colOffset = 0, 0
	}
	g.cols, g.rows, g.raw = res.Columns, res.Rows, res.Raw
	if g.raw == nil {
		g.raw = g.rows
	}
	g.computeWidths()
	g.build()
}

// resetColumns moves back to the first column (applied on the next build).
func (g *resultGrid) resetColumns() { g.colCursor, g.colOffset = 0, 0 }

// column is the name of the active column.
func (g *resultGrid) column() (string, bool) {
	if g.colCursor >= len(g.cols) {
		return "", false
	}
	return g.cols[g.colCursor], true
}

// colInfo describes the active column for a status line ("" without data).
func (g *resultGrid) colInfo() string {
	name, ok := g.column()
	if !ok {
		return ""
	}
	return fmt.Sprintf("column %d/%d: %s", g.colCursor+1, len(g.cols), name)
}

// Update handles the grid keys: ←→ move the active column, y / Y copy; the
// rest (↑↓, pgup/pgdown, g/G …) moves the table's row cursor.
func (g *resultGrid) Update(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h":
		if g.colCursor > 0 {
			g.colCursor--
			g.build()
		}
		return nil
	case "right", "l":
		if g.colCursor < len(g.cols)-1 {
			g.colCursor++
			g.build()
		}
		return nil
	case "y":
		return g.copyCell()
	case "Y":
		return g.copyRow()
	}
	var cmd tea.Cmd
	g.table, cmd = g.table.Update(msg)
	return cmd
}

// copyCell copies the full text of the active cell. NULL and empty cells leave
// the clipboard alone.
func (g *resultGrid) copyCell() tea.Cmd {
	r, c := g.table.Cursor(), g.colCursor
	if r < 0 || r >= len(g.raw) || c >= len(g.raw[r]) {
		return nil
	}
	col, text := g.cols[c], g.raw[r][c]
	if text == "" {
		what := "empty"
		if g.rows[r][c] == "∅" {
			what = "NULL"
		}
		return func() tea.Msg { return statusMsg(col + " is " + what + " — nothing copied") }
	}
	return copyToClipboard(text, col+" ("+plural(len([]rune(text)), "char")+")")
}

// copyRow copies the selected row, tab-separated (pastes into a spreadsheet).
func (g *resultGrid) copyRow() tea.Cmd {
	r := g.table.Cursor()
	if r < 0 || r >= len(g.raw) {
		return nil
	}
	return copyToClipboard(tsvRow(g.raw[r]),
		fmt.Sprintf("row %d (%s, tab-separated)", r+1, plural(len(g.raw[r]), "column")))
}

// tsvRow joins cells with tabs. A cell holding a tab, line break or quote is
// quoted the way spreadsheets read it back ("say ""hi""").
func tsvRow(cells []string) string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if strings.ContainsAny(c, "\t\n\r\"") {
			c = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
		}
		out[i] = c
	}
	return strings.Join(out, "\t")
}

// --- layout ---

func (g *resultGrid) computeWidths() {
	g.widths = make([]int, len(g.cols))
	for i, c := range g.cols {
		g.widths[i] = len([]rune(c)) + 1
	}
	for _, row := range g.rows {
		for i, cell := range row {
			if i < len(g.widths) {
				if l := len([]rune(cell)) + 1; l > g.widths[i] {
					g.widths[i] = l
				}
			}
		}
	}
	for i := range g.widths {
		g.widths[i] = clampInt(g.widths[i], 3, g.maxCol)
	}
}

func (g *resultGrid) avail() int {
	a := g.width - 2
	if a < 20 {
		a = 20
	}
	return a
}

// ensureVisible adjusts colOffset so the active column stays visible.
func (g *resultGrid) ensureVisible() {
	if g.colCursor < g.colOffset {
		g.colOffset = g.colCursor
		return
	}
	for g.colOffset < len(g.cols)-1 {
		w, last := 0, g.colOffset
		for i := g.colOffset; i < len(g.cols); i++ {
			if w+g.widths[i]+cellPad > g.avail() && i > g.colOffset {
				break
			}
			last = i
			w += g.widths[i] + cellPad
		}
		if g.colCursor <= last {
			break
		}
		g.colOffset++
	}
}

// build slices the horizontal column window and feeds the table.
func (g *resultGrid) build() {
	if len(g.cols) == 0 {
		g.visible = nil
		g.table.SetRows(nil)
		g.table.SetColumns([]table.Column{{Title: "", Width: 10}})
		return
	}
	g.ensureVisible()

	var cols []table.Column
	var idxs []int
	w := 0
	for i := g.colOffset; i < len(g.cols); i++ {
		if w+g.widths[i]+cellPad > g.avail() && len(idxs) > 0 {
			break
		}
		title := g.cols[i]
		if i == g.colCursor {
			title = "›" + title // mark the active column
		}
		cols = append(cols, table.Column{Title: strings.ToUpper(title), Width: g.widths[i]})
		idxs = append(idxs, i)
		w += g.widths[i] + cellPad
	}

	cur := g.table.Cursor()
	rows := make([]table.Row, len(g.rows))
	for r, full := range g.rows {
		cells := make([]string, len(idxs))
		for j, ci := range idxs {
			if ci < len(full) {
				cells[j] = full[ci]
			}
		}
		rows[r] = table.Row(cells)
	}
	// Order matters: the bubbles table renders on each setter. Clearing the rows
	// before swapping the columns avoids an intermediate render with a cell
	// count ≠ column count (index out of range in renderRow).
	g.visible = idxs
	g.table.SetRows(nil)
	g.table.SetColumns(cols)
	g.table.SetRows(rows)
	if cur >= 0 && cur < len(rows) {
		g.table.SetCursor(cur)
	}
}

// plural formats a count with its noun: "1 char", "3 chars".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
