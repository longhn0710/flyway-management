package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Config contains the small set of settings needed by the Phase 1 server.
type Config struct {
	ListenAddress string `json:"listen_address"`
	DataDir       string `json:"data_dir"`
	LogLevel      string `json:"log_level"`
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
	if value, ok := os.LookupEnv("FLYWAY_MANAGER_LOG_LEVEL"); ok {
		cfg.LogLevel = value
	}
	var err error
	cfg.DataDir, err = expandHome(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data_dir: %w", err)
	}
	if cfg.ListenAddress == "" || cfg.DataDir == "" {
		return Config{}, errors.New("listen_address and data_dir must not be empty")
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
