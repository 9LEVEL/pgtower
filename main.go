// Command pgtui is a PostgreSQL administration TUI for sysadmins.
//
// It manages one or more Postgres servers (configured in config.yml or via
// DATABASE_URL) and offers, with keyboard navigation: a health dashboard,
// browsing of databases and tables with sizes, a blocking tree, and a query
// runner with confirmation for write/destructive statements.
package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/ui"
)

// version is injected at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

const usage = `usage: pgtui [-s NAME] [--list]

PostgreSQL administration TUI.

  -s, --server NAME   open the server NAME from config.yml
      --list          list the configured servers and exit
  -v, --version       print the version
  -h, --help          this help

Servers are managed in the app (press S) and saved to config.yml, searched in
./  the binary's dir  ~/.config/pgtui/  /opt/pgtui/  /etc/pgtui/.
DATABASE_URL (or PGHOST/PGUSER/…) adds a session-only server that opens first.
Shortcuts: '?' inside the app.`

func main() {
	var server string
	var list bool
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-v" || a == "--version":
			fmt.Println("pgtui " + version)
			return
		case a == "-h" || a == "--help":
			fmt.Println(usage)
			return
		case a == "--list":
			list = true
		case a == "-s" || a == "--server":
			if i+1 >= len(args) {
				fail(2, a+" needs a server name")
			}
			i++
			server = args[i]
		case strings.HasPrefix(a, "--server="):
			server = strings.TrimPrefix(a, "--server=")
		default:
			fail(2, "unknown argument "+a+"\n\n"+usage)
		}
	}

	store, err := config.Load()
	if err != nil {
		fail(1, err.Error())
	}
	if list {
		printServers(store)
		return
	}
	start, ok, err := store.Initial(server)
	if err != nil {
		fail(2, err.Error())
	}
	var startPtr *config.Connection
	if ok {
		startPtr = &start
	}

	m := ui.New(store, version, startPtr)
	defer m.Close()
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fail(1, err.Error())
	}
}

func printServers(s *config.Store) {
	if len(s.Names()) == 0 {
		fmt.Println("no servers configured — run pgtui and press S, or set DATABASE_URL")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTARGET\tTAG\tDEFAULT")
	for _, n := range s.Names() {
		c, _ := s.Find(n)
		target := "?"
		if cfg, err := s.Resolve(c, version); err == nil {
			target = fmt.Sprintf("%s@%s:%s/%s", cfg.User, cfg.Host, cfg.Port, cfg.AdminDB)
		}
		tag, def := c.Tag, ""
		if s.Env != nil && n == s.Env.Name {
			tag = "env"
		}
		if n == s.Default {
			def = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n, target, tag, def)
	}
	tw.Flush()
	if s.Path != "" {
		fmt.Println("\nconfig: " + s.Path)
	}
}

func fail(code int, msg string) {
	fmt.Fprintln(os.Stderr, "pgtui: "+msg)
	os.Exit(code)
}
