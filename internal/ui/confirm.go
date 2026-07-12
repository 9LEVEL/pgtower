package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type confirmResult int

const (
	confirmNone confirmResult = iota
	confirmYes
	confirmNo
)

// confirmModal is a reusable confirmation dialog. In critical mode, the user
// must type exactly `token` (e.g. the object name) — a guard against accidental
// deletion of databases/roles.
type confirmModal struct {
	active   bool
	critical bool
	title    string
	body     string
	token    string
	input    textinput.Model
}

func newConfirmModal() confirmModal {
	ti := textinput.New()
	ti.CharLimit = 128
	ti.Width = 32
	return confirmModal{input: ti}
}

func (c *confirmModal) ask(title, body string) tea.Cmd {
	c.active, c.critical = true, false
	c.title, c.body = title, body
	return nil
}

func (c *confirmModal) askCritical(title, body, token string) tea.Cmd {
	c.active, c.critical = true, true
	c.title, c.body, c.token = title, body, token
	c.input.SetValue("")
	c.input.Placeholder = "type: " + token
	return c.input.Focus()
}

func (c *confirmModal) close() {
	c.active = false
	c.input.Blur()
	c.input.SetValue("")
}

func (c *confirmModal) update(msg tea.KeyMsg) confirmResult {
	if !c.active {
		return confirmNone
	}
	if c.critical {
		switch msg.Type {
		case tea.KeyEsc:
			c.close()
			return confirmNo
		case tea.KeyEnter:
			if strings.TrimSpace(c.input.Value()) == c.token {
				c.close()
				return confirmYes
			}
			return confirmNone // wrong text: does not confirm
		}
		c.input, _ = c.input.Update(msg)
		return confirmNone
	}
	switch strings.ToLower(msg.String()) {
	case "y":
		c.close()
		return confirmYes
	case "n", "esc":
		c.close()
		return confirmNo
	}
	return confirmNone
}

func (c *confirmModal) view(w, h int) string {
	titleBg := colWarn
	if c.critical {
		titleBg = colDanger
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(titleBg).
		Padding(0, 1).Render(" " + c.title + " ")

	var footer string
	if c.critical {
		footer = c.input.View() + "\n\n" + stKey.Render("enter") + " confirm   " + stKey.Render("esc") + " cancel"
	} else {
		footer = stKey.Render("y") + " confirm   " + stKey.Render("n") + " cancel"
	}

	content := title + "\n\n" + c.body + "\n\n" + footer
	box := stModal.BorderForeground(titleBg).Width(clampInt(w-8, 30, 70)).Render(content)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}
