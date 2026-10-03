package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// pgtower was called pgtui up to v0.9. This file keeps installs from that era
// working: PGTUI_* variables are still read (after PGTOWER_*), and the old
// config directories are moved to their pgtower names on first run.

const (
	envPrefix       = "PGTOWER_"
	legacyEnvPrefix = "PGTUI_"
)

// Env returns the PGTOWER_<name> variable, falling back to the pre-v0.10
// PGTUI_<name> spelling.
func Env(name string) string {
	if v := strings.TrimSpace(os.Getenv(envPrefix + name)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(legacyEnvPrefix + name))
}

// LegacyEnv lists the PGTUI_* variables set in the environment, so the UI can
// suggest renaming them.
func LegacyEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, legacyEnvPrefix) {
			out = append(out, k)
		}
	}
	return out
}

// legacyDirPairs maps each pgtui config directory to its pgtower successor.
// The per-user one goes straight to UserDir. A variable so tests can point it
// at temp directories.
var legacyDirPairs = func() [][2]string {
	var pairs [][2]string
	if home, err := os.UserConfigDir(); err == nil && UserDir() != "" {
		pairs = append(pairs, [2]string{filepath.Join(home, "pgtui"), UserDir()})
	}
	return append(pairs,
		[2]string{legacyOptDir, systemDir},
		[2]string{"/etc/pgtui", etcDir})
}

// relocateLegacyDirs moves pgtui's config directories to their pgtower names.
// A directory that cannot be moved (e.g. /opt/pgtui owned by root while
// running as a user) is left in place and still read — see configDirs.
func relocateLegacyDirs() (moved []string, err error) {
	return relocateDirs(legacyDirPairs(), ".pgtui")
}

// relocateDirs moves each directory to its new path, or merges it into a new
// path that already exists; a file on both sides keeps the new one and the
// old one beside it, with suffix.
func relocateDirs(pairs [][2]string, suffix string) (moved []string, err error) {
	for _, p := range pairs {
		from, to := p[0], p[1]
		if fi, e := os.Stat(from); e != nil || !fi.IsDir() {
			continue
		}
		if _, e := os.Stat(to); errors.Is(e, os.ErrNotExist) {
			_ = os.MkdirAll(filepath.Dir(to), 0o755) // ~/.config may not exist yet
			if e := os.Rename(from, to); e != nil {
				err = errors.Join(err, fmt.Errorf("move %s to %s: %w", from, to, e))
				continue
			}
			moved = append(moved, from+" → "+to)
			continue
		}
		// Both exist (e.g. the installer already seeded the new one): move the
		// old files over, then drop the old directory once it is empty.
		if e := mergeDir(from, to, suffix); e != nil {
			err = errors.Join(err, e)
			continue
		}
		if os.Remove(from) == nil {
			moved = append(moved, from+" → "+to)
		}
	}
	return moved, err
}

func mergeDir(from, to, suffix string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, en := range entries {
		src, dst := filepath.Join(from, en.Name()), filepath.Join(to, en.Name())
		_, statErr := os.Stat(dst)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
		case isConfigName(en.Name()) && !hasConnections(dst):
			// The new file is only the installer's empty seed: the old one wins.
		default:
			// Both hold real data: keep the new file, keep the old one beside it.
			dst = uniquePath(dst + suffix)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("move %s to %s: %w", src, dst, err)
		}
	}
	return nil
}

func isConfigName(n string) bool { return n == "config.yml" || n == "config.yaml" }

func hasConnections(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var lf legacyFile
	if yaml.Unmarshal(b, &lf) != nil {
		return true // unreadable: treat as real data, never overwrite it
	}
	return len(lf.Connections) > 0 || lf.DatabaseURL != "" || lf.Host != ""
}
