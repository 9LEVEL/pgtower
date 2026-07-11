package ui

import "github.com/charmbracelet/lipgloss"

// Paleta adaptável (funciona em terminais claros e escuros).
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

	stTabActive = lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).Padding(0, 2)
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

	stGood = lipgloss.NewStyle().Foreground(colSuccess).Bold(true)
	stWarnV = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	stBadV  = lipgloss.NewStyle().Foreground(colDanger).Bold(true)

	stModal = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		Padding(1, 2)
)

// hint renderiza "key label" para o rodapé de atalhos.
func hint(k, label string) string {
	return stKey.Render(k) + " " + stKeyHint.Render(label)
}
