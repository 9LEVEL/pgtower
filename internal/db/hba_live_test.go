package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/9level/pgtui/internal/db"
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

// TestApplyHBAContentLive proves the guarded pg_hba writer end-to-end: it appends
// a valid rule (which must appear), then attempts an INVALID change (which must
// be rejected and rolled back, leaving a parseable file). It restores the
// original file at the end. Gated behind PGTUI_HBA_LIVE_TEST because it rewrites
// pg_hba.conf on the target cluster.
func TestApplyHBAContentLive(t *testing.T) {
	if os.Getenv("PGTUI_HBA_LIVE_TEST") == "" {
		t.Skip("set PGTUI_HBA_LIVE_TEST=1 to run (rewrites pg_hba.conf on the target)")
	}
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
	if !db.HBAWritable(ctx, p) {
		t.Skip("connection cannot write server files (needs superuser)")
	}

	orig, err := db.HBAFileContent(ctx, p)
	if err != nil {
		t.Fatalf("read hba: %v", err)
	}
	// always restore the original file.
	defer func() {
		if err := db.ApplyHBAContent(ctx, mgr, dsn, orig); err != nil {
			t.Errorf("restore original hba: %v", err)
		}
	}()

	parseErrors := func() int {
		rules, _ := db.ListHBARules(ctx, p)
		n := 0
		for _, r := range rules {
			if r.Error != "" {
				n++
			}
		}
		return n
	}

	// 1) a valid append must land and be visible. Use a TEST-NET-1 address
	//    (192.0.2.0/24) that isn't in any default pg_hba.conf, and 'reject' so
	//    it can never affect real logins.
	line := db.BuildHBALine(db.HBARuleInput{Type: "host", Database: "all", User: "all", Address: "192.0.2.10/32", Method: "reject"})
	if err := db.ApplyHBAContent(ctx, mgr, dsn, db.HBAAppendLine(orig, line)); err != nil {
		t.Fatalf("apply valid rule: %v", err)
	}
	if content, _ := db.HBAFileContent(ctx, p); !strings.Contains(content, "192.0.2.10") {
		t.Error("appended rule not written to the file")
	}
	rules, _ := db.ListHBARules(ctx, p)
	found := false
	for _, r := range rules {
		if strings.HasPrefix(r.Address, "192.0.2.10") && r.AuthMethod == "reject" {
			found = true
		}
	}
	if !found {
		t.Error("appended rule not visible in pg_hba_file_rules")
	}

	// 2) an invalid change must be rejected and rolled back (no parse errors left).
	bad := db.HBAAppendLine(orig, "notatype all all 127.0.0.1/32 trust")
	if err := db.ApplyHBAContent(ctx, mgr, dsn, bad); err == nil {
		t.Error("expected an error for an invalid pg_hba.conf")
	}
	if n := parseErrors(); n > 0 {
		t.Errorf("file has %d parse error(s) after rollback — safety net failed", n)
	}
}
