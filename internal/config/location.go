package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Where config.yml lives. Each user's file is in UserDir (~/.config/pgtower),
// and pgtower always saves there. The system directories are read after it,
// for admin-managed setups, and never written. Before v0.12 the installer put
// each user's config.yml in /opt/pgtower and pgtower saved there; Load moves
// such a file to UserDir (relocateConfig).

// systemDir and etcDir are the system-wide config directories (variables so
// tests can point them at temp dirs).
var (
	systemDir = "/opt/pgtower"
	etcDir    = "/etc/pgtower"
)

// legacyOptDir is pgtui's name for systemDir (see legacy.go).
const legacyOptDir = "/opt/pgtui"

// movedBackupSuffix marks the original of a config.yml moved to UserDir.
const movedBackupSuffix = ".moved.bak"

// UserDir is where pgtower keeps the user's config.yml and always saves it:
// $XDG_CONFIG_HOME/pgtower, else ~/.config/pgtower — on macOS too, so the
// path is the same everywhere. "" when the home directory is unknown.
func UserDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "pgtower")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "pgtower")
	}
	return ""
}

// platformDirPairs maps the per-OS directory pgtower used before v0.12
// (os.UserConfigDir: ~/Library/Application Support on macOS) to UserDir. On
// Linux both are ~/.config, so there is nothing to move. A variable so tests
// can point it at temp directories.
var platformDirPairs = func() [][2]string {
	base, err := os.UserConfigDir()
	to := UserDir()
	if err != nil || to == "" {
		return nil
	}
	from := filepath.Join(base, "pgtower")
	if sameDir(from, to) {
		return nil
	}
	return [][2]string{{from, to}}
}

// systemDirs are the admin-managed config directories: read, never saved to.
func systemDirs() []string {
	return []string{systemDir, etcDir, legacyOptDir, "/etc/pgtui"}
}

func inSystemDir(path string) bool {
	for _, d := range systemDirs() {
		if sameDir(filepath.Dir(path), d) {
			return true
		}
	}
	return false
}

func sameDir(a, b string) bool {
	x, _ := filepath.Abs(a)
	y, _ := filepath.Abs(b)
	return x == y
}

// relocateConfig moves the config.yml just loaded from /opt/pgtower (or pgtui's
// /opt/pgtui) to UserDir when it is this user's private file, the one an older
// installer or pgtower itself wrote there. A file owned by someone else, or
// readable by others, is admin-managed: it is read, never moved or written.
// The original is kept as config.yml.moved.bak.
func (s *Store) relocateConfig() {
	from := s.Path
	dir := filepath.Dir(from)
	if !(sameDir(dir, systemDir) || sameDir(dir, legacyOptDir)) || !privateToMe(from) {
		return
	}
	user := UserDir()
	if user == "" {
		return
	}
	to := filepath.Join(user, "config.yml")
	if _, err := os.Lstat(to); !errors.Is(err, os.ErrNotExist) {
		return // never replace a file there, even one this user cannot read
	}
	mig := s.Migration
	if mig == nil {
		mig = &Migration{Path: from}
		s.Migration = mig
	}
	if err := writeFileAtomic(to, renderConfig(s.file)); err != nil {
		mig.RelocateErr = fmt.Errorf("could not write %s: %w", to, err)
		return // keep using the original; the next start tries again
	}
	// Any upgrade of this run is now on disk, in its new place.
	s.Path, mig.Path, mig.Err = to, to, nil
	mig.Relocated = append(mig.Relocated, from+" → "+to)
	bak := uniquePath(from + movedBackupSuffix)
	if err := os.Rename(from, bak); err != nil {
		mig.RelocateErr = fmt.Errorf("%s could not be renamed (%v); it is no longer used, delete it", from, err)
		return
	}
	_ = os.Chmod(bak, 0o600)
	mig.Backups = append(mig.Backups, bak)
}
