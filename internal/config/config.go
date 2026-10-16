package config

import (
	"encoding/json"
	"errors"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config contains native server settings; authentication providers are managed in SQLite.
type Config struct {
	ListenAddress  string          `json:"listen_address"`
	DataDir        string          `json:"data_dir"`
	MasterKeyFile  string          `json:"master_key_file,omitempty"`
	LogLevel       string          `json:"log_level"`
	PublicURL      string          `json:"public_url"`
	Flyway         flyway.Config   `json:"flyway"`
	ArtifactLimits artifact.Limits `json:"artifact_limits"`
}

func Load() (Config, error) {
	configPath := os.Getenv("FLYWAY_MANAGER_CONFIG")
	cfg := Config{ListenAddress: "127.0.0.1:8080", DataDir: "~/.flyway-manager", LogLevel: "info"}
	if configPath != "" {
		var err error
		configPath, err = expandHome(configPath)
		if err != nil {
			return Config{}, fmt.Errorf("resolve config path: %w", err)
		}
		f, err := os.Open(configPath)
		if err != nil {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		dec := json.NewDecoder(f)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			f.Close()
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
			f.Close()
			return Config{}, errors.New("decode config: expected a single JSON object")
		}
		f.Close()
	}
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_LISTEN_ADDRESS"); ok {
		cfg.ListenAddress = value
	}
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_DATA_DIR"); ok {
		cfg.DataDir = value
	}
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_MASTER_KEY_FILE"); ok {
		cfg.MasterKeyFile = value
	}
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_LOG_LEVEL"); ok {
		cfg.LogLevel = value
	}
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_PUBLIC_URL"); ok {
		cfg.PublicURL = value
	}
	cfg.ArtifactLimits = artifact.EffectiveLimits(cfg.ArtifactLimits)
	if cfg.ArtifactLimits.MaxUploadBytes > 200<<20 || cfg.ArtifactLimits.MaxExpandedBytes > 1<<30 || cfg.ArtifactLimits.MaxFiles > 10000 || cfg.ArtifactLimits.MaxFileBytes > 200<<20 || cfg.ArtifactLimits.MaxPathDepth > 64 || cfg.ArtifactLimits.MaxPathBytes > 4096 || cfg.ArtifactLimits.MaxExpansionRatio > 1000 {
		return Config{}, errors.New("artifact limits exceed supported safety ceilings")
	}
	if cfg.Flyway.TimeoutSeconds < 0 || cfg.Flyway.TimeoutSeconds > 3600 {
		return Config{}, errors.New("flyway.timeout_seconds must be between 0 and 3600")
	}
	var err error
	cfg.Flyway.Executable, err = expandHome(cfg.Flyway.Executable)
	if err != nil {
		return Config{}, err
	}
	cfg.DataDir, err = expandHome(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data_dir: %w", err)
	}
	cfg.MasterKeyFile, err = expandHome(cfg.MasterKeyFile)
	if err != nil {
		return Config{}, fmt.Errorf("resolve master_key_file: %w", err)
	}
	if cfg.ListenAddress == "" || cfg.DataDir == "" {
		return Config{}, errors.New("listen_address and data_dir must not be empty")
	}
	if cfg.PublicURL != "" {
		u, e := url.Parse(cfg.PublicURL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
			return Config{}, errors.New("public_url must be an HTTP(S) origin")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return Config{}, errors.New("remote public_url requires HTTPS")
		}
		cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	}
	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("invalid log_level %q", cfg.LogLevel)
	}
	return cfg, nil
}

func expandHome(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Clean(path), nil
}
