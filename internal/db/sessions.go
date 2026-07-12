package db

import "context"

// Session is a row from pg_stat_activity (client connection).
type Session struct {
	PID        int32
	User       string
	DB         string
	AppName    string
	ClientAddr string
	State      string
	Wait       string
	Duration   string // now - query_start
	Query      string
}

// ListSessions lists client connections (except pgtui's own), active first,
// then by oldest.
func ListSessions(ctx context.Context, p Pinger) ([]Session, error) {
	rows, err := p.Query(ctx, `
		select pid,
		       coalesce(usename, ''),
		       coalesce(datname, ''),
		       coalesce(application_name, ''),
		       coalesce(host(client_addr), 'local'),
		       coalesce(state, ''),
		       coalesce(nullif(wait_event_type || ':' || wait_event, ':'), ''),
		       coalesce(to_char(now() - query_start, 'HH24:MI:SS'), ''),
		       coalesce(left(regexp_replace(query, '\s+', ' ', 'g'), 200), '')
		from pg_stat_activity
		where backend_type = 'client backend'
		  and pid <> pg_backend_pid()
		order by (state = 'active') desc, query_start nulls last`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.PID, &s.User, &s.DB, &s.AppName, &s.ClientAddr,
			&s.State, &s.Wait, &s.Duration, &s.Query); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CancelBackend sends SIGINT (pg_cancel_backend) — cancels the session's
// current query without dropping it. Requires being the session owner, a member
// of the owning role, or pg_signal_backend/superuser.
func CancelBackend(ctx context.Context, p Pinger, pid int32) (bool, error) {
	var ok bool
	err := p.QueryRow(ctx, `select pg_cancel_backend($1)`, pid).Scan(&ok)
	return ok, err
}

// TerminateBackend sends SIGTERM (pg_terminate_backend) — drops the connection.
func TerminateBackend(ctx context.Context, p Pinger, pid int32) (bool, error) {
	var ok bool
	err := p.QueryRow(ctx, `select pg_terminate_backend($1)`, pid).Scan(&ok)
	return ok, err
}
