package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFallsBackToLegacyName(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PGTUI_REFRESH_SECONDS", "7")
	if got := Env("REFRESH_SECONDS"); got != "7" {
		t.Errorf("PGTUI_* must still be read, got %q", got)
	}
	t.Setenv("PGTOWER_REFRESH_SECONDS", "3")
	if got := Env("REFRESH_SECONDS"); got != "3" {
		t.Errorf("PGTOWER_* must win over PGTUI_*, got %q", got)
	}
	found := false
	for _, k := range LegacyEnv() {
		found = found || k == "PGTUI_REFRESH_SECONDS"
	}
	if !found {
		t.Error("LegacyEnv should report PGTUI_* variables so the UI can suggest renaming them")
	}
}

// The old directory is moved whole when the new one does not exist yet.
func TestLegacyDirIsMovedWhole(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "pgtui"), filepath.Join(root, "pgtower")
	os.MkdirAll(from, 0o755)
	writeFile(t, filepath.Join(from, "config.yml"), "version: 2\nconnections:\n  - name: a\n    host: h\n")
	writeFile(t, filepath.Join(from, "no-update-check"), "x\n")
	withPairs(t, [2]string{from, to})

	moved, err := relocateLegacyDirs()
	if err != nil || len(moved) != 1 {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Error("nothing may be left behind in the old directory")
	}
	for _, f := range []string{"config.yml", "no-update-check"} {
		if _, err := os.Stat(filepath.Join(to, f)); err != nil {
			t.Errorf("%s should now live in the new directory", f)
		}
	}
}

// The installer may already have seeded an empty config in the new directory:
// the user's real config from the old one must win, and the old dir goes away.
func TestLegacyDirMergesOverInstallerSeed(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "pgtui"), filepath.Join(root, "pgtower")
	os.MkdirAll(from, 0o755)
	os.MkdirAll(to, 0o755)
	writeFile(t, filepath.Join(from, "config.yml"), "version: 2\nconnections:\n  - name: real\n    host: h\n")
	writeFile(t, filepath.Join(to, "config.yml"), "version: 2\nconnections: []\n")
	withPairs(t, [2]string{from, to})

	if _, err := relocateLegacyDirs(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(to, "config.yml")), "name: real") {
		t.Error("the real config must replace the installer's empty seed")
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Error("the emptied old directory must be removed")
	}
}

// Two real configs: keep the new one, keep the old one beside it, lose nothing.
func TestLegacyDirNeverOverwritesRealConfig(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "pgtui"), filepath.Join(root, "pgtower")
	os.MkdirAll(from, 0o755)
	os.MkdirAll(to, 0o755)
	writeFile(t, filepath.Join(from, "config.yml"), "version: 2\nconnections:\n  - name: old\n    host: h\n")
	writeFile(t, filepath.Join(to, "config.yml"), "version: 2\nconnections:\n  - name: new\n    host: h\n")
	withPairs(t, [2]string{from, to})

	if _, err := relocateLegacyDirs(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(to, "config.yml")), "name: new") {
		t.Error("an existing pgtower config must not be overwritten")
	}
	if !strings.Contains(readFile(t, filepath.Join(to, "config.yml.pgtui")), "name: old") {
		t.Error("the old config must be kept as config.yml.pgtui")
	}
}

func withPairs(t *testing.T, pairs ...[2]string) {
	t.Helper()
	orig := legacyDirPairs
	legacyDirPairs = func() [][2]string { return pairs }
	t.Cleanup(func() { legacyDirPairs = orig })
}
