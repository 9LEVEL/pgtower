package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// finderResult reports what a key did to the finder.
type finderResult int

const (
	finderNone finderResult = iota
	finderSelect
	finderCancel
)

// finderItem is one searchable entry. Index points back into the caller's own
// data (e.g. a table row) so the caller can act on the selection.
type finderItem struct {
	index int
	label string
}

// finderMatch is an item that passed the fuzzy filter, carrying the matched
// rune positions (for highlighting) and a rank score.
type finderMatch struct {
	item      finderItem
	positions []int
	score     int
}

// finder is a keyboard-driven "quick find" overlay: type to fuzzy-filter a list,
// ↑↓ / ctrl+p / ctrl+n move, enter picks, esc cancels. It captures text while
// open, so the owning tab must report CapturingInput()==true (otherwise the root
// would steal digits, 'q', tab, … before they reach the search box).
type finder struct {
	active  bool
	title   string
	input   textinput.Model
	items   []finderItem
	matches []finderMatch
	cursor  int
}

func newFinder() finder {
	ti := textinput.New()
	ti.Placeholder = "type to search…"
	ti.CharLimit = 80
	ti.Width = 32
	return finder{input: ti}
}

// open activates the finder over items (label is what the user searches/sees).
func (f *finder) open(title string, items []finderItem) tea.Cmd {
	f.active = true
	f.title = title
	f.items = items
	f.input.SetValue("")
	f.cursor = 0
	f.refilter()
	return f.input.Focus()
}

func (f *finder) close() {
	f.active = false
	f.input.Blur()
	f.items = nil
	f.matches = nil
	f.cursor = 0
}

// update advances the finder. On finderSelect the caller reads selectedIndex()
// and then calls close(); on finderCancel the finder has already closed itself.
func (f *finder) update(msg tea.KeyMsg) (finderResult, tea.Cmd) {
	if !f.active {
		return finderNone, nil
	}
	switch msg.String() {
	case "esc":
		f.close()
		return finderCancel, nil
	case "enter":
		if len(f.matches) > 0 {
			return finderSelect, nil
		}
		return finderNone, nil
	case "up", "ctrl+p":
		if f.cursor > 0 {
			f.cursor--
		}
		return finderNone, nil
	case "down", "ctrl+n":
		if f.cursor < len(f.matches)-1 {
			f.cursor++
		}
		return finderNone, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	f.refilter()
	return finderNone, cmd
}

// selectedIndex returns the caller-data index of the highlighted match, or -1.
func (f *finder) selectedIndex() int {
	if f.cursor < 0 || f.cursor >= len(f.matches) {
		return -1
	}
	return f.matches[f.cursor].item.index
}

// refilter recomputes and ranks the matches from the current query. Equal scores
// keep the caller's original order (stable), so an empty query shows the list
// unchanged.
func (f *finder) refilter() {
	q := strings.TrimSpace(f.input.Value())
	f.matches = f.matches[:0]
	for _, it := range f.items {
		score, pos, ok := fuzzyMatch(q, it.label)
		if !ok {
			continue
		}
		f.matches = append(f.matches, finderMatch{item: it, positions: pos, score: score})
	}
	sort.SliceStable(f.matches, func(i, j int) bool {
		return f.matches[i].score > f.matches[j].score
	})
	if f.cursor >= len(f.matches) {
		f.cursor = len(f.matches) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f *finder) view(w, h int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render(" " + f.title + " ")

	boxW := clampInt(w-8, 40, 72)
	innerW := boxW - 6 // content width inside the double border + padding(1,2)
	maxRows := clampInt(h-12, 4, 14)

	start := 0
	if f.cursor >= maxRows {
		start = f.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(f.matches) {
		end = len(f.matches)
	}

	var list strings.Builder
	if len(f.matches) == 0 {
		list.WriteString(stKeyHint.Render("  (no match)"))
	}
	for i := start; i < end; i++ {
		list.WriteString(f.renderRow(f.matches[i], i == f.cursor, innerW) + "\n")
	}

	count := fmt.Sprintf("%d/%d", len(f.matches), len(f.items))
	hints := stKeyHint.Render("↑↓ select · enter jump · esc cancel · " + count)
	body := stKeyHint.Render("search: ") + f.input.View() + "\n\n" +
		strings.TrimRight(list.String(), "\n") + "\n\n" + hints

	box := stModal.BorderForeground(colAccent).Width(boxW).Render(title + "\n\n" + body)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

// renderRow draws one candidate: the selected one as a full-width accent bar,
// the rest with the matched characters highlighted.
func (f *finder) renderRow(m finderMatch, selected bool, innerW int) string {
	if selected {
		line := pad(truncate("› "+m.item.label, innerW), innerW)
		return lipgloss.NewStyle().Foreground(colOnDark).Background(colAccent).Bold(true).Render(line)
	}
	return "  " + highlightMatch(m.item.label, m.positions, innerW-2)
}

// highlightMatch renders label with the matched rune positions emphasized,
// truncating to maxW runes (ellipsis) so long labels never overflow the box.
func highlightMatch(label string, positions []int, maxW int) string {
	runes := []rune(label)
	ell := false
	if maxW > 1 && len(runes) > maxW {
		runes = runes[:maxW-1]
		ell = true
	}
	hit := make(map[int]bool, len(positions))
	for _, p := range positions {
		if p < len(runes) {
			hit[p] = true
		}
	}
	base := lipgloss.NewStyle().Foreground(colFg)
	var b strings.Builder
	for i := 0; i < len(runes); {
		on := hit[i]
		j := i
		for j < len(runes) && hit[j] == on {
			j++
		}
		seg := string(runes[i:j])
		if on {
			b.WriteString(stKey.Render(seg))
		} else {
			b.WriteString(base.Render(seg))
		}
		i = j
	}
	if ell {
		b.WriteString(stKeyHint.Render("…"))
	}
	return b.String()
}

// fuzzyMatch reports whether every rune of query appears in target in order
// (case-insensitive). It returns a rank score (higher is better) and the matched
// rune positions in target for highlighting. An empty query matches everything
// with score 0. The scan is greedy left-to-right: predictable and allocation-
// light, which is plenty for short labels like role or database names.
func fuzzyMatch(query, target string) (int, []int, bool) {
	if strings.TrimSpace(query) == "" {
		return 0, nil, true
	}
	q := []rune(strings.ToLower(query))
	tRaw := []rune(target)
	t := []rune(strings.ToLower(target))

	positions := make([]int, 0, len(q))
	score, qi, prev := 0, 0, -2
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if t[ti] != q[qi] {
			continue
		}
		s := 1
		if ti == prev+1 {
			s += 5 // consecutive run
		}
		if ti == 0 || isWordBoundary(tRaw[ti-1]) {
			s += 8 // start of a word (after _-./ space :@)
		}
		score += s
		positions = append(positions, ti)
		prev = ti
		qi++
	}
	if qi != len(q) {
		return 0, nil, false
	}
	// Prefer an earlier first hit and a shorter (less noisy) target.
	score -= positions[0]
	score -= (len(t) - len(q)) / 8
	return score, positions, true
}

func isWordBoundary(r rune) bool {
	switch r {
	case '_', '-', '.', '/', ' ', ':', '@':
		return true
	}
	return false
}
