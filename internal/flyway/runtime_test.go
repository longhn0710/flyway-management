package flyway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeVersion = "10.0.0"

func fakeRuntime(t *testing.T, body string, timeout int) (*Runtime, string) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "flyway")
	script := "#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo 'Flyway Community Edition " + fakeVersion + " by Redgate'; exit 0; fi\n" + body + "\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"flyway":                              []byte(script),
		"drivers/ojdbc11-test.jar":            []byte("fake oracle driver"),
		"lib/flyway-database-oracle-test.jar": []byte("fake flyway oracle module"),
		"jre/bin/java":                        []byte("#!/bin/sh\nexit 0\n"),
	}
	for name, data := range files {
		if name == "flyway" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if name == "jre/bin/java" {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	manifest := ""
	for _, name := range []string{"drivers/ojdbc11-test.jar", "flyway", "jre/bin/java", "lib/flyway-database-oracle-test.jar"} {
		h := sha256.Sum256(files[name])
		manifest += fmt.Sprintf("%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	manifestPath := filepath.Join(root, "flyway-manifest.sha256")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256([]byte(manifest))
	r := New(Config{Executable: exe, Version: fakeVersion, SHA256: hex.EncodeToString(manifestHash[:]), TimeoutSeconds: timeout}, filepath.Join(t.TempDir(), "private"))
	return r, root
}

func testRequest(command string) Request {
	return Request{Command: command, Host: "db.example.test", Port: 1521, Service: "ORCLPDB1", Transport: "TCP", Schema: "APP", HistorySchema: "APP", HistoryTable: "flyway_schema_history", Username: "APP_USER", Password: "p@ss word&secret", Files: map[string][]byte{"V1__initial.sql": []byte("select 1 from dual;\n")}}
}

func TestCheckVerifiesManifestAndVersion(t *testing.T) {
	r, root := fakeRuntime(t, `printf '{"operation":"info","schemaVersion":null,"migrations":[],"flywayVersion":"10.0.0"}'`, 3)
	got := r.Check(context.Background())
	if !got.Ready || got.Integrity != "verified" || got.Dependency != "bundled_java_and_oracle_jdbc_available" || got.Version != fakeVersion {
		t.Fatalf("unexpected readiness: %+v", got)
	}
	r.config.Version = "10.0.1"
	wrongVersion := r.Check(context.Background())
	if wrongVersion.Ready || !strings.Contains(strings.Join(wrongVersion.Blockers, " "), "version does not match") {
		t.Fatalf("wrong Flyway version accepted: %+v", wrongVersion)
	}
	r.config.Version = fakeVersion
	if err := os.WriteFile(filepath.Join(root, "unlisted.jar"), []byte("changed distribution"), 0600); err != nil {
		t.Fatal(err)
	}
	got = r.Check(context.Background())
	if got.Ready || !strings.Contains(strings.Join(got.Blockers, " "), "unlisted distribution file") {
		t.Fatalf("unlisted runtime file not rejected: %+v", got)
	}
	if err := os.Remove(filepath.Join(root, "unlisted.jar")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flyway"), []byte("tampered launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	got = r.Check(context.Background())
	if got.Ready || !strings.Contains(strings.Join(got.Blockers, " "), "distribution file checksum mismatch") {
		t.Fatalf("changed runtime file not rejected: %+v", got)
	}
}

func TestExecuteUsesStructuredSuccessAndRedactsSecrets(t *testing.T) {
	body := `printf '{"operation":"migrate","migrationsExecuted":1,"migrations":[{"version":"1"}],"flywayVersion":"10.0.0","message":"p@ss word&secret","password":"p@ss word&secret"}'`
	r, _ := fakeRuntime(t, body, 3)
	got := r.Execute(context.Background(), testRequest("migrate"))
	if !got.Success || got.ExitCode != 0 || got.Version != fakeVersion {
		t.Fatalf("unexpected result: %+v", got)
	}
	joined := got.Stdout + got.Stderr + string(got.JSON)
	if strings.Contains(joined, "p@ss") || strings.Contains(joined, "secret") {
		t.Fatalf("secret leaked: %s", joined)
	}
	if !strings.Contains(string(got.JSON), "[REDACTED]") {
		t.Fatalf("structured secret was not redacted: %s", got.JSON)
	}
}

func TestExecuteRejectsExitZeroWithMalformedOrUnsuccessfulJSON(t *testing.T) {
	for _, body := range []string{`echo 'not json'`, `printf '{"operation":"migrate","migrationsExecuted":1,"migrations":[{"version":"1"}],"flywayVersion":"10.0.0"}'`, `printf '{"operation":"validate","operation":"validate","validationSuccessful":true,"invalidMigrations":[],"validateCount":0,"flywayVersion":"10.0.0"}'`} {
		r, _ := fakeRuntime(t, body, 3)
		got := r.Execute(context.Background(), testRequest("validate"))
		if got.Success {
			t.Fatalf("invalid result accepted: %+v", got)
		}
	}
}

func TestExecuteTimeoutIsAmbiguousAndCleansUp(t *testing.T) {
	r, _ := fakeRuntime(t, `sleep 5; printf '{"operation":"migrate","migrationsExecuted":1,"migrations":[{"version":"1"}],"flywayVersion":"10.0.0"}'`, 1)
	start := time.Now()
	got := r.Execute(context.Background(), testRequest("migrate"))
	if !got.TimedOut || !got.Ambiguous || got.Success {
		t.Fatalf("timeout not marked ambiguous: %+v", got)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("process group cleanup took too long: %s", time.Since(start))
	}
}

func TestExecuteRejectsUnsafeInputAndNoShellExpansion(t *testing.T) {
	r, _ := fakeRuntime(t, `printf '{"operation":"info","schemaVersion":null,"migrations":[],"flywayVersion":"10.0.0"}'`, 3)
	bad := testRequest("info")
	bad.Host = "db.example;touch /tmp/unsafe"
	if got := r.Execute(context.Background(), bad); got.Success || !strings.Contains(got.Stderr, "invalid Oracle host") {
		t.Fatalf("unsafe host accepted: %+v", got)
	}
	bad = testRequest("info")
	bad.Files = map[string][]byte{"../../escaped.sql": []byte("bad")}
	if got := r.Execute(context.Background(), bad); got.Success || !strings.Contains(got.Stderr, "unsafe migration path") {
		t.Fatalf("unsafe path accepted: %+v", got)
	}
}

func TestExecuteUsesArgvPrivateConfigAndRestrictedEnvironment(t *testing.T) {
	t.Setenv("SHOULD_NOT_INHERIT", "ambient-value")
	body := `for arg in "$@"; do case "$arg" in *secret*) exit 21;; -configFiles=*) config=${arg#-configFiles=};; esac; done
[ -n "$JAVA_HOME" ] || exit 22
case "$PATH" in "$JAVA_HOME/bin:"*) ;; *) exit 22;; esac
[ -z "$SHOULD_NOT_INHERIT" ] || exit 23
[ "$HOME" = "$TMPDIR" ] || exit 24
case "$config" in *.toml) ;; *) exit 25;; esac
grep -F 'password = "!{p@ss word\u0026secret}"' "$config" >/dev/null || exit 25
printf '{"operation":"info","schemaVersion":null,"migrations":[],"flywayVersion":"10.0.0"}'`
	r, _ := fakeRuntime(t, body, 3)
	got := r.Execute(context.Background(), testRequest("info"))
	if !got.Success {
		t.Fatalf("expected isolated invocation to succeed: %+v", got)
	}
}

func TestMutatingCommandWithUnverifiedOutputIsAmbiguous(t *testing.T) {
	r, _ := fakeRuntime(t, `echo '{"unexpected":true}'`, 3)
	got := r.Execute(context.Background(), testRequest("migrate"))
	if got.Success || !got.Ambiguous {
		t.Fatalf("unverified migrate result must be ambiguous: %+v", got)
	}
}

func TestInstallCopiesVerifiedDistributionAndPreservesExistingPin(t *testing.T) {
	sourceRuntime, source := fakeRuntime(t, `printf '{"operation":"info","schemaVersion":null,"migrations":[],"flywayVersion":"10.0.0"}'`, 3)
	dataDir := filepath.Join(t.TempDir(), "application-data")
	installed, err := Install(context.Background(), source, "flyway", fakeVersion, sourceRuntime.config.SHA256, dataDir)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if installed.Executable != filepath.Join(dataDir, "runtime", "flyway", fakeVersion+"-"+sourceRuntime.config.SHA256[:12], "flyway") {
		t.Fatalf("unexpected installed path: %s", installed.Executable)
	}
	if ready := New(installed, dataDir).Check(context.Background()); !ready.Ready {
		t.Fatalf("installed runtime is not ready: %+v", ready)
	}
	installedBytes, err := os.ReadFile(installed.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), source, "flyway", fakeVersion, sourceRuntime.config.SHA256, dataDir); err == nil {
		t.Fatal("second install unexpectedly replaced the pinned installation")
	}
	gotBytes, err := os.ReadFile(installed.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBytes) != string(installedBytes) {
		t.Fatal("existing installation changed after duplicate install")
	}
	info, err := os.Stat(filepath.Dir(installed.Executable))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("installed directory mode = %o, want 700", info.Mode().Perm())
	}
}

func TestInstallRejectsUntrustedOrUnsafeSource(t *testing.T) {
	sourceRuntime, source := fakeRuntime(t, `echo Flyway`, 3)
	dataDir := filepath.Join(t.TempDir(), "application-data")
	if _, err := Install(context.Background(), source, "../flyway", fakeVersion, sourceRuntime.config.SHA256, dataDir); err == nil {
		t.Fatal("path traversal executable accepted")
	}
	if err := os.WriteFile(filepath.Join(source, "drivers", "ojdbc11-test.jar"), []byte("tampered driver"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), source, "flyway", fakeVersion, sourceRuntime.config.SHA256, dataDir); err == nil {
		t.Fatal("distribution with a bad file checksum accepted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runtime", "flyway")); !os.IsNotExist(err) {
		t.Fatalf("failed installation left an install directory: %v", err)
	}
}

func TestTruncatedStructuredOutputCannotReportSuccess(t *testing.T) {
	body := `printf '{"operation":"migrate","migrationsExecuted":0,"migrations":[],"flywayVersion":"10.0.0"}'; head -c 3000000 /dev/zero | tr '\000' ' '`
	r, _ := fakeRuntime(t, body, 5)
	got := r.Execute(context.Background(), testRequest("migrate"))
	if got.Success || !got.Ambiguous || !strings.Contains(got.Stdout, "output truncated") {
		t.Fatalf("truncated output not handled safely: %+v", got)
	}
}

func TestStrictSuccessShapesForValidationAndRepair(t *testing.T) {
	validate := []byte(`{"operation":"validate","validationSuccessful":true,"invalidMigrations":[],"validateCount":2,"flywayVersion":"10.0.0"}`)
	if !validSuccess("validate", validate, fakeVersion) {
		t.Fatal("valid validation result rejected")
	}
	validateWithInvalid := []byte(`{"operation":"validate","validationSuccessful":true,"invalidMigrations":[{"state":"FAILED"}],"validateCount":2,"flywayVersion":"10.0.0"}`)
	if validSuccess("validate", validateWithInvalid, fakeVersion) {
		t.Fatal("validation with invalid migrations accepted")
	}
	repair := []byte(`{"operation":"repair","repairActions":[],"migrationsRemoved":[],"migrationsDeleted":[],"migrationsAligned":[],"flywayVersion":"10.0.0"}`)
	if !validSuccess("repair", repair, fakeVersion) {
		t.Fatal("complete repair result rejected")
	}
	if validSuccess("repair", []byte(`{"operation":"repair","repairActions":[],"migrationsRemoved":[],"migrationsDeleted":[],"flywayVersion":"10.0.0"}`), fakeVersion) {
		t.Fatal("repair without aligned field accepted")
	}
	withError := []byte(`{"operation":"migrate","migrationsExecuted":0,"migrations":[],"errorDetails":{"message":"failure"},"flywayVersion":"10.0.0"}`)
	if validSuccess("migrate", withError, fakeVersion) {
		t.Fatal("migrate result containing errorDetails accepted")
	}
}

func TestRedactionLeavesPublicUsernameMetadataIntact(t *testing.T) {
	req := testRequest("info")
	got := string(redactJSON([]byte(`{"schemaName":"APP_USER","installedBy":"APP_USER"}`), req))
	if !strings.Contains(got, `"schemaName":"APP_USER"`) || !strings.Contains(got, `"installedBy":"APP_USER"`) {
		t.Fatalf("public username metadata was altered: %s", got)
	}
}

func TestRuntimeCapacityIsBoundedAcrossHTTPAndWorkers(t *testing.T) {
	r, _ := fakeRuntime(t, `exit 0`, 3)
	for i := 0; i < 4; i++ {
		if !r.acquire(context.Background()) {
			t.Fatal("capacity lease")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ready := r.Check(ctx)
	if ready.Ready || !strings.Contains(strings.Join(ready.Blockers, " "), "capacity") {
		t.Fatalf("unbounded readiness process %+v", ready)
	}
	for i := 0; i < 4; i++ {
		r.release()
	}
	if !r.Check(context.Background()).Ready {
		t.Fatal("runtime did not recover its capacity")
	}
}

func TestJSONEscapedPasswordsAreRedactedFromBothLogChannels(t *testing.T) {
	req := testRequest("info")
	req.Password = "quote\"backslash\\and&'secret="
	payload, _ := json.Marshal(map[string]any{"operation": "info", "schemaVersion": nil, "migrations": []any{}, "flywayVersion": fakeVersion, "diagnostic": req.Password, "password": req.Password})
	quoted := "'" + strings.ReplaceAll(string(payload), "'", "'\\''") + "'"
	r, _ := fakeRuntime(t, "printf '%s' "+quoted+"\nprintf '%s' "+quoted+" >&2", 3)
	got := r.Execute(context.Background(), req)
	if !got.Success {
		t.Fatalf("structured result failed %+v", got)
	}
	for _, text := range []string{got.Stdout, got.Stderr, string(got.JSON)} {
		if strings.Contains(text, "secret") || strings.Contains(text, "backslash") {
			t.Fatalf("JSON-escaped credential leaked: %s", text)
		}
	}
}
