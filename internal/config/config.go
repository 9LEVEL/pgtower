// Package config loads the TUI configuration from the environment / .env.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

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

	// Version is the binary version (injected in main via -ldflags), shown
	// in the header. Filled in by whoever builds the Config.
	Version string
}

// Load looks for a .env file (in the current directory and next to the
// executable), loads it into the environment and builds the Config. Variables
// already present in the environment take precedence over the .env.
func Load() (*Config, error) {
	loadDotenv()

	raw := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if raw == "" {
		raw = buildFromParts()
	}
	if raw == "" {
		return nil, errors.New("DATABASE_URL not set (nor PGHOST/PGUSER/...). Copy .env.example to .env and adjust it")
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
		RefreshSeconds:  envInt("PGTUI_REFRESH_SECONDS", 5),
		SCRAMIterations: envInt("PGTUI_SCRAM_ITERATIONS", 0),
		HostRAMMB:       envInt("PGTUI_HOST_RAM_MB", 0),
		HostCPUs:        envInt("PGTUI_HOST_CPUS", 0),
	}, nil
}

// loadDotenv loads .env from the working directory and next to the binary,
// without overwriting already-exported variables.
func loadDotenv() {
	_ = godotenv.Load(".env")
	if exe, err := os.Executable(); err == nil {
		dir := exe[:strings.LastIndex(exe, "/")+1]
		if dir != "" {
			_ = godotenv.Load(dir + ".env")
		}
	}
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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
