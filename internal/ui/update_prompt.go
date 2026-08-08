package ui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtui/internal/update"
)

// updateCheckedMsg carries the newest release tag found on GitHub (or an error,
// which is silently ignored — a failed check just means "no update offered").
type updateCheckedMsg struct {
	latest string
	err    error
}

// updateResultMsg reports the outcome of a self-update attempt. On failure text
// holds a human-facing message (manual steps or the error).
type updateResultMsg struct {
	ok     bool
	text   string
	danger bool
}

// checkUpdateCmd asks GitHub for the latest release, bounded by a short timeout.
func checkUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		latest, err := update.Latest(ctx, update.Repo)
		return updateCheckedMsg{latest: latest, err: err}
	}
}

// handleUpdateChoice runs the action for the selected menu item and closes it.
func (m *Model) handleUpdateChoice(idx int) tea.Cmd {
	m.updateMenu.close()
	switch idx {
	case 0: // Update now
		m.updating = true
		m.status = ""
		return doSelfUpdateCmd(m.cfg.Version, m.updateLatest)
	case 2: // Never suggest again
		return optOutUpdatesCmd()
	}
	return nil // Not now
}

// doSelfUpdateCmd downloads and installs updateLatest, replacing this binary.
func doSelfUpdateCmd(current, latest string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := update.SelfUpdate(ctx, update.Repo, latest)
		if err == nil {
			return updateResultMsg{ok: true}
		}
		var me *update.ManualError
		if errors.As(err, &me) {
			return updateResultMsg{text: me.Instructions(), danger: false}
		}
		return updateResultMsg{text: "Update failed:\n\n" + err.Error(), danger: true}
	}
}

// optOutUpdatesCmd persists the "never suggest updates" choice.
func optOutUpdatesCmd() tea.Cmd {
	return func() tea.Msg {
		if err := update.OptOut(); err != nil {
			return statusMsg("could not save the preference: " + err.Error())
		}
		return statusMsg("update prompts disabled — re-enable with update_check: true in config.yml")
	}
}

// renderUpdating draws the "downloading…" box shown while a self-update runs.
func (m *Model) renderUpdating() string {
	box := stModal.BorderForeground(colAccent).Render(
		stKey.Render("Updating pgtui…") + "\n\n" +
			stKeyHint.Render("downloading "+m.updateLatest+" and verifying its checksum"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
