package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Migration describes a legacy configuration upgraded during Load. The UI
// shows it once, on the run that performed it.
type Migration struct {
	From     []string // what was migrated, e.g. "config.yml (v1)", ".env"
	Path     string   // the config.yml now in use
	Backups  []string // originals, kept (0600) next to the new file
	Imported []string // connection names created
	// Err is set when the upgraded file could not be written; pgtower then runs
	// from the in-memory upgrade and retries on the next save.
	Err error

	// Moved lists pgtower-era config directories moved to their pgtower names;
	// MoveErr is set when one could not be moved (it is still read in place).
	Moved   []string
	MoveErr error
}

// legacyFile is config.yml as written by v0.8 and earlier: one connection at
// the top level, no version key.
type legacyFile struct {
	fileV2      `yaml:",inline"`
	DatabaseURL string `yaml:"database_url"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	User        string `yaml:"user"`
	Password    string `yaml:"password"`
	Database    string `yaml:"database"`
	SSLMode     string `yaml:"sslmode"`
}

const legacyBackupSuffix = ".v1.bak"

// parseAndMigrate reads config.yml; a legacy (unversioned) file is converted to
// format 2, the original backed up, and the new file written in its place.
func parseAndMigrate(path string, raw []byte) (fileV2, *Migration, error) {
	var lf legacyFile
	if err := yaml.Unmarshal(raw, &lf); err != nil {
		return fileV2{}, nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if lf.Version > FileVersion {
		return fileV2{}, nil, fmt.Errorf("%s uses config format %d, but this pgtower only knows format %d — update pgtower",
			path, lf.Version, FileVersion)
	}
	if lf.Version == FileVersion {
		return lf.fileV2, nil, nil
	}

	// Legacy: lift the single top-level connection into the list.
	fc := lf.fileV2
	fc.Version = FileVersion
	mig := &Migration{From: []string{"config.yml (v1)"}, Path: path}
	c := Connection{URL: strings.TrimSpace(lf.DatabaseURL), Host: lf.Host, Port: lf.Port,
		User: lf.User, Password: lf.Password, Database: lf.Database, SSLMode: lf.SSLMode}
	// A pre-v0.8 .env next to it has been ignored since v0.8. If the v1 file
	// only had the commented template, the real connection is still there.
	// Only pgtower's own directories are touched: a .env in the working
	// directory most likely belongs to another project.
	var envPath string
	if dir := filepath.Dir(path); ownedDir(dir) {
		p, env := legacyDotenv(dir)
		if isPgtowerDotenv(env) {
			envPath = p
			if c.URL == "" && c.Host == "" && env["DATABASE_URL"] != "" {
				c.URL = env["DATABASE_URL"]
				applyDotenvSettings(&fc, env)
			}
			mig.From = append(mig.From, ".env")
		}
	}
	if c.URL != "" || c.Host != "" {
		c.Name = nameFromConn(c)
		fc.Connections = []Connection{c}
		fc.Default = c.Name
		mig.Imported = []string{c.Name}
	}

	mig.Err = commitMigration(path, raw, fc, envPath, mig)
	return fc, mig, nil
}

// importLegacyDotenv handles installs with no config.yml at all but a pre-v0.8
// .env in one of pgtower's own config directories (never the working directory:
// a .env there most likely belongs to another project).
func (s *Store) importLegacyDotenv() *Migration {
	for _, dir := range ownedDirs() {
		envPath, env := legacyDotenv(dir)
		if env["DATABASE_URL"] == "" {
			continue
		}
		c := Connection{URL: env["DATABASE_URL"]}
		c.Name = nameFromConn(c)
		fc := fileV2{Version: FileVersion, Default: c.Name, Connections: []Connection{c}}
		applyDotenvSettings(&fc, env)
		path := filepath.Join(dir, "config.yml")
		mig := &Migration{From: []string{".env"}, Path: path, Imported: []string{c.Name}}
		mig.Err = commitMigration(path, nil, fc, envPath, mig)
		s.Path, s.file = path, fc
		return mig
	}
	return nil
}

// commitMigration backs up the originals, writes the new config.yml and
// retires the .env, in an order that never loses data: nothing is removed
// until the new file is safely on disk.
func commitMigration(path string, raw []byte, fc fileV2, envPath string, mig *Migration) error {
	if raw != nil {
		bak, err := backupFile(path, raw)
		if err != nil {
			return err
		}
		mig.Backups = append(mig.Backups, bak)
	}
	if err := writeFileAtomic(path, renderConfig(fc)); err != nil {
		return err
	}
	if envPath != "" {
		bak := uniquePath(envPath + legacyBackupSuffix)
		if err := os.Rename(envPath, bak); err != nil {
			return fmt.Errorf("retire %s: %w", envPath, err)
		}
		_ = os.Chmod(bak, 0o600)
		mig.Backups = append(mig.Backups, bak)
	}
	return nil
}

// ownedDirs are the config directories that belong to pgtower alone.
func ownedDirs() []string {
	if d := Env("CONFIG_DIR"); d != "" {
		return []string{d}
	}
	var dirs []string
	if d := userConfigDir(); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, DefaultConfigDir, "/etc/pgtower")
	for _, p := range legacyDirPairs() {
		dirs = append(dirs, p[0])
	}
	return dirs
}

func ownedDir(dir string) bool {
	abs, _ := filepath.Abs(dir)
	for _, d := range ownedDirs() {
		if a, _ := filepath.Abs(d); a == abs {
			return true
		}
	}
	return false
}

// isPgtowerDotenv reports whether a .env carries pgtower (or pgtui-era) settings.
func isPgtowerDotenv(env map[string]string) bool {
	for k := range env {
		if k == "DATABASE_URL" || strings.HasPrefix(k, legacyEnvPrefix) || strings.HasPrefix(k, envPrefix) {
			return true
		}
	}
	return false
}

func backupFile(path string, raw []byte) (string, error) {
	bak := uniquePath(path + legacyBackupSuffix)
	if err := os.WriteFile(bak, raw, 0o600); err != nil {
		return "", fmt.Errorf("back up %s: %w", path, err)
	}
	return bak, nil
}

// uniquePath returns p, or p.1, p.2 … if p already exists.
func uniquePath(p string) string {
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	for i := 1; ; i++ {
		q := fmt.Sprintf("%s.%d", p, i)
		if _, err := os.Stat(q); errors.Is(err, os.ErrNotExist) {
			return q
		}
	}
}

// legacyDotenv reads dir/.env (KEY=VALUE lines, as godotenv did up to v0.7).
func legacyDotenv(dir string) (string, map[string]string) {
	p := filepath.Join(dir, ".env")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil
	}
	env := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		env[strings.TrimSpace(k)] = v
	}
	return p, env
}

func applyDotenvSettings(fc *fileV2, env map[string]string) {
	// .env files predate the rename, so their keys are PGTUI_*.
	set := func(key string, dst *int) {
		v := env[legacyEnvPrefix+key]
		if v == "" {
			v = env[envPrefix+key]
		}
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && *dst == 0 {
			*dst = n
		}
	}
	set("REFRESH_SECONDS", &fc.RefreshSeconds)
	set("SCRAM_ITERATIONS", &fc.SCRAMIterations)
	set("HOST_RAM_MB", &fc.HostRAMMB)
	set("HOST_CPUS", &fc.HostCPUs)
}

// nameFromConn derives a readable name for a migrated connection: its host.
func nameFromConn(c Connection) string {
	host := c.Host
	if c.URL != "" {
		if u, err := url.Parse(c.URL); err == nil {
			host = u.Hostname()
			if host == "" {
				host = u.Query().Get("host")
			}
		}
	}
	host = strings.TrimSpace(host)
	if host == "" || strings.HasPrefix(host, "/") {
		return "local"
	}
	return host
}

// Save writes the store to config.yml (format 2, mode 0600 — it may hold
// passwords). The first save without an existing file picks DefaultConfigDir
// when writable, else the user config dir.
func (s *Store) Save() error {
	fc := s.file
	fc.Version = FileVersion
	fc.Default = s.Default
	fc.Connections = s.Connections
	path := s.Path
	if path == "" {
		path = defaultSavePath()
	}
	if err := writeFileAtomic(path, renderConfig(fc)); err != nil {
		return err
	}
	s.Path, s.file = path, fc
	if s.Migration != nil && s.Migration.Err != nil {
		s.Migration.Err = nil // the in-memory upgrade is now on disk
	}
	return nil
}

func defaultSavePath() string {
	if f := Env("CONFIG"); f != "" {
		return f
	}
	if d := Env("CONFIG_DIR"); d != "" {
		return filepath.Join(d, "config.yml")
	}
	if dirWritable(DefaultConfigDir) {
		return filepath.Join(DefaultConfigDir, "config.yml")
	}
	return filepath.Join(userConfigDir(), "config.yml")
}

func dirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".pgtower-probe-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

const configHeader = `# pgtower configuration — https://github.com/9level/pgtower
#
# Managed by pgtower: the Servers screen (press S) saves here. Hand edits
# are fine, but comments other than this header are not preserved.
# Environment variables (DATABASE_URL, PGTOWER_*) still take precedence.
# All keys are documented in config.yml.example.

`

func renderConfig(fc fileV2) []byte {
	var buf bytes.Buffer
	buf.WriteString(configHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(fc)
	_ = enc.Close()
	return buf.Bytes()
}

// writeFileAtomic replaces path via a temp file + rename, mode 0600.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config.yml.tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
