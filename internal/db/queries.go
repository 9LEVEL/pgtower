package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

// DashboardData aggregates cluster health metrics.
type DashboardData struct {
	Version       string
	StartedAt     time.Time
	Uptime        time.Duration
	MaxConns      int
	Reserved      int // superuser_reserved_connections (+ reserved_connections, PG16)
	TotalConns    int
	Active        int
	Idle          int
	IdleInTx      int
	CacheHitRatio float64 // percentage
	Commits       int64
	Rollbacks     int64
	DBCount       int
	TotalSize     string
	LongestQuery  time.Duration
	Replicas      []Replica
}

// Replica describes a standby connected via streaming replication.
type Replica struct {
	ClientAddr string
	State      string
	SyncState  string
	Lag        string
}

// LoadDashboard collects the cluster metrics from the admin database.
func LoadDashboard(ctx context.Context, p Pinger) (DashboardData, error) {
	var d DashboardData

	row := p.QueryRow(ctx, `
		select
			version(),
			pg_postmaster_start_time(),
			(select setting::int from pg_settings where name = 'max_connections'),
			(select count(*) from pg_stat_activity),
			(select count(*) from pg_stat_activity where state = 'active'),
			(select count(*) from pg_stat_activity where state = 'idle'),
			(select count(*) from pg_stat_activity where state = 'idle in transaction'),
			coalesce(round(sum(blks_hit) * 100.0 / nullif(sum(blks_hit) + sum(blks_read), 0), 2), 0)::float8,
			coalesce(sum(xact_commit), 0),
			coalesce(sum(xact_rollback), 0),
			(select count(*) from pg_database where not datistemplate),
			(select pg_size_pretty(coalesce(sum(pg_database_size(datname)), 0)) from pg_database),
			coalesce((select extract(epoch from max(now() - query_start))
			          from pg_stat_activity where state = 'active'), 0)::float8,
			(select setting::int from pg_settings where name = 'superuser_reserved_connections')
			  + coalesce((select setting::int from pg_settings where name = 'reserved_connections'), 0)
		from pg_stat_database`)

	var longestSecs float64
	if err := row.Scan(
		&d.Version, &d.StartedAt, &d.MaxConns, &d.TotalConns,
		&d.Active, &d.Idle, &d.IdleInTx, &d.CacheHitRatio,
		&d.Commits, &d.Rollbacks, &d.DBCount, &d.TotalSize, &longestSecs, &d.Reserved,
	); err != nil {
		return d, err
	}
	d.Uptime = time.Since(d.StartedAt).Truncate(time.Second)
	d.LongestQuery = time.Duration(longestSecs) * time.Second
	d.Version = shortVersion(d.Version)

	rows, err := p.Query(ctx, `
		select coalesce(host(client_addr), 'local'), state, sync_state,
		       coalesce(pg_size_pretty(pg_wal_lsn_diff(sent_lsn, replay_lsn)), '0 bytes')
		from pg_stat_replication
		order by 1`)
	if err == nil {
		for rows.Next() {
			var r Replica
			if err := rows.Scan(&r.ClientAddr, &r.State, &r.SyncState, &r.Lag); err == nil {
				d.Replicas = append(d.Replicas, r)
			}
		}
		rows.Close()
	}
	return d, nil
}

func shortVersion(v string) string {
	// "PostgreSQL 18.4 (Ubuntu ...)" -> "PostgreSQL 18.4"
	if i := strings.Index(v, " ("); i > 0 {
		return v[:i]
	}
	if len(v) > 40 {
		return v[:40]
	}
	return v
}

// ---------------------------------------------------------------------------
// Databases & tables
// ---------------------------------------------------------------------------

// Database is a row in the database list.
type Database struct {
	Name        string
	Owner       string
	SizeBytes   int64
	SizePretty  string
	Connections int
}

// ListDatabases returns all non-template databases ordered by size.
func ListDatabases(ctx context.Context, p Pinger) ([]Database, error) {
	rows, err := p.Query(ctx, `
		select d.datname,
		       pg_catalog.pg_get_userbyid(d.datdba) as owner,
		       pg_database_size(d.datname) as size,
		       pg_size_pretty(pg_database_size(d.datname)) as size_pretty,
		       (select count(*) from pg_stat_activity a where a.datname = d.datname) as conns
		from pg_database d
		where not d.datistemplate
		order by size desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Database
	for rows.Next() {
		var db Database
		if err := rows.Scan(&db.Name, &db.Owner, &db.SizeBytes, &db.SizePretty, &db.Connections); err != nil {
			return nil, err
		}
		out = append(out, db)
	}
	return out, rows.Err()
}

// Table is a row in a database's table list.
type Table struct {
	Schema     string
	Name       string
	TotalBytes int64
	TotalSize  string
	TableSize  string
	IndexSize  string
	EstRows    int64
}

// ListTables lists tables (ordinary and partitioned) from user schemas,
// ordered by total size (heap + indexes + toast).
func ListTables(ctx context.Context, p Pinger) ([]Table, error) {
	rows, err := p.Query(ctx, `
		select n.nspname as schema,
		       c.relname as name,
		       pg_total_relation_size(c.oid) as total_bytes,
		       pg_size_pretty(pg_total_relation_size(c.oid)) as total,
		       pg_size_pretty(pg_relation_size(c.oid)) as heap,
		       pg_size_pretty(pg_indexes_size(c.oid)) as idx,
		       c.reltuples::bigint as est_rows
		from pg_class c
		join pg_namespace n on n.oid = c.relnamespace
		where c.relkind in ('r','p')
		  and n.nspname not in ('pg_catalog','information_schema')
		  and n.nspname not like 'pg_temp%'
		order by total_bytes desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name, &t.TotalBytes, &t.TotalSize, &t.TableSize, &t.IndexSize, &t.EstRows); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Locks / blocking
// ---------------------------------------------------------------------------

// BlockPair describes a blocked session and the session blocking it.
type BlockPair struct {
	BlockedPID   int32
	BlockedUser  string
	BlockedDB    string
	BlockedFor   string
	BlockedQuery string
	BlockingPID  int32
	BlockingUser string
	BlockingSt   string
	BlockingQ    string
}

// ListBlocks returns the current blocking tree (who waits on whom).
func ListBlocks(ctx context.Context, p Pinger) ([]BlockPair, error) {
	rows, err := p.Query(ctx, `
		select blocked.pid,
		       coalesce(blocked.usename, ''),
		       coalesce(blocked.datname, ''),
		       coalesce(to_char(now() - blocked.query_start, 'HH24:MI:SS'), ''),
		       coalesce(left(regexp_replace(blocked.query, '\s+', ' ', 'g'), 90), ''),
		       blocking.pid,
		       coalesce(blocking.usename, ''),
		       coalesce(blocking.state, ''),
		       coalesce(left(regexp_replace(blocking.query, '\s+', ' ', 'g'), 90), '')
		from pg_stat_activity blocked
		join lateral unnest(pg_blocking_pids(blocked.pid)) as bp(pid) on true
		join pg_stat_activity blocking on blocking.pid = bp.pid
		order by blocked.query_start`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BlockPair
	for rows.Next() {
		var b BlockPair
		if err := rows.Scan(
			&b.BlockedPID, &b.BlockedUser, &b.BlockedDB, &b.BlockedFor, &b.BlockedQuery,
			&b.BlockingPID, &b.BlockingUser, &b.BlockingSt, &b.BlockingQ,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Query runner
// ---------------------------------------------------------------------------

// QueryResult carries the result of an ad-hoc query.
type QueryResult struct {
	Columns   []string
	Rows      [][]string
	Command   string // command tag (e.g. "UPDATE 3") for statements without a result
	RowCount  int    // rows returned (SELECT) or affected
	Elapsed   time.Duration
	Truncated bool
}

// maxResultRows limits the material brought to the UI for a single query.
const maxResultRows = 1000

// RunQuery runs arbitrary SQL on the given pool and returns the (limited) rows
// or the command tag. It uses a statement timeout to avoid hanging.
func RunQuery(ctx context.Context, p Pinger, sql string) (QueryResult, error) {
	start := time.Now()
	var res QueryResult

	rows, err := p.Query(ctx, sql)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	for _, f := range fields {
		res.Columns = append(res.Columns, string(f.Name))
	}

	for rows.Next() {
		if len(res.Rows) >= maxResultRows {
			res.Truncated = true
			break
		}
		vals, err := rows.Values()
		if err != nil {
			return res, err
		}
		res.Rows = append(res.Rows, formatRow(vals))
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	tag := rows.CommandTag()
	res.Elapsed = time.Since(start)

	if len(res.Columns) == 0 {
		// Statement without a result set (INSERT/UPDATE/DDL...).
		res.Command = tag.String()
		res.RowCount = int(tag.RowsAffected())
	} else {
		res.RowCount = len(res.Rows)
	}
	return res, nil
}

func formatRow(vals []any) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = formatValue(v)
	}
	return out
}

func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "∅" // NULL
	case []byte:
		return "\\x" + hexPreview(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	case string:
		return collapse(t)
	default:
		return collapse(fmt.Sprint(v))
	}
}

// collapse normalizes whitespace to fit in a table cell.
func collapse(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

func hexPreview(b []byte) string {
	const max = 16
	if len(b) > max {
		b = b[:max]
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}

// Pinger abstracts *pgxpool.Pool to ease testing and reuse.
type Pinger interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
