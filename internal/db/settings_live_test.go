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
