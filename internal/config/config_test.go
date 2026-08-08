package config

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateEnv clears the variables that could otherwise leak a real connection
// into the test, so Load() resolves purely from the config.yml we write.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "PGHOST", "PGPORT", "PGUSER", "PGPASSWORD",
		"PGDATABASE", "PGSSLMODE", "PGTUI_UPDATE_CHECK", "PGTUI_REFRESH_SECONDS",
		"PGTUI_HOST_RAM_MB", "PGTUI_HOST_CPUS", "PGTUI_SCRAM_ITERATIONS", "PGTUI_CONFIG", "PGTUI_ENV_FILE"} {
		t.Setenv(k, "")
	}
}

func TestLoadFromConfigYMLDSN(t *testing.T) {
	dir := t.TempDir()
	yml := "" +
		"database_url: postgres://alice:secret@10.0.0.5:6543/appdb?sslmode=require\n" +
		"refresh_seconds: 9\n" +
		"host_ram_mb: 4096\n" +
		"host_cpus: 8\n" +
		"scram_iterations: 20000\n" +
		"update_check: false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	isolateEnv(t)
	t.Setenv("PGTUI_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "10.0.0.5" || cfg.Port != "6543" || cfg.User != "alice" || cfg.AdminDB != "appdb" {
		t.Errorf("DSN parsed wrong: host=%s port=%s user=%s db=%s", cfg.Host, cfg.Port, cfg.User, cfg.AdminDB)
	}
	if cfg.RefreshSeconds != 9 || cfg.HostRAMMB != 4096 || cfg.HostCPUs != 8 || cfg.SCRAMIterations != 20000 {
		t.Errorf("scalars parsed wrong: %+v", cfg)
	}
	if cfg.UpdateCheck {
		t.Error("update_check: false in config.yml should disable the update check")
	}
}

func TestLoadFromConfigYMLParts(t *testing.T) {
	dir := t.TempDir()
	yml := "host: db.internal\nport: 5555\nuser: svc\npassword: pw\ndatabase: core\nsslmode: require\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	isolateEnv(t)
	t.Setenv("PGTUI_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "db.internal" || cfg.Port != "5555" || cfg.User != "svc" || cfg.AdminDB != "core" {
		t.Errorf("parts→DSN wrong: %+v", cfg)
	}
	// default when unset in the file
	if !cfg.UpdateCheck {
		t.Error("update_check should default to true when omitted")
	}
}

func TestEnvOverridesConfigYML(t *testing.T) {
	dir := t.TempDir()
	yml := "database_url: postgres://file@10.0.0.5:5432/filedb?sslmode=disable\nrefresh_seconds: 9\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	isolateEnv(t)
	t.Setenv("PGTUI_CONFIG_DIR", dir)
	// the real environment must win over the file
	t.Setenv("DATABASE_URL", "postgres://envuser@192.0.2.9:5432/envdb?sslmode=disable")
	t.Setenv("PGTUI_REFRESH_SECONDS", "3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "192.0.2.9" || cfg.AdminDB != "envdb" {
		t.Errorf("env DATABASE_URL should override config.yml, got host=%s db=%s", cfg.Host, cfg.AdminDB)
	}
	if cfg.RefreshSeconds != 3 {
		t.Errorf("env PGTUI_REFRESH_SECONDS should override config.yml, got %d", cfg.RefreshSeconds)
	}
}

func TestLoadNoConfigErrors(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PGTUI_CONFIG_DIR", t.TempDir()) // empty dir, no config.yml
	if _, err := Load(); err == nil {
		t.Error("Load() with no DSN anywhere should return an error")
	}
}
