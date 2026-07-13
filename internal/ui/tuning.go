package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

// tuningView is the read-only configuration advisor (Unit 2). It reads the key
// GUCs and shows current vs recommended with a verdict, using the host RAM/cores
// from config when available.
type tuningView struct {
	cfg *config.Config
	mgr *db.Manager

	in   db.TuningInput
	recs []db.TuningRec

	loading bool
	err     error

	width, height int
}

func newTuningView(cfg *config.Config, mgr *db.Manager) *tuningView {
	return &tuningView{cfg: cfg, mgr: mgr}
}

func (v *tuningView) Title() string        { return "Tuning" }
func (v *tuningView) CapturingInput() bool { return false }
func (v *tuningView) SetSize(w, h int)     { v.width, v.height = w, h }
func (v *tuningView) FooterHints() string  { return hint("r", "refresh") }

func (v *tuningView) Init() tea.Cmd {
	v.loading = true
	v.err = nil
	return loadTuning(v.mgr, v.cfg.HostRAMMB, v.cfg.HostCPUs)
}

func (v *tuningView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tuningMsg:
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.recs = msg.recs
			v.in = msg.in
		}
		return nil
	case tea.KeyMsg:
		if msg.String() == "r" {
			return v.Init()
		}
	}
	return nil
}

func (v *tuningView) View() string {
	if v.err != nil {
		return "\n" + stErr.Render("Failed to read settings: "+v.err.Error())
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
		line := verdictMark(r.Verdict) + " " +
			stValue.Render(pad(r.Name, 22)) +
			stLabel.Render(pad(r.Current, 13)) +
			pad(rec, 15) +
			stKeyHint.Render(truncate(r.Note, noteW))
		lines = append(lines, line)
	}

	return "\n" + title + host + "\n\n" + header + "\n" + strings.Join(lines, "\n")
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
