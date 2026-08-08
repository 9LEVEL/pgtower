// Package config loads the TUI configuration from a config.yml file and the
// environment. Precedence, highest first: environment variables > config.yml >
// built-in defaults.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigDir is where the installer creates config.yml and where pgtui
// looks for it when no more specific location has one.
const DefaultConfigDir = "/opt/pgtui"

// Config holds the connection parameters for the managed Postgres cluster.
type Config struct {
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
	// only. From PGTUI_HOST_RAM_MB / PGTUI_HOST_CPUS.
	HostRAMMB int
	HostCPUs  int

	// UpdateCheck enables the startup "a newer release is available" prompt.
	// Default true; set false in config.yml (update_check: false) or via
	// PGTUI_UPDATE_CHECK=0 to disable it entirely.
	UpdateCheck bool

	// Version is the binary version (injected in main via -ldflags), shown
	// in the header. Filled in by whoever builds the Config.
	Version string
}

// fileConfig mirrors config.yml. Pointer/zero values distinguish "unset" from
// "set to the zero value" so the environment can still override.
type fileConfig struct {
	DatabaseURL     string `yaml:"database_url"`
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	Database        string `yaml:"database"`
	SSLMode         string `yaml:"sslmode"`
	RefreshSeconds  int    `yaml:"refresh_seconds"`
	SCRAMIterations int    `yaml:"scram_iterations"`
	HostRAMMB       int    `yaml:"host_ram_mb"`
	HostCPUs        int    `yaml:"host_cpus"`
	UpdateCheck     *bool  `yaml:"update_check"`
}

// Load reads config.yml from the search directories, then builds the Config
// with the environment taking precedence.
func Load() (*Config, error) {
	fc := loadFileConfig()

	raw := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if raw == "" {
		raw = strings.TrimSpace(fc.DatabaseURL)
	}
	if raw == "" {
		raw = buildFromParts() // PGHOST/PGUSER/... in the environment
	}
	if raw == "" {
		raw = fc.dsn() // host/user/... from config.yml
	}
	if raw == "" {
		return nil, errors.New("no connection configured: set DATABASE_URL (or PGHOST/PGUSER/...), " +
			"or put database_url in config.yml (see " + DefaultConfigDir + "/config.yml)")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}

	admin := strings.TrimPrefix(u.Path, "/")
	if admin == "" {
		admin = "postgres"
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	user := ""
	if u.User != nil {
		user = u.User.Username()
	}

	return &Config{
		URL:             raw,
		AdminDB:         admin,
		Host:            u.Hostname(),
		Port:            port,
		User:            user,
		RefreshSeconds:  pickInt("PGTUI_REFRESH_SECONDS", fc.RefreshSeconds, 5),
		SCRAMIterations: pickInt("PGTUI_SCRAM_ITERATIONS", fc.SCRAMIterations, 0),
		HostRAMMB:       pickInt("PGTUI_HOST_RAM_MB", fc.HostRAMMB, 0),
		HostCPUs:        pickInt("PGTUI_HOST_CPUS", fc.HostCPUs, 0),
		UpdateCheck:     pickBool("PGTUI_UPDATE_CHECK", fc.UpdateCheck, true),
	}, nil
}

// configDirs lists where pgtui looks for config.yml, highest priority first.
// PGTUI_CONFIG_DIR (if set) wins, then the working directory, next to the
// binary, the user config dir, and finally the system locations.
func configDirs() []string {
	var dirs []string
	if d := strings.TrimSpace(os.Getenv("PGTUI_CONFIG_DIR")); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, ".")
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if home, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "pgtui"))
	}
	dirs = append(dirs, DefaultConfigDir, "/etc/pgtui")
	return dirs
}

// loadFileConfig reads the first config.yml found (or PGTUI_CONFIG, an explicit
// path). A missing or unreadable file yields an empty config, never an error —
// the environment/.env path still applies.
func loadFileConfig() fileConfig {
	var paths []string
	if f := strings.TrimSpace(os.Getenv("PGTUI_CONFIG")); f != "" {
		paths = append(paths, f)
	}
	for _, dir := range configDirs() {
		paths = append(paths, filepath.Join(dir, "config.yml"), filepath.Join(dir, "config.yaml"))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var fc fileConfig
		if yaml.Unmarshal(b, &fc) == nil {
			return fc
		}
	}
	return fileConfig{}
}

// dsn builds a DSN from the individual host/user/... fields of config.yml.
func (fc fileConfig) dsn() string {
	host := strings.TrimSpace(fc.Host)
	if host == "" {
		return ""
	}
	port := fc.Port
	if port == 0 {
		port = 5432
	}
	user := fc.User
	if user == "" {
		user = "postgres"
	}
	db := fc.Database
	if db == "" {
		db = "postgres"
	}
	ssl := fc.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	userinfo := url.User(user)
	if fc.Password != "" {
		userinfo = url.UserPassword(user, fc.Password)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     userinfo,
		Host:     fmt.Sprintf("%s:%d", host, port),
		Path:     "/" + db,
		RawQuery: "sslmode=" + ssl,
	}
	return u.String()
}

func buildFromParts() string {
	host := os.Getenv("PGHOST")
	if host == "" {
		return ""
	}
	port := envOr("PGPORT", "5432")
	user := envOr("PGUSER", "postgres")
	pass := os.Getenv("PGPASSWORD")
	db := envOr("PGDATABASE", "postgres")
	ssl := envOr("PGSSLMODE", "disable")

	userinfo := url.User(user)
	if pass != "" {
		userinfo = url.UserPassword(user, pass)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     userinfo,
		Host:     host + ":" + port,
		Path:     "/" + db,
		RawQuery: "sslmode=" + ssl,
	}
	return u.String()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// pickInt resolves a positive integer setting: environment > config.yml > def.
func pickInt(key string, fileVal, def int) int {
	if v := os.Getenv(key); v != "" {
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
	if v := strings.TrimSpace(strings.ToLower(os.Getenv(key))); v != "" {
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
