package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptNameRenamesAndKeepsAlias(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "pgtui")
	os.WriteFile(old, []byte("new binary"), 0o755)

	res, err := adoptName(old)
	if err != nil || !res.Renamed || res.Manual != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "pgtower")); string(b) != "new binary" {
		t.Error("the binary must now be called pgtower")
	}
	if dst, err := os.Readlink(old); err != nil || dst != "pgtower" {
		t.Errorf("pgtui must become a relative symlink to pgtower, got %q %v", dst, err)
	}

	// Started again through the alias: nothing more to do.
	if res, _ := adoptName(old); res.Renamed {
		t.Error("an alias must not be renamed again")
	}
}

func TestAdoptNameReplacesLeftoverCopy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pgtower"), []byte("installed"), 0o755)
	old := filepath.Join(dir, "pgtui")
	os.WriteFile(old, []byte("stale copy"), 0o755)

	if res, err := adoptName(old); err != nil || !res.Renamed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "pgtower")); string(b) != "installed" {
		t.Error("an installed pgtower must not be overwritten by the leftover copy")
	}
	if dst, _ := os.Readlink(old); dst != "pgtower" {
		t.Error("the leftover pgtui copy must be replaced by the alias")
	}
}

func TestAdoptNameIgnoresNewName(t *testing.T) {
	if res, _ := AdoptName("/usr/local/bin/pgtower"); res.Renamed || res.Manual != "" {
		t.Error("running as pgtower must be a no-op")
	}
}
