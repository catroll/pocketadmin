// Package config resolves the admin panel's runtime configuration.
//
// Values are layered in order of increasing precedence:
//
//	defaults  <  YAML file  <  environment variables  <  command-line flags
//
// The YAML file is optional. When -config is not given, DefaultConfigPath is
// looked up and a missing file there is not an error, so the panel still runs
// with pure defaults.
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultConfigPath is the YAML file consulted when neither -config nor
	// POCKETADMIN_CONFIG is set.
	DefaultConfigPath = "pocketadmin.yaml"

	defaultAddr    = "127.0.0.1:8090"
	defaultDataDir = "./pb_data"
	defaultPerPage = 30

	// DefaultLogLevel and DefaultLogFormat keep stdout machine readable unless
	// the operator asks otherwise.
	DefaultLogLevel  = "info"
	DefaultLogFormat = "json"

	configEnv       = "POCKETADMIN_CONFIG"
	addrEnv         = "POCKETADMIN_ADDR"
	dataDirEnv      = "POCKETADMIN_DATA_DIR"
	sessionKeyEnv   = "POCKETADMIN_SESSION_KEY"
	secureCookieEnv = "POCKETADMIN_SECURE_COOKIE"
	devEnv          = "POCKETADMIN_DEV"
	perPageEnv      = "POCKETADMIN_PER_PAGE"
	logLevelEnv     = "POCKETADMIN_LOG_LEVEL"
	logFormatEnv    = "POCKETADMIN_LOG_FORMAT"

	// sessionKeyFile is where a generated session signing key is persisted so
	// that restarting the process does not invalidate every session.
	sessionKeyFile = "admin_session.key"

	// minSessionKeyLen is the smallest accepted key length. The key is fed to
	// the cookie store's HMAC, so a short key would weaken every session.
	minSessionKeyLen = 32
)

// LogConfig controls the application logger.
type LogConfig struct {
	// Level is one of debug, info, warn or error.
	Level string

	// Format is either json (the default) or text.
	Format string
}

// Config is the fully resolved runtime configuration.
type Config struct {
	// Addr is the TCP address the HTTP server listens on.
	Addr string

	// DataDir is the PocketBase data directory (SQLite database, uploaded
	// files and the generated session key).
	DataDir string

	// SessionKey signs the session cookie. It is never a hard-coded default:
	// it is supplied by the operator or generated once and persisted inside
	// DataDir.
	SessionKey []byte

	// SecureCookie marks the session cookie as HTTPS-only.
	SecureCookie bool

	// Dev enables PocketBase dev mode (verbose SQL and HTTP logging).
	Dev bool

	// PerPage is the default page size for record listings.
	PerPage int

	// Log configures the slog handler installed on stdout.
	Log LogConfig

	// ConfigFile is the YAML file that was loaded, or "" when none was found.
	ConfigFile string

	// Args holds the positional arguments left after flag parsing. A non-empty
	// value means the caller asked for a PocketBase CLI subcommand rather than
	// the HTTP panel.
	Args []string
}

// fileConfig mirrors the YAML document.
//
// Every field is a pointer so that an absent key is distinguishable from a
// deliberate false or zero value; a partial file therefore overrides only the
// keys it actually sets.
type fileConfig struct {
	Addr         *string `yaml:"addr"`
	DataDir      *string `yaml:"dataDir"`
	PerPage      *int    `yaml:"perPage"`
	SecureCookie *bool   `yaml:"secureCookie"`
	Dev          *bool   `yaml:"dev"`
	Log          *struct {
		Level  *string `yaml:"level"`
		Format *string `yaml:"format"`
	} `yaml:"log"`
}

// Load resolves the configuration from args (typically os.Args[1:]).
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("pocketadmin", flag.ContinueOnError)

	// Flag defaults are left empty. They are only consulted after fs.Visit
	// reports which flags the operator actually typed, so that flags override
	// the YAML file and the environment rather than silently replacing them.
	var (
		configPath   = envOr(configEnv, DefaultConfigPath)
		addr         string
		dataDir      string
		perPage      int
		secureCookie bool
		dev          bool
		logLevel     string
		logFormat    string
	)

	fs.StringVar(&configPath, "config", configPath, "path to the YAML configuration file")
	fs.StringVar(&addr, "addr", "", "HTTP listen address")
	fs.StringVar(&dataDir, "dir", "", "PocketBase data directory")
	fs.IntVar(&perPage, "per-page", 0, "default number of records per page")
	fs.BoolVar(&secureCookie, "secure-cookie", false, "mark the session cookie as HTTPS-only")
	fs.BoolVar(&dev, "dev", false, "enable PocketBase dev mode")
	fs.StringVar(&logLevel, "log-level", "", "log level: debug, info, warn or error")
	fs.StringVar(&logFormat, "log-format", "", "log format: json or text")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	cfg := &Config{
		Addr:    defaultAddr,
		DataDir: defaultDataDir,
		PerPage: defaultPerPage,
		Log:     LogConfig{Level: DefaultLogLevel, Format: DefaultLogFormat},
	}

	if err := applyFile(cfg, configPath, explicit["config"]); err != nil {
		return nil, err
	}

	applyEnv(cfg)

	if explicit["addr"] {
		cfg.Addr = addr
	}
	if explicit["dir"] {
		cfg.DataDir = dataDir
	}
	if explicit["per-page"] {
		cfg.PerPage = perPage
	}
	if explicit["secure-cookie"] {
		cfg.SecureCookie = secureCookie
	}
	if explicit["dev"] {
		cfg.Dev = dev
	}
	if explicit["log-level"] {
		cfg.Log.Level = logLevel
	}
	if explicit["log-format"] {
		cfg.Log.Format = logFormat
	}

	if cfg.PerPage < 1 || cfg.PerPage > 500 {
		return nil, fmt.Errorf("perPage must be between 1 and 500, got %d", cfg.PerPage)
	}

	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	cfg.DataDir = absDataDir

	if cfg.SessionKey, err = resolveSessionKey(cfg.DataDir); err != nil {
		return nil, err
	}

	cfg.Args = fs.Args()

	return cfg, nil
}

// applyFile layers the YAML document over cfg.
//
// A missing file is tolerated unless the path came from an explicit -config or
// POCKETADMIN_CONFIG, in which case the operator clearly expected it to exist.
func applyFile(cfg *Config, path string, required bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !required {
			return nil
		}
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	// Reject unknown keys so that a typo surfaces at startup instead of being
	// silently ignored.
	decoder.KnownFields(true)

	var fc fileConfig
	if err := decoder.Decode(&fc); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	if fc.Addr != nil {
		cfg.Addr = *fc.Addr
	}
	if fc.DataDir != nil {
		cfg.DataDir = *fc.DataDir
	}
	if fc.PerPage != nil {
		cfg.PerPage = *fc.PerPage
	}
	if fc.SecureCookie != nil {
		cfg.SecureCookie = *fc.SecureCookie
	}
	if fc.Dev != nil {
		cfg.Dev = *fc.Dev
	}
	if fc.Log != nil {
		if fc.Log.Level != nil {
			cfg.Log.Level = *fc.Log.Level
		}
		if fc.Log.Format != nil {
			cfg.Log.Format = *fc.Log.Format
		}
	}

	cfg.ConfigFile = path

	return nil
}

// applyEnv layers environment variables over cfg.
//
// Malformed booleans and integers are ignored rather than fatal, matching the
// convention that a broken environment variable should not stop the process
// from starting with its file or default value.
func applyEnv(cfg *Config) {
	cfg.Addr = envOr(addrEnv, cfg.Addr)
	cfg.DataDir = envOr(dataDirEnv, cfg.DataDir)

	if v := os.Getenv(perPageEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PerPage = n
		}
	}
	if v := os.Getenv(secureCookieEnv); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.SecureCookie = b
		}
	}
	if v := os.Getenv(devEnv); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Dev = b
		}
	}

	cfg.Log.Level = envOr(logLevelEnv, cfg.Log.Level)
	cfg.Log.Format = envOr(logFormatEnv, cfg.Log.Format)
}

// resolveSessionKey returns the operator-provided key when set, otherwise a
// random key persisted in the data directory.
func resolveSessionKey(dataDir string) ([]byte, error) {
	if raw := os.Getenv(sessionKeyEnv); raw != "" {
		if len(raw) < minSessionKeyLen {
			return nil, fmt.Errorf("%s must be at least %d characters", sessionKeyEnv, minSessionKeyLen)
		}
		return []byte(raw), nil
	}

	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	keyPath := filepath.Join(dataDir, sessionKeyFile)

	if raw, err := os.ReadFile(keyPath); err == nil && len(raw) >= minSessionKeyLen {
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
