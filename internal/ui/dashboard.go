package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

type dashboardView struct {
	cfg *config.Config
	mgr *db.Manager

	data     db.DashboardData
	loaded   bool
	err      error
	updated  time.Time
	interval time.Duration
	started  bool // garante um único loop de tick

	width, height int
}

func newDashboardView(cfg *config.Config, mgr *db.Manager) *dashboardView {
	return &dashboardView{
		cfg:      cfg,
		mgr:      mgr,
		interval: time.Duration(cfg.RefreshSeconds) * time.Second,
	}
}

func (v *dashboardView) Title() string          { return "Dashboard" }
func (v *dashboardView) CapturingInput() bool    { return false }
func (v *dashboardView) SetSize(w, h int)         { v.width, v.height = w, h }

func (v *dashboardView) Init() tea.Cmd {
	// O tick se auto-reagenda em Update; iniciá-lo só uma vez evita acumular
	// loops de auto-refresh a cada vez que a aba é reaberta. Reaberturas
	// posteriores apenas disparam um refresh imediato.
	if v.started {
		return loadDashboard(v.mgr)
	}
	v.started = true
	return tea.Batch(loadDashboard(v.mgr), tick(v.interval))
}

func (v *dashboardView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dashboardMsg:
		v.loaded = true
		v.err = msg.err
		if msg.err == nil {
			v.data = msg.data
			v.updated = time.Now()
		}
		return nil
	case tickMsg:
		// Re-agenda o próximo tick e recarrega.
		return tea.Batch(loadDashboard(v.mgr), tick(v.interval))
	case tea.KeyMsg:
		if msg.String() == "r" {
			return loadDashboard(v.mgr)
		}
	}
	return nil
}

func (v *dashboardView) FooterHints() string {
	h := hint("r", "atualizar")
	if !v.updated.IsZero() {
		h += stKeyHint.Render("  ·  atualizado " + v.updated.Format("15:04:05"))
	}
	return h
}

func (v *dashboardView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Falha ao carregar dashboard: "+v.err.Error())
	}
	if !v.loaded {
		return "\n  " + stStatus.Render("Carregando métricas do cluster…")
	}

	d := v.data

	// --- cartões de topo ---
	connValue := fmt.Sprintf("%d / %d", d.TotalConns, d.MaxConns)
	connCard := v.card("Conexões", []string{
		colorConns(d.TotalConns, d.MaxConns, connValue),
		stLabel.Render(fmt.Sprintf("ativ %d · idle %d · tx %d", d.Active, d.Idle, d.IdleInTx)),
	})

	cacheCard := v.card("Cache hit", []string{
		colorRatio(d.CacheHitRatio),
		stLabel.Render(fmt.Sprintf("commits %s · rollbacks %s",
			human(d.Commits), human(d.Rollbacks))),
	})

	sizeCard := v.card("Armazenamento", []string{
		stValue.Render(d.TotalSize),
		stLabel.Render(fmt.Sprintf("%d databases", d.DBCount)),
	})

	uptimeCard := v.card("Uptime", []string{
		stValue.Render(humanDuration(d.Uptime)),
		stLabel.Render("desde " + d.StartedAt.Format("2006-01-02 15:04")),
	})

	row1 := lipgloss.JoinHorizontal(lipgloss.Top, connCard, cacheCard, sizeCard, uptimeCard)

	// --- servidor / query mais longa ---
	longest := "—"
	longStyle := stValue
	if d.LongestQuery > 0 {
		longest = humanDuration(d.LongestQuery)
		if d.LongestQuery > 5*time.Minute {
			longStyle = stBadV
		} else if d.LongestQuery > 30*time.Second {
			longStyle = stWarnV
		}
	}
	serverCard := v.card("Servidor", []string{
		stValue.Render(d.Version),
		stLabel.Render("query ativa mais longa: ") + longStyle.Render(longest),
	})

	// --- replicação ---
	var repl string
	if len(d.Replicas) == 0 {
		repl = v.card("Replicação", []string{stLabel.Render("nenhum standby conectado")})
	} else {
		lines := make([]string, 0, len(d.Replicas))
		for _, r := range d.Replicas {
			st := stGood.Render(r.State)
			if r.State != "streaming" {
				st = stWarnV.Render(r.State)
			}
			lines = append(lines, fmt.Sprintf("%s  %s  sync=%s  lag=%s",
				stValue.Render(r.ClientAddr), st, r.SyncState, r.Lag))
		}
		repl = v.card("Replicação", lines)
	}

	row2 := lipgloss.JoinHorizontal(lipgloss.Top, serverCard, repl)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(row1)
	b.WriteString("\n\n")
	b.WriteString(row2)
	return b.String()
}

// card renderiza um cartão com título e linhas de conteúdo.
func (v *dashboardView) card(title string, lines []string) string {
	w := v.cardWidth()
	head := lipgloss.NewStyle().Foreground(colMuted).Bold(true).Render(strings.ToUpper(title))
	content := head + "\n" + strings.Join(lines, "\n")
	return stCardBorder.Width(w).Render(content)
}

func (v *dashboardView) cardWidth() int {
	// 4 cartões por linha com folga para bordas.
	w := (v.width / 4) - 2
	if w < 16 {
		w = 16
	}
	if w > 40 {
		w = 40
	}
	return w
}

func colorConns(cur, max int, s string) string {
	if max <= 0 {
		return stValue.Render(s)
	}
	ratio := float64(cur) / float64(max)
	switch {
	case ratio >= 0.9:
		return stBadV.Render(s)
	case ratio >= 0.7:
		return stWarnV.Render(s)
	default:
		return stGood.Render(s)
	}
}

func colorRatio(pct float64) string {
	s := fmt.Sprintf("%.2f%%", pct)
	switch {
	case pct >= 99:
		return stGood.Render(s)
	case pct >= 90:
		return stWarnV.Render(s)
	default:
		return stBadV.Render(s)
	}
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm %ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

func human(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	// separador de milhar com ponto (pt-BR)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	return string(out)
}
