package db

import "context"

// HBARule is a parsed pg_hba.conf line as reported by pg_hba_file_rules.
// Error is non-empty when Postgres failed to parse that line.
type HBARule struct {
	LineNumber int
	Type       string
	Database   string
	UserName   string
	Address    string
	Netmask    string
	AuthMethod string
	Options    string
	Error      string
}

// HBAFilePath returns the path of the active pg_hba.conf.
func HBAFilePath(ctx context.Context, p Pinger) (string, error) {
	var path string
	err := p.QueryRow(ctx, "select current_setting('hba_file')").Scan(&path)
	return path, err
}

// ListHBARules returns the parsed authentication rules (pg_hba_file_rules,
// PG10+). Requires superuser. array columns are flattened to comma lists.
func ListHBARules(ctx context.Context, p Pinger) ([]HBARule, error) {
	rows, err := p.Query(ctx, `
		select coalesce(line_number, 0),
		       coalesce(type, ''),
		       array_to_string(coalesce(database, '{}'), ','),
		       array_to_string(coalesce(user_name, '{}'), ','),
		       coalesce(address, ''),
		       coalesce(netmask, ''),
		       coalesce(auth_method, ''),
		       array_to_string(coalesce(options, '{}'), ','),
		       coalesce(error, '')
		from pg_hba_file_rules
		order by line_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HBARule
	for rows.Next() {
		var r HBARule
		if err := rows.Scan(&r.LineNumber, &r.Type, &r.Database, &r.UserName,
			&r.Address, &r.Netmask, &r.AuthMethod, &r.Options, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
