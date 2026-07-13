package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

type tuningSection int

const (
	secAdvisor tuningSection = iota
	secHBA
)

// tuningView is tab 7. It hosts several read-mostly config sections: the
// settings advisor (Unit 2) and the pg_hba viewer (Unit 3).
type tuningView struct {
	cfg *config.Config
	mgr *db.Manager

	section tuningSection

	// advisor
	in     db.TuningInput
	recs   []db.TuningRec
	advErr error

	// pg_hba
	hbaFile     string
	hbaRules    []db.HBARule
	hbaErr      error
	hbaErrCount int
	hbaTbl      table.Model

	width, height int
}

func newTuningView(cfg *config.Config, mgr *db.Manager) *tuningView {
	return &tuningView{cfg: cfg, mgr: mgr, hbaTbl: newTable()}
}

func (v *tuningView) Title() string        { return "Tuning" }
func (v *tuningView) CapturingInput() bool { return false }

func (v *tuningView) SetSize(w, h int) {
	v.width, v.height = w, h
	th := h - 5
	if th < 3 {
		th = 3
	}
	v.hbaTbl.SetHeight(th)
	addrW := clampInt(w-60, 12, 30)
	v.hbaTbl.SetColumns([]table.Column{
		{Title: "LINE", Width: 5},
		{Title: "TYPE", Width: 8},
		{Title: "DATABASE", Width: 14},
		{Title: "USER", Width: 14},
		{Title: "ADDRESS", Width: addrW},
		{Title: "METHOD", Width: 14},
	})
}

func (v *tuningView) FooterHints() string {
	return hint("a", "advisor") + "  " + hint("h", "pg_hba") + "  " + hint("r", "refresh")
}

func (v *tuningView) Init() tea.Cmd {
	v.advErr, v.hbaErr = nil, nil
	return tea.Batch(loadTuning(v.mgr, v.cfg.HostRAMMB, v.cfg.HostCPUs), loadHBA(v.mgr))
}

func (v *tuningView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tuningMsg:
		v.advErr = msg.err
		if msg.err == nil {
			v.recs, v.in = msg.recs, msg.in
		}
		return nil
	case hbaMsg:
		v.hbaErr = msg.err
		v.hbaFile = msg.file
		if msg.err == nil {
			v.hbaRules = msg.rules
			v.rebuildHBATable()
		}
		return nil
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			return v.Init()
		case "a":
			v.section = secAdvisor
			return nil
		case "h":
			v.section = secHBA
			return nil
		}
		if v.section == secHBA {
			var cmd tea.Cmd
			v.hbaTbl, cmd = v.hbaTbl.Update(msg)
			return cmd
		}
	}
	return nil
}

func (v *tuningView) rebuildHBATable() {
	v.hbaErrCount = 0
	rows := make([]table.Row, 0, len(v.hbaRules))
	for _, r := range v.hbaRules {
		method := r.AuthMethod
		if r.Error != "" {
			method = "⚠ error"
			v.hbaErrCount++
		}
		addr := r.Address
		if addr == "" {
			addr = "-"
		}
		rows = append(rows, table.Row{
			strconv.Itoa(r.LineNumber), r.Type, r.Database, r.UserName, addr, method,
		})
	}
	v.hbaTbl.SetRows(rows)
	if v.hbaTbl.Cursor() >= len(rows) {
		v.hbaTbl.SetCursor(0)
	}
}

func (v *tuningView) View() string {
	selector := v.sectionSelector()
	switch v.section {
	case secHBA:
		return selector + "\n" + v.hbaView()
	default:
		return selector + "\n" + v.advisorView()
	}
}

func (v *tuningView) sectionSelector() string {
	item := func(active bool, label string) string {
		if active {
			return stTabActive.Render(label)
		}
		return stTabInactive.Render(label)
	}
	return "\n" + item(v.section == secAdvisor, "a · Advisor") + item(v.section == secHBA, "h · pg_hba")
}

func (v *tuningView) advisorView() string {
	if v.advErr != nil {
		return "\n" + stErr.Render("Failed to read settings: "+v.advErr.Error())
	}
	if v.recs == nil {
		return "\n  " + stStatus.Render("Reading pg_settings…")
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Configuration advisor")
	var host string
	if v.cfg.HostRAMMB > 0 || v.cfg.HostCPUs > 0 {
		var parts []string
		if v.cfg.HostRAMMB > 0 {
			parts = append(parts, "host RAM "+humanMB(v.cfg.HostRAMMB))
		}
		if v.cfg.HostCPUs > 0 {
			parts = append(parts, fmt.Sprintf("%d CPUs", v.cfg.HostCPUs))
		}
		host = stLabel.Render("  " + strings.Join(parts, " · "))
	} else {
		host = stWarnV.Render("  host RAM/cores unknown") +
			stKeyHint.Render(" — set PGTUI_HOST_RAM_MB and PGTUI_HOST_CPUS for concrete targets")
	}

	header := stLabel.Render(pad("  SETTING", 24)) + stLabel.Render(pad("CURRENT", 13)) +
		stLabel.Render(pad("RECOMMENDED", 15)) + stLabel.Render("NOTE")

	noteW := clampInt(v.width-56, 16, 90)
	var lines []string
	for _, r := range v.recs {
		rec := stKeyHint.Render("—")
		if r.Recommended != "" {
			rec = stKey.Render(r.Recommended)
		}
		lines = append(lines, verdictMark(r.Verdict)+" "+
			stValue.Render(pad(r.Name, 22))+
			stLabel.Render(pad(r.Current, 13))+
			pad(rec, 15)+
			stKeyHint.Render(truncate(r.Note, noteW)))
	}
	return "\n" + title + host + "\n\n" + header + "\n" + strings.Join(lines, "\n")
}

func (v *tuningView) hbaView() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Host-based authentication")
	head := "\n" + title
	if v.hbaFile != "" {
		head += stLabel.Render("  " + v.hbaFile)
	}
	if v.hbaErr != nil {
		return head + "\n\n" + stErr.Render("Cannot read pg_hba_file_rules: "+v.hbaErr.Error()) +
			"\n" + stKeyHint.Render("(the view requires a superuser connection)")
	}
	if v.hbaErrCount > 0 {
		head += "   " + stBadV.Render(fmt.Sprintf("⚠ %d line(s) failed to parse", v.hbaErrCount))
	}
	return head + "\n" + v.hbaTbl.View()
}

// verdictMark renders a colored bullet for a verdict.
func verdictMark(vd db.Verdict) string {
	switch vd {
	case db.VerdictWarn:
		return stWarnV.Render("●")
	case db.VerdictOK:
		return stGood.Render("●")
	default:
		return stLabel.Render("●")
	}
}

// humanMB renders a megabyte count as MB or GB.
func humanMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}
