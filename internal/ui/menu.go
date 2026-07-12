package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type menuResult int

const (
	menuNone menuResult = iota
	menuSelect
	menuCancel
)

// actionMenu is a small keyboard-driven list of actions rendered as a modal.
// ↑↓/jk move the cursor, enter selects (menuSelect + current cursor), esc/q
// cancels. It is deliberately minimal so tabs can offer an "Enter → options"
// entry point without a heavier component.
type actionMenu struct {
	active bool
	title  string
	items  []string
	cursor int
}

func (mn *actionMenu) open(title string, items []string) {
	mn.active = true
	mn.title = title
	mn.items = items
	mn.cursor = 0
}

func (mn *actionMenu) close() {
	mn.active = false
	mn.items = nil
	mn.cursor = 0
}

// update advances the menu state and reports whether an item was selected or
// the menu was cancelled.
func (mn *actionMenu) update(msg tea.KeyMsg) menuResult {
	if !mn.active {
		return menuNone
	}
	n := len(mn.items)
	switch msg.String() {
	case "esc", "q":
		mn.close()
		return menuCancel
	case "up", "k":
		if n > 0 {
			mn.cursor = (mn.cursor - 1 + n) % n
		}
	case "down", "j":
		if n > 0 {
			mn.cursor = (mn.cursor + 1) % n
		}
	case "enter", " ":
		if n > 0 {
			return menuSelect
		}
	}
	return menuNone
}

func (mn *actionMenu) view(w, h int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render(" " + mn.title + " ")

	var b []string
	for i, it := range mn.items {
		if i == mn.cursor {
			b = append(b, stKey.Render("› "+it))
		} else {
			b = append(b, stLabel.Render("  "+it))
		}
	}

	hints := stKeyHint.Render("↑↓ move · enter select · esc cancel")
	content := title + "\n\n" + joinLines(b) + "\n\n" + hints
	box := stModal.BorderForeground(colAccent).Width(clampInt(w-8, 40, 74)).Render(content)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}
