package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Adaptive palette (works in light and dark terminals).
var (
	colAccent  = lipgloss.AdaptiveColor{Light: "#0b6bcb", Dark: "#4c9fff"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8a8f98"}
	colSubtle  = lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#5c6370"}
	colFg      = lipgloss.AdaptiveColor{Light: "#1f2933", Dark: "#e6e6e6"}
	colSuccess = lipgloss.AdaptiveColor{Light: "#0a7d33", Dark: "#4ec26b"}
	colWarn    = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#e0a92e"}
	colDanger  = lipgloss.AdaptiveColor{Light: "#c02626", Dark: "#ff5c5c"}
	colBorder  = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#3a3f4b"}
	colOnDark  = lipgloss.Color("#ffffff")
)

var (
	stTitle = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).Padding(0, 1)

	stTabActive   = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).Padding(0, 2)
	stTabInactive = lipgloss.NewStyle().Foreground(colMuted).Padding(0, 2)

	stStatus  = lipgloss.NewStyle().Foreground(colMuted)
	stErr     = lipgloss.NewStyle().Foreground(colDanger).Bold(true)
	stKeyHint = lipgloss.NewStyle().Foreground(colSubtle)
	stKey     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	stLabel = lipgloss.NewStyle().Foreground(colMuted)
	stValue = lipgloss.NewStyle().Foreground(colFg).Bold(true)

	stCardBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	stGood  = lipgloss.NewStyle().Foreground(colSuccess).Bold(true)
	stWarnV = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	stBadV  = lipgloss.NewStyle().Foreground(colDanger).Bold(true)

	stModal = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		Padding(1, 2)

	stBrand = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	// stServer renders the active connection's name in the header.
	stServer = lipgloss.NewStyle().Foreground(colFg).Bold(true)

	// stHeaderVer renders the running version as a readable badge so it is
	// always legible in the header (and reused in the About overlay).
	stHeaderVer = lipgloss.NewStyle().Foreground(colOnDark).Background(colMuted).Bold(true).Padding(0, 1)
)

// brand is the lightweight brand signature shown in the header/help.
const brand = "9level.dev"

// hint renders "key label" for the shortcut footer.
func hint(k, label string) string {
	return stKey.Render(k) + " " + stKeyHint.Render(label)
}

// tagBadge renders a connection tag. prod is loud on purpose: it is the one
// place a wrong keystroke hurts.
func tagBadge(tag string) string {
	var bg lipgloss.TerminalColor
	switch tag {
	case "prod":
		bg = colDanger
	case "staging":
		bg = colWarn
	case "dev":
		bg = colSuccess
	case "env":
		bg = colMuted
	default:
		return ""
	}
	return lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(bg).
		Padding(0, 1).Render(strings.ToUpper(tag))
}
