package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// openHelp popula o viewport da ajuda e o abre.
func (m *Model) openHelp() {
	m.showHelp = true
	m.helpVP.Width = clampInt(m.width-8, 30, 84)
	m.helpVP.Height = clampInt(m.height-8, 4, 40)
	m.helpVP.SetContent(helpBody())
	m.helpVP.GotoTop()
}

// helpBody monta o corpo (rolável) da ajuda.
func helpBody() string {
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
		{"F", "forçar drop: reatribui posse a outro role e remove"},
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
	return strings.TrimRight(b.String(), "\n")
}

// overlayHelp desenha a ajuda: cabeçalho (versão + marca) e rodapé fixos, com
// os atalhos num viewport rolável para caber em qualquer altura de terminal.
func (m *Model) overlayHelp(bg string) string {
	ver := m.cfg.Version
	if ver == "" {
		ver = "dev"
	}
	header := stBrand.Render("pgtui") + stVersion.Render(" "+ver) +
		stKeyHint.Render("  ·  ") + stBrand.Render(brand)
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render("Atalhos do teclado")

	footer := stKeyHint.Render("esc fechar")
	if m.helpVP.TotalLineCount() > m.helpVP.Height {
		footer += stKeyHint.Render(" · ↑↓ rolar")
	}

	content := header + "\n" + title + "\n\n" + m.helpVP.View() + "\n\n" + footer
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
