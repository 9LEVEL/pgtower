package db

import (
	"context"
	"fmt"
	"strconv"
)

// ConnStateCount is the number of client backends in a given state, plus the
// age (seconds) of the oldest one in that bucket.
type ConnStateCount struct {
	State     string
	Count     int
	OldestSec float64
}

// ConnClientGroup groups client backends by who/what opened them.
type ConnClientGroup struct {
	User  string
	DB    string
	App   string
	State string
	Count int
}

// ConnHeadroom summarizes connection capacity versus current usage. Used counts
// client backends only (excludes autovacuum/background workers).
type ConnHeadroom struct {
	MaxConnections int
	Reserved       int // superuser_reserved_connections (+ reserved_connections, PG16)
	Used           int
	Active         int
	Idle           int
	IdleInTx       int
}

// ConnStateSummary returns the client-backend count per state, busiest first.
func ConnStateSummary(ctx context.Context, p Pinger) ([]ConnStateCount, error) {
	rows, err := p.Query(ctx, `
		select coalesce(state, '(none)') as state,
		       count(*),
		       coalesce(extract(epoch from now() - min(state_change)), 0)
		from pg_stat_activity
		where backend_type = 'client backend'
		group by 1
		order by 2 desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnStateCount
	for rows.Next() {
		var c ConnStateCount
		if err := rows.Scan(&c.State, &c.Count, &c.OldestSec); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConnByClient groups client backends by user, database, application and state,
// most connections first — the breakdown that reveals which app is hogging slots.
func ConnByClient(ctx context.Context, p Pinger) ([]ConnClientGroup, error) {
	rows, err := p.Query(ctx, `
		select coalesce(usename, ''),
		       coalesce(datname, ''),
		       coalesce(nullif(application_name, ''), '(none)'),
		       coalesce(state, '(none)'),
		       count(*)
		from pg_stat_activity
		where backend_type = 'client backend'
		group by 1, 2, 3, 4
		order by 5 desc, 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnClientGroup
	for rows.Next() {
		var g ConnClientGroup
		if err := rows.Scan(&g.User, &g.DB, &g.App, &g.State, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetConnHeadroom reads the connection limits and the current client-backend
// usage in a single round-trip. reserved_connections exists only on PG16+; it
// reads as 0 elsewhere.
func GetConnHeadroom(ctx context.Context, p Pinger) (ConnHeadroom, error) {
	var h ConnHeadroom
	var superReserved, reserved int
	err := p.QueryRow(ctx, `
		select current_setting('max_connections')::int,
		       current_setting('superuser_reserved_connections')::int,
		       coalesce(current_setting('reserved_connections', true)::int, 0),
		       count(*) filter (where backend_type = 'client backend'),
		       count(*) filter (where backend_type = 'client backend' and state = 'active'),
		       count(*) filter (where backend_type = 'client backend' and state = 'idle'),
		       count(*) filter (where backend_type = 'client backend' and state = 'idle in transaction')
		from pg_stat_activity`).
		Scan(&h.MaxConnections, &superReserved, &reserved, &h.Used, &h.Active, &h.Idle, &h.IdleInTx)
	if err != nil {
		return ConnHeadroom{}, err
	}
	// Reserved = total slots kept back from ordinary roles (superuser + PG16
	// reserved_connections).
	h.Reserved = superReserved + reserved
	return h, nil
}

// ConnFinding is one diagnostic about connection usage. Level is
// "info" | "warn" | "crit".
type ConnFinding struct {
	Level string
	Msg   string
}

// AnalyzeConnections turns a headroom snapshot into actionable findings. It is
// pure (no I/O) so its advice is unit-testable and auditable.
func AnalyzeConnections(h ConnHeadroom) []ConnFinding {
	var out []ConnFinding
	if h.MaxConnections <= 0 {
		return out
	}
	ratio := float64(h.Used) / float64(h.MaxConnections)
	switch {
	case ratio >= 0.9:
		out = append(out, ConnFinding{"crit", fmt.Sprintf(
			"%d/%d connections used (%.0f%%) — at the limit", h.Used, h.MaxConnections, ratio*100)})
	case ratio >= 0.75:
		out = append(out, ConnFinding{"warn", fmt.Sprintf(
			"%d/%d connections used (%.0f%%)", h.Used, h.MaxConnections, ratio*100)})
	}
	// Idle-dominated near capacity: the classic "raise max_connections?" trap —
	// the answer is usually a pooler, not more slots.
	if h.Used >= 10 && h.Idle*2 >= h.Used && ratio >= 0.5 {
		out = append(out, ConnFinding{"warn", fmt.Sprintf(
			"%d of %d connections are idle — use a pooler (PgBouncer) or lower the app pool instead of raising max_connections",
			h.Idle, h.Used)})
	}
	if h.IdleInTx > 0 {
		out = append(out, ConnFinding{"warn", fmt.Sprintf(
			"%d connection(s) idle in transaction — holding locks/snapshots; set idle_in_transaction_session_timeout and check for missing commits",
			h.IdleInTx)})
	}
	// No headroom left after the reserved slots: admin access is at risk.
	if usable := h.MaxConnections - h.Reserved; usable > 0 && h.Used >= usable {
		out = append(out, ConnFinding{"crit",
			"no headroom beyond reserved slots — raise superuser_reserved_connections or free connections to keep admin access"})
	}
	if len(out) == 0 {
		out = append(out, ConnFinding{"info", fmt.Sprintf(
			"%d/%d connections used — healthy headroom", h.Used, h.MaxConnections)})
	}
	return out
}

// BuildRoleConnLimit builds ALTER ROLE ... CONNECTION LIMIT. -1 means unlimited.
func BuildRoleConnLimit(role string, limit int) string {
	return "ALTER ROLE " + QuoteIdent(role) + " CONNECTION LIMIT " + strconv.Itoa(limit)
}

// BuildDatabaseConnLimit builds ALTER DATABASE ... CONNECTION LIMIT. -1 = unlimited.
func BuildDatabaseConnLimit(dbname string, limit int) string {
	return "ALTER DATABASE " + QuoteIdent(dbname) + " CONNECTION LIMIT " + strconv.Itoa(limit)
}
