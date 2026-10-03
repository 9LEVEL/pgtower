//go:build unix

package config

import (
	"os"
	"syscall"
)

// privateToMe reports whether path is a regular file owned by this user that
// nobody else may read (no group or other permission bits). A variable so
// tests can simulate files owned by someone else.
var privateToMe = func(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
