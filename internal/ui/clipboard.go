package ui

import (
	"io"
	"os"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// Swapped out by tests, so they never touch the real clipboard or terminal.
var (
	writeClipboard           = clipboard.WriteAll
	osc52Out       io.Writer = os.Stdout
)

// copyToClipboard puts text on the clipboard in the background and reports
// the outcome on the status line. The system clipboard (pbcopy, wl-copy, xclip,
// xsel …) comes first; without one — typically over SSH — the text goes out as
// an OSC 52 sequence, which asks the terminal itself to set its clipboard.
func copyToClipboard(text, what string) tea.Cmd {
	return func() tea.Msg {
		if err := writeClipboard(text); err == nil {
			return statusMsg("copied " + what)
		}
		writeOSC52(osc52Out, text)
		return statusMsg("sent " + what + " to the terminal clipboard (OSC 52)")
	}
}

// writeOSC52 emits the OSC 52 "set clipboard" sequence, wrapped for the
// multiplexer when running inside one.
func writeOSC52(w io.Writer, text string) {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		_, _ = seq.WriteTo(w) // honoured with tmux's set-clipboard on
		seq = seq.Tmux()      // honoured with allow-passthrough on
	case os.Getenv("STY") != "":
		seq = seq.Screen()
	}
	_, _ = seq.WriteTo(w)
}
