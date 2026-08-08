package db_test

import (
	"strings"
	"testing"

	"github.com/9level/pgtui/internal/db"
)

func TestBuildConnLimit(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{db.BuildRoleConnLimit("app", 30), `ALTER ROLE "app" CONNECTION LIMIT 30`},
		{db.BuildRoleConnLimit(`we"ird`, -1), `ALTER ROLE "we""ird" CONNECTION LIMIT -1`},
		{db.BuildDatabaseConnLimit("prod", 40), `ALTER DATABASE "prod" CONNECTION LIMIT 40`},
		{db.BuildDatabaseConnLimit("prod", 0), `ALTER DATABASE "prod" CONNECTION LIMIT 0`},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestAnalyzeConnections(t *testing.T) {
	find := func(fs []db.ConnFinding, level, substr string) bool {
		for _, f := range fs {
			if f.Level == level && strings.Contains(f.Msg, substr) {
				return true
			}
		}
		return false
	}

	// The user's real case: 47/50, mostly idle, a couple idle-in-transaction.
	h := db.ConnHeadroom{MaxConnections: 50, Reserved: 3, Used: 47, Active: 5, Idle: 40, IdleInTx: 2}
	fs := db.AnalyzeConnections(h)
	if !find(fs, "crit", "at the limit") {
		t.Errorf("expected a crit near-limit finding, got %+v", fs)
	}
	if !find(fs, "warn", "idle") || !find(fs, "warn", "PgBouncer") {
		t.Errorf("expected an idle-dominated/pooler finding, got %+v", fs)
	}
	if !find(fs, "warn", "idle in transaction") {
		t.Errorf("expected an idle-in-transaction finding, got %+v", fs)
	}
	if !find(fs, "crit", "headroom beyond reserved") {
		t.Errorf("expected a no-admin-headroom finding (47 >= 50-3), got %+v", fs)
	}

	// Healthy cluster: only an info line.
	fs = db.AnalyzeConnections(db.ConnHeadroom{MaxConnections: 100, Reserved: 3, Used: 12, Active: 3, Idle: 9})
	if len(fs) != 1 || fs[0].Level != "info" {
		t.Errorf("healthy cluster should yield a single info finding, got %+v", fs)
	}

	// Idle but well under capacity: no pooler nag (ratio < 0.5).
	fs = db.AnalyzeConnections(db.ConnHeadroom{MaxConnections: 100, Reserved: 3, Used: 20, Idle: 18})
	if find(fs, "warn", "PgBouncer") {
		t.Errorf("should not nag about pooling under 50%% usage, got %+v", fs)
	}

	// Unknown max: no findings (avoids divide-by-zero noise).
	if fs := db.AnalyzeConnections(db.ConnHeadroom{}); len(fs) != 0 {
		t.Errorf("zero max_connections should yield no findings, got %+v", fs)
	}
}
