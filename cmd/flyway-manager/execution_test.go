package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const nativeFlywayVersion = "11.20.0"

type nativeFlywayFixture struct {
	configPath string
	stateDir   string
	manifest   string
}

func makeNativeFlyway(t *testing.T, root string, blockMigration bool) nativeFlywayFixture {
	t.Helper()
	state := filepath.Join(filepath.Dir(root), "fake-state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	block := ""
	if blockMigration {
		block = "yes"
	}
	// All dynamic paths are generated below from TempDir paths; shell quoting
	// keeps the fixture safe even when the test runner's temporary path has spaces.
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-v" ]; then echo 'Flyway Community Edition %s by Redgate'; exit 0; fi
cmd="$1"
for arg in "$@"; do case "$arg" in -configFiles=*) cfg=${arg#-configFiles=};; esac; done
printf '%%s\n' "$cmd" >> %s
if [ "$cmd" = "migrate" ]; then
  printf 'M\n' >> %s
  if [ %s = yes ]; then
    echo "$$" > %s
    exec /usr/bin/sleep 2
  fi
  : > %s
	  printf '%%s' '{"operation":"migrate","migrationsExecuted":1,"migrations":[{"version":"1","description":"native","type":"SQL","filepath":"/tmp/flyway-run/sql/V1__native.sql"}],"flywayVersion":"%s"}'
  exit 0
fi
if [ "$cmd" = "info" ]; then
  if [ -f %s ]; then
    printf '%%s' '{"operation":"info","schemaVersion":null,"migrations":[{"category":"Versioned","version":"1","description":"native","type":"SQL","filepath":"/tmp/flyway-run/sql/V1__native.sql","state":"Success","installedOnUTC":"2026-10-16T00:00:00Z","installedBy":"DEPLOY_ADMIN","installedRank":1}],"flywayVersion":"%s"}'
  else
    printf '%%s' '{"operation":"info","schemaVersion":null,"migrations":[{"category":"Versioned","version":"1","description":"native","type":"SQL","filepath":"/tmp/flyway-run/sql/V1__native.sql","state":"Pending","installedOnUTC":"","installedBy":"","installedRank":0}],"flywayVersion":"%s"}'
  fi
  exit 0
fi
if [ "$cmd" = "validate" ]; then
  printf '%%s' '{"operation":"validate","validationSuccessful":true,"invalidMigrations":[],"validateCount":1,"flywayVersion":"%s"}'
  exit 0
fi
exit 2
`, nativeFlywayVersion,
		q(filepath.Join(state, "commands")), q(filepath.Join(state, "migrations")), q(block),
		q(filepath.Join(state, "migrate.pid")), q(filepath.Join(state, "migrated")), nativeFlywayVersion,
		q(filepath.Join(state, "migrated")), nativeFlywayVersion, nativeFlywayVersion, nativeFlywayVersion)
	files := map[string][]byte{
		"flyway":                              []byte(script),
		"drivers/ojdbc11-native-test.jar":     []byte("test oracle driver bytes"),
		"lib/flyway-database-oracle-test.jar": []byte("test flyway oracle module bytes"),
		"jre/bin/java":                        []byte("#!/bin/sh\nexit 0\n"),
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if name == "flyway" || name == "jre/bin/java" {
			mode = 0700
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	var manifest strings.Builder
	for _, name := range []string{"drivers/ojdbc11-native-test.jar", "flyway", "jre/bin/java", "lib/flyway-database-oracle-test.jar"} {
		h := sha256.Sum256(files[name])
		fmt.Fprintf(&manifest, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	manifestBytes := []byte(manifest.String())
	manifestPath := filepath.Join(root, "flyway-manifest.sha256")
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifestBytes)
	configPath := filepath.Join(filepath.Dir(root), "manager.json")
	config := map[string]any{
		"listen_address": "127.0.0.1:0",
		"data_dir":       root,
		"log_level":      "info",
		"flyway": map[string]any{
			"executable":      filepath.Join(root, "flyway"),
			"version":         nativeFlywayVersion,
			"sha256":          hex.EncodeToString(manifestHash[:]),
			"timeout_seconds": 10,
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return nativeFlywayFixture{configPath: configPath, stateDir: state, manifest: hex.EncodeToString(manifestHash[:])}
}

func nativeJSONCall(t *testing.T, client *http.Client, base, method, path string, csrf string, body any, headers map[string]string, want int) json.RawMessage {
	t.Helper()
	var data io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, base+"/api/v1"+path, data)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	response, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s returned %d, want %d: %s", method, path, res.StatusCode, want, response)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(response, &envelope); err != nil {
		t.Fatalf("decode %s response: %v: %s", path, err, response)
	}
	return envelope.Data
}

func nativeUpload(t *testing.T, client *http.Client, base, csrf, target, schema string) json.RawMessage {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": "V1__native.sql"}))
	h.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("CREATE TABLE native_fixture(id NUMBER);\n")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/artifacts?target_id="+target+"&schema_id="+schema, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	content, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("artifact upload returned %d: %s", res.StatusCode, content)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(content, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func nativeIdentityAndRunSetup(t *testing.T, client *http.Client, p *nativeProcess, dir string) (csrf string, runInput map[string]string, artifactID string) {
	t.Helper()
	token, err := os.ReadFile(filepath.Join(dir, "secrets", "bootstrap.token"))
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/auth/bootstrap", "", map[string]string{"username": "native-executor", "password": "native-password-2026", "setup_token": strings.TrimSpace(string(token))}, nil, http.StatusCreated), &identity)
	if identity.User.ID == "" || identity.CSRF == "" {
		t.Fatal("bootstrap did not return identity and CSRF token")
	}
	nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/database-targets", identity.CSRF, map[string]any{"id": "native-e2e-target", "name": "Native E2E Oracle", "environment": "UAT", "host": "native.example", "port": 1521, "service": "APP", "transport": "TCP"}, nil, http.StatusCreated)
	var schema struct {
		ID string `json:"id"`
	}
	json.Unmarshal(nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/database-targets/native-e2e-target/schemas", identity.CSRF, map[string]string{"name": "NATIVE_APP"}, nil, http.StatusCreated), &schema)
	var account struct {
		ID string `json:"id"`
	}
	accountData := nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/execution-accounts", identity.CSRF, map[string]any{"target_id": "native-e2e-target", "name": "native-deployer", "username": "DEPLOY_ADMIN", "schema_ids": []string{schema.ID}, "password": "native-oracle-password-2026 @&"}, nil, http.StatusCreated)
	if err = json.Unmarshal(accountData, &account); err != nil || account.ID == "" {
		t.Fatalf("execution account creation failed: %v %s", err, accountData)
	}
	nativeJSONCall(t, client, p.baseURL, http.MethodPut, "/access/users/"+identity.User.ID+"/grants", identity.CSRF, map[string]any{"grants": []map[string]any{{"target_id": "native-e2e-target", "schema_id": schema.ID, "read": true, "operate": true, "prod": false}}}, nil, http.StatusOK)
	artifactData := nativeUpload(t, client, p.baseURL, identity.CSRF, "native-e2e-target", schema.ID)
	var a struct {
		ID string `json:"id"`
	}
	json.Unmarshal(artifactData, &a)
	if a.ID == "" {
		t.Fatal("artifact did not return an ID")
	}
	return identity.CSRF, map[string]string{"target_id": "native-e2e-target", "schema_id": schema.ID, "execution_account_id": account.ID, "artifact_id": a.ID}, a.ID
}

func decodeNative[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode native API response: %v: %s", err, raw)
	}
	return value
}

func waitNativeStatus(t *testing.T, client *http.Client, p *nativeProcess, csrf, runID string, statuses ...string) map[string]any {
	t.Helper()
	want := map[string]bool{}
	for _, status := range statuses {
		want[status] = true
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		raw := nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+runID, csrf, nil, nil, http.StatusOK)
		var run map[string]any
		if err := json.Unmarshal(raw, &run); err != nil {
			t.Fatal(err)
		}
		if want[fmt.Sprint(run["status"])] {
			return run
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %v", runID, statuses)
	return nil
}

func TestNativeFlywayHTTPWorkflowAndIdempotencyAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	dir := t.TempDir()
	fake := makeNativeFlyway(t, filepath.Join(dir, "distribution"), false)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	p := launchNative(t, dir, true, fake.configPath)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	csrf, input, _ := nativeIdentityAndRunSetup(t, client, p, dir)
	runtime := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/runtime", csrf, nil, nil, http.StatusOK))
	if runtime["ready"] != true || runtime["version"] != nativeFlywayVersion || runtime["integrity"] != "verified" {
		t.Fatalf("unexpected local runtime readiness: %+v", runtime)
	}
	preflight := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-preflight", csrf, input, nil, http.StatusOK))
	if preflight["ready"] != true || preflight["live_status"] != "VERIFIED" {
		t.Fatalf("fake CLI workflow did not verify preflight: %+v", preflight)
	}
	const idem = "native-flyway-run-key"
	raw := nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-runs", csrf, input, map[string]string{"Idempotency-Key": idem}, http.StatusAccepted)
	run := decodeNative[map[string]any](t, raw)
	runID := fmt.Sprint(run["id"])
	if runID == "" || runID == "<nil>" || run["status"] != "QUEUED" {
		t.Fatalf("unexpected accepted run: %+v", run)
	}
	finished := waitNativeStatus(t, client, p, csrf, runID, "SUCCEEDED")
	if finished["flyway_version"] != nativeFlywayVersion || finished["migration_outcome"] != "SUCCEEDED" {
		t.Fatalf("run omitted verified Flyway success/version: %+v", finished)
	}
	executions := decodeNative[[]map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+runID+"/executions", csrf, nil, nil, http.StatusOK))
	if len(executions) < 5 {
		t.Fatalf("actual runtime subprocess evidence missing: %+v", executions)
	}
	for _, execution := range executions {
		if execution["version"] != nil && execution["version"] != nativeFlywayVersion {
			t.Fatalf("subprocess recorded wrong version: %+v", execution)
		}
		for _, field := range []string{"stdout", "stderr", "json"} {
			if strings.Contains(fmt.Sprint(execution[field]), "native-oracle-password") {
				t.Fatalf("credential leaked in %s: %+v", field, execution)
			}
		}
	}
	logs := decodeNative[[]map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+runID+"/logs?limit=100", csrf, nil, nil, http.StatusOK))
	if len(logs) == 0 {
		t.Fatal("run did not persist command output logs")
	}
	if got, _ := os.ReadFile(filepath.Join(fake.stateDir, "migrations")); strings.Count(string(got), "M\n") != 1 {
		t.Fatalf("expected exactly one fake migration invocation, got %q", got)
	}
	p.stop(t)
	p = nil
	p = launchNative(t, dir, true, fake.configPath)
	replayed := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-runs", csrf, input, map[string]string{"Idempotency-Key": idem}, http.StatusAccepted))
	if replayed["id"] != runID || replayed["status"] != "SUCCEEDED" {
		t.Fatalf("idempotency replay after manager restart changed run: %+v", replayed)
	}
	if got, _ := os.ReadFile(filepath.Join(fake.stateDir, "migrations")); strings.Count(string(got), "M\n") != 1 {
		t.Fatalf("idempotency replay reran SQL: %q", got)
	}
	p.stop(t)
	p = nil
}

func TestNativeCrashRecoveryDoesNotReplayBlockedMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	dir := t.TempDir()
	distribution := filepath.Join(dir, "distribution")
	fake := makeNativeFlyway(t, distribution, true)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	p := launchNative(t, dir, true, fake.configPath)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	csrf, input, _ := nativeIdentityAndRunSetup(t, client, p, dir)
	first := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-runs", csrf, input, map[string]string{"Idempotency-Key": "crash-first"}, http.StatusAccepted))
	firstID := fmt.Sprint(first["id"])
	entered := filepath.Join(fake.stateDir, "migrate.pid")
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	pidData, err := os.ReadFile(entered)
	if err != nil {
		t.Fatalf("fake migrate did not reach its controlled pause: %v", err)
	}
	if strings.TrimSpace(string(pidData)) == "" {
		t.Fatal("fake migration pause omitted subprocess PID")
	}
	started := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+firstID, csrf, nil, nil, http.StatusOK))
	if started["status"] != "RUNNING" {
		t.Fatalf("fake migrate paused while run was not RUNNING: %+v", started)
	}
	secondInput := map[string]string{"target_id": input["target_id"], "schema_id": input["schema_id"], "execution_account_id": input["execution_account_id"]}
	// A second run needs its own immutable artifact. Keep it versioned after V1;
	// the current run's frozen dependency must make it BLOCKED after recovery.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": "V2__later.sql"}))
	h.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("ALTER TABLE native_fixture ADD value NUMBER;\n"))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq, err := http.NewRequest(http.MethodPost, p.baseURL+"/api/v1/artifacts?target_id="+input["target_id"]+"&schema_id="+input["schema_id"], &body)
	if err != nil {
		t.Fatal(err)
	}
	uploadReq.Header.Set("Origin", p.baseURL)
	uploadReq.Header.Set("X-CSRF-Token", csrf)
	uploadReq.Header.Set("Content-Type", w.FormDataContentType())
	uploadRes, err := client.Do(uploadReq)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	uploadBytes, _ := io.ReadAll(uploadRes.Body)
	uploadRes.Body.Close()
	if uploadRes.StatusCode != http.StatusCreated || json.Unmarshal(uploadBytes, &uploaded) != nil || uploaded.Data.ID == "" {
		t.Fatalf("V2 artifact upload failed: %d %s", uploadRes.StatusCode, uploadBytes)
	}
	secondInput["artifact_id"] = uploaded.Data.ID
	second := decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-runs", csrf, secondInput, map[string]string{"Idempotency-Key": "crash-second"}, http.StatusAccepted))
	secondID := fmt.Sprint(second["id"])
	// Simulate an abrupt process crash. The isolated fake CLI sleeps briefly and
	// exits on its own, closing the old manager's inherited output pipe.
	old := p
	if err = old.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	// Wait for the old stdout pipe reader to finish before using the same session.
	// The manager was killed, so its goroutine reports a nonzero process exit.
	// If this blocks, the timeout fails the test rather than leaking the child.
	// (The old process object is retained separately from the replacement.)

	select {
	case exitErr := <-old.done:
		if exitErr == nil {
			t.Fatal("manager crash unexpectedly exited cleanly")
		}
		<-old.logs
	case <-time.After(6 * time.Second):
		t.Fatal("old native manager process did not exit after crash")
	}
	p = nil
	p = launchNative(t, dir, true, fake.configPath)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	firstRecovered := waitNativeStatus(t, client, p, csrf, firstID, "NEEDS_REVIEW")
	secondRecovered := waitNativeStatus(t, client, p, csrf, secondID, "BLOCKED")
	if firstRecovered["migration_outcome"] != "" {
		t.Fatalf("uncertain migration was assigned a final outcome: %+v", firstRecovered)
	}
	if !strings.Contains(fmt.Sprint(secondRecovered["reason"]), "held") && !strings.Contains(fmt.Sprint(secondRecovered["reason"]), "reconciliation") {
		t.Fatalf("queued dependent run lacks recovery reason: %+v", secondRecovered)
	}
	executions := decodeNative[[]map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+firstID+"/executions", csrf, nil, nil, http.StatusOK))
	commands := make([]string, 0, len(executions))
	for _, execution := range executions {
		commands = append(commands, fmt.Sprint(execution["command"]))
	}
	// NEEDS_REVIEW is durable before asynchronous read-only reconciliation starts;
	// inspection evidence may already be arriving when this request completes.
	if strings.Contains(strings.Join(commands, ","), "migrate") || len(commands) < 2 || len(commands) > 4 || commands[0] != "info" || commands[1] != "validate" {
		t.Fatalf("interrupted mutate was recorded as completed or inspection evidence missing: %v", commands)
	}
	// Startup reconciliation is deliberately read-only and records its own
	// info/validate evidence. Wait for it to finish before checking that the
	// blocked preflight below adds no subprocess calls of its own.
	reconcileDeadline := time.Now().Add(5 * time.Second)
	var reconciled []map[string]any
	for time.Now().Before(reconcileDeadline) {
		raw := nativeJSONCall(t, client, p.baseURL, http.MethodGet, "/migration-runs/"+firstID+"/executions", csrf, nil, nil, http.StatusOK)
		if err := json.Unmarshal(raw, &reconciled); err != nil {
			t.Fatal(err)
		}
		if len(reconciled) >= 4 && reconciled[len(reconciled)-2]["command"] == "info" && reconciled[len(reconciled)-1]["command"] == "validate" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(reconciled) != 4 || reconciled[0]["command"] != "info" || reconciled[1]["command"] != "validate" || reconciled[2]["command"] != "info" || reconciled[3]["command"] != "validate" {
		t.Fatalf("startup read-only reconciliation did not persist completed info/validate evidence: %+v", reconciled)
	}
	before, err := os.ReadFile(filepath.Join(fake.stateDir, "commands"))
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	var preflight map[string]any
	for time.Now().Before(deadline) {
		preflight = decodeNative[map[string]any](t, nativeJSONCall(t, client, p.baseURL, http.MethodPost, "/migration-preflight", csrf, secondInput, nil, http.StatusOK))
		blockers, _ := preflight["blockers"].([]any)
		busy := false
		for _, value := range blockers {
			item, _ := value.(map[string]any)
			busy = busy || item["code"] == "STREAM_BUSY"
		}
		if !busy {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if preflight == nil || preflight["ready"] == true {
		t.Fatalf("held stream passed read-only preflight: %+v", preflight)
	}
	after, err := os.ReadFile(filepath.Join(fake.stateDir, "commands"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("held-stream preflight executed database commands after read-only recovery inspection: preflight=%+v before=%q after=%q err=%v", preflight, before, after, err)
	}
	if got, err := os.ReadFile(filepath.Join(fake.stateDir, "migrations")); err != nil || strings.Count(string(got), "M\n") != 1 {
		t.Fatalf("migration SQL was replayed after recovery: %q err=%v", got, err)
	}
	p.stop(t)
	p = nil
}
