package db

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func listenUnix(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	t.Cleanup(func() { l.Close() })
}

func TestSocketsIn(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	listenUnix(t, filepath.Join(a, ".s.PGSQL.5433"))
	listenUnix(t, filepath.Join(a, ".s.PGSQL.5432"))
	listenUnix(t, filepath.Join(b, ".s.PGSQL.6000"))
	// Neither a lock file nor a regular file named like a socket counts.
	for _, f := range []string{".s.PGSQL.5432.lock", ".s.PGSQL.7000"} {
		if err := os.WriteFile(filepath.Join(a, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}

	got := socketsIn([]string{a, link, "/nonexistent-pgtower", b})
	want := []LocalSocket{{a, 5432}, {a, 5433}, {b, 6000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("socketsIn = %v, want %v", got, want)
	}
}
