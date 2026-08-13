package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config contains the small set of runtime settings required by the first Server slice.
type Config struct {
	Host           string
	Port           int
	DatabasePath   string
	LogDirectory   string
	Environment    string
	PublicOrigin   string
	DevOrigin      string
	SessionIdleTTL time.Duration
	SessionMaxTTL  time.Duration
	CookieSecure   bool
}

// Load reads Server configuration from environment variables and safe local defaults.
func Load() (Config, error) {
	port, err := intEnv("OMC_SERVER_PORT", 3000)
	if err != nil {
		return Config{}, err
	}
	environment := strings.ToLower(strings.TrimSpace(env("OMC_ENV", "development")))
	publicOrigin := strings.TrimRight(env("OMC_PUBLIC_ORIGIN", "http://127.0.0.1:3000"), "/")
	devOrigin := ""
	if environment != "production" {
		devOrigin = strings.TrimRight(env("OMC_DEV_ORIGIN", "http://127.0.0.1:5173"), "/")
	}
	secure := strings.HasPrefix(strings.ToLower(publicOrigin), "https://")
	if raw := strings.TrimSpace(os.Getenv("OMC_COOKIE_SECURE")); raw != "" {
		secure, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse OMC_COOKIE_SECURE: %w", err)
		}
	}
	databasePath := env("OMC_DATABASE_PATH", "./data/ohmycine.db")
	logDirectory := strings.TrimSpace(os.Getenv("OMC_LOG_DIR"))
	if logDirectory == "" {
		logDirectory = filepath.Join(filepath.Dir(databasePath), "logs")
	}
	config := Config{
		Host:           env("OMC_SERVER_HOST", "127.0.0.1"),
		Port:           port,
		DatabasePath:   databasePath,
		LogDirectory:   logDirectory,
		Environment:    environment,
		PublicOrigin:   publicOrigin,
		DevOrigin:      devOrigin,
		SessionIdleTTL: 2 * time.Hour,
		SessionMaxTTL:  7 * 24 * time.Hour,
		CookieSecure:   secure,
	}
	if config.Port < 1 || config.Port > 65535 {
		return Config{}, fmt.Errorf("OMC_SERVER_PORT must be between 1 and 65535")
	}
	absLogDirectory, err := filepath.Abs(config.LogDirectory)
	if err != nil {
		return Config{}, fmt.Errorf("resolve OMC_LOG_DIR: %w", err)
	}
	config.LogDirectory = filepath.Clean(absLogDirectory)
	if err := validateOrigin("OMC_PUBLIC_ORIGIN", config.PublicOrigin); err != nil {
		return Config{}, err
	}
	if config.DevOrigin != "" {
		if err := validateOrigin("OMC_DEV_ORIGIN", config.DevOrigin); err != nil {
			return Config{}, err
		}
	}
	if config.CookieSecure && !strings.HasPrefix(strings.ToLower(config.PublicOrigin), "https://") {
		return Config{}, fmt.Errorf("OMC_COOKIE_SECURE=true requires an https OMC_PUBLIC_ORIGIN")
	}
	return config, nil
}

// Address returns the listen address for http.Server.
func (c Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// AllowedOrigins returns the exact browser origins accepted for mutation requests.
func (c Config) AllowedOrigins() []string {
	origins := []string{c.PublicOrigin}
	if c.DevOrigin != "" && c.DevOrigin != c.PublicOrigin {
		origins = append(origins, c.DevOrigin)
	}
	return origins
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func validateOrigin(key, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute http(s) origin", key)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must not include credentials, a path, query, or fragment", key)
	}
	return nil
}
