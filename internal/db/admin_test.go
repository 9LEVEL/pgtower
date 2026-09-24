package db_test

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/9level/pgtower/internal/db"
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

	const role = "pgtower_selftest_role"
	const database = "pgtower_selftest_db"

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

	const role = "pgtower_force_role"
	const database = "pgtower_force_db"

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
	mustExec("CREATE TABLE", p, "create table pgtower_t (id int)")
	mustExec("ALTER TABLE OWNER", p, "alter table pgtower_t owner to "+db.QuoteIdent(role))

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
	d, err := db.DescribeTable(ctx, p2, "public", "pgtower_t")
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

	const role = "pgtower_scram_role"
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

// TestDashboardCountsOwnConnsLive proves the dashboard sees pgtower's own
// backends (application_name = 'pgtower'), so the UI can disclose that footprint
// instead of letting it silently inflate "connections used".
func TestDashboardCountsOwnConnsLive(t *testing.T) {
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

	d, err := db.LoadDashboard(ctx, p)
	if err != nil {
		t.Fatalf("LoadDashboard: %v", err)
	}
	if d.OwnConns < 1 {
		t.Errorf("OwnConns = %d, want >= 1 (our own connection carries application_name=pgtower)", d.OwnConns)
	}
	if d.TotalConns < d.OwnConns {
		t.Errorf("TotalConns %d < OwnConns %d — impossible", d.TotalConns, d.OwnConns)
	}
}

// TestRoleAccessLive grants the read-only preset on a throwaway database and
// checks RoleAccess surfaces it — schema-public USAGE and SELECT on the table —
// which is exactly the access no plain role listing reveals.
func TestRoleAccessLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mgr, err := db.NewManager(ctx, dsn, "postgres")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	admin, _ := mgr.Pool(ctx, "postgres")

	const role = "pgtower_access_selftest"
	const database = "pgtower_access_selftest_db"
	cleanup := func() {
		_, _ = db.ExecAdmin(ctx, admin, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
		_, _ = db.ExecAdmin(ctx, admin, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	}
	cleanup()
	defer cleanup()

	if _, err := db.ExecAdmin(ctx, admin, db.BuildCreateRole(role, "s3cr3t", true, false, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, admin, db.BuildCreateDatabase(database, "postgres")); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	target, err := mgr.Pool(ctx, database)
	if err != nil {
		t.Fatalf("Pool(%s): %v", database, err)
	}
	if _, err := db.ExecAdmin(ctx, target, "CREATE TABLE t (id int)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	_, stmts := db.BuildGrant(db.GrantSchemaReadOnly, database, role)
	for _, s := range stmts {
		if _, err := db.ExecAdmin(ctx, target, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	acc, err := db.RoleAccess(ctx, mgr, role)
	if err != nil {
		t.Fatalf("RoleAccess: %v", err)
	}
	var found *db.DBAccess
	for i := range acc {
		if acc[i].Database == database {
			found = &acc[i]
		}
	}
	if found == nil {
		t.Fatalf("RoleAccess did not report %s after a read-only grant; got %+v", database, acc)
	}
	if len(found.Schema) == 0 {
		t.Errorf("expected an explicit schema-public grant, got %+v", *found)
	}
	var sawSelect bool
	for _, tp := range found.Tables {
		if tp.Privilege == "SELECT" && tp.Count >= 1 {
			sawSelect = true
		}
	}
	if !sawSelect {
		t.Errorf("expected SELECT on >= 1 table, got tables=%+v", found.Tables)
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

func TestBuildAlterRoleAttrs(t *testing.T) {
	all := db.RoleAttrs{Login: true, CreateDB: true, CreateRole: true,
		Superuser: true, Replication: true, BypassRLS: true}
	none := db.RoleAttrs{}

	// Nothing changed: no statement at all, so the caller never runs an ALTER
	// ROLE that needs privileges it does not have.
	if got := db.BuildAlterRoleAttrs("app", all, all); got != "" {
		t.Errorf("no-op = %q, want empty", got)
	}

	// Only the differences are emitted. This is the gap that made an attribute
	// impossible to remove once set: there was no ALTER ROLE path at all.
	if got := db.BuildAlterRoleAttrs("app", db.RoleAttrs{Login: true, CreateDB: true},
		db.RoleAttrs{Login: true}); got != `ALTER ROLE "app" NOCREATEDB` {
		t.Errorf("drop createdb = %q", got)
	}

	if got := db.BuildAlterRoleAttrs(`we"ird`, none, all); got !=
		`ALTER ROLE "we""ird" LOGIN CREATEDB CREATEROLE SUPERUSER REPLICATION BYPASSRLS` {
		t.Errorf("turn everything on = %q", got)
	}
	if got := db.BuildAlterRoleAttrs("app", all, none); got !=
		`ALTER ROLE "app" NOLOGIN NOCREATEDB NOCREATEROLE NOSUPERUSER NOREPLICATION NOBYPASSRLS` {
		t.Errorf("turn everything off = %q", got)
	}
}

func TestRoleAttrsEscalates(t *testing.T) {
	none := db.RoleAttrs{}

	for _, c := range []struct {
		name string
		to   db.RoleAttrs
		want bool
	}{
		{"superuser", db.RoleAttrs{Superuser: true}, true},
		{"bypassrls", db.RoleAttrs{BypassRLS: true}, true},
		{"createdb is not escalation", db.RoleAttrs{CreateDB: true}, false},
		{"login is not escalation", db.RoleAttrs{Login: true}, false},
	} {
		if got := c.to.Escalates(none); got != c.want {
			t.Errorf("%s: Escalates = %v, want %v", c.name, got, c.want)
		}
	}

	// Turning an attribute OFF is never escalation, otherwise the UI would ask
	// for a typed confirmation to make a role safer.
	privileged := db.RoleAttrs{Superuser: true, BypassRLS: true}
	if none.Escalates(privileged) {
		t.Error("dropping superuser/bypassrls counted as escalation")
	}
}

func TestBuildRevoke(t *testing.T) {
	tgt, stmts := db.BuildRevoke(db.GrantConnect, "app_db", "app")
	if tgt != "" || len(stmts) != 1 || stmts[0] != `REVOKE CONNECT ON DATABASE "app_db" FROM "app"` {
		t.Errorf("revoke connect = %q %v", tgt, stmts)
	}

	// Ownership is a transfer, not a privilege: there is nothing to revoke.
	if _, stmts := db.BuildRevoke(db.GrantOwner, "app_db", "app"); len(stmts) != 0 {
		t.Errorf("revoke owner should be a no-op, got %v", stmts)
	}
	if db.GrantOwner.Revocable() {
		t.Error("GrantOwner reported as revocable")
	}

	// ⚠ The default privileges must be undone too. Without it the revoke looks
	// like it worked and the role keeps access to every table created later.
	tgt, stmts = db.BuildRevoke(db.GrantSchemaAll, "app_db", "app")
	if tgt != "app_db" {
		t.Errorf("revoke schema targetDB = %q, want app_db", tgt)
	}
	var sawDefaults bool
	for _, s := range stmts {
		if strings.Contains(s, "ALTER DEFAULT PRIVILEGES") && strings.Contains(s, "REVOKE") {
			sawDefaults = true
		}
	}
	if !sawDefaults {
		t.Errorf("revoke schema does not undo ALTER DEFAULT PRIVILEGES: %v", stmts)
	}
}

func TestBuildGrantReadWriteHasNoDDL(t *testing.T) {
	tgt, stmts := db.BuildGrant(db.GrantSchemaReadWrite, "app_db", "app")
	if tgt != "app_db" || len(stmts) == 0 {
		t.Fatalf("read/write grant = %q %v", tgt, stmts)
	}

	// The whole point of the preset: an application role that reaches the data
	// and never the schema. "GRANT ALL" here would silently include DDL.
	for _, s := range stmts {
		if strings.Contains(s, "GRANT ALL") {
			t.Errorf("read/write preset grants ALL: %q", s)
		}
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES",
		"GRANT USAGE, SELECT ON ALL SEQUENCES",
		"ALTER DEFAULT PRIVILEGES",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("read/write preset missing %q in:\n%s", want, joined)
		}
	}
}

func TestBuildGrantReadOnlyIsSelectOnly(t *testing.T) {
	tgt, stmts := db.BuildGrant(db.GrantSchemaReadOnly, "app_db", "app")
	if tgt != "app_db" || len(stmts) == 0 {
		t.Fatalf("read-only grant = %q %v", tgt, stmts)
	}
	joined := strings.Join(stmts, "\n")

	// The whole point: reads, never writes. No write verb or DDL may appear.
	for _, forbidden := range []string{"INSERT", "UPDATE", "DELETE", "GRANT ALL", "ALL PRIVILEGES"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("read-only preset contains %q:\n%s", forbidden, joined)
		}
	}
	// It grants SELECT and covers tables created later via default privileges —
	// the mirror of the revoke trap: "read everything" must include the future.
	for _, want := range []string{
		"GRANT USAGE ON SCHEMA public",
		"GRANT SELECT ON ALL TABLES",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("read-only preset missing %q in:\n%s", want, joined)
		}
	}

	// It is revocable, and shares the schema revoke path (REVOKE ALL + undo
	// default privileges), so it can never leave a role stuck on future tables.
	if !db.GrantSchemaReadOnly.Revocable() {
		t.Error("read-only should be revocable")
	}
	rtgt, rstmts := db.BuildRevoke(db.GrantSchemaReadOnly, "app_db", "app")
	if rtgt != "app_db" || len(rstmts) == 0 {
		t.Fatalf("read-only revoke = %q %v", rtgt, rstmts)
	}
	var undoesDefaults bool
	for _, s := range rstmts {
		if strings.Contains(s, "ALTER DEFAULT PRIVILEGES") && strings.Contains(s, "REVOKE") {
			undoesDefaults = true
		}
	}
	if !undoesDefaults {
		t.Errorf("read-only revoke must undo default privileges: %v", rstmts)
	}
}

// TestRoleAttributesRoundtripLive is the cycle that had no way back before:
// give a role an attribute, then take it away. Until ALTER ROLE existed here,
// the only way to undo CREATEDB was to drop the role.
//
// It also checks that BYPASSRLS is read back, because a listing that cannot show
// it lets a role that defeats row-level security hide in plain sight.
func TestRoleAttributesRoundtripLive(t *testing.T) {
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

	const role = "pgtower_attrs_selftest"
	cleanup := func() { _, _ = db.ExecAdmin(ctx, p, "DROP ROLE IF EXISTS "+db.QuoteIdent(role)) }
	cleanup()
	defer cleanup()

	find := func() db.Role {
		t.Helper()
		roles, err := db.ListRoles(ctx, p)
		if err != nil {
			t.Fatalf("ListRoles: %v", err)
		}
		for _, r := range roles {
			if r.Name == role {
				return r
			}
		}
		t.Fatalf("role %s not listed", role)
		return db.Role{}
	}

	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateRole(role, "s3cr3t", true, true, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	created := find()
	if !created.CreateDB {
		t.Fatal("role was created without CREATEDB; the rest of the test proves nothing")
	}
	if created.BypassRLS {
		t.Error("a freshly created role should not have BYPASSRLS")
	}

	// Take CREATEDB away — the operation that previously had no path.
	want := created.Attrs()
	want.CreateDB = false
	sql := db.BuildAlterRoleAttrs(role, created.Attrs(), want)
	if sql == "" {
		t.Fatal("BuildAlterRoleAttrs produced no statement for a real change")
	}
	if _, err := db.ExecAdmin(ctx, p, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if find().CreateDB {
		t.Error("CREATEDB survived the ALTER ROLE")
	}

	// And BYPASSRLS round-trips, which is what the new column reports.
	on := want
	on.BypassRLS = true
	if _, err := db.ExecAdmin(ctx, p, db.BuildAlterRoleAttrs(role, want, on)); err != nil {
		t.Fatalf("grant BYPASSRLS: %v", err)
	}
	if !find().BypassRLS {
		t.Fatal("BYPASSRLS was set but ListRoles reports it off")
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildAlterRoleAttrs(role, on, want)); err != nil {
		t.Fatalf("revoke BYPASSRLS: %v", err)
	}
	if find().BypassRLS {
		t.Error("BYPASSRLS survived the ALTER ROLE")
	}
}

// TestRevokeRoundtripLive grants the read/write preset and takes it back,
// including the default privileges. Revoking the tables but leaving the
// defaults behind is the failure that looks like success.
func TestRevokeRoundtripLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	const role = "pgtower_revoke_selftest"
	const database = "pgtower_revoke_selftest_db"
	cleanup := func() {
		_, _ = db.ExecAdmin(ctx, admin, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
		_, _ = db.ExecAdmin(ctx, admin, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	}
	cleanup()
	defer cleanup()

	if _, err := db.ExecAdmin(ctx, admin, db.BuildCreateRole(role, "s3cr3t", true, false, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, admin, db.BuildCreateDatabase(database, "postgres")); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}

	target, err := mgr.Pool(ctx, database)
	if err != nil {
		t.Fatalf("Pool(%s): %v", database, err)
	}
	if _, err := db.ExecAdmin(ctx, target, "CREATE TABLE t_before (id int)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	run := func(p db.Pinger, stmts []string) {
		t.Helper()
		for _, s := range stmts {
			if _, err := db.ExecAdmin(ctx, p, s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
	}

	_, stmts := db.BuildGrant(db.GrantSchemaReadWrite, database, role)
	run(target, stmts)

	// A table created AFTER the grant is the one the default privileges cover.
	if _, err := db.ExecAdmin(ctx, target, "CREATE TABLE t_after (id int)"); err != nil {
		t.Fatalf("CREATE TABLE after grant: %v", err)
	}

	granted := func(tbl string) bool {
		t.Helper()
		var ok bool
		row := target.QueryRow(ctx, "select has_table_privilege($1, $2, 'SELECT')", role, tbl)
		if err := row.Scan(&ok); err != nil {
			t.Fatalf("has_table_privilege(%s): %v", tbl, err)
		}
		return ok
	}
	if !granted("t_before") || !granted("t_after") {
		t.Fatalf("grant did not reach the tables (before=%v after=%v); the revoke test would prove nothing",
			granted("t_before"), granted("t_after"))
	}

	_, stmts = db.BuildRevoke(db.GrantSchemaReadWrite, database, role)
	run(target, stmts)

	if granted("t_before") {
		t.Error("revoke left privileges on an existing table")
	}
	if _, err := db.ExecAdmin(ctx, target, "CREATE TABLE t_later (id int)"); err != nil {
		t.Fatalf("CREATE TABLE after revoke: %v", err)
	}
	if granted("t_later") {
		t.Error("revoke did not undo ALTER DEFAULT PRIVILEGES: new tables are still granted")
	}
}
