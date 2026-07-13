package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/9level/pg-tui/internal/db"
)

// TestReadTuningSettingsLive reads the advisor GUCs from a live cluster, parses
// them and produces recommendations end-to-end.
func TestReadTuningSettingsLive(t *testing.T) {
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

	m, err := db.ReadTuningSettings(ctx, p)
	if err != nil {
		t.Fatalf("ReadTuningSettings: %v", err)
	}
	for _, name := range []string{
		"shared_buffers", "effective_cache_size", "work_mem",
		"maintenance_work_mem", "max_connections",
	} {
		if _, ok := m[name]; !ok {
			t.Errorf("missing %q from pg_settings read", name)
		}
	}

	in := db.BuildTuningInput(m, 8<<30, 4)
	if in.SharedBuffers <= 0 || in.MaxConnections <= 0 {
		t.Fatalf("settings did not parse into bytes: %+v", in)
	}
	if recs := db.Recommend(in); len(recs) != 5 {
		t.Errorf("Recommend returned %d rows, want 5", len(recs))
	}
}

// TestAlterSystemRoundtripLive changes a reloadable GUC via ALTER SYSTEM, reloads
// and confirms it took effect, then RESETs it back to the original value — a
// fully reversible, self-cleaning round-trip.
func TestAlterSystemRoundtripLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
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

	all, err := db.ListAllSettings(ctx, p)
	if err != nil {
		t.Fatalf("ListAllSettings: %v", err)
	}
	if len(all) < 50 {
		t.Fatalf("pg_settings returned only %d rows", len(all))
	}
	var wm db.Setting
	for _, s := range all {
		if s.Name == "work_mem" {
			wm = s
		}
	}
	if wm.Name == "" || wm.VarType != "integer" {
		t.Fatalf("work_mem not found or unexpected: %+v", wm)
	}

	current := func() string {
		var v string
		if err := p.QueryRow(ctx, "select current_setting('work_mem')").Scan(&v); err != nil {
			t.Fatalf("current_setting: %v", err)
		}
		return v
	}
	// SIGHUP (pg_reload_conf) propagates asynchronously, so poll until the
	// backend picks up the new value.
	waitFor := func(want string) bool {
		for i := 0; i < 40; i++ {
			if current() == want {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}
	orig := current()

	// always leave the cluster as we found it.
	defer func() {
		_, _ = db.ExecAdmin(ctx, p, db.BuildAlterSystemReset("work_mem"))
		_ = db.ReloadConf(ctx, p)
	}()

	if _, err := db.ExecAdmin(ctx, p, db.BuildAlterSystemSet("work_mem", "8MB")); err != nil {
		t.Fatalf("ALTER SYSTEM SET: %v", err)
	}
	if err := db.ReloadConf(ctx, p); err != nil {
		t.Fatalf("ReloadConf: %v", err)
	}
	if !waitFor("8MB") {
		t.Errorf("after set+reload, work_mem = %q, want 8MB", current())
	}

	if _, err := db.ExecAdmin(ctx, p, db.BuildAlterSystemReset("work_mem")); err != nil {
		t.Fatalf("ALTER SYSTEM RESET: %v", err)
	}
	if err := db.ReloadConf(ctx, p); err != nil {
		t.Fatalf("ReloadConf: %v", err)
	}
	if !waitFor(orig) {
		t.Errorf("after reset+reload, work_mem = %q, want original %q", current(), orig)
	}
}
