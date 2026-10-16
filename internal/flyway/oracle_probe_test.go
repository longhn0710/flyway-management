package flyway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/oracle"
)

func TestOracleProbeHelperArtifact(t *testing.T) {
	digest := sha256.Sum256(oracleProbeClass)
	if hex.EncodeToString(digest[:]) != oracleProbeClassSHA256 {
		t.Fatal("embedded Oracle JDBC helper digest does not match its source pin")
	}
	if len(oracleProbeClass) < 8 || oracleProbeClass[6] != 0 || oracleProbeClass[7] != 55 {
		t.Fatal("Oracle JDBC helper must target Java 11 bytecode")
	}

	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not installed; embedded class digest remains checked")
	}
	version, err := exec.Command(javac, "-version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "javac 21") {
		t.Skip("exact helper regeneration is pinned to the available JDK 21 compiler")
	}
	work := t.TempDir()
	source := filepath.Join("..", "oracle", "OracleJdbcProbe.java")
	cmd := exec.Command(javac, "--release", "11", "-proc:none", "-g:none", "-d", work, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper source does not compile: %v (%s)", err, output)
	}
	generated, err := os.ReadFile(filepath.Join(work, "OracleJdbcProbe.class"))
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest := sha256.Sum256(generated)
	if hex.EncodeToString(generatedDigest[:]) != oracleProbeClassSHA256 {
		t.Fatal("checked-in Oracle JDBC class is not reproducible from its checked-in source")
	}
}

func TestOracleProbeProtocolStreamsRowsAndFrames(t *testing.T) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	lines := []string{
		"READY", "CONNECTED",
		"IDENTITY\t" + encode("APP") + "\t" + encode("APP") + "\t" + encode("FREEPDB1") + "\t" + encode("FREEPDB1"),
		"USER\t" + encode("APP") + "\t" + encode("N"),
		"VIEW_DONE\tSESSION_IDENTITY", "VIEW_DONE\tALL_USERS", "DONE",
	}
	metadata, code, connected, done, err := readOracleProbeProtocol(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil || code != "" || !connected || !done || metadata.ActualUser != "APP" || len(metadata.Users) != 1 || !metadata.CompletedViews["ALL_USERS"] {
		t.Fatalf("protocol decode failed: %#v %q %t %t %v", metadata, code, connected, done, err)
	}
}

func TestOracleProbeProtocolRetainsRowsWhenLaterViewTimesOut(t *testing.T) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	var lines []string
	lines = append(lines, "READY", "CONNECTED", "IDENTITY\t"+encode("APP")+"\t"+encode("APP")+"\t"+encode("SVC")+"\t"+encode("PDB"))
	for i := 0; i < 220; i++ {
		lines = append(lines, "USER\t"+encode(fmt.Sprintf("APP%03d", i))+"\t"+encode("N"))
	}
	lines = append(lines, "VIEW_DONE\tSESSION_IDENTITY", "VIEW_DONE\tALL_USERS")
	metadata, code, connected, done, err := readOracleProbeProtocol(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil || code != "" || !connected || done || len(metadata.Users) != 220 || !metadata.CompletedViews["ALL_USERS"] {
		t.Fatalf("partial protocol rows were lost: rows=%d connected=%t done=%t err=%v", len(metadata.Users), connected, done, err)
	}
	for _, view := range []string{"SESSION_IDENTITY", "ALL_USERS", "ALL_OBJECTS", "SYSTEM_PRIVS", "ENABLED_ROLES", "SCHEMA_PRIVS", "ROLE_SCHEMA_PRIVS"} {
		if !metadata.CompletedViews[view] {
			metadata.Errors[metadataErrorView(view)] = "TIMEOUT"
		}
	}
	metadata.Errors["PROBE_TIMEOUT"] = "TIMEOUT"
	result, err := oracle.ProbeFromMetadata(validOracleConfig(), metadata)
	if err != nil || result.SchemaTotal != 221 || !strings.Contains(strings.Join(result.Warnings, " "), "discovery timed out") {
		t.Fatalf("completed rows not reported on timeout: count=%d warnings=%v err=%v", result.SchemaTotal, result.Warnings, err)
	}
}

func TestOracleProbeOutputLimitMarksUnfinishedViewsUnavailable(t *testing.T) {
	metadata := oracle.Metadata{ActualUser: "APP", Users: []oracle.UserMetadata{{Name: "APP"}}, Errors: map[string]string{}, CompletedViews: map[string]bool{"SESSION_IDENTITY": true, "ALL_USERS": true}}
	markIncompleteViews(&metadata, "LIMIT")
	if metadata.Errors["ALL_OBJECTS"] != "LIMIT" || metadata.Errors["SESSION_PRIVS"] != "LIMIT" {
		t.Fatalf("unfinished views were not marked unavailable after output limit: %#v", metadata.Errors)
	}
	result, err := oracle.ProbeFromMetadata(validOracleConfig(), metadata)
	if err != nil || len(result.Schemas) != 1 || result.Schemas[0].MigrationCapability != "unknown" {
		t.Fatalf("partial output must not claim own schema unsupported: %#v %v", result, err)
	}
}

func TestOracleProbeLive(t *testing.T) {
	credentialsPath := os.Getenv("FLYWAY_ORACLE_ACCEPTANCE_FILE")
	runtimePath := os.Getenv("FLYWAY_TEST_RUNTIME_CONFIG")
	if credentialsPath == "" || runtimePath == "" {
		t.Skip("set private Oracle credentials and approved runtime config paths for live acceptance")
	}
	credentialsInfo, err := os.Stat(credentialsPath)
	if err != nil || credentialsInfo.Mode().Perm()&0077 != 0 {
		t.Fatal("private Oracle credentials file unavailable or permissions are too broad")
	}
	runtimeInfo, err := os.Stat(runtimePath)
	if err != nil || runtimeInfo.Mode().Perm()&0077 != 0 {
		t.Fatal("private runtime config unavailable or permissions are too broad")
	}
	credentialBytes, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal("cannot read Oracle credentials config")
	}
	var input struct {
		Host        string            `json:"host"`
		Port        int               `json:"port"`
		Transport   string            `json:"transport"`
		Service     string            `json:"service"`
		Users       map[string]string `json:"users"`
		Passwords   map[string]string `json:"passwords"`
		BadPassword string            `json:"bad_password"`
	}
	if json.Unmarshal(credentialBytes, &input) != nil {
		t.Fatal("Oracle credentials config is invalid")
	}
	if input.Users["view"] == "" || input.Passwords["view"] == "" || input.Users["long40"] == "" || input.Passwords["long40"] == "" || input.Users["space40"] == "" || input.Passwords["space40"] == "" || input.Users["multi"] == "" || input.Passwords["multi"] == "" || input.Users["schema_a"] == "" || input.Passwords["schema_a"] == "" || input.Users["special30"] == "" || input.Passwords["special30"] == "" {
		t.Fatal("live JDBC credential fidelity fixture is incomplete")
	}
	runtimeBytes, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal("cannot read approved runtime config")
	}
	var runtimeConfig struct {
		Flyway Config `json:"flyway"`
	}
	if json.Unmarshal(runtimeBytes, &runtimeConfig) != nil {
		t.Fatal("approved runtime config is invalid")
	}
	runner := New(runtimeConfig.Flyway, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if ready := runner.Check(ctx); !ready.Ready {
		t.Fatal("approved Oracle JDBC runtime is not ready")
	}
	for _, kind := range []string{"view", "none", "multi", "schema_a"} {
		cfg := oracle.Config{Host: input.Host, Port: input.Port, Transport: input.Transport, Service: input.Service, Username: input.Users[kind], Password: input.Passwords[kind]}
		result, err := runner.ProbeOracle(ctx, cfg)
		if err != nil || result.AuthenticationState != "verified" || result.ActualUser != input.Users[kind] || result.SchemaTotal != len(result.Schemas) {
			t.Fatalf("JDBC probe failed credential fidelity case %s: net=%s auth=%s code=%s err=%v", kind, result.NetworkState, result.AuthenticationState, result.ErrorCode, err)
		}
	}
	bad := oracle.Config{Host: input.Host, Port: input.Port, Transport: input.Transport, Service: input.Service, Username: input.Users["view"], Password: input.BadPassword}
	badResult, err := runner.ProbeOracle(ctx, bad)
	if err != nil || badResult.NetworkState != "verified" || badResult.AuthenticationState != "failed" || badResult.ErrorCode != "ORA-01017" {
		t.Fatalf("JDBC incorrect-password classification failed: net=%s auth=%s code=%s", badResult.NetworkState, badResult.AuthenticationState, badResult.ErrorCode)
	}
	extended := oracle.Config{Host: input.Host, Port: input.Port, Transport: input.Transport, Service: input.Service, Username: input.Users["long40"], Password: input.Passwords["long40"]}
	extendedResult, err := runner.ProbeOracle(ctx, extended)
	if err != nil || extendedResult.AuthenticationState != "failed" || extendedResult.ErrorCode != "ORA-01017" || !strings.Contains(extendedResult.ErrorMessage, "pinned JDBC 21") {
		t.Fatalf("extended-password JDBC compatibility was not reported actionably: auth=%s code=%s err=%v", extendedResult.AuthenticationState, extendedResult.ErrorCode, err)
	}
	special := oracle.Config{Host: input.Host, Port: input.Port, Transport: input.Transport, Service: input.Service, Username: input.Users["special30"], Password: input.Passwords["special30"]}
	specialResult, err := runner.ProbeOracle(ctx, special)
	if err != nil || specialResult.AuthenticationState != "failed" || specialResult.ErrorCode != "ORA-01017" || !strings.Contains(specialResult.ErrorMessage, "leading or trailing whitespace") {
		t.Fatalf("whitespace-password JDBC compatibility was not reported actionably: auth=%s code=%s err=%v", specialResult.AuthenticationState, specialResult.ErrorCode, err)
	}
	trimmed := special
	trimmed.Password = strings.TrimSpace(trimmed.Password)
	trimmedResult, err := runner.ProbeOracle(ctx, trimmed)
	if err != nil {
		t.Fatalf("trimmed-password compatibility control errored: %v", err)
	}
	t.Logf("JDBC21 trimmed-password control: network=%s authentication=%s code=%s", trimmedResult.NetworkState, trimmedResult.AuthenticationState, trimmedResult.ErrorCode)
	unreachable := oracle.Config{Host: input.Host, Port: 1, Transport: input.Transport, Service: input.Service, Username: input.Users["view"], Password: input.Passwords["view"], ConnectTimeout: time.Second, ReadTimeout: time.Second, OverallTimeout: 5 * time.Second}
	networkResult, err := runner.ProbeOracle(ctx, unreachable)
	if err != nil || networkResult.NetworkState != "failed" || networkResult.AuthenticationState != "unknown" {
		t.Fatalf("JDBC listener failure classification failed: net=%s auth=%s code=%s err=%v", networkResult.NetworkState, networkResult.AuthenticationState, networkResult.ErrorCode, err)
	}
}

func validOracleConfig() oracle.Config {
	return oracle.Config{Host: "oracle.example.test", Port: 1521, Transport: "TCP", Service: "APP_PDB", Username: "APP", Password: "secret"}
}
