package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ForceDropRole remove um role mesmo com dependências, SEM apagar dados:
// reatribui a posse dos databases e dos objetos ao successor, revoga os
// privilégios do role em cada database, e por fim executa DROP ROLE. Retorna
// avisos não-fatais por database e o erro do DROP ROLE final (se houver).
func ForceDropRole(ctx context.Context, mgr *Manager, doomed, successor string) ([]string, error) {
	admin, err := mgr.Pool(ctx, mgr.AdminDB())
	if err != nil {
		return nil, err
	}
	var warnings []string

	// 1. reatribui a posse dos databases que o role possui (não apaga banco).
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

	// 2. em cada database: reatribui objetos (REASSIGN OWNED, sem perda de
	//    dados) e depois revoga privilégios (DROP OWNED).
	dbs, err := ConnectableDatabases(ctx, admin)
	if err != nil {
		return warnings, err
	}
	for _, dbn := range dbs {
		p, e := mgr.Pool(ctx, dbn)
		if e != nil {
			warnings = append(warnings, "conectar em "+dbn+": "+oneLine(e))
			continue
		}
		if _, e := ExecAdmin(ctx, p, "REASSIGN OWNED BY "+QuoteIdent(doomed)+" TO "+QuoteIdent(successor)); e != nil {
			warnings = append(warnings, "REASSIGN em "+dbn+": "+oneLine(e))
			continue
		}
		if _, e := ExecAdmin(ctx, p, "DROP OWNED BY "+QuoteIdent(doomed)); e != nil {
			warnings = append(warnings, "DROP OWNED em "+dbn+": "+oneLine(e))
		}
	}

	// 3. finalmente remove o role.
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

// ExecAdmin executa um statement administrativo (CREATE/DROP/GRANT/ALTER) no
// protocolo simples. Necessário porque CREATE/DROP DATABASE não rodam no
// protocolo estendido (prepared statement) do pgx. Só recebe SQL construído
// internamente (identificadores quotados), nunca entrada crua do usuário.
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

// Role é uma linha de pg_roles.
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

// ListRoles lista os roles do cluster.
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

// DatabasesOwnedBy retorna os databases (não-template) cujo dono é o role.
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

// ConnectableDatabases retorna os databases não-template que aceitam conexão.
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

// --- construtores de SQL (não executam; a UI roda via RunQuery) ---

// BuildCreateRole monta um CREATE ROLE. login controla LOGIN/NOLOGIN; password
// vazio omite PASSWORD. createdb/createrole adicionam os atributos.
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

// BuildDropRole monta um DROP ROLE.
func BuildDropRole(name string) string {
	return "DROP ROLE " + QuoteIdent(name)
}

// BuildCreateDatabase monta um CREATE DATABASE (owner opcional).
func BuildCreateDatabase(name, owner string) string {
	s := "CREATE DATABASE " + QuoteIdent(name)
	if owner != "" {
		s += " OWNER " + QuoteIdent(owner)
	}
	return s
}

// BuildDropDatabase monta um DROP DATABASE.
func BuildDropDatabase(name string) string {
	return "DROP DATABASE " + QuoteIdent(name)
}

// GrantScope define os presets de GRANT entre role e database.
type GrantScope int

const (
	GrantConnect     GrantScope = iota // GRANT CONNECT ON DATABASE
	GrantAllDatabase                   // GRANT ALL PRIVILEGES ON DATABASE
	GrantOwner                         // ALTER DATABASE ... OWNER TO
	GrantSchemaAll                     // acesso total ao schema public (roda no db alvo)
)

func (g GrantScope) Label() string {
	switch g {
	case GrantConnect:
		return "CONNECT no database"
	case GrantAllDatabase:
		return "ALL PRIVILEGES no database"
	case GrantOwner:
		return "tornar owner do database"
	case GrantSchemaAll:
		return "acesso total ao schema public"
	default:
		return "?"
	}
}

// BuildGrant retorna o database em que os statements devem rodar ("" = pode
// rodar no admin) e a lista de statements do preset escolhido.
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
