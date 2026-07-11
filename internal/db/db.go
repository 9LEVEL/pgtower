// Package db gerencia pools de conexão pgx por database e expõe as consultas
// de administração usadas pelo TUI.
package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager mantém um pool por database. O primeiro pool (AdminDB) é usado para
// consultas de cluster; pools adicionais são criados sob demanda ao navegar
// para tabelas de outro banco ou rodar queries nele.
type Manager struct {
	baseCfg *pgxpool.Config
	adminDB string

	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

// NewManager parseia o DSN base e abre o pool do database administrativo,
// validando a conectividade com um ping.
func NewManager(ctx context.Context, dsn, adminDB string) (*Manager, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("dsn inválido: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 8 * time.Second
	// Identifica a origem das conexões no pg_stat_activity.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "pgtui"

	m := &Manager{
		baseCfg: cfg,
		adminDB: adminDB,
		pools:   make(map[string]*pgxpool.Pool),
	}

	if _, err := m.Pool(ctx, adminDB); err != nil {
		return nil, err
	}
	return m, nil
}

// AdminDB retorna o nome do database administrativo.
func (m *Manager) AdminDB() string { return m.adminDB }

// Pool retorna (criando se necessário) o pool para o database informado.
func (m *Manager) Pool(ctx context.Context, dbname string) (*pgxpool.Pool, error) {
	m.mu.Lock()
	if p, ok := m.pools[dbname]; ok {
		m.mu.Unlock()
		return p, nil
	}
	// Clona a config base trocando apenas o database.
	cfg := m.baseCfg.Copy()
	cfg.ConnConfig.Database = dbname
	m.mu.Unlock()

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("conectar em %q: %w", dbname, err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := p.Ping(pingCtx); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping em %q: %w", dbname, err)
	}

	m.mu.Lock()
	m.pools[dbname] = p
	m.mu.Unlock()
	return p, nil
}

// Close fecha todos os pools abertos.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pools {
		p.Close()
	}
	m.pools = map[string]*pgxpool.Pool{}
}
