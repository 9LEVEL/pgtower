package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// overlayHelp desenha o painel de ajuda centralizado sobre a página.
func (m *Model) overlayHelp(bg string) string {
	rows := [][2]string{
		{"Navegação global", ""},
		{"1 – 6", "trocar de aba"},
		{"tab / shift+tab", "próxima / aba anterior"},
		{"?", "abrir/fechar esta ajuda"},
		{"q  /  ctrl+c", "sair"},
		{"", ""},
		{"Dashboard", ""},
		{"r", "atualizar agora (auto a cada N s)"},
		{"", ""},
		{"Bancos & Tabelas", ""},
		{"↑/↓  j/k", "navegar lista"},
		{"enter", "banco → tabelas → dados da tabela"},
		{"esc", "voltar um nível"},
		{"r", "recarregar"},
		{"", ""},
		{"Dados da tabela (leitura)", ""},
		{"←/→  h/l", "navegar entre colunas (scroll horizontal)"},
		{"/", "buscar na coluna ativa (ILIKE)"},
		{"e  ou  :", "editar a query do topo (somente leitura)"},
		{"r", "resetar para SELECT *"},
		{"esc", "voltar para a lista de tabelas"},
		{"", ""},
		{"Bancos: criar / apagar / describe", ""},
		{"n", "criar database (na lista de bancos)"},
		{"D", "apagar database (digita o nome p/ confirmar)"},
		{"d", "describe da tabela (colunas, índices, constraints)"},
		{"", ""},
		{"Query Runner", ""},
		{"enter / i", "focar o editor SQL"},
		{"/  ou  ctrl+t", "trocar o database alvo (lista filtrável)"},
		{"x", "EXPLAIN (plano, sem executar)"},
		{"ctrl+r  /  f5", "executar a query"},
		{"esc", "sair do editor (foca resultados)"},
		{"↑/↓ ←/→", "rolar o grid de resultados"},
		{"", ""},
		{"Sessões", ""},
		{"c", "cancelar query da sessão (pg_cancel_backend)"},
		{"k", "encerrar conexão (pg_terminate_backend)"},
		{"r", "atualizar"},
		{"", ""},
		{"Roles", ""},
		{"n", "criar role/usuário"},
		{"g", "grant a um database"},
		{"D", "apagar role (digita o nome p/ confirmar)"},
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
