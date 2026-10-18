package flyway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Uses the approved CLI against its bundled H2 driver and a process-local memory
// database. It cannot reach Oracle or a configured external database. This proves
// actual CLI failure serialization, not Oracle DDL/Repair acceptance.
func TestActualFlywayMigrationFailureEnvelope(t *testing.T) {
	configPath := os.Getenv("FLYWAY_TEST_RUNTIME_CONFIG")
	if configPath == "" {
		t.Skip("requires an approved local runtime; uses only H2 in memory")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("cannot read approved runtime configuration")
	}
	var cfg struct {
		Flyway Config `json:"flyway"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid approved runtime configuration")
	}
	runtime := New(cfg.Flyway, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if ready := runtime.Check(ctx); !ready.Ready {
		t.Fatal("approved runtime not ready", ready.Blockers)
	}
	root, err := runtime.distributionRoot()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	sqlDir := filepath.Join(work, "sql")
	if err = os.Mkdir(sqlDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sqlDir, "V42__native_failure.sql"), []byte("THIS IS INTENTIONALLY INVALID SQL;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configuration := `[environments.memory]
url = "jdbc:h2:mem:fwm_failure_envelope"
user = "sa"
password = "synthetic"
[flyway]
environment = "memory"
cleanDisabled = true
baselineOnMigrate = false
createSchemas = false
placeholderReplacement = false
callbacks = []
`
	privateConfig := filepath.Join(work, "flyway.toml")
	if err = os.WriteFile(privateConfig, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, cfg.Flyway.Executable, "migrate", "-configFiles="+privateConfig, "-workingDirectory="+work, "-locations=filesystem:"+sqlDir, "-outputType=json")
	command.Dir, command.Env = work, restrictedEnv(work, root)
	configureProcessGroup(command)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("expected local SQL failure, got %v: %s", err, stderr.String())
	}
	payload, err := parseJSONOutput(stdout.Bytes())
	if err != nil || !ConfirmedMigrationFailure(payload, cfg.Flyway.Version) {
		t.Fatalf("native failure was not recognized: %v %s %s", err, stdout.String(), stderr.String())
	}
	var result struct {
		Error struct {
			Code    string `json:"errorCode"`
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != "FAILED_VERSIONED_MIGRATION" || filepath.Base(result.Error.Path) != "V42__native_failure.sql" || !strings.Contains(result.Error.Message, "INTENTIONALLY") {
		t.Fatalf("native failure identity/error missing: %s", payload)
	}
}
