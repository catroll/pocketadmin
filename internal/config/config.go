// Package config holds the admin panel runtime configuration.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	defaultAddr    = "127.0.0.1:8090"
	defaultDataDir = "./pb_data"

	// sessionKeyEnv lets operators pin the cookie signing key explicitly, which
	// is what you want in production so sessions survive restarts across hosts.
	sessionKeyEnv = "POCKETADMIN_SESSION_KEY"
	addrEnv       = "POCKETADMIN_ADDR"
	dataDirEnv    = "POCKETADMIN_DATA_DIR"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Addr is the TCP address the HTTP server listens on.
	Addr string

	// DataDir is the PocketBase data directory (SQLite db + uploaded files).
	DataDir string

	// SessionKey signs the session cookie. It is never a hard-coded default:
	// it is either supplied by the operator or generated once and persisted
	// inside DataDir.
	SessionKey []byte

	// SecureCookie marks the session cookie as HTTPS-only. Enable it whenever
	// the panel is served over TLS.
	SecureCookie bool

	// Dev enables PocketBase dev mode (verbose SQL/HTTP logging).
	Dev bool

	// PerPage is the default page size for record listings.
	PerPage int

	// Args holds the positional arguments left after flag parsing. A non-empty
	// value means the caller asked for a PocketBase CLI subcommand rather than
	// the HTTP panel.
	Args []string
}

// Load resolves the configuration from CLI flags, falling back to environment
// variables and finally to defaults.
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("pocketadmin", flag.ContinueOnError)

	cfg := &Config{}
	fs.StringVar(&cfg.Addr, "addr", envOr(addrEnv, defaultAddr), "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "dir", envOr(dataDirEnv, defaultDataDir), "PocketBase data directory")
	fs.BoolVar(&cfg.SecureCookie, "secure-cookie", false, "mark the session cookie as HTTPS-only")
	fs.BoolVar(&cfg.Dev, "dev", false, "enable PocketBase dev mode")
	fs.IntVar(&cfg.PerPage, "per-page", 30, "default number of records per page")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.Args = fs.Args()

	if cfg.PerPage < 1 || cfg.PerPage > 500 {
		return nil, fmt.Errorf("per-page must be between 1 and 500, got %d", cfg.PerPage)
	}

	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}
	cfg.DataDir = absDataDir

	key, err := resolveSessionKey(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	cfg.SessionKey = key

	return cfg, nil
}

// resolveSessionKey returns the operator-provided key when set, otherwise a
// random key persisted in the data directory so that restarting the process
// does not log everybody out.
func resolveSessionKey(dataDir string) ([]byte, error) {
	if raw := os.Getenv(sessionKeyEnv); raw != "" {
		return []byte(raw), nil
	}

	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	keyPath := filepath.Join(dataDir, "admin_session.key")

	if raw, err := os.ReadFile(keyPath); err == nil && len(raw) >= 32 {
		return raw, nil
	}

	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	encoded := []byte(hex.EncodeToString(key))

	if err := os.WriteFile(keyPath, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("persist session key: %w", err)
	}

	return encoded, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// EnvIntOr reads an integer environment variable, falling back on parse errors.
func EnvIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
