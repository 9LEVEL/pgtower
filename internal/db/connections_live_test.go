package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/9level/pg-tui/internal/db"
)

// TestConnLimitAndDiagnosticsLive proves the CONNECTION LIMIT builders apply and
// read back correctly (role and database), and that the diagnostic readers
// return coherent data against a live cluster.
func TestConnLimitAndDiagnosticsLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	const role = "pgtui_connlimit_role"
	const database = "pgtui_connlimit_db"
	_, _ = db.ExecAdmin(ctx, p, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
	_, _ = db.ExecAdmin(ctx, p, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	defer func() {
		_, _ = db.ExecAdmin(ctx, p, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
		_, _ = db.ExecAdmin(ctx, p, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	}()

	// --- role CONNECTION LIMIT: set -> read back -> reset ---
	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateRole(role, "", true, false, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	roleLimit := func() int {
		var n int
		if err := p.QueryRow(ctx, "select rolconnlimit from pg_roles where rolname=$1", role).Scan(&n); err != nil {
			t.Fatalf("read rolconnlimit: %v", err)
		}
		return n
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildRoleConnLimit(role, 30)); err != nil {
		t.Fatalf("set role limit 30: %v", err)
	}
	if got := roleLimit(); got != 30 {
		t.Errorf("rolconnlimit = %d, want 30", got)
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildRoleConnLimit(role, -1)); err != nil {
		t.Fatalf("reset role limit: %v", err)
	}
	if got := roleLimit(); got != -1 {
		t.Errorf("rolconnlimit after reset = %d, want -1 (unlimited)", got)
	}

	// --- database CONNECTION LIMIT: set -> read back ---
	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateDatabase(database, "")); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildDatabaseConnLimit(database, 40)); err != nil {
		t.Fatalf("set db limit 40: %v", err)
	}
	var dbLimit int
	if err := p.QueryRow(ctx, "select datconnlimit from pg_database where datname=$1", database).Scan(&dbLimit); err != nil {
		t.Fatalf("read datconnlimit: %v", err)
	}
	if dbLimit != 40 {
		t.Errorf("datconnlimit = %d, want 40", dbLimit)
	}

	// --- diagnostic readers return coherent data ---
	h, err := db.GetConnHeadroom(ctx, p)
	if err != nil {
		t.Fatalf("GetConnHeadroom: %v", err)
	}
	if h.MaxConnections <= 0 || h.Used < 1 || h.Reserved < 0 {
		t.Errorf("headroom looks wrong: %+v", h)
	}
	if states, err := db.ConnStateSummary(ctx, p); err != nil {
		t.Fatalf("ConnStateSummary: %v", err)
	} else if len(states) == 0 {
		t.Error("expected at least one connection-state bucket")
	}
	if groups, err := db.ConnByClient(ctx, p); err != nil {
		t.Fatalf("ConnByClient: %v", err)
	} else if len(groups) == 0 {
		t.Error("expected at least one client group")
	}
}
