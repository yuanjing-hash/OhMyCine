package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config contains the small set of runtime settings required by the first Server slice.
type Config struct {
	Host                          string
	Port                          int
	DatabasePath                  string
	LogDirectory                  string
	CredentialKeyFile             string
	CredentialMasterKey           string
	PluginDirectory               string
	FFmpegPath                    string
	TMDBDeploymentCredentialKind  string
	TMDBDeploymentCredentialValue string
	Environment                   string
	PublicOrigin                  string
	DevOrigin                     string
	SessionIdleTTL                time.Duration
	SessionMaxTTL                 time.Duration
	DeviceTokenIdleTTL            time.Duration
	DeviceTokenMaxTTL             time.Duration
	CookieSecure                  bool
	CloakBrowserCompanionURL      string
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
	deploymentToken := strings.TrimSpace(os.Getenv("OMC_TMDB_READ_ACCESS_TOKEN"))
	deploymentAPIKey := strings.TrimSpace(os.Getenv("OMC_TMDB_API_KEY"))
	if deploymentToken != "" && deploymentAPIKey != "" {
		return Config{}, fmt.Errorf("configure only one of OMC_TMDB_READ_ACCESS_TOKEN and OMC_TMDB_API_KEY")
	}
	deploymentKind, deploymentValue := "", ""
	if deploymentToken != "" {
		deploymentKind, deploymentValue = "read_access_token", deploymentToken
	} else if deploymentAPIKey != "" {
		deploymentKind, deploymentValue = "api_key", deploymentAPIKey
	}
	if len(deploymentValue) > 4096 || strings.ContainsAny(deploymentValue, "\r\n") {
		return Config{}, fmt.Errorf("TMDB deployment credential is invalid")
	}
	config := Config{
		Host:                          env("OMC_SERVER_HOST", "0.0.0.0"),
		Port:                          port,
		DatabasePath:                  databasePath,
		LogDirectory:                  logDirectory,
		CredentialKeyFile:             env("OMC_CREDENTIAL_KEY_FILE", filepath.Join(filepath.Dir(databasePath), "credentials.key")),
		CredentialMasterKey:           strings.TrimSpace(os.Getenv("OMC_CREDENTIAL_MASTER_KEY")),
		TMDBDeploymentCredentialKind:  deploymentKind,
		TMDBDeploymentCredentialValue: deploymentValue,
		Environment:                   environment,
		FFmpegPath:                    strings.TrimSpace(os.Getenv("OMC_FFMPEG_PATH")),
		PublicOrigin:                  publicOrigin,
		DevOrigin:                     devOrigin,
		SessionIdleTTL:                2 * time.Hour,
		SessionMaxTTL:                 7 * 24 * time.Hour,
		DeviceTokenIdleTTL:            30 * 24 * time.Hour,
		DeviceTokenMaxTTL:             180 * 24 * time.Hour,
		CookieSecure:                  secure,
		CloakBrowserCompanionURL:      strings.TrimSpace(os.Getenv("OMC_CLOAKBROWSER_COMPANION_URL")),
	}
	if len(config.CloakBrowserCompanionURL) > 2048 || strings.ContainsAny(config.CloakBrowserCompanionURL, "\x00\r\n") {
		return Config{}, fmt.Errorf("OMC_CLOAKBROWSER_COMPANION_URL is invalid")
	}
	if strings.ContainsAny(config.FFmpegPath, "\x00\r\n") {
		return Config{}, fmt.Errorf("OMC_FFMPEG_PATH is invalid")
	}
	if config.Port < 1 || config.Port > 65535 {
		return Config{}, fmt.Errorf("OMC_SERVER_PORT must be between 1 and 65535")
	}
	absLogDirectory, err := filepath.Abs(config.LogDirectory)
	if err != nil {
		return Config{}, fmt.Errorf("resolve OMC_LOG_DIR: %w", err)
	}
	config.LogDirectory = filepath.Clean(absLogDirectory)
	absCredentialKeyFile, err := filepath.Abs(config.CredentialKeyFile)
	if err != nil {
		return Config{}, fmt.Errorf("resolve OMC_CREDENTIAL_KEY_FILE: %w", err)
	}
	config.CredentialKeyFile = filepath.Clean(absCredentialKeyFile)
	pluginDirectory := strings.TrimSpace(os.Getenv("OMC_PLUGIN_DIR"))
	if pluginDirectory == "" {
		pluginDirectory = filepath.Join(filepath.Dir(config.DatabasePath), "plugins")
	}
	absPluginDirectory, err := filepath.Abs(pluginDirectory)
	if err != nil {
		return Config{}, fmt.Errorf("resolve OMC_PLUGIN_DIR: %w", err)
	}
	config.PluginDirectory = filepath.Clean(absPluginDirectory)
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
	if ip := net.ParseIP(strings.TrimSuffix(parsed.Hostname(), ".")); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("%s must advertise a reachable host, not a wildcard listen address", key)
	}
	return nil
}
