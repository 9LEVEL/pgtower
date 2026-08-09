// Package db manages pgx connection pools per database and exposes the
// administration queries used by the TUI.
package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager keeps one pool per database. The first pool (AdminDB) is used for
// cluster queries; additional pools are created on demand when navigating to
// another database's tables or running queries against it.
type Manager struct {
	baseCfg *pgxpool.Config
	adminDB string

	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

// NewManager parses the base DSN and opens the administrative database's pool,
// validating connectivity with a ping.
func NewManager(ctx context.Context, dsn, adminDB string) (*Manager, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid dsn: %w", err)
	}
	// Keep pgtui's own footprint small so it doesn't dominate the cluster's
	// connection count (a monitoring tool that shows up as 20 backends makes the
	// dashboard's "connections used" misleading). Few connections per database,
	// released quickly once idle, and reclaimed by frequent health checks.
	cfg.MaxConns = 3
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 20 * time.Second
	cfg.HealthCheckPeriod = 15 * time.Second
	cfg.ConnConfig.ConnectTimeout = 8 * time.Second
	// Identifies the origin of connections in pg_stat_activity.
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

// AdminDB returns the name of the administrative database.
func (m *Manager) AdminDB() string { return m.adminDB }

// Pool returns (creating it if necessary) the pool for the given database.
func (m *Manager) Pool(ctx context.Context, dbname string) (*pgxpool.Pool, error) {
	m.mu.Lock()
	if p, ok := m.pools[dbname]; ok {
		m.mu.Unlock()
		return p, nil
	}
	// Clone the base config, swapping only the database.
	cfg := m.baseCfg.Copy()
	cfg.ConnConfig.Database = dbname
	m.mu.Unlock()

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %q: %w", dbname, err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := p.Ping(pingCtx); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping %q: %w", dbname, err)
	}

	m.mu.Lock()
	m.pools[dbname] = p
	m.mu.Unlock()
	return p, nil
}

// Close closes all open pools.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pools {
		p.Close()
	}
	m.pools = map[string]*pgxpool.Pool{}
}
