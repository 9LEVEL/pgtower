package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/9level/pg-tui/internal/db"
)

// TestSmoke exercita todas as queries contra um Postgres real. Só roda quando
// DATABASE_URL está definido (ex.: exportando do .env). É read-only.
func TestSmoke(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL não definido; pulando smoke test de integração")
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
		t.Fatal("ListDatabases retornou vazio")
	}
	t.Logf("databases: %d", len(dbs))
	for _, d := range dbs {
		t.Logf("  %-24s %-14s %10s  conns=%d", d.Name, d.Owner, d.SizePretty, d.Connections)
	}

	tbls, err := db.ListTables(ctx, p)
	if err != nil {
		t.Fatalf("ListTables(postgres): %v", err)
	}
	t.Logf("tabelas em 'postgres': %d", len(tbls))

	blocks, err := db.ListBlocks(ctx, p)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	t.Logf("bloqueios ativos: %d", len(blocks))

	res, err := db.RunQuery(ctx, p, "select datname, numbackends from pg_stat_database order by numbackends desc limit 5")
	if err != nil {
		t.Fatalf("RunQuery(select): %v", err)
	}
	if len(res.Columns) != 2 {
		t.Fatalf("esperava 2 colunas, veio %d", len(res.Columns))
	}
	t.Logf("query runner: %d colunas, %d linhas, %s", len(res.Columns), res.RowCount, res.Elapsed)

	// Classificador de segurança.
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
			t.Errorf("Classify(%q) = %v, quero %v", sql, got, want)
		}
	}
}
