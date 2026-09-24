// Package config loads pgtower's configuration: a list of named connections plus
// global settings, from config.yml and the environment. Precedence, highest
// first: environment variables > config.yml > built-in defaults.
//
// config.yml is owned by pgtower from format version 2 on: the Connections screen
// saves to it. Older (v1, single-connection) files are migrated in place on
// first run — see migrate.go.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultConfigDir is where the installer creates config.yml and where pgtower
// looks for it when no more specific location has one.
const DefaultConfigDir = "/opt/pgtower"

// FileVersion is the config.yml format written by this build.
const FileVersion = 2

// Connection tags. Prod connections are highlighted in the header.
const (
	TagNone    = ""
	TagDev     = "dev"
	TagStaging = "staging"
	TagProd    = "prod"
)

// Tags lists the valid connection tags, in display order.
var Tags = []string{TagNone, TagDev, TagStaging, TagProd}

// EnvConnectionName names the session-only connection built from DATABASE_URL
// or PG* variables. It is never written to config.yml.
const EnvConnectionName = "env"

// Connection is one named Postgres server. Either URL or the individual parts
// are set; the form in the Connections screen always writes parts.
type Connection struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url,omitempty"`
	Host        string `yaml:"host,omitempty"` // hostname, IP, or a unix-socket directory (/var/run/postgresql)
	Port        int    `yaml:"port,omitempty"`
	User        string `yaml:"user,omitempty"`
	Password    string `yaml:"password,omitempty"`
	PasswordEnv string `yaml:"password_env,omitempty"` // read the password from this variable instead
	Database    string `yaml:"database,omitempty"`
	SSLMode     string `yaml:"sslmode,omitempty"`
	Tag         string `yaml:"tag,omitempty"`
	// Per-server overrides of the global tuning-advisor host facts.
	HostRAMMB int `yaml:"host_ram_mb,omitempty"`
	HostCPUs  int `yaml:"host_cpus,omitempty"`
}

// Store is the loaded configuration: every known connection and the global
// settings. It is what the Connections screen edits and saves.
type Store struct {
	// Path is the config.yml this store was read from and saves to. Empty until
	// the first save when no file existed.
	Path string

	Default     string
	Connections []Connection

	// Env is the connection from DATABASE_URL / PG* variables, if any. It wins
	// at startup (env > file) but lives only for this process.
	Env *Connection

	RefreshSeconds  int
	SCRAMIterations int
	HostRAMMB       int
	HostCPUs        int
	UpdateCheck     bool

	// file-level values, kept so Save writes back exactly what the user set
	// (not the env-resolved values above).
	file fileV2

	// Migration is set when Load upgraded a legacy config during this run.
	Migration *Migration
}

// Config is the resolved, flat configuration of the active connection. The UI
// tabs consume it.
type Config struct {
	// Name and Tag identify the connection (header, prod highlight).
	Name string
	Tag  string

	// URL is the base DSN (postgres://user:pass@host:port/db?sslmode=...).
	// The database name is swapped at runtime when navigating between databases.
	URL string

	// AdminDB is the database used for cluster-level queries
	// (pg_database, pg_stat_activity, replication). Derived from the URL.
	AdminDB string

	// Host/Port only for display in the TUI header.
	Host string
	Port string
	User string

	// RefreshSeconds controls the dashboard auto-refresh.
	RefreshSeconds int

	// SCRAMIterations is the PBKDF2 round count for password resets. 0 means
	// "use the built-in default"; the db layer clamps it to a safe range.
	SCRAMIterations int

	// HostRAMMB and HostCPUs describe the server host (Postgres can't report
	// them via SQL). 0 = unknown; the tuning advisor then shows relative checks
	// only.
	HostRAMMB int
	HostCPUs  int

	// UpdateCheck enables the startup "a newer release is available" prompt.
	UpdateCheck bool

	// Version is the binary version (injected in main via -ldflags), shown
	// in the header. Filled in by whoever builds the Config.
	Version string
}

// fileV2 mirrors config.yml (format 2). Zero values mean "unset" so the
// environment can still override and Save omits them.
type fileV2 struct {
	Version         int          `yaml:"version"`
	Default         string       `yaml:"default,omitempty"`
	Connections     []Connection `yaml:"connections"`
	RefreshSeconds  int          `yaml:"refresh_seconds,omitempty"`
	SCRAMIterations int          `yaml:"scram_iterations,omitempty"`
	HostRAMMB       int          `yaml:"host_ram_mb,omitempty"`
	HostCPUs        int          `yaml:"host_cpus,omitempty"`
	UpdateCheck     *bool        `yaml:"update_check,omitempty"`
}

// Load finds config.yml (migrating a legacy one), then layers the environment
// on top. A missing file is not an error: the store is simply empty and the UI
// asks for a first connection.
func Load() (*Store, error) {
	s := &Store{}
	var moved []string
	var moveErr error
	if Env("CONFIG") == "" && Env("CONFIG_DIR") == "" {
		moved, moveErr = relocateLegacyDirs()
	}
	path, raw, err := findConfigFile()
	if err != nil {
		return nil, err
	}
	if path != "" {
		s.Path = path
		fc, mig, err := parseAndMigrate(path, raw)
		if err != nil {
			return nil, err
		}
		s.file = fc
		s.Migration = mig
	} else {
		// No config.yml at all, but a pre-v0.8 .env may still hold the
		// connection that an upgraded install silently lost.
		s.Migration = s.importLegacyDotenv()
	}

	if len(moved) > 0 || moveErr != nil {
		if s.Migration == nil {
			s.Migration = &Migration{Path: s.Path}
		}
		s.Migration.Moved = moved
		s.Migration.MoveErr = moveErr
	}

	s.Default = s.file.Default
	s.Connections = append([]Connection(nil), s.file.Connections...)
	s.RefreshSeconds = pickInt("REFRESH_SECONDS", s.file.RefreshSeconds, 5)
	s.SCRAMIterations = pickInt("SCRAM_ITERATIONS", s.file.SCRAMIterations, 0)
	s.HostRAMMB = pickInt("HOST_RAM_MB", s.file.HostRAMMB, 0)
	s.HostCPUs = pickInt("HOST_CPUS", s.file.HostCPUs, 0)
	s.UpdateCheck = pickBool("UPDATE_CHECK", s.file.UpdateCheck, true)

	if raw := strings.TrimSpace(os.Getenv("DATABASE_URL")); raw != "" {
		s.Env = &Connection{Name: EnvConnectionName, URL: raw}
	} else if c := connFromPGEnv(); c != nil {
		s.Env = c
	}
	return s, nil
}

// Find returns the connection with the given name (the env one included).
func (s *Store) Find(name string) (Connection, bool) {
	if s.Env != nil && name == s.Env.Name {
		return *s.Env, true
	}
	for _, c := range s.Connections {
		if c.Name == name {
			return c, true
		}
	}
	return Connection{}, false
}

// Names lists every connection name, the env one first.
func (s *Store) Names() []string {
	var out []string
	if s.Env != nil {
		out = append(out, s.Env.Name)
	}
	for _, c := range s.Connections {
		out = append(out, c.Name)
	}
	return out
}

// Initial picks the connection to open at startup: an explicit name (-s),
// then the environment, then the saved default, then the only one. ok=false
// means the user has to choose (or create) one.
func (s *Store) Initial(explicit string) (Connection, bool, error) {
	if explicit != "" {
		c, ok := s.Find(explicit)
		if !ok {
			return Connection{}, false, fmt.Errorf("no connection named %q (have: %s)",
				explicit, strings.Join(s.Names(), ", "))
		}
		return c, true, nil
	}
	if s.Env != nil {
		return *s.Env, true, nil
	}
	if c, ok := s.Find(s.Default); ok && s.Default != "" {
		return c, true, nil
	}
	if len(s.Connections) == 1 {
		return s.Connections[0], true, nil
	}
	return Connection{}, false, nil
}

// Resolve turns a connection into the flat Config the UI runs with.
func (s *Store) Resolve(c Connection, version string) (*Config, error) {
	raw, err := c.DSN()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("connection %q: invalid URL: %w", c.Name, err)
	}
	admin := strings.TrimPrefix(u.Path, "/")
	if admin == "" {
		admin = "postgres"
	}
	host := u.Hostname()
	if host == "" {
		host = u.Query().Get("host") // unix socket: postgres://user@/db?host=/var/run/postgresql
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	user := ""
	if u.User != nil {
		user = u.User.Username()
	}
	ram, cpus := s.HostRAMMB, s.HostCPUs
	if c.HostRAMMB > 0 {
		ram = c.HostRAMMB
	}
	if c.HostCPUs > 0 {
		cpus = c.HostCPUs
	}
	return &Config{
		Name:            c.Name,
		Tag:             c.Tag,
		URL:             raw,
		AdminDB:         admin,
		Host:            host,
		Port:            port,
		User:            user,
		RefreshSeconds:  s.RefreshSeconds,
		SCRAMIterations: s.SCRAMIterations,
		HostRAMMB:       ram,
		HostCPUs:        cpus,
		UpdateCheck:     s.UpdateCheck,
		Version:         version,
	}, nil
}

// Validate checks a connection before it is saved.
func (c Connection) Validate() error {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if name == EnvConnectionName {
		return fmt.Errorf("%q is reserved for the DATABASE_URL connection", EnvConnectionName)
	}
	if c.URL == "" && strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	_, err := c.DSN()
	return err
}

// DSN builds the connection URL, filling in the password from PasswordEnv
// when set.
func (c Connection) DSN() (string, error) {
	if raw := strings.TrimSpace(c.URL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" {
			return "", fmt.Errorf("connection %q: invalid URL", c.Name)
		}
		if pw := c.password(); pw != "" && u.User != nil {
			if _, has := u.User.Password(); !has {
				u.User = url.UserPassword(u.User.Username(), pw)
			}
		}
		return u.String(), nil
	}

	host := strings.TrimSpace(c.Host)
	if host == "" {
		return "", fmt.Errorf("connection %q: no host", c.Name)
	}
	port := c.Port
	if port == 0 {
		port = 5432
	}
	user := orDefault(c.User, "postgres")
	userinfo := url.User(user)
	if pw := c.password(); pw != "" {
		userinfo = url.UserPassword(user, pw)
	}
	q := url.Values{}
	u := url.URL{Scheme: "postgres", User: userinfo, Path: "/" + orDefault(c.Database, "postgres")}
	if strings.HasPrefix(host, "/") {
		q.Set("host", host) // unix-socket directory
		q.Set("port", fmt.Sprint(port))
	} else {
		u.Host = fmt.Sprintf("%s:%d", host, port)
	}
	q.Set("sslmode", orDefault(c.SSLMode, "disable"))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c Connection) password() string {
	if c.PasswordEnv != "" {
		if v := os.Getenv(c.PasswordEnv); v != "" {
			return v
		}
	}
	return c.Password
}

// Parts returns the connection as individual fields, decomposing URL when
// that is how it was stored (the form edits parts).
func (c Connection) Parts() Connection {
	if c.URL == "" {
		return c
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return c
	}
	p := c
	p.URL = ""
	p.Host = u.Hostname()
	if p.Host == "" {
		p.Host = u.Query().Get("host")
	}
	if port := u.Port(); port != "" {
		fmt.Sscanf(port, "%d", &p.Port)
	}
	if u.User != nil {
		p.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			p.Password = pw
		}
	}
	p.Database = strings.TrimPrefix(u.Path, "/")
	p.SSLMode = u.Query().Get("sslmode")
	return p
}

// Upsert adds a connection or replaces the one named oldName.
func (s *Store) Upsert(oldName string, c Connection) error {
	c.Name = strings.TrimSpace(c.Name)
	if err := c.Validate(); err != nil {
		return err
	}
	for _, x := range s.Connections {
		if x.Name == c.Name && x.Name != oldName {
			return fmt.Errorf("a connection named %q already exists", c.Name)
		}
	}
	for i, x := range s.Connections {
		if x.Name == oldName && oldName != "" {
			s.Connections[i] = c
			if s.Default == oldName {
				s.Default = c.Name
			}
			return nil
		}
	}
	s.Connections = append(s.Connections, c)
	return nil
}

// Remove deletes a connection (and clears it as default).
func (s *Store) Remove(name string) {
	out := s.Connections[:0]
	for _, c := range s.Connections {
		if c.Name != name {
			out = append(out, c)
		}
	}
	s.Connections = out
	if s.Default == name {
		s.Default = ""
	}
}

// findConfigFile returns the first config.yml found (path "" when none).
func findConfigFile() (string, []byte, error) {
	for _, p := range configPaths() {
		b, err := os.ReadFile(p)
		if err == nil {
			return p, b, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("read %s: %w", p, err)
		}
	}
	return "", nil, nil
}

// configPaths lists candidate config files, highest priority first.
// PGTOWER_CONFIG (a file) or PGTOWER_CONFIG_DIR (a directory) replace the search
// entirely; otherwise: the working directory, next to the binary, the user
// config dir, and the system locations.
func configPaths() []string {
	if f := Env("CONFIG"); f != "" {
		return []string{f}
	}
	var paths []string
	for _, dir := range configDirs() {
		paths = append(paths, filepath.Join(dir, "config.yml"), filepath.Join(dir, "config.yaml"))
	}
	return paths
}

func configDirs() []string {
	if d := Env("CONFIG_DIR"); d != "" {
		return []string{d}
	}
	dirs := []string{"."}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if d := userConfigDir(); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, DefaultConfigDir, "/etc/pgtower")
	// pgtower-era directories that could not be moved are still read, last.
	for _, p := range legacyDirPairs() {
		dirs = append(dirs, p[0])
	}
	return dirs
}

func userConfigDir() string {
	if home, err := os.UserConfigDir(); err == nil {
		return filepath.Join(home, "pgtower")
	}
	return ""
}

// connFromPGEnv builds the env connection from PGHOST/PGUSER/... .
func connFromPGEnv() *Connection {
	host := os.Getenv("PGHOST")
	if host == "" {
		return nil
	}
	c := &Connection{
		Name:     EnvConnectionName,
		Host:     host,
		User:     os.Getenv("PGUSER"),
		Password: os.Getenv("PGPASSWORD"),
		Database: os.Getenv("PGDATABASE"),
		SSLMode:  os.Getenv("PGSSLMODE"),
	}
	fmt.Sscanf(os.Getenv("PGPORT"), "%d", &c.Port)
	return c
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// pickInt resolves a positive integer setting: environment > config.yml > def.
func pickInt(key string, fileVal, def int) int {
	if v := Env(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	if fileVal > 0 {
		return fileVal
	}
	return def
}

// pickBool resolves a boolean setting: environment > config.yml > def.
func pickBool(key string, fileVal *bool, def bool) bool {
	if v := strings.ToLower(Env(key)); v != "" {
		switch v {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
}
