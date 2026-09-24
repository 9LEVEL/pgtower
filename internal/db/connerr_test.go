package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestExplainConnectKinds(t *testing.T) {
	opErr := func(errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
	}
	cases := []struct {
		name string
		err  error
		host string
		want ConnErrorKind
		hint string
	}{
		{"deadline (unrouted host, packets dropped)", fmt.Errorf("ping %q: %w", "postgres", context.DeadlineExceeded),
			"192.168.150.171", ConnTimeout, "nc -vz 192.168.150.171 5432"},
		{"refused", opErr(syscall.ECONNREFUSED), "10.0.0.1", ConnRefused, "listen_addresses"},
		{"no route", opErr(syscall.EHOSTUNREACH), "10.0.0.1", ConnUnreachable, "ip route get 10.0.0.1"},
		{"net unreachable", opErr(syscall.ENETUNREACH), "10.0.0.1", ConnUnreachable, "VPN"},
		{"dns", &net.DNSError{Err: "no such host", Name: "db.nope", IsNotFound: true}, "db.nope", ConnUnknownHost, "IP address"},
		{"socket missing", opErr(syscall.ENOENT), "/var/run/postgresql", ConnNoSocket, "systemctl status postgresql"},
		{"bad password", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}, "h", ConnAuth, "credentials"},
		{"pg_hba", &pgconn.PgError{Code: "28000", Message: "no pg_hba.conf entry"}, "h", ConnRejected, "pg_hba.conf"},
		{"no db", &pgconn.PgError{Code: "3D000", Message: `database "x" does not exist`}, "h", ConnNoDatabase, "postgres"},
		{"tls refused", errors.New("server refused TLS connection"), "h", ConnTLS, "sslmode"},
		{"unknown", errors.New("boom"), "h", ConnOther, "technical details"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ExplainConnect(c.err, c.host, "5432")
			if e.Kind != c.want {
				t.Fatalf("kind = %s, want %s (%s)", e.Kind, c.want, e.Error())
			}
			if e.Title == "" || e.Detail == "" {
				t.Errorf("title and detail must be set: %+v", e)
			}
			if !strings.Contains(e.Hint, c.hint) {
				t.Errorf("hint %q should mention %q", e.Hint, c.hint)
			}
			if !errors.Is(e, c.err) {
				t.Error("the raw error must stay reachable through Unwrap")
			}
		})
	}
	if ExplainConnect(nil, "h", "1") != nil {
		t.Error("nil in, nil out")
	}
}

// A real dial against a closed local port goes through pgx's own error
// wrapping and must still be recognised.
func TestExplainConnectRealRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close() // now nothing listens there

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = NewManager(ctx, "postgres://u@127.0.0.1:"+port+"/postgres?sslmode=disable", "postgres")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if e := ExplainConnect(err, "127.0.0.1", port); e.Kind != ConnRefused {
		t.Errorf("kind = %s, want refused (raw: %v)", e.Kind, err)
	}

	_, err = Probe(ctx, "postgres://u@127.0.0.1:"+port+"/postgres?sslmode=disable", "127.0.0.1", port)
	var ce *ConnError
	if !errors.As(err, &ce) || ce.Kind != ConnRefused {
		t.Errorf("Probe should return an explained error, got %v", err)
	}
}
