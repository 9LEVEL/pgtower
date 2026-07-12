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
	ConnLimit   int
	ValidUntil  string
	MemberOf    string
}

// ListRoles lists the cluster roles.
func ListRoles(ctx context.Context, p Pinger) ([]Role, error) {
	rows, err := p.Query(ctx, `
		select r.rolname, r.rolcanlogin, r.rolsuper, r.rolcreatedb,
		       r.rolcreaterole, r.rolreplication, r.rolconnlimit,
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
			&r.CreateRole, &r.Replication, &r.ConnLimit, &r.ValidUntil, &r.MemberOf); err != nil {
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

// BuildDropRole builds a DROP ROLE.
func BuildDropRole(name string) string {
	return "DROP ROLE " + QuoteIdent(name)
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
	GrantConnect     GrantScope = iota // GRANT CONNECT ON DATABASE
	GrantAllDatabase                   // GRANT ALL PRIVILEGES ON DATABASE
	GrantOwner                         // ALTER DATABASE ... OWNER TO
	GrantSchemaAll                     // full access to the public schema (runs on the target db)
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
	default:
		return "?"
	}
}

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
	default:
		return "", nil
	}
}
