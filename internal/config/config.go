// Package config carrega a configuração do TUI a partir do ambiente / .env.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config guarda os parâmetros de conexão ao cluster Postgres gerenciado.
type Config struct {
	// URL é o DSN base (postgres://user:pass@host:port/db?sslmode=...).
	// O nome do database é trocado em runtime ao navegar entre bancos.
	URL string

	// AdminDB é o database usado para consultas de nível de cluster
	// (pg_database, pg_stat_activity, replicação). Derivado da URL.
	AdminDB string

	// Host/Port apenas para exibição no cabeçalho do TUI.
	Host string
	Port string
	User string

	// RefreshSeconds controla o auto-refresh do dashboard.
	RefreshSeconds int
}

// Load procura por um arquivo .env (no diretório atual e no do executável),
// carrega no ambiente e monta a Config. Variáveis já presentes no ambiente
// têm precedência sobre o .env.
func Load() (*Config, error) {
	loadDotenv()

	raw := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if raw == "" {
		raw = buildFromParts()
	}
	if raw == "" {
		return nil, errors.New("DATABASE_URL não definido (nem PGHOST/PGUSER/...). Copie .env.example para .env e ajuste")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL inválido: %w", err)
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
		URL:            raw,
		AdminDB:        admin,
		Host:           u.Hostname(),
		Port:           port,
		User:           user,
		RefreshSeconds: envInt("PGTUI_REFRESH_SECONDS", 5),
	}, nil
}

// loadDotenv carrega .env do diretório de trabalho e ao lado do binário,
// sem sobrescrever variáveis já exportadas.
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
