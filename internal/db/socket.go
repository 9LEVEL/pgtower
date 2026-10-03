package db

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SocketDirs are where PostgreSQL puts its unix socket by default: the
// Debian/Ubuntu and RHEL packages use /var/run/postgresql (/run/postgresql);
// source builds, Homebrew and Postgres.app use /tmp.
var SocketDirs = []string{"/var/run/postgresql", "/run/postgresql", "/tmp", "/private/tmp"}

// LocalSocket is a PostgreSQL unix socket found on this machine.
type LocalSocket struct {
	Dir  string // the directory, which is what goes in a connection's host
	Port int
}

// LocalSockets lists the PostgreSQL sockets in SocketDirs, in that order and
// lowest port first within a directory. A directory reached twice through a
// symlink (/var/run → /run) is listed once, under its first name.
func LocalSockets() []LocalSocket {
	return socketsIn(SocketDirs)
}

func socketsIn(dirs []string) []LocalSocket {
	var out []LocalSocket
	seen := map[string]bool{}
	for _, dir := range dirs {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] {
			continue
		}
		seen[real] = true
		matches, _ := filepath.Glob(filepath.Join(dir, ".s.PGSQL.*"))
		var found []LocalSocket
		for _, m := range matches {
			port, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(m), ".s.PGSQL."))
			if err != nil {
				continue // .s.PGSQL.5432.lock
			}
			if fi, err := os.Stat(m); err != nil || fi.Mode()&os.ModeSocket == 0 {
				continue
			}
			found = append(found, LocalSocket{Dir: dir, Port: port})
		}
		sort.Slice(found, func(i, j int) bool { return found[i].Port < found[j].Port })
		out = append(out, found...)
	}
	return out
}
