package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ForceDropRole removes a role even with dependencies, WITHOUT dropping data:
// it reassigns ownership of the databases and objects to the successor, revokes
// the role's privileges in each database, and finally runs DROP ROLE. It
// returns non-fatal warnings per database and the final DROP ROLE error (if any).
func ForceDropRole(ctx context.Context, mgr *Manager, doomed, successor string) ([]string, error) {
	admin, err := mgr.Pool(ctx, mgr.AdminDB())
	if err != nil {
		return nil, err
	}
	var warnings []string

	// 1. reassign ownership of the databases the role owns (keeps the database).
	ownedDBs, err := DatabasesOwnedBy(ctx, admin, doomed)
	if err != nil {
		return nil, err
	}
	for _, d := range ownedDBs {
		sql := "ALTER DATABASE " + QuoteIdent(d) + " OWNER TO " + QuoteIdent(successor)
		if _, e := ExecAdmin(ctx, admin, sql); e != nil {
			warnings = append(warnings, "ALTER DATABASE "+d+": "+oneLine(e))
		}
	}

	// 2. in each database: reassign objects (REASSIGN OWNED, no data loss)
	//    and then revoke privileges (DROP OWNED).
	dbs, err := ConnectableDatabases(ctx, admin)
	if err != nil {
		return warnings, err
	}
	for _, dbn := range dbs {
		p, e := mgr.Pool(ctx, dbn)
		if e != nil {
			warnings = append(warnings, "connect to "+dbn+": "+oneLine(e))
			continue
		}
		if _, e := ExecAdmin(ctx, p, "REASSIGN OWNED BY "+QuoteIdent(doomed)+" TO "+QuoteIdent(successor)); e != nil {
			warnings = append(warnings, "REASSIGN in "+dbn+": "+oneLine(e))
			continue
		}
		if _, e := ExecAdmin(ctx, p, "DROP OWNED BY "+QuoteIdent(doomed)); e != nil {
			warnings = append(warnings, "DROP OWNED in "+dbn+": "+oneLine(e))
		}
	}

	// 3. finally remove the role.
	if _, e := ExecAdmin(ctx, admin, BuildDropRole(doomed)); e != nil {
		return warnings, e
	}
	return warnings, nil
}

func oneLine(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ExecAdmin runs an administrative statement (CREATE/DROP/GRANT/ALTER) in the
// simple protocol. Required because CREATE/DROP DATABASE do not run in pgx's
// extended protocol (prepared statement). It only receives SQL built internally
// (quoted identifiers), never raw user input.
func ExecAdmin(ctx context.Context, p Pinger, sql string) (string, error) {
	rows, err := p.Query(ctx, sql, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return "", err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	return rows.CommandTag().String(), nil
}

// Role is a row from pg_roles.
type Role struct {
	Name        string
	CanLogin    bool
	Super       bool
	CreateDB    bool
	CreateRole  bool
	Replication bool
	// BypassRLS means the role ignores row-level security. Listing it matters as
	// much as SUPERUSER: on a database whose tenant isolation rests on RLS, one
	// role with this attribute reads every tenant's rows and nothing errors.
	BypassRLS  bool
	ConnLimit  int
	ValidUntil string
	MemberOf   string
}

// RoleAttrs are the boolean attributes of a role that ALTER ROLE can flip.
type RoleAttrs struct {
	Login       bool
	CreateDB    bool
	CreateRole  bool
	Superuser   bool
	Replication bool
	BypassRLS   bool
}

// Attrs returns the role's current attributes.
func (r Role) Attrs() RoleAttrs {
	return RoleAttrs{
		Login: r.CanLogin, CreateDB: r.CreateDB, CreateRole: r.CreateRole,
		Superuser: r.Super, Replication: r.Replication, BypassRLS: r.BypassRLS,
	}
}

// Escalates reports whether moving from `from` to `a` grants SUPERUSER or
// BYPASSRLS. Both let the role read every row of every table regardless of
// row-level security, so the UI guards the transition the way it guards a drop.
func (a RoleAttrs) Escalates(from RoleAttrs) bool {
	return (a.Superuser && !from.Superuser) || (a.BypassRLS && !from.BypassRLS)
}

// ListRoles lists the cluster roles.
func ListRoles(ctx context.Context, p Pinger) ([]Role, error) {
	rows, err := p.Query(ctx, `
		select r.rolname, r.rolcanlogin, r.rolsuper, r.rolcreatedb,
		       r.rolcreaterole, r.rolreplication, r.rolbypassrls, r.rolconnlimit,
		       coalesce(to_char(r.rolvaliduntil, 'YYYY-MM-DD'), ''),
		       coalesce((select string_agg(b.rolname, ',' order by b.rolname)
		                 from pg_auth_members m
		                 join pg_roles b on b.oid = m.roleid
		                 where m.member = r.oid), '')
		from pg_roles r
		order by r.rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.CanLogin, &r.Super, &r.CreateDB,
			&r.CreateRole, &r.Replication, &r.BypassRLS, &r.ConnLimit,
			&r.ValidUntil, &r.MemberOf); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DatabasesOwnedBy returns the (non-template) databases owned by the role.
func DatabasesOwnedBy(ctx context.Context, p Pinger, role string) ([]string, error) {
	rows, err := p.Query(ctx, `
		select d.datname
		from pg_database d
		join pg_roles r on r.oid = d.datdba
		where r.rolname = $1 and not d.datistemplate
		order by 1`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ConnectableDatabases returns the non-template databases that accept connections.
func ConnectableDatabases(ctx context.Context, p Pinger) ([]string, error) {
	rows, err := p.Query(ctx, `
		select datname from pg_database
		where not datistemplate and datallowconn
		order by 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- SQL builders (they do not execute; the UI runs them via RunQuery) ---

// BuildCreateRole builds a CREATE ROLE. login controls LOGIN/NOLOGIN; an empty
// password omits PASSWORD. createdb/createrole add the attributes.
func BuildCreateRole(name, password string, login, createdb, createrole bool) string {
	s := "CREATE ROLE " + QuoteIdent(name)
	if login {
		s += " LOGIN"
	} else {
		s += " NOLOGIN"
	}
	if createdb {
		s += " CREATEDB"
	}
	if createrole {
		s += " CREATEROLE"
	}
	if password != "" {
		s += " PASSWORD " + QuoteLiteral(password)
	}
	return s
}

// BuildAlterRoleAttrs builds an ALTER ROLE that flips only the attributes that
// differ between `from` and `to`. It returns "" when nothing changed.
//
// Emitting only the differences is deliberate. Restating every attribute would
// need privileges the caller may not have (NOSUPERUSER requires superuser even
// when it is already off) and would hide, in the audit trail, which attribute
// the operator actually meant to change.
func BuildAlterRoleAttrs(name string, from, to RoleAttrs) string {
	var clauses []string
	add := func(changed bool, on bool, yes, no string) {
		if !changed {
			return
		}
		if on {
			clauses = append(clauses, yes)
			return
		}
		clauses = append(clauses, no)
	}

	add(from.Login != to.Login, to.Login, "LOGIN", "NOLOGIN")
	add(from.CreateDB != to.CreateDB, to.CreateDB, "CREATEDB", "NOCREATEDB")
	add(from.CreateRole != to.CreateRole, to.CreateRole, "CREATEROLE", "NOCREATEROLE")
	add(from.Superuser != to.Superuser, to.Superuser, "SUPERUSER", "NOSUPERUSER")
	add(from.Replication != to.Replication, to.Replication, "REPLICATION", "NOREPLICATION")
	add(from.BypassRLS != to.BypassRLS, to.BypassRLS, "BYPASSRLS", "NOBYPASSRLS")

	if len(clauses) == 0 {
		return ""
	}
	return "ALTER ROLE " + QuoteIdent(name) + " " + strings.Join(clauses, " ")
}

// BuildDropRole builds a DROP ROLE.
func BuildDropRole(name string) string {
	return "DROP ROLE " + QuoteIdent(name)
}

// BuildAlterRolePassword builds an ALTER ROLE ... PASSWORD statement to reset a
// role's password. The secret is quoted as a SQL string literal. It may be a
// plaintext password or a pre-computed verifier (e.g. from SCRAMSHA256Secret):
// when it is a valid "SCRAM-SHA-256$..." string PostgreSQL stores it verbatim,
// so the plaintext never reaches the server.
func BuildAlterRolePassword(name, secret string) string {
	return "ALTER ROLE " + QuoteIdent(name) + " PASSWORD " + QuoteLiteral(secret)
}

// BuildCreateDatabase builds a CREATE DATABASE (optional owner).
func BuildCreateDatabase(name, owner string) string {
	s := "CREATE DATABASE " + QuoteIdent(name)
	if owner != "" {
		s += " OWNER " + QuoteIdent(owner)
	}
	return s
}

// BuildDropDatabase builds a DROP DATABASE.
func BuildDropDatabase(name string) string {
	return "DROP DATABASE " + QuoteIdent(name)
}

// GrantScope defines the GRANT presets between role and database.
type GrantScope int

const (
	GrantConnect         GrantScope = iota // GRANT CONNECT ON DATABASE
	GrantAllDatabase                       // GRANT ALL PRIVILEGES ON DATABASE
	GrantOwner                             // ALTER DATABASE ... OWNER TO
	GrantSchemaAll                         // full access to the public schema (runs on the target db)
	GrantSchemaReadWrite                   // read/write rows, no DDL (runs on the target db)
)

func (g GrantScope) Label() string {
	switch g {
	case GrantConnect:
		return "CONNECT on database"
	case GrantAllDatabase:
		return "ALL PRIVILEGES on database"
	case GrantOwner:
		return "make database owner"
	case GrantSchemaAll:
		return "full access to public schema"
	case GrantSchemaReadWrite:
		return "read/write rows, no DDL"
	default:
		return "?"
	}
}

// Revocable reports whether the scope has a REVOKE counterpart. Ownership is a
// transfer, not a privilege: it is undone by handing the database to someone
// else, never by revoking it.
func (g GrantScope) Revocable() bool { return g != GrantOwner }

// BuildGrant returns the database the statements should run on ("" = can run on
// admin) and the list of statements for the chosen preset.
func BuildGrant(scope GrantScope, database, role string) (targetDB string, stmts []string) {
	db := QuoteIdent(database)
	r := QuoteIdent(role)
	switch scope {
	case GrantConnect:
		return "", []string{"GRANT CONNECT ON DATABASE " + db + " TO " + r}
	case GrantAllDatabase:
		return "", []string{"GRANT ALL PRIVILEGES ON DATABASE " + db + " TO " + r}
	case GrantOwner:
		return "", []string{"ALTER DATABASE " + db + " OWNER TO " + r}
	case GrantSchemaAll:
		return database, []string{
			"GRANT USAGE ON SCHEMA public TO " + r,
			"GRANT ALL ON ALL TABLES IN SCHEMA public TO " + r,
			"GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO " + r,
		}
	case GrantSchemaReadWrite:
		// What an application role needs and no more: it reads and writes rows
		// but cannot create, alter or drop a table. A SQL injection through such
		// a role reaches the data, never the schema.
		return database, []string{
			"GRANT USAGE ON SCHEMA public TO " + r,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + r,
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO " + r,
		}
	default:
		return "", nil
	}
}

// BuildRevoke mirrors BuildGrant. It returns no statements for GrantOwner,
// which is a transfer rather than a privilege.
//
// Each preset also undoes the matching ALTER DEFAULT PRIVILEGES. Without that,
// revoking looks like it worked and the role silently keeps access to every
// table created afterwards — the failure mode that makes "granted once, stuck
// forever" so hard to see.
func BuildRevoke(scope GrantScope, database, role string) (targetDB string, stmts []string) {
	dbq := QuoteIdent(database)
	r := QuoteIdent(role)
	switch scope {
	case GrantConnect:
		return "", []string{"REVOKE CONNECT ON DATABASE " + dbq + " FROM " + r}
	case GrantAllDatabase:
		return "", []string{"REVOKE ALL PRIVILEGES ON DATABASE " + dbq + " FROM " + r}
	case GrantSchemaAll, GrantSchemaReadWrite:
		return database, []string{
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM " + r,
			"REVOKE ALL ON ALL TABLES IN SCHEMA public FROM " + r,
			"REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM " + r,
			"REVOKE USAGE ON SCHEMA public FROM " + r,
		}
	default:
		return "", nil
	}
}
