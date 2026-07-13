package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/9level/pg-tui/internal/db"
)

// TestListHBARulesLive reads the parsed pg_hba rules and the file path from a
// live (superuser) cluster.
func TestListHBARulesLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	path, err := db.HBAFilePath(ctx, p)
	if err != nil {
		t.Fatalf("HBAFilePath: %v", err)
	}
	if path == "" {
		t.Error("empty hba_file path")
	}

	rules, err := db.ListHBARules(ctx, p)
	if err != nil {
		t.Fatalf("ListHBARules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected at least one pg_hba rule")
	}
	wellFormed := false
	for _, r := range rules {
		if r.LineNumber > 0 && r.Type != "" && r.AuthMethod != "" && r.Error == "" {
			wellFormed = true
		}
	}
	if !wellFormed {
		t.Errorf("no well-formed rule found in %d rules", len(rules))
	}
}
