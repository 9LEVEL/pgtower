package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// LegacyName is what the binary was called before v0.10.
const LegacyName = "pgtui"

// NameResult reports what AdoptName did.
type NameResult struct {
	Renamed bool   // the binary now lives at Path
	Path    string // …/pgtower
	Alias   string // …/pgtui, now a symlink kept for old scripts ("" if none)
	Manual  string // instructions when it could not be done automatically
}

// AdoptName gives a pgtui-named binary its pgtower name. It runs when the
// program was started as "pgtui" — typically right after a v0.9 self-update,
// which replaces the old file in place. The binary is moved to "pgtower" in
// the same directory and "pgtui" becomes a symlink to it, so existing scripts
// and habits keep working. Nothing happens when already running as pgtower.
func AdoptName(arg0 string) (NameResult, error) {
	if filepath.Base(arg0) != LegacyName {
		return NameResult{}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return NameResult{}, err
	}
	return adoptName(exe)
}

func adoptName(exe string) (NameResult, error) {
	if fi, err := os.Lstat(exe); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return NameResult{}, err // already an alias to the real binary
	}
	if filepath.Base(exe) != LegacyName {
		return NameResult{}, nil
	}
	dir := filepath.Dir(exe)
	target := filepath.Join(dir, "pgtower")
	res := NameResult{Path: target}

	if _, err := os.Stat(target); err == nil {
		// A pgtower binary is already installed next to it: the old file is
		// just a leftover copy. Replace it with the alias.
		if err := replaceWithAlias(exe); err != nil {
			res.Manual = manualRename(dir)
			return res, nil
		}
		res.Renamed, res.Alias = true, exe
		return res, nil
	}
	if err := os.Rename(exe, target); err != nil {
		res.Manual = manualRename(dir)
		return res, nil
	}
	res.Renamed = true
	if err := os.Symlink("pgtower", exe); err == nil {
		res.Alias = exe
	}
	return res, nil
}

func replaceWithAlias(exe string) error {
	tmp := exe + ".alias.tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink("pgtower", tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func manualRename(dir string) string {
	return fmt.Sprintf("Finish the rename with root rights:\n\n"+
		"  sudo mv %[1]s/pgtui %[1]s/pgtower && sudo ln -s pgtower %[1]s/pgtui\n\n"+
		"or re-run the installer: curl -fsSL https://pgtower.sh | sh", dir)
}
