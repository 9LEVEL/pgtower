// Command pgtui is a PostgreSQL administration TUI for sysadmins.
//
// It connects to a Postgres cluster via DATABASE_URL (.env file) and offers,
// with keyboard navigation: a health dashboard, browsing of databases and
// tables with sizes, a blocking tree, and a query runner with confirmation
// for write/destructive statements.
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
	"github.com/9level/pgtui/internal/ui"
)

// version is injected at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	for _, a := range os.Args[1:] {
		if a == "-v" || a == "--version" {
			fmt.Println("pgtui " + version)
			return
		}
		if a == "-h" || a == "--help" {
			fmt.Println("usage: pgtui   (reads config.yml or PG*/DATABASE_URL variables)\n\n" +
				"PostgreSQL administration TUI. Config search: ./  the binary's dir  " +
				"~/.config/pgtui/  /opt/pgtui/  /etc/pgtui/.\nShortcuts: '?' inside the app.")
			return
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pgtui: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Version = version

	ctx := context.Background()
	mgr, err := db.NewManager(ctx, cfg.URL, cfg.AdminDB)
	if err != nil {
		return fmt.Errorf("connect to Postgres (%s@%s:%s): %w", cfg.User, cfg.Host, cfg.Port, err)
	}
	defer mgr.Close()

	p := tea.NewProgram(ui.New(cfg, mgr), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
