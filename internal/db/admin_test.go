package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

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
	tgt, stmts := db.BuildGrant(db.GrantConnect, "app_db", "app")
	if tgt != "" || len(stmts) != 1 || stmts[0] != `GRANT CONNECT ON DATABASE "app_db" TO "app"` {
		t.Errorf("BuildGrant connect = %q %v", tgt, stmts)
	}
	tgt, stmts = db.BuildGrant(db.GrantSchemaAll, "app_db", "app")
	if tgt != "app_db" || len(stmts) == 0 || !strings.Contains(stmts[0], "SCHEMA public") {
		t.Errorf("BuildGrant schema = %q %v", tgt, stmts)
	}
}

// TestAdminRoundtripLive cria e apaga um role e um database descartáveis, sem
// tocar nos objetos existentes. Prova o caminho de ExecAdmin (protocolo
// simples, necessário para CREATE/DROP DATABASE).
func TestAdminRoundtripLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL não definido")
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

	// cleanup preventivo + no fim (idempotente)
	cleanup := func() {
		_, _ = db.ExecAdmin(ctx, p, "DROP DATABASE IF EXISTS "+db.QuoteIdent(database))
		_, _ = db.ExecAdmin(ctx, p, "DROP ROLE IF EXISTS "+db.QuoteIdent(role))
	}
	cleanup()
	defer cleanup()

	if _, err := db.ExecAdmin(ctx, p, db.BuildCreateRole(role, "s3cr3t", true, true, false)); err != nil {
		t.Fatalf("CREATE ROLE: %v", err)
	}
	// CREATE DATABASE: o teste-chave do protocolo simples.
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
		t.Fatalf("role criado não aparece em ListRoles (err=%v)", err)
	}
	dbs, err := db.ListDatabases(ctx, p)
	if err != nil || !hasDB(dbs, database) {
		t.Fatalf("database criado não aparece em ListDatabases (err=%v)", err)
	}

	// derruba tudo e confirma que sumiu
	if _, err := db.ExecAdmin(ctx, p, db.BuildDropDatabase(database)); err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if _, err := db.ExecAdmin(ctx, p, db.BuildDropRole(role)); err != nil {
		t.Fatalf("DROP ROLE: %v", err)
	}
	dbs, _ = db.ListDatabases(ctx, p)
	if hasDB(dbs, database) {
		t.Error("database ainda existe após DROP")
	}
}

// TestForceDropRoleLive prova que forçar a remoção de um role dono de um
// database e de uma tabela REMOVE o role mas PRESERVA o banco e a tabela
// (posse reatribuída ao successor, sem perda de dados).
func TestForceDropRoleLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL não definido")
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

	// cleanup: fecha pools (libera conexões) e dropa com conexão nova.
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

	// força a remoção reatribuindo tudo para postgres
	warnings, err := db.ForceDropRole(ctx, mgr, role, "postgres")
	if err != nil {
		t.Fatalf("ForceDropRole: %v (avisos: %v)", err, warnings)
	}

	roles, _ := db.ListRoles(ctx, admin)
	if hasRole(roles, role) {
		t.Error("role ainda existe após forçar drop")
	}
	dbs, _ := db.ListDatabases(ctx, admin)
	if !hasDB(dbs, database) {
		t.Fatal("o database foi APAGADO — deveria ter sido reatribuído, não removido")
	}
	// a tabela deve continuar existindo (agora dona = postgres)
	p2, _ := mgr.Pool(ctx, database)
	d, err := db.DescribeTable(ctx, p2, "public", "pgtui_t")
	if err != nil || len(d.Columns) == 0 {
		t.Errorf("a tabela sumiu após forçar drop (perda de dados!): %v", err)
	}
}

func TestDescribeLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL não definido")
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
		t.Errorf("pg_class deveria ter várias colunas, veio %d", len(d.Columns))
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
