package db_test

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/9level/pg-tui/internal/db"
)

func TestBuilders(t *testing.T) {
	if got := db.BuildCreateRole("app", "p'w", true, true, false); got !=
		`CREATE ROLE "app" LOGIN CREATEDB PASSWORD 'p''w'` {
		t.Errorf("BuildCreateRole = %q", got)
	}
	if got := db.BuildCreateRole(`we"ird`, "", false, false, true); got !=
		`CREATE ROLE "we""ird" NOLOGIN CREATEROLE` {
		t.Errorf("BuildCreateRole quoting = %q", got)
	}
	if got := db.BuildCreateDatabase("app_db", "app"); got != `CREATE DATABASE "app_db" OWNER "app"` {
		t.Errorf("BuildCreateDatabase = %q", got)
	}
	if got := db.BuildDropDatabase("app_db"); got != `DROP DATABASE "app_db"` {
		t.Errorf("BuildDropDatabase = %q", got)
	}
	if got := db.BuildAlterRolePassword("app", "p'w"); got != `ALTER ROLE "app" PASSWORD 'p''w'` {
		t.Errorf("BuildAlterRolePassword = %q", got)
	}
	tgt, stmts := db.BuildGrant(db.GrantConnect, "app_db", "app")
	if tgt != "" || len(stmts) != 1 || stmts[0] != `GRANT CONNECT ON DATABASE "app_db" TO "app"` {
		t.Errorf("BuildGrant connect = %q %v", tgt, stmts)
	}
	tgt, stmts = db.BuildGrant(db.GrantSchemaAll, "app_db", "app")
	if tgt != "app_db" || len(stmts) == 0 || !strings.Contains(stmts[0], "SCHEMA public") {
		t.Errorf("BuildGrant schema = %q %v", tgt, stmts)
	}
}

func TestGeneratePassword(t *testing.T) {
	const n = 32
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	a, err := db.GeneratePassword(n)
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	if len(a) != n {
		t.Errorf("len = %d, want %d", len(a), n)
	}
	for _, c := range a {
		if !strings.ContainsRune(safe, c) {
			t.Errorf("password contains terminal-unsafe char %q", c)
		}
	}
	// n <= 0 falls back to 32.
	if d, _ := db.GeneratePassword(0); len(d) != 32 {
		t.Errorf("GeneratePassword(0) len = %d, want 32", len(d))
	}
	// Extremely unlikely to collide unless generation is not random.
	if b, _ := db.GeneratePassword(n); a == b {
		t.Error("two generated passwords are identical")
	}
}

func TestSCRAMSHA256Secret(t *testing.T) {
	s, rounds, err := db.SCRAMSHA256Secret("s3cr3tPassw0rd", 4096)
	if err != nil {
		t.Fatalf("SCRAMSHA256Secret: %v", err)
	}
	if rounds != 4096 {
		t.Errorf("rounds = %d, want 4096", rounds)
	}
	// SCRAM-SHA-256$<iter>:<b64 salt>$<b64 StoredKey>:<b64 ServerKey>
	if !strings.HasPrefix(s, "SCRAM-SHA-256$4096:") {
		t.Fatalf("unexpected prefix: %q", s)
	}
	parts := strings.SplitN(s, "$", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 $-separated parts, got %d: %q", len(parts), s)
	}
	iterSalt := strings.SplitN(parts[1], ":", 2)
	keys := strings.SplitN(parts[2], ":", 2)
	if len(iterSalt) != 2 || len(keys) != 2 {
		t.Fatalf("malformed verifier: %q", s)
	}
	salt := mustB64(t, iterSalt[1])
	stored := mustB64(t, keys[0])
	server := mustB64(t, keys[1])
	if len(salt) != 16 {
		t.Errorf("salt = %d bytes, want 16", len(salt))
	}
	if len(stored) != 32 || len(server) != 32 {
		t.Errorf("StoredKey/ServerKey = %d/%d bytes, want 32/32", len(stored), len(server))
	}
	// Each call uses a fresh random salt -> different verifier.
	if s2, _, _ := db.SCRAMSHA256Secret("s3cr3tPassw0rd", 4096); s == s2 {
		t.Error("two verifiers for the same password are identical (salt not random?)")
	}

	// iteration clamping: 0 -> default, below floor -> 4096, above ceiling capped.
	if _, r, _ := db.SCRAMSHA256Secret("x", 0); r != db.SCRAMDefaultIterations {
		t.Errorf("default rounds = %d, want %d", r, db.SCRAMDefaultIterations)
	}
	if got := db.ClampSCRAMIterations(0); got != db.SCRAMDefaultIterations {
		t.Errorf("Clamp(0) = %d, want %d", got, db.SCRAMDefaultIterations)
	}
	if got := db.ClampSCRAMIterations(10); got != 4096 {
		t.Errorf("Clamp(10) = %d, want 4096 (floor)", got)
	}
	if got := db.ClampSCRAMIterations(5_000_000); got != 1_000_000 {
		t.Errorf("Clamp(5M) = %d, want 1000000 (ceiling)", got)
	}
}

func TestPasswordSecret(t *testing.T) {
	// Printable ASCII -> hashed into a SCRAM verifier (plaintext stays off the wire).
	sec, hashed, err := db.PasswordSecret("Str0ng-P@ss_123", 0)
	if err != nil {
		t.Fatalf("PasswordSecret: %v", err)
	}
	if !hashed {
		t.Error("ASCII password should be hashed")
	}
	if !strings.HasPrefix(sec, "SCRAM-SHA-256$") || strings.Contains(sec, "Str0ng") {
		t.Errorf("expected a SCRAM verifier without the plaintext, got %q", sec)
	}

	// Non-ASCII -> returned as-is so the server can SASLprep it correctly.
	for _, pw := range []string{"café", "naïve", "senhaç"} {
		sec, hashed, err := db.PasswordSecret(pw, 0)
		if err != nil {
			t.Fatalf("PasswordSecret(%q): %v", pw, err)
		}
		if hashed || sec != pw {
			t.Errorf("non-ASCII %q should pass through unchanged, got %q hashed=%v", pw, sec, hashed)
		}
	}

	// Empty -> unchanged, not hashed (caller omits PASSWORD).
	if sec, hashed, _ := db.PasswordSecret("", 0); sec != "" || hashed {
		t.Errorf("empty password should stay empty/unhashed, got %q hashed=%v", sec, hashed)
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 %q: %v", s, err)
	}
	return b
}

// TestAdminRoundtripLive creates and drops a throwaway role and database,
// without touching existing objects. It exercises the ExecAdmin path (simple
// protocol, required for CREATE/DROP DATABASE).
func TestAdminRoundtripLive(t *testing.T) {
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

	const role = "pgtui_selftest_role"
	const database = "pgtui_selftest_db"

	// preventive cleanup + at the end (idempotent)
	cleanup := func() {
		_, _ = db.ExecAdmin(ctx, p, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
		_, _ = db.ExecAdmin(ctx, p, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	}
	cleanup()
	defer cleanup()

	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateRole(role, "s3cr3t", true, true, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	// CREATE DATABASE: the key test of the simple protocol.
	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateDatabase(database, role)); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	_, stmts := db.BuildGrant(db.GrantConnect, database, role)
	for _, s := range stmts {
		if _, err := db.ExecAdmin(ctx, p, s); err != nil {
			t.Fatalf("GRANT: %v", err)
		}
	}

	roles, err := db.ListRoles(ctx, p)
	if err != nil || !hasRole(roles, role) {
		t.Fatalf("created role does not appear in ListRoles (err=%v)", err)
	}
	dbs, err := db.ListDatabases(ctx, p)
	if err != nil || !hasDB(dbs, database) {
		t.Fatalf("created database does not appear in ListDatabases (err=%v)", err)
	}

	// drop everything and confirm it is gone
	if _, err := db.ExecAdmin(ctx, p, db.BuildDropDatabase(database)); err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildDropRole(role)); err != nil {
		t.Fatalf("DROP ROLE: %v", err)
	}
	dbs, _ = db.ListDatabases(ctx, p)
	if hasDB(dbs, database) {
		t.Error("database still exists after DROP")
	}
}

// TestForceDropRoleLive proves that force-dropping a role that owns a database
// and a table REMOVES the role but PRESERVES the database and table (ownership
// reassigned to the successor, no data loss).
func TestForceDropRoleLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const role = "pgtui_force_role"
	const database = "pgtui_force_db"

	mgr, err := db.NewManager(ctx, dsn, "postgres")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	admin, _ := mgr.Pool(ctx, "postgres")

	drop := func(p db.Pinger, sql string) { _, _ = db.ExecAdmin(ctx, p, sql) }
	drop(admin, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
	drop(admin, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))

	// cleanup: close pools (release connections) and drop with a fresh connection.
	defer func() {
		mgr.Close()
		ctx2, c2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer c2()
		if m2, err := db.NewManager(ctx2, dsn, "postgres"); err == nil {
			p, _ := m2.Pool(ctx2, "postgres")
			drop2 := func(sql string) { _, _ = db.ExecAdmin(ctx2, p, sql) }
			drop2("DROP DATABASE IF EXISTS " + db.QuoteIdent(database))
			drop2("DROP ROLE IF EXISTS " + db.QuoteIdent(role))
			m2.Close()
		}
	}()

	mustExec := func(what string, p db.Pinger, sql string) {
		if _, e := db.ExecAdmin(ctx, p, sql); e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}
	mustExec("CREATE ROLE", admin, db.BuildCreateRole(role, "x", true, false, false))
	mustExec("CREATE DATABASE", admin, db.BuildCreateDatabase(database, role))
	p, err := mgr.Pool(ctx, database)
	if err != nil {
		t.Fatalf("Pool(%s): %v", database, err)
	}
	mustExec("CREATE TABLE", p, "create table pgtui_t (id int)")
	mustExec("ALTER TABLE OWNER", p, "alter table pgtui_t owner to "+db.QuoteIdent(role))

	// force removal by reassigning everything to postgres
	warnings, err := db.ForceDropRole(ctx, mgr, role, "postgres")
	if err != nil {
		t.Fatalf("ForceDropRole: %v (warnings: %v)", err, warnings)
	}

	roles, _ := db.ListRoles(ctx, admin)
	if hasRole(roles, role) {
		t.Error("role still exists after force-drop")
	}
	dbs, _ := db.ListDatabases(ctx, admin)
	if !hasDB(dbs, database) {
		t.Fatal("the database was DROPPED — it should have been reassigned, not removed")
	}
	// the table must still exist (owner is now postgres)
	p2, _ := mgr.Pool(ctx, database)
	d, err := db.DescribeTable(ctx, p2, "public", "pgtui_t")
	if err != nil || len(d.Columns) == 0 {
		t.Errorf("the table disappeared after force-drop (data loss!): %v", err)
	}
}

// TestSCRAMPasswordLoginLive proves the client-side verifiers are correct for
// BOTH password paths: CREATE ROLE with a hashed password (PasswordSecret) and
// ALTER ROLE with a hashed reset (SCRAMSHA256Secret). After each, it actually
// authenticates with the plaintext — Postgres validates the login against the
// stored verifier, so a wrong hash fails the login. It also checks that the
// server stored the ALTER verifier verbatim (when pg_authid is readable) and
// that the old password stops working after the reset.
func TestSCRAMPasswordLoginLive(t *testing.T) {
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
	admin, err := mgr.Pool(ctx, "postgres")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}

	const role = "pgtui_scram_role"
	_, _ = db.ExecAdmin(ctx, admin, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	defer func() { _, _ = db.ExecAdmin(ctx, admin, "DROP ROLE IF EXISTS "+db.QuoteIdent(role)) }()

	login := func(pwd string) error {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return err
		}
		cfg.User, cfg.Password = role, pwd
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return err
		}
		defer conn.Close(ctx)
		var one int
		return conn.QueryRow(ctx, "select 1").Scan(&one)
	}

	// Phase 1: CREATE ROLE with a client-hashed password, then log in.
	pwd1, err := db.GeneratePassword(32)
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	create, hashed, err := db.PasswordSecret(pwd1, 0)
	if err != nil {
		t.Fatalf("PasswordSecret: %v", err)
	}
	if !hashed {
		t.Fatal("a generated ASCII password should be hashed client-side")
	}
	if _, err := db.ExecAdmin(ctx, admin, db.BuildCreateRole(role, create, true, false, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	if err := login(pwd1); err != nil {
		t.Fatalf("login after CREATE ROLE failed (verifier likely wrong): %v", err)
	}

	// Phase 2: ALTER ROLE to a new client-hashed password (the reset flow).
	pwd2, err := db.GeneratePassword(32)
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	secret2, _, err := db.SCRAMSHA256Secret(pwd2, 0)
	if err != nil {
		t.Fatalf("SCRAMSHA256Secret: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, admin, db.BuildAlterRolePassword(role, secret2)); err != nil {
		t.Fatalf("ALTER ROLE PASSWORD: %v", err)
	}
	// If we can read pg_authid, the stored verifier must equal what we sent.
	var stored string
	if err := admin.QueryRow(ctx, "select rolpassword from pg_authid where rolname=$1", role).Scan(&stored); err == nil {
		if stored != secret2 {
			t.Errorf("stored verifier differs from the one sent:\n got %q\nwant %q", stored, secret2)
		}
	}
	if err := login(pwd2); err != nil {
		t.Fatalf("login after ALTER ROLE failed (verifier likely wrong): %v", err)
	}
	if err := login(pwd1); err == nil {
		t.Error("the old password still authenticates after the reset")
	}
}

func TestDescribeLive(t *testing.T) {
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
	p, _ := mgr.Pool(ctx, "postgres")

	d, err := db.DescribeTable(ctx, p, "pg_catalog", "pg_class")
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if len(d.Columns) < 5 {
		t.Errorf("pg_class should have several columns, got %d", len(d.Columns))
	}

	if _, err := db.ListSessions(ctx, p); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
}

func hasRole(rs []db.Role, name string) bool {
	for _, r := range rs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func hasDB(ds []db.Database, name string) bool {
	for _, d := range ds {
		if d.Name == name {
			return true
		}
	}
	return false
}
