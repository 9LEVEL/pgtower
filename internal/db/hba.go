package db

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

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

// HBAFileContent returns the raw text of pg_hba.conf (superuser / pg_read_server_files).
func HBAFileContent(ctx context.Context, p Pinger) (string, error) {
	var s string
	err := p.QueryRow(ctx, "select pg_read_file(current_setting('hba_file'))").Scan(&s)
	return s, err
}

// HBAWritable reports whether this connection can rewrite server files. Editing
// uses COPY ... TO PROGRAM, which requires a superuser (or pg_execute_server_program).
func HBAWritable(ctx context.Context, p Pinger) bool {
	var super bool
	if err := p.QueryRow(ctx, "select current_setting('is_superuser')::bool").Scan(&super); err != nil {
		return false
	}
	return super
}

// HBARuleInput describes a rule to build a pg_hba.conf line from.
type HBARuleInput struct {
	Type     string // local | host | hostssl | hostnossl | ...
	Database string
	User     string
	Address  string // empty for local
	Method   string // trust | scram-sha-256 | md5 | peer | reject | ...
}

// BuildHBALine renders an HBARuleInput as a single pg_hba.conf line. local rules
// omit the address field.
func BuildHBALine(r HBARuleInput) string {
	fields := []string{r.Type, r.Database, r.User}
	if r.Type != "local" && r.Address != "" {
		fields = append(fields, r.Address)
	}
	fields = append(fields, r.Method)
	return strings.Join(fields, " ")
}

// HBAAppendLine appends a rule line to the file content (ensuring a trailing
// newline).
func HBAAppendLine(content, line string) string {
	content = strings.TrimRight(content, "\n")
	if content != "" {
		content += "\n"
	}
	return content + line + "\n"
}

// HBAReplaceLine replaces the 1-based physical line lineNum with newLine.
func HBAReplaceLine(content string, lineNum int, newLine string) (string, error) {
	lines := strings.Split(content, "\n")
	i := lineNum - 1
	if i < 0 || i >= len(lines) {
		return "", fmt.Errorf("line %d out of range", lineNum)
	}
	lines[i] = newLine
	return strings.Join(lines, "\n"), nil
}

// HBADeleteLine removes the 1-based physical line lineNum.
func HBADeleteLine(content string, lineNum int) (string, error) {
	lines := strings.Split(content, "\n")
	i := lineNum - 1
	if i < 0 || i >= len(lines) {
		return "", fmt.Errorf("line %d out of range", lineNum)
	}
	lines = append(lines[:i], lines[i+1:]...)
	return strings.Join(lines, "\n"), nil
}

// hbaErrorLines collects the parse errors reported for a rule set.
func hbaErrorLines(rules []HBARule) []string {
	var errs []string
	for _, r := range rules {
		if r.Error != "" {
			errs = append(errs, fmt.Sprintf("line %d: %s", r.LineNumber, r.Error))
		}
	}
	return errs
}

// shellSingleQuote single-quotes a string for a POSIX shell.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeServerFile writes content to a server-side file via COPY ... TO PROGRAM
// and base64 -d (avoids COPY's text escaping). Requires superuser.
func writeServerFile(ctx context.Context, p Pinger, path, content string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	prog := "base64 -d > " + shellSingleQuote(path)
	sql := "COPY (SELECT " + QuoteLiteral(b64) + ") TO PROGRAM " + QuoteLiteral(prog)
	_, err := ExecAdmin(ctx, p, sql)
	return err
}

// ApplyHBAContent rewrites pg_hba.conf with newContent, guarded by a safety net:
// it backs up the file, writes the new content, checks pg_hba_file_rules for
// parse errors, reloads, and opens a FRESH admin connection to confirm login
// still works. Any failure restores the backup and reloads, so a bad edit can't
// lock you out. Returns a descriptive error on rollback.
func ApplyHBAContent(ctx context.Context, mgr *Manager, dsn, newContent string) error {
	admin, err := mgr.Pool(ctx, mgr.AdminDB())
	if err != nil {
		return err
	}
	path, err := HBAFilePath(ctx, admin)
	if err != nil {
		return fmt.Errorf("locate hba_file: %w", err)
	}
	old, err := HBAFileContent(ctx, admin)
	if err != nil {
		return fmt.Errorf("read pg_hba.conf: %w", err)
	}

	if err := writeServerFile(ctx, admin, path+".pgtui.bak", old); err != nil {
		return fmt.Errorf("could not write backup (aborted, nothing changed): %w", err)
	}
	rollback := func() {
		_ = writeServerFile(ctx, admin, path, old)
		_ = ReloadConf(ctx, admin)
	}

	if err := writeServerFile(ctx, admin, path, newContent); err != nil {
		return fmt.Errorf("write pg_hba.conf: %w", err)
	}

	rules, err := ListHBARules(ctx, admin)
	if err != nil {
		rollback()
		return fmt.Errorf("could not re-read rules (rolled back): %w", err)
	}
	if errs := hbaErrorLines(rules); len(errs) > 0 {
		rollback()
		return fmt.Errorf("new pg_hba.conf has parse errors (rolled back): %s", strings.Join(errs, "; "))
	}

	if err := ReloadConf(ctx, admin); err != nil {
		rollback()
		return fmt.Errorf("reload failed (rolled back): %w", err)
	}

	if err := verifyLogin(ctx, dsn); err != nil {
		rollback()
		return fmt.Errorf("admin login stopped working after the change — rolled back to keep you in: %w", err)
	}
	return nil
}

// verifyLogin opens a brand-new connection (re-running authentication) to prove
// the current admin credentials still work after an hba change.
func verifyLogin(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var one int
	return conn.QueryRow(ctx, "select 1").Scan(&one)
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
