// Command pgtui é um TUI de administração de PostgreSQL para sysadmins.
//
// Conecta em um cluster Postgres via DATABASE_URL (arquivo .env) e oferece,
// com navegação por teclado: dashboard de saúde, navegação de bancos e
// tabelas com tamanhos, árvore de bloqueios e um query runner com
// confirmação para statements de escrita/destrutivos.
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
	"github.com/9level/pg-tui/internal/ui"
)

// version é injetado no build via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	for _, a := range os.Args[1:] {
		if a == "-v" || a == "--version" {
			fmt.Println("pgtui " + version)
			return
		}
		if a == "-h" || a == "--help" {
			fmt.Println("uso: pgtui   (lê ./.env ou variáveis PG*/DATABASE_URL)\n\n" +
				"TUI de administração de PostgreSQL. Atalhos: '?' dentro do app.")
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

	ctx := context.Background()
	mgr, err := db.NewManager(ctx, cfg.URL, cfg.AdminDB)
	if err != nil {
		return fmt.Errorf("conectar ao Postgres (%s@%s:%s): %w", cfg.User, cfg.Host, cfg.Port, err)
	}
	defer mgr.Close()

	p := tea.NewProgram(ui.New(cfg, mgr), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
