package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ConnErrorKind classifies why a connection could not be established.
type ConnErrorKind string

const (
	ConnTimeout     ConnErrorKind = "timeout"
	ConnRefused     ConnErrorKind = "refused"
	ConnUnreachable ConnErrorKind = "unreachable"
	ConnUnknownHost ConnErrorKind = "unknown-host"
	ConnNoSocket    ConnErrorKind = "no-socket"
	ConnAuth        ConnErrorKind = "auth"
	ConnRejected    ConnErrorKind = "rejected"
	ConnNoDatabase  ConnErrorKind = "no-database"
	ConnTooMany     ConnErrorKind = "too-many"
	ConnStarting    ConnErrorKind = "starting"
	ConnTLS         ConnErrorKind = "tls"
	ConnOther       ConnErrorKind = "other"
)

// ConnError is a connection failure explained for a human: what happened, the
// likely cause, and what to check next. The raw driver error stays available
// via Unwrap (and Cause) for the details view.
type ConnError struct {
	Kind   ConnErrorKind
	Title  string
	Detail string
	Hint   string
	Err    error
}

func (e *ConnError) Error() string { return e.Title + ": " + e.Detail }
func (e *ConnError) Unwrap() error { return e.Err }

// Cause is the underlying driver error text, for "technical details".
func (e *ConnError) Cause() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// ExplainConnect turns a raw connect/ping error into a ConnError. host and
// port are what the user configured (a unix-socket directory is a valid host).
func ExplainConnect(err error, host, port string) *ConnError {
	if err == nil {
		return nil
	}
	var ce *ConnError
	if errors.As(err, &ce) {
		return ce
	}
	target := host + ":" + port
	socket := strings.HasPrefix(host, "/")
	if socket {
		target = fmt.Sprintf("%s/.s.PGSQL.%s", strings.TrimRight(host, "/"), port)
	}
	e := &ConnError{Kind: ConnOther, Err: err}

	var pg *pgconn.PgError
	var dns *net.DNSError
	switch {
	case errors.As(err, &pg):
		explainPg(e, pg)

	case errors.As(err, &dns):
		e.Kind, e.Title = ConnUnknownHost, "Unknown host"
		e.Detail = fmt.Sprintf("The name %q could not be resolved.", host)
		e.Hint = "Check the host name for typos, or use the server's IP address."

	case socket && (errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)):
		e.Kind, e.Title = ConnNoSocket, "PostgreSQL socket not found"
		e.Detail = fmt.Sprintf("There is no PostgreSQL socket at %s.", target)
		e.Hint = "Check that the local server is running (systemctl status postgresql) and the socket directory/port."

	case errors.Is(err, syscall.ECONNREFUSED):
		e.Kind, e.Title = ConnRefused, "Connection refused"
		e.Detail = fmt.Sprintf("%s is reachable, but nothing is accepting connections on port %s.", host, port)
		e.Hint = "Check that PostgreSQL is running and listening on that port " +
			"(listen_addresses / port in postgresql.conf), and that the port is right."

	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		e.Kind, e.Title = ConnUnreachable, "No route to the server"
		e.Detail = fmt.Sprintf("This machine has no network path to %s.", host)
		e.Hint = "Check the VPN / routing towards that network: ip route get " + host

	case isTimeout(err):
		e.Kind, e.Title = ConnTimeout, "Server did not respond"
		e.Detail = fmt.Sprintf("No answer from %s within the connect timeout.", target)
		e.Hint = "The host may be down, unreachable from this machine (missing route or VPN), " +
			"or a firewall is silently dropping port " + port + ". Check with: nc -vz " + host + " " + port

	case strings.Contains(err.Error(), "server refused TLS connection"):
		e.Kind, e.Title = ConnTLS, "Server does not accept TLS"
		e.Detail = "The connection requires SSL (sslmode), but the server has SSL disabled."
		e.Hint = "Use sslmode prefer or disable for this connection, or enable ssl on the server."

	case strings.Contains(err.Error(), "tls:") || strings.Contains(err.Error(), "x509:"):
		e.Kind, e.Title = ConnTLS, "TLS handshake failed"
		e.Detail = "The server's certificate could not be verified."
		e.Hint = "With sslmode verify-ca/verify-full the server certificate must match; use require to encrypt without verifying."

	default:
		e.Title = "Could not connect"
		e.Detail = fmt.Sprintf("Connecting to %s failed.", target)
		e.Hint = "See the technical details below."
	}
	return e
}

func explainPg(e *ConnError, pg *pgconn.PgError) {
	switch pg.Code {
	case "28P01":
		e.Kind, e.Title = ConnAuth, "Authentication failed"
		e.Detail = "The server rejected the user name or password."
		e.Hint = "Check the credentials of this connection."
	case "28000":
		e.Kind, e.Title = ConnRejected, "Connection not allowed"
		e.Detail = pg.Message
		e.Hint = "The server's pg_hba.conf has no rule for this user/database/address, " +
			"or peer authentication does not match the OS user."
	case "3D000":
		e.Kind, e.Title = ConnNoDatabase, "Database does not exist"
		e.Detail = pg.Message
		e.Hint = "Set the connection's database to one that exists (postgres usually does)."
	case "53300":
		e.Kind, e.Title = ConnTooMany, "Server is out of connections"
		e.Detail = pg.Message
		e.Hint = "max_connections is exhausted; close idle sessions or raise the limit."
	case "57P03":
		e.Kind, e.Title = ConnStarting, "Server is not ready"
		e.Detail = pg.Message
		e.Hint = "The server is starting up, shutting down or in recovery; try again shortly."
	default:
		e.Title = "Server refused the connection"
		e.Detail = pg.Message
		e.Hint = pg.Hint
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ProbeResult is what "test connection" reports.
type ProbeResult struct {
	Latency time.Duration
	Version string
}

// Probe opens one short-lived connection, reads the server version and closes
// it. Errors come back already explained.
func Probe(ctx context.Context, dsn, host, port string) (ProbeResult, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return ProbeResult{}, &ConnError{Kind: ConnOther, Title: "Invalid connection settings",
			Detail: err.Error(), Err: err}
	}
	cfg.ConnectTimeout = connectTimeout
	cfg.RuntimeParams["application_name"] = "pgtui"

	start := time.Now()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return ProbeResult{}, ExplainConnect(err, host, port)
	}
	defer conn.Close(context.Background())
	var ver string
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version')").Scan(&ver); err != nil {
		return ProbeResult{}, ExplainConnect(err, host, port)
	}
	return ProbeResult{Latency: time.Since(start), Version: ver}, nil
}
