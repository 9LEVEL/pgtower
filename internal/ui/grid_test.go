package ui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/9level/pgtower/internal/config"
	"github.com/9level/pgtower/internal/db"
)

// stubClipboard captures what the copy commands write, instead of the real
// clipboard. err makes the system clipboard fail (OSC 52 fallback).
func stubClipboard(t *testing.T, err error) (*[]string, *bytes.Buffer) {
	t.Helper()
	var got []string
	var osc bytes.Buffer
	prevW, prevOut := writeClipboard, osc52Out
	writeClipboard = func(s string) error {
		if err != nil {
			return err
		}
		got = append(got, s)
		return nil
	}
	osc52Out = &osc
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	t.Cleanup(func() { writeClipboard, osc52Out = prevW, prevOut })
	return &got, &osc
}

// wideResult is a result with more columns than fit in 80 cells.
func wideResult() db.QueryResult {
	res := db.QueryResult{RowCount: 2}
	for i := 0; i < 12; i++ {
		res.Columns = append(res.Columns, fmt.Sprintf("column_%02d", i))
	}
	for r := 0; r < 2; r++ {
		var disp, raw []string
		for c := range res.Columns {
			disp = append(disp, fmt.Sprintf("value_r%d_c%02d", r, c))
			raw = append(raw, fmt.Sprintf("value_r%d_c%02d", r, c))
		}
		res.Rows = append(res.Rows, disp)
		res.Raw = append(res.Raw, raw)
	}
	// r1 c1: multi-line text (display collapsed, raw intact); r1 c2: NULL.
	res.Rows[1][1], res.Raw[1][1] = "line one -- note line two", "line one -- note\nline two"
	res.Rows[1][2], res.Raw[1][2] = "∅", ""
	return res
}

func newTestQueryView() *queryView {
	v := newQueryView(&config.Config{}, &db.Manager{})
	v.SetSize(80, 30)
	return v
}

func runStatus(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("expected a statusMsg, got %T", msg)
	}
	return string(msg)
}

// TestQueryResultsColumnNavigation: the Query tab scrolls wide results
// horizontally, like the data browser.
func TestQueryResultsColumnNavigation(t *testing.T) {
	v := newTestQueryView()
	v.Update(queryMsg{res: wideResult()})

	if strings.Contains(v.View(), "COLUMN_11") {
		t.Fatal("test needs a result wider than the screen")
	}
	assertContains(t, v.View(), "›COLUMN_00", "column 1/12: column_00")

	for i := 0; i < 20; i++ { // past the last column: stays on it
		v.Update(key("right"))
		_ = v.View()
	}
	if v.results.colCursor != 11 {
		t.Fatalf("colCursor = %d, want 11", v.results.colCursor)
	}
	out := v.View()
	assertContains(t, out, "›COLUMN_11", "column 12/12: column_11")
	if strings.Contains(out, "COLUMN_00") {
		t.Error("the first column should have scrolled out of view")
	}

	v.Update(key("h")) // vim keys too
	if v.results.colCursor != 10 {
		t.Errorf("colCursor after h = %d, want 10", v.results.colCursor)
	}
	assertContains(t, v.FooterHints(), "columns", "copy cell/row")

	// Re-running the same query keeps the column; a new shape starts over.
	v.Update(queryMsg{res: wideResult()})
	if v.results.colCursor != 10 {
		t.Errorf("same-shape result moved the column to %d", v.results.colCursor)
	}
	v.Update(queryMsg{res: db.QueryResult{Columns: []string{"a"}, Rows: [][]string{{"1"}}, RowCount: 1}})
	if v.results.colCursor != 0 {
		t.Errorf("new result shape should reset the column, got %d", v.results.colCursor)
	}
}

// TestResultGridFitsWidth: every line fits the screen whichever column is
// active — the table pads each cell by a space on both sides, and a window
// that ignored that cut off the rightmost (possibly active) column.
func TestResultGridFitsWidth(t *testing.T) {
	for w := 40; w <= 120; w++ {
		g := newResultGrid(40)
		g.SetHeight(5)
		g.SetWidth(w)
		g.SetData(wideResult())
		for c := 0; c < len(g.cols); c++ {
			for _, line := range strings.Split(g.View(), "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Fatalf("width %d, column %d: line is %d cells wide: %q", w, c, lw, line)
				}
			}
			if name, _ := g.column(); !strings.Contains(g.View(), "›"+strings.ToUpper(name)) {
				t.Fatalf("width %d: active column %s not fully shown", w, name)
			}
			g.Update(key("right"))
		}
	}
}

// TestResultGridHeaderHighlight: the active column's title gets a background,
// the others don't; without colors the redrawn header is the table's own.
func TestResultGridHeaderHighlight(t *testing.T) {
	g := newResultGrid(40)
	g.SetHeight(5)
	g.SetWidth(100)
	g.SetData(wideResult())
	if g.View() != g.table.View() {
		t.Fatalf("plain header differs from the table's:\n%s\n---\n%s", g.View(), g.table.View())
	}

	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	// sgrBefore is the style sequence in effect where title starts.
	sgrBefore := func(title string) string {
		head, _, _ := strings.Cut(g.View(), "\n")
		i := strings.Index(head, title)
		if i < 0 {
			t.Fatalf("%q not in header %q", title, head)
		}
		return head[strings.LastIndex(head[:i], "\x1b["):i]
	}
	g.Update(key("right"))
	g.Update(key("right"))
	if sgr := sgrBefore("›COLUMN_02"); !strings.Contains(sgr, "48;") {
		t.Errorf("active title has no background: %q", sgr)
	}
	for _, other := range []string{"COLUMN_01", "COLUMN_03"} {
		if sgr := sgrBefore(other); strings.Contains(sgr, "48;") {
			t.Errorf("%s has a background: %q", other, sgr)
		}
	}
}

func TestQueryResultsCopy(t *testing.T) {
	got, _ := stubClipboard(t, nil)
	v := newTestQueryView()
	v.Update(queryMsg{res: wideResult()})

	// y copies the active cell.
	v.Update(key("right"))
	status := runStatus(t, v.Update(key("y")))
	if len(*got) != 1 || (*got)[0] != "value_r0_c01" {
		t.Fatalf("copied %q, want value_r0_c01", *got)
	}
	assertContains(t, status, "copied column_01")

	// The full text is copied, not the one-line display.
	v.Update(key("down"))
	runStatus(t, v.Update(key("y")))
	if (*got)[1] != "line one -- note\nline two" {
		t.Errorf("multi-line cell copied as %q", (*got)[1])
	}

	// A NULL cell leaves the clipboard alone.
	v.Update(key("right"))
	status = runStatus(t, v.Update(key("y")))
	if len(*got) != 2 {
		t.Errorf("NULL cell was copied: %q", *got)
	}
	assertContains(t, status, "column_02 is NULL", "nothing copied")

	// Y copies the whole row, tab-separated.
	status = runStatus(t, v.Update(key("Y")))
	row := (*got)[2]
	if cells := strings.Split(row, "\t"); len(cells) != 12 {
		t.Fatalf("row has %d tab-separated cells, want 12: %q", len(cells), row)
	}
	if !strings.HasPrefix(row, "value_r1_c00\t\"line one -- note\nline two\"\t\tvalue_r1_c03\t") {
		t.Errorf("row copied as %q", row)
	}
	assertContains(t, status, "row 2", "12 columns")

	// Without results (a statement with no rows) there is nothing to copy.
	v.Update(queryMsg{res: db.QueryResult{Command: "UPDATE 1"}})
	if cmd := v.Update(key("y")); cmd != nil {
		t.Error("y copied a hidden, previous result")
	}
}

func TestCopyFallsBackToOSC52(t *testing.T) {
	_, osc := stubClipboard(t, errors.New("no clipboard utilities"))
	status := runStatus(t, copyToClipboard("héllo", "x"))
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("héllo")) + "\x07"
	if osc.String() != want {
		t.Errorf("OSC 52 output = %q, want %q", osc.String(), want)
	}
	assertContains(t, status, "OSC 52")

	osc.Reset()
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	copyToClipboard("hi", "x")()
	if n := strings.Count(osc.String(), "]52;c;"); n != 2 {
		t.Errorf("inside tmux expected a plain and a passthrough sequence, got %d: %q", n, osc.String())
	}
}

// TestModelCopyStatus: the copy outcome reaches the footer through the
// session scope.
func TestModelCopyStatus(t *testing.T) {
	stubClipboard(t, nil)
	cfg := &config.Config{Host: "h", Port: "5432", User: "postgres", AdminDB: "postgres", RefreshSeconds: 5}
	var m tea.Model = newConnected(cfg, &db.Manager{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("3"))
	m, _ = m.Update(queryMsg{res: wideResult()})

	m, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y on the Query tab returned no command")
	}
	m, _ = m.Update(cmd())
	assertContains(t, m.View(), "copied column_00")
}

func TestTSVRow(t *testing.T) {
	got := tsvRow([]string{"plain", "", "tab\there", `say "hi"`, "a\nb"})
	want := "plain\t\t\"tab\there\"\t\"say \"\"hi\"\"\"\t\"a\nb\""
	if got != want {
		t.Errorf("tsvRow = %q, want %q", got, want)
	}
}
