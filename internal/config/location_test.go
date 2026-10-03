package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateSearch runs the default search (no PGTOWER_CONFIG / _DIR) over temp
// directories only: the user dir (through XDG_CONFIG_HOME) and the system
// dirs, with no pgtui-era or macOS directories. Returns the user and system
// dirs.
func isolateSearch(t *testing.T) (user, system string) {
	t.Helper()
	isolateEnv(t)
	t.Setenv("PGTOWER_CONFIG_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	origSys, origEtc, origPlat := systemDir, etcDir, platformDirPairs
	systemDir, etcDir = t.TempDir(), t.TempDir()
	platformDirPairs = func() [][2]string { return nil }
	t.Cleanup(func() { systemDir, etcDir, platformDirPairs = origSys, origEtc, origPlat })
	withPairs(t)
	return filepath.Join(xdg, "pgtower"), systemDir
}

const oneServer = "version: 2\nconnections:\n  - name: db1\n    host: 10.0.0.1\n"

func writePrivate(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUserDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/y")
	if got := UserDir(); got != "/x/y/pgtower" {
		t.Errorf("with XDG_CONFIG_HOME: %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/is/ignored")
	t.Setenv("HOME", "/home/ana")
	if got := UserDir(); got != "/home/ana/.config/pgtower" {
		t.Errorf("without XDG_CONFIG_HOME: %s", got)
	}
}

func TestSaveGoesToUserDir(t *testing.T) {
	user, _ := isolateSearch(t)
	s := mustLoad(t)
	s.Connections = []Connection{{Name: "a", Host: "h"}}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(user, "config.yml")
	if s.Path != p {
		t.Errorf("saved to %s, want %s", s.Path, p)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config must be written 0600: %v %v", fi, err)
	}
}

// The upgrade path of every install before v0.12: the user's own config.yml
// in /opt/pgtower moves to ~/.config/pgtower, original kept as a backup.
func TestOwnSystemConfigMovesToUserDir(t *testing.T) {
	user, system := isolateSearch(t)
	old := filepath.Join(system, "config.yml")
	writePrivate(t, old, oneServer)

	s := mustLoad(t)
	want := filepath.Join(user, "config.yml")
	if s.Path != want || len(s.Connections) != 1 {
		t.Fatalf("now using %s with %d servers, want %s with 1", s.Path, len(s.Connections), want)
	}
	if !strings.Contains(readFile(t, want), "name: db1") {
		t.Error("the servers must come along")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the original must not stay in place, where it would keep being found")
	}
	bak := old + movedBackupSuffix
	if !strings.Contains(readFile(t, bak), "name: db1") {
		t.Error("the original must be kept as config.yml.moved.bak")
	}
	mig := s.Migration
	if mig == nil || len(mig.Relocated) != 1 || mig.RelocateErr != nil || len(mig.Backups) != 1 {
		t.Fatalf("the move must be reported once: %+v", mig)
	}

	if s := mustLoad(t); s.Migration != nil || s.Path != want {
		t.Errorf("second run: nothing left to move, got %+v at %s", s.Migration, s.Path)
	}
}

// A config readable by others (or owned by someone else) is an admin-managed
// one: read it, never move or write it; edits are saved to the user's own.
func TestSharedSystemConfigIsReadNotMoved(t *testing.T) {
	user, system := isolateSearch(t)
	shared := filepath.Join(system, "config.yml")
	writeFile(t, shared, oneServer) // 0644

	s := mustLoad(t)
	if s.Path != shared || s.Migration != nil {
		t.Fatalf("a shared config must be read in place: %s %+v", s.Path, s.Migration)
	}
	if want := filepath.Join(user, "config.yml"); s.SavePath() != want {
		t.Errorf("SavePath = %s, want %s", s.SavePath(), want)
	}
	s.Connections = append(s.Connections, Connection{Name: "mine", Host: "h2"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(user, "config.yml")); !strings.Contains(got, "name: mine") {
		t.Errorf("edits belong in the user's own config.yml:\n%s", got)
	}
	if readFile(t, shared) != oneServer {
		t.Error("the shared config must not be written")
	}
}

func TestV1SystemConfigIsUpgradedAndMoved(t *testing.T) {
	user, system := isolateSearch(t)
	writePrivate(t, filepath.Join(system, "config.yml"), "database_url: postgres://u@10.0.0.5/postgres\n")

	s := mustLoad(t)
	mig := s.Migration
	if mig == nil || len(mig.From) == 0 || len(mig.Relocated) != 1 || mig.Err != nil {
		t.Fatalf("want an upgrade and a move: %+v", mig)
	}
	want := filepath.Join(user, "config.yml")
	if s.Path != want || mig.Path != want {
		t.Errorf("now using %s (notice says %s), want %s", s.Path, mig.Path, want)
	}
	if got := readFile(t, want); !strings.Contains(got, "version: 2") || !strings.Contains(got, "10.0.0.5") {
		t.Errorf("the upgraded config must land in the user dir:\n%s", got)
	}
}

// ~/.config/pgtower/config.yml exists but this user cannot read it (left by a
// "sudo pgtower"): it must not be replaced, by the move or by a save.
func TestRelocationNeverOverwrites(t *testing.T) {
	user, system := isolateSearch(t)
	mine, old := filepath.Join(user, "config.yml"), filepath.Join(system, "config.yml")
	writePrivate(t, mine, "theirs\n")
	writePrivate(t, old, oneServer)
	stubReads(t, []string{mine}, old, oneServer)

	s := mustLoad(t)
	if s.Path != old || s.Migration != nil {
		t.Fatalf("with the target taken, keep using the original: %s %+v", s.Path, s.Migration)
	}
	if err := s.Save(); err == nil {
		t.Error("saving over the unreadable user config must fail")
	}
	if readFile(t, mine) != "theirs\n" || readFile(t, old) != oneServer {
		t.Error("neither file may change")
	}
}

// macOS kept the config in ~/Library/Application Support/pgtower before v0.12.
func TestPlatformDirMovesToUserDir(t *testing.T) {
	user, _ := isolateSearch(t)
	old := filepath.Join(t.TempDir(), "Application Support", "pgtower")
	writePrivate(t, filepath.Join(old, "config.yml"), oneServer)
	writePrivate(t, filepath.Join(old, "no-update-check"), "")
	platformDirPairs = func() [][2]string { return [][2]string{{old, user}} }

	s := mustLoad(t)
	if s.Path != filepath.Join(user, "config.yml") || len(s.Connections) != 1 {
		t.Fatalf("now using %s with %d servers", s.Path, len(s.Connections))
	}
	if _, err := os.Stat(filepath.Join(user, "no-update-check")); err != nil {
		t.Error("the whole directory moves, markers included")
	}
	if s.Migration == nil || len(s.Migration.Relocated) != 1 {
		t.Errorf("the move must be reported: %+v", s.Migration)
	}
}
