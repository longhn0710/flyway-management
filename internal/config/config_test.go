package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(home, ".flyway-manager") {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, filepath.Join(home, ".flyway-manager"))
	}
}

func TestLoadConfigFileAndEnvironmentPrecedence(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"listen_address":"127.0.0.1:9000","data_dir":"~/from-file","log_level":"warn"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLYWAY_MANAGER_CONFIG", configPath)
	t.Setenv("FLYWAY_MANAGER_LISTEN_ADDRESS", "127.0.0.1:9100")
	t.Setenv("FLYWAY_MANAGER_DATA_DIR", "~/from-env")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if cfg.ListenAddress != "127.0.0.1:9100" || cfg.DataDir != filepath.Join(home, "from-env") || cfg.LogLevel != "warn" {
		t.Fatalf("environment must override file fields while omitted env fields retain file values; got %+v", cfg)
	}
}

func TestLoadExpandsTildeInConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	configDir := filepath.Join(home, "settings")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "local.json"), []byte(`{"data_dir":"~/application"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLYWAY_MANAGER_CONFIG", "~/settings/local.json")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(home, "application") {
		t.Fatalf("tilde data dir = %q, want %q", cfg.DataDir, filepath.Join(home, "application"))
	}
}

func TestLoadRejectsUnknownTrailingAndBlankEnvironmentConfig(t *testing.T) {
	for _, contents := range []string{
		`{"unknown":true}`,
		`{"log_level":"debug"} {}`,
	} {
		t.Run(contents, func(t *testing.T) {
			clearConfigEnv(t)
			configPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FLYWAY_MANAGER_CONFIG", configPath)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted invalid configuration %s", contents)
			}
		})
	}
	clearConfigEnv(t)
	t.Setenv("FLYWAY_MANAGER_DATA_DIR", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a blank explicitly set data directory")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"FLYWAY_MANAGER_CONFIG", "FLYWAY_MANAGER_LISTEN_ADDRESS", "FLYWAY_MANAGER_DATA_DIR", "FLYWAY_MANAGER_LOG_LEVEL", "FLYWAY_MANAGER_PUBLIC_URL"} {
		value, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func TestPublicURLValidation(t *testing.T) {
	for _, value := range []string{"http://remote.example", "https://user:password@console.example", "https://console.example/app", "https://console.example?query=1", "ftp://console.example"} {
		t.Run(value, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("FLYWAY_MANAGER_PUBLIC_URL", value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid public origin accepted")
			}
		})
	}
	for _, value := range []string{"https://console.example", "http://127.0.0.1:5173", "http://[::1]:8080"} {
		t.Run(value, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("FLYWAY_MANAGER_PUBLIC_URL", value)
			cfg, err := Load()
			if err != nil || cfg.PublicURL != value {
				t.Fatalf("valid origin rejected: %v", err)
			}
		})
	}
}

func TestRuntimeAndArtifactConfiguration(t *testing.T) {
	clearConfigEnv(t)
	for _, contents := range []string{
		`{"flyway":{"executable":"/approved/flyway","version":"11.20.0","sha256":"approved-externally","timeout_seconds":60},"artifact_limits":{"max_files":12}}`,
		`{"flyway":{"timeout_seconds":3601}}`,
		`{"artifact_limits":{"max_files":10001}}`,
	} {
		path := filepath.Join(t.TempDir(), "runtime.json")
		if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
			t.Fatal(e)
		}
		t.Setenv("FLYWAY_MANAGER_CONFIG", path)
		cfg, e := Load()
		if strings.Contains(contents, "approved-externally") {
			if e != nil || cfg.Flyway.TimeoutSeconds != 60 || cfg.ArtifactLimits.MaxFiles != 12 || cfg.ArtifactLimits.MaxUploadBytes != 50<<20 {
				t.Fatalf("config %+v %v", cfg, e)
			}
		} else if e == nil {
			t.Fatal("accepted unsafe resource limits")
		}
	}
}
