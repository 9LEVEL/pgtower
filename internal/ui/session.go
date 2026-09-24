package ui

import (
	"reflect"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pgtower/internal/config"
	"github.com/9level/pgtower/internal/db"
)

// session is one live server connection: its resolved config, pool manager and
// the tabs bound to them. Switching servers replaces the whole session.
//
// Every command a tab returns is tagged with the session's generation (see
// scope). Results that arrive after a switch — a slow dashboard query, the old
// dashboard's refresh tick — carry a stale generation and are dropped, so data
// from server A can never render under server B's header.
type session struct {
	gen  int
	cfg  *config.Config
	mgr  *db.Manager
	tabs []tabView
}

func newSession(gen int, cfg *config.Config, mgr *db.Manager) *session {
	return &session{gen: gen, cfg: cfg, mgr: mgr, tabs: []tabView{
		newDashboardView(cfg, mgr),
		newDatabasesView(mgr),
		newQueryView(cfg, mgr),
		newLocksView(mgr),
		newSessionsView(mgr),
		newRolesView(cfg, mgr),
		newTuningView(cfg, mgr),
	}}
}

// scope tags the messages of a tab command with this session's generation.
func (s *session) scope(cmd tea.Cmd) tea.Cmd { return scope(s.gen, cmd) }

// close releases the session's pools in the background: pgxpool.Close waits
// for in-flight queries, which must not freeze the UI.
func (s *session) close() {
	if s != nil && s.mgr != nil {
		go s.mgr.Close()
	}
}

// scopedMsg is a tab result tagged with the generation of the session that
// issued it.
type scopedMsg struct {
	gen int
	msg tea.Msg
}

var teaPkg = reflect.TypeOf(tea.QuitMsg{}).PkgPath()

// scope wraps cmd so its result arrives as a scopedMsg. Batches are unpacked
// and each member wrapped, and Bubble Tea's own control messages (quit,
// window title, exec …) pass through untouched so the runtime still acts on
// them.
func scope(gen int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			out := make(tea.BatchMsg, 0, len(batch))
			for _, c := range batch {
				if c != nil {
					out = append(out, scope(gen, c))
				}
			}
			return out
		}
		t := reflect.TypeOf(msg)
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.PkgPath() == teaPkg {
			return msg
		}
		return scopedMsg{gen: gen, msg: msg}
	}
}
