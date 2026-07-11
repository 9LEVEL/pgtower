package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// overlayHelp desenha o painel de ajuda centralizado sobre a página.
func (m *Model) overlayHelp(bg string) string {
	rows := [][2]string{
		{"Navegação global", ""},
		{"1 – 4", "trocar de aba"},
		{"tab / shift+tab", "próxima / aba anterior"},
		{"?", "abrir/fechar esta ajuda"},
		{"q  /  ctrl+c", "sair"},
		{"", ""},
		{"Dashboard", ""},
		{"r", "atualizar agora (auto a cada N s)"},
		{"", ""},
		{"Bancos & Tabelas", ""},
		{"↑/↓  j/k", "navegar lista"},
		{"enter", "abrir tabelas do banco selecionado"},
		{"esc", "voltar para a lista de bancos"},
		{"r", "recarregar"},
		{"", ""},
		{"Query Runner", ""},
		{"enter / i", "focar o editor SQL"},
		{"ctrl+r  /  f5", "executar a query"},
		{"esc", "sair do editor (foca resultados)"},
		{"↑/↓ ←/→", "rolar o grid de resultados"},
		{"", ""},
		{"Locks", ""},
		{"r", "recarregar árvore de bloqueios"},
	}

	var b strings.Builder
	for _, r := range rows {
		switch {
		case r[0] == "" && r[1] == "":
			b.WriteString("\n")
		case r[1] == "":
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render(r[0]))
			b.WriteString("\n")
		default:
			key := stKey.Render(pad(r[0], 16))
			b.WriteString("  " + key + stKeyHint.Render(r[1]) + "\n")
		}
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render("Atalhos do teclado")

	content := title + "\n\n" + strings.TrimRight(b.String(), "\n")
	box := stModal.BorderForeground(colAccent).Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// pad ajusta uma string para largura fixa (à esquerda).
func pad(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// truncate corta uma string (largura de célula) adicionando reticências.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	// Corta por runas até caber, reservando 1 para "…".
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
