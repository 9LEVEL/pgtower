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
	started  bool // ensures a single tick loop

	width, height int
}

func newDashboardView(cfg *config.Config, mgr *db.Manager) *dashboardView {
	return &dashboardView{
		cfg:      cfg,
		mgr:      mgr,
		interval: time.Duration(cfg.RefreshSeconds) * time.Second,
	}
}

func (v *dashboardView) Title() string        { return "Dashboard" }
func (v *dashboardView) CapturingInput() bool { return false }
func (v *dashboardView) SetSize(w, h int)     { v.width, v.height = w, h }

func (v *dashboardView) Init() tea.Cmd {
	// The tick reschedules itself in Update; starting it only once avoids
	// accumulating auto-refresh loops each time the tab is reopened. Later
	// reopens just trigger an immediate refresh.
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
		// Reschedule the next tick and reload.
		return tea.Batch(loadDashboard(v.mgr), tick(v.interval))
	case tea.KeyMsg:
		if msg.String() == "r" {
			return loadDashboard(v.mgr)
		}
	}
	return nil
}

func (v *dashboardView) FooterHints() string {
	h := hint("r", "refresh")
	if !v.updated.IsZero() {
		h += stKeyHint.Render("  ·  updated " + v.updated.Format("15:04:05"))
	}
	return h
}

func (v *dashboardView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Failed to load dashboard: "+v.err.Error())
	}
	if !v.loaded {
		return "\n  " + stStatus.Render("Loading cluster metrics…")
	}

	d := v.data

	// --- top cards ---
	connValue := fmt.Sprintf("%d / %d", d.TotalConns, d.MaxConns)
	connHead := colorConns(d.TotalConns, d.MaxConns, connValue)
	if d.Reserved > 0 {
		connHead += stLabel.Render(fmt.Sprintf("  ·  resv %d", d.Reserved))
	}
	connCard := v.card("Connections", []string{
		connHead,
		stLabel.Render(fmt.Sprintf("active %d · idle %d · tx %d", d.Active, d.Idle, d.IdleInTx)),
	})

	cacheCard := v.card("Cache hit", []string{
		colorRatio(d.CacheHitRatio),
		stLabel.Render(fmt.Sprintf("commits %s · rollbacks %s",
			human(d.Commits), human(d.Rollbacks))),
	})

	sizeCard := v.card("Storage", []string{
		stValue.Render(d.TotalSize),
		stLabel.Render(fmt.Sprintf("%d databases", d.DBCount)),
	})

	uptimeCard := v.card("Uptime", []string{
		stValue.Render(humanDuration(d.Uptime)),
		stLabel.Render("since " + d.StartedAt.Format("2006-01-02 15:04")),
	})

	row1 := lipgloss.JoinHorizontal(lipgloss.Top, connCard, cacheCard, sizeCard, uptimeCard)

	// --- server / longest query ---
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
	serverCard := v.card("Server", []string{
		stValue.Render(d.Version),
		stLabel.Render("client: ") + stValue.Render("pgtui "+appVersion(v.cfg.Version)),
		stLabel.Render("longest active query: ") + longStyle.Render(longest),
	})

	// --- replication ---
	var repl string
	if len(d.Replicas) == 0 {
		repl = v.card("Replication", []string{stLabel.Render("no standby connected")})
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
		repl = v.card("Replication", lines)
	}

	row2 := lipgloss.JoinHorizontal(lipgloss.Top, serverCard, repl)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(row1)
	b.WriteString("\n\n")
	b.WriteString(row2)
	if adv := v.connAdvisor(d); adv != "" {
		b.WriteString("\n\n")
		b.WriteString(adv)
	}
	return b.String()
}

// connAdvisor renders the connection-usage findings (Unit 1 advisor) so the
// dashboard tells the operator what to do, not just the raw numbers.
func (v *dashboardView) connAdvisor(d db.DashboardData) string {
	findings := db.AnalyzeConnections(db.ConnHeadroom{
		MaxConnections: d.MaxConns,
		Reserved:       d.Reserved,
		Used:           d.TotalConns,
		Active:         d.Active,
		Idle:           d.Idle,
		IdleInTx:       d.IdleInTx,
	})
	if len(findings) == 0 {
		return ""
	}
	title := lipgloss.NewStyle().Foreground(colMuted).Bold(true).Render("CONNECTION ADVISOR")
	wrapW := clampInt(v.width-4, 20, 108)
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		mark := stLabel
		switch f.Level {
		case "crit":
			mark = stBadV
		case "warn":
			mark = stWarnV
		case "info":
			mark = stGood
		}
		lines = append(lines, mark.Render("●")+" "+lipgloss.NewStyle().Width(wrapW).Render(f.Msg))
	}
	return title + "\n" + strings.Join(lines, "\n")
}

// card renders a card with a title and content lines.
func (v *dashboardView) card(title string, lines []string) string {
	w := v.cardWidth()
	head := lipgloss.NewStyle().Foreground(colMuted).Bold(true).Render(strings.ToUpper(title))
	content := head + "\n" + strings.Join(lines, "\n")
	return stCardBorder.Width(w).Render(content)
}

func (v *dashboardView) cardWidth() int {
	// 4 cards per row with room for borders.
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
	// thousands separator with a comma (e.g. 13,303,136)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
