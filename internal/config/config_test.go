package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateEnv clears the variables that could otherwise leak a real connection
// into the test, and pins the config search to a fresh temp dir (which the
// search then treats as its only location). Returns that dir.
func isolateEnv(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "PGHOST", "PGPORT", "PGUSER", "PGPASSWORD",
		"PGDATABASE", "PGSSLMODE"} {
		t.Setenv(k, "")
	}
	// Both spellings: PGTUI_* is still read as a fallback.
	for _, k := range []string{"UPDATE_CHECK", "REFRESH_SECONDS", "HOST_RAM_MB", "HOST_CPUS",
		"SCRAM_ITERATIONS", "CONFIG", "CONFIG_DIR"} {
		t.Setenv(envPrefix+k, "")
		t.Setenv(legacyEnvPrefix+k, "")
	}
	dir := t.TempDir()
	t.Setenv("PGTOWER_CONFIG_DIR", dir)
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustLoad(t *testing.T) *Store {
	t.Helper()
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func mustResolveInitial(t *testing.T, s *Store) *Config {
	t.Helper()
	c, ok, err := s.Initial("")
	if err != nil || !ok {
		t.Fatalf("Initial: ok=%v err=%v", ok, err)
	}
	cfg, err := s.Resolve(c, "v0.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return cfg
}

func TestLegacyDSNIsMigrated(t *testing.T) {
	dir := isolateEnv(t)
	legacy := "" +
		"# old comments\n" +
		"database_url: postgres://alice:secret@10.0.0.5:6543/appdb?sslmode=require\n" +
		"refresh_seconds: 9\n" +
		"host_ram_mb: 4096\n" +
		"host_cpus: 8\n" +
		"scram_iterations: 20000\n" +
		"update_check: false\n"
	path := filepath.Join(dir, "config.yml")
	writeFile(t, path, legacy)

	s := mustLoad(t)
	if s.Migration == nil || s.Migration.Err != nil {
		t.Fatalf("a v1 file should be migrated, got %+v", s.Migration)
	}
	cfg := mustResolveInitial(t, s)
	if cfg.Name != "10.0.0.5" || cfg.Host != "10.0.0.5" || cfg.Port != "6543" || cfg.User != "alice" || cfg.AdminDB != "appdb" {
		t.Errorf("DSN parsed wrong: %+v", cfg)
	}
	if cfg.RefreshSeconds != 9 || cfg.HostRAMMB != 4096 || cfg.HostCPUs != 8 || cfg.SCRAMIterations != 20000 {
		t.Errorf("settings lost in migration: %+v", cfg)
	}
	if cfg.UpdateCheck {
		t.Error("update_check: false must survive the migration")
	}

	// The original is backed up verbatim, the new file is v2 and private.
	if got := readFile(t, path+".v1.bak"); got != legacy {
		t.Errorf("backup should hold the original file, got %q", got)
	}
	now := readFile(t, path)
	for _, want := range []string{"version: 2", "default: 10.0.0.5", "name: 10.0.0.5", "refresh_seconds: 9"} {
		if !strings.Contains(now, want) {
			t.Errorf("migrated config.yml missing %q:\n%s", want, now)
		}
	}
	if strings.Contains(now, "database_url") {
		t.Error("legacy key database_url must not remain in the migrated file")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("config.yml may hold passwords and must be 0600, got %v", fi.Mode().Perm())
	}

	// Idempotent: the second run neither migrates nor backs up again.
	s2 := mustLoad(t)
	if s2.Migration != nil {
		t.Error("a v2 file must not be migrated again")
	}
	if _, err := os.Stat(path + ".v1.bak.1"); err == nil {
		t.Error("a second run must not create another backup")
	}
}

func TestLegacyPartsAreMigrated(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"),
		"host: db.internal\nport: 5555\nuser: svc\npassword: pw\ndatabase: core\nsslmode: require\n")

	cfg := mustResolveInitial(t, mustLoad(t))
	if cfg.Host != "db.internal" || cfg.Port != "5555" || cfg.User != "svc" || cfg.AdminDB != "core" {
		t.Errorf("parts→DSN wrong: %+v", cfg)
	}
	if !strings.Contains(cfg.URL, "sslmode=require") || !strings.Contains(cfg.URL, "svc:pw@") {
		t.Errorf("DSN should carry sslmode and credentials: %s", cfg.URL)
	}
	if !cfg.UpdateCheck {
		t.Error("update_check should default to true when omitted")
	}
}

// A pre-v0.8 install: the installer's commented v1 template plus the real
// connection in a .env that v0.8 silently stopped reading.
func TestLegacyTemplatePlusDotenv(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"), "# database_url: \"postgres://...\"\n# refresh_seconds: 5\n")
	writeFile(t, filepath.Join(dir, ".env"),
		"# comment\nDATABASE_URL='postgres://bob:pw@db.example:5432/postgres?sslmode=disable'\nexport PGTUI_REFRESH_SECONDS=7\n")

	s := mustLoad(t)
	cfg := mustResolveInitial(t, s)
	if cfg.Host != "db.example" || cfg.User != "bob" || cfg.RefreshSeconds != 7 {
		t.Errorf("the .env connection should be imported: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error("the obsolete .env must be retired after import")
	}
	if got := readFile(t, filepath.Join(dir, ".env.v1.bak")); !strings.Contains(got, "bob") {
		t.Error("the .env must be kept as .env.v1.bak")
	}
	if len(s.Migration.Backups) != 2 {
		t.Errorf("both originals should be reported as backed up: %v", s.Migration.Backups)
	}
}

func TestDotenvOnlyInstallIsImported(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://carol@10.1.1.1:5432/postgres\n")

	s := mustLoad(t)
	if s.Migration == nil || s.Path != filepath.Join(dir, "config.yml") {
		t.Fatalf("a .env-only install should produce config.yml, got path=%q mig=%+v", s.Path, s.Migration)
	}
	if cfg := mustResolveInitial(t, s); cfg.User != "carol" {
		t.Errorf("imported wrong connection: %+v", cfg)
	}
	if !strings.Contains(readFile(t, s.Path), "version: 2") {
		t.Error("config.yml should be written in format 2")
	}
}

func TestV2FileLeavesDotenvAlone(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"),
		"version: 2\nconnections:\n  - name: a\n    host: 10.0.0.1\n")
	writeFile(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://x@10.9.9.9/postgres\n")

	s := mustLoad(t)
	if s.Migration != nil || len(s.Connections) != 1 || s.Connections[0].Name != "a" {
		t.Errorf("a v2 config must be used as-is: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Error("with a v2 config, an unrelated .env must not be touched")
	}
}

func TestEnvOverridesConfig(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"),
		"version: 2\ndefault: file\nrefresh_seconds: 9\nconnections:\n  - name: file\n    url: postgres://file@10.0.0.5:5432/filedb\n")
	t.Setenv("DATABASE_URL", "postgres://envuser@192.0.2.9:5432/envdb?sslmode=disable")
	t.Setenv("PGTOWER_REFRESH_SECONDS", "3")

	s := mustLoad(t)
	cfg := mustResolveInitial(t, s)
	if cfg.Name != EnvConnectionName || cfg.Host != "192.0.2.9" || cfg.AdminDB != "envdb" {
		t.Errorf("env DATABASE_URL should win at startup, got %+v", cfg)
	}
	if cfg.RefreshSeconds != 3 {
		t.Errorf("env PGTOWER_REFRESH_SECONDS should override config.yml, got %d", cfg.RefreshSeconds)
	}
	// The env connection is session-only: saving must not persist it.
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, s.Path), "envuser") {
		t.Error("the DATABASE_URL connection must never be written to config.yml")
	}
}

func TestNoConfigIsNotAnError(t *testing.T) {
	isolateEnv(t)
	s := mustLoad(t)
	if _, ok, err := s.Initial(""); ok || err != nil {
		t.Errorf("with nothing configured the UI should ask for a connection: ok=%v err=%v", ok, err)
	}
}

func TestInitialSelection(t *testing.T) {
	s := &Store{Connections: []Connection{{Name: "a", Host: "h1"}, {Name: "b", Host: "h2"}}}
	if _, ok, _ := s.Initial(""); ok {
		t.Error("two connections and no default: the user must choose")
	}
	s.Default = "b"
	if c, ok, _ := s.Initial(""); !ok || c.Name != "b" {
		t.Errorf("default should be picked, got %q", c.Name)
	}
	if c, _, _ := s.Initial("a"); c.Name != "a" {
		t.Error("an explicit -s name should win over the default")
	}
	if _, _, err := s.Initial("nope"); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("unknown -s name should list the known ones, got %v", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	isolateEnv(t)
	s := mustLoad(t)
	if err := s.Upsert("", Connection{Name: "local", Host: "/var/run/postgresql", User: "root", Tag: TagDev}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("", Connection{Name: "prod", Host: "10.0.0.5", Password: "pw", SSLMode: "require", Tag: TagProd}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("", Connection{Name: "prod", Host: "x"}); err == nil {
		t.Error("duplicate names must be rejected")
	}
	s.Default = "prod"
	if err := s.Upsert("prod", Connection{Name: "production", Host: "10.0.0.5", Password: "pw", Tag: TagProd}); err != nil {
		t.Fatal(err)
	}
	if s.Default != "production" {
		t.Error("renaming the default connection must keep it the default")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2 := mustLoad(t)
	if len(s2.Connections) != 2 || s2.Default != "production" {
		t.Fatalf("round trip lost data: %+v", s2)
	}
	cfg, err := s2.Resolve(s2.Connections[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "/var/run/postgresql" || !strings.Contains(cfg.URL, "host=%2Fvar%2Frun%2Fpostgresql") {
		t.Errorf("unix-socket host should round-trip: host=%s url=%s", cfg.Host, cfg.URL)
	}
	s2.Remove("production")
	if s2.Default != "" || len(s2.Connections) != 1 {
		t.Error("removing the default connection must clear the default")
	}
}

func TestPasswordEnv(t *testing.T) {
	t.Setenv("PG_TEST_PASS", "s3cr3t")
	dsn, err := Connection{Name: "x", Host: "h", User: "u", PasswordEnv: "PG_TEST_PASS"}.DSN()
	if err != nil || !strings.Contains(dsn, "u:s3cr3t@") {
		t.Errorf("password_env should supply the password: %s %v", dsn, err)
	}
}

func TestNewerFormatIsRefused(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"), "version: 99\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "update pgtower") {
		t.Errorf("a newer config format should ask to update pgtower, got %v", err)
	}
}

func TestInvalidYAMLIsReported(t *testing.T) {
	dir := isolateEnv(t)
	writeFile(t, filepath.Join(dir, "config.yml"), "connections: [\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("broken YAML must be reported, not silently ignored: %v", err)
	}
}

func TestPartsDecomposesURL(t *testing.T) {
	p := Connection{Name: "x", URL: "postgres://u:pw@h:6000/db?sslmode=require"}.Parts()
	if p.URL != "" || p.Host != "h" || p.Port != 6000 || p.User != "u" || p.Password != "pw" ||
		p.Database != "db" || p.SSLMode != "require" {
		t.Errorf("Parts() wrong: %+v", p)
	}
}
