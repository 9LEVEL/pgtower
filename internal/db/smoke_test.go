package db_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/9level/pgtower/internal/db"
)

// TestSmoke exercises all the queries against a real Postgres. It only runs
// when DATABASE_URL is set (exported into the environment). It is read-only.
func TestSmoke(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mgr, err := db.NewManager(ctx, dsn, "postgres")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	p, err := mgr.Pool(ctx, "postgres")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}

	dash, err := db.LoadDashboard(ctx, p)
	if err != nil {
		t.Fatalf("LoadDashboard: %v", err)
	}
	t.Logf("dashboard: %s · conns %d/%d · cache %.2f%% · dbs %d · size %s · uptime %s · replicas %d",
		dash.Version, dash.TotalConns, dash.MaxConns, dash.CacheHitRatio,
		dash.DBCount, dash.TotalSize, dash.Uptime, len(dash.Replicas))

	dbs, err := db.ListDatabases(ctx, p)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) == 0 {
		t.Fatal("ListDatabases returned empty")
	}
	t.Logf("databases: %d", len(dbs))
	for _, d := range dbs {
		t.Logf("  %-24s %-14s %10s  conns=%d", d.Name, d.Owner, d.SizePretty, d.Connections)
	}

	tbls, err := db.ListTables(ctx, p)
	if err != nil {
		t.Fatalf("ListTables(postgres): %v", err)
	}
	t.Logf("tables in 'postgres': %d", len(tbls))

	blocks, err := db.ListBlocks(ctx, p)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	t.Logf("active blocks: %d", len(blocks))

	res, err := db.RunQuery(ctx, p, "select datname, numbackends from pg_stat_database order by numbackends desc limit 5")
	if err != nil {
		t.Fatalf("RunQuery(select): %v", err)
	}
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(res.Columns))
	}
	t.Logf("query runner: %d columns, %d rows, %s", len(res.Columns), res.RowCount, res.Elapsed)

	// Raw keeps what the one-line grid cell drops (it is what gets copied).
	res, err = db.RunQuery(ctx, p, `select E'a\nb' as t, null as n, '\x000102'::bytea as b`)
	if err != nil {
		t.Fatalf("RunQuery(raw): %v", err)
	}
	if got, want := res.Rows[0], []string{"a b", "∅", `\x000102`}; !slices.Equal(got, want) {
		t.Errorf("display row = %q, want %q", got, want)
	}
	if got, want := res.Raw[0], []string{"a\nb", "", `\x000102`}; !slices.Equal(got, want) {
		t.Errorf("raw row = %q, want %q", got, want)
	}

	// Safety classifier.
	cases := map[string]db.Danger{
		"select 1":                             db.Safe,
		"  SELECT * from users where id=1":     db.Safe,
		"update t set x=1 where id=2":          db.Write,
		"update t set x=1":                     db.Critical,
		"delete from t":                        db.Critical,
		"delete from t where id=1":             db.Write,
		"drop table foo":                       db.Critical,
		"DROP DATABASE prod":                   db.Critical,
		"truncate t":                           db.Critical,
		"insert into t values (1)":             db.Write,
		"alter table t add column c int":       db.Write,
		"with x as (select 1) select * from x": db.Safe,
	}
	for sql, want := range cases {
		if got := db.Classify(sql); got != want {
			t.Errorf("Classify(%q) = %v, want %v", sql, got, want)
		}
	}
}
