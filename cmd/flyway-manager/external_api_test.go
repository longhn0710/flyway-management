package main

import (
	"archive/zip"
	"bytes"
	"context"
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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type externalResponse struct {
	status int
	header http.Header
	body   []byte
}

func externalCall(t *testing.T, client *http.Client, base, method, path, token, csrf string, body any, headers map[string]string) externalResponse {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, base+path, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Origin", base)
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
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return externalResponse{status: res.StatusCode, header: res.Header.Clone(), body: data}
}

func externalData(t *testing.T, res externalResponse, want int) json.RawMessage {
	t.Helper()
	if res.status != want {
		t.Fatalf("HTTP status %d, want %d: %s", res.status, want, res.body)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.body, &envelope); err != nil {
		t.Fatalf("decode response: %v: %s", err, res.body)
	}
	return envelope.Data
}

func externalMultipart(t *testing.T, client *http.Client, base, path, token, csrf, key string, archive []byte, revision int, checksum, source string) externalResponse {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": "release.zip"}))
	h.Set("Content-Type", "application/zip")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(archive); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"deployment_target_revision": fmt.Sprint(revision), "input_sha256": checksum, "external_source": source} {
		if err = w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Idempotency-Key", key)
	if csrf != "" {
		req.Header.Set("Origin", base)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return externalResponse{status: res.StatusCode, header: res.Header.Clone(), body: data}
}

func externalZIP(t *testing.T, filename, sql string) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	f, err := w.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestExternalProjectAPIUsesNativeWorkerAndPersistsImmutableEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	dir := t.TempDir()
	fake := makeNativeFlyway(t, filepath.Join(dir, "distribution"), false)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	admin := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	client := &http.Client{Timeout: 8 * time.Second}
	p := launchNative(t, dir, true, fake.configPath)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	setup, err := os.ReadFile(filepath.Join(dir, "secrets", "bootstrap.token"))
	if err != nil {
		t.Fatal(err)
	}
	identity := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/auth/bootstrap", "", map[string]string{"username": "external-admin", "password": "external-admin-password-2026", "setup_token": strings.TrimSpace(string(setup))}, nil, http.StatusCreated))
	user := identity["user"].(map[string]any)
	adminID := user["id"].(string)
	csrf := identity["csrf_token"].(string)
	targetID := "external-native-target"
	nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/database-targets", csrf, map[string]any{"id": targetID, "name": "External native Oracle", "environment": "UAT", "host": "native.example", "port": 1521, "service": "APP", "transport": "TCP"}, nil, http.StatusCreated)
	schemaID := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/database-targets/"+targetID+"/schemas", csrf, map[string]string{"name": "EXTERNAL_APP"}, nil, http.StatusCreated))["id"].(string)
	account := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/execution-accounts", csrf, map[string]any{"target_id": targetID, "name": "external-deployer", "username": "DEPLOY_ADMIN", "schema_ids": []string{schemaID}, "password": "native-oracle-password-2026 @&"}, nil, http.StatusCreated))
	accountID := account["id"].(string)
	nativeJSONCall(t, admin, p.baseURL, http.MethodPut, "/access/users/"+adminID+"/grants", csrf, map[string]any{"grants": []map[string]any{{"target_id": targetID, "schema_id": schemaID, "read": true, "operate": true, "prod": false}}}, nil, http.StatusOK)
	project := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/projects", csrf, map[string]any{"name": "external-main", "status": "active", "members": []map[string]string{{"target_id": targetID, "schema_id": schemaID}}}, nil, http.StatusCreated))
	projectID := project["id"].(string)
	otherProject := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/projects", csrf, map[string]any{"name": "external-other", "status": "active", "members": []any{}}, nil, http.StatusCreated))
	otherProjectID := otherProject["id"].(string)
	targetPath := "/deployment-targets"
	dep := externalData(t, externalCall(t, admin, p.baseURL, http.MethodPost, "/api/v2/projects/"+projectID+targetPath, "", csrf, map[string]any{"name": "uat", "environment": "UAT", "target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "active": true}, nil), http.StatusCreated)
	deployment := decodeNative[map[string]any](t, dep)
	deploymentID := deployment["id"].(string)
	grantBody := map[string]any{"principal_kind": "service", "principal_id": "", "read": true, "upload": true, "preflight": true, "execute": true}
	service := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/access/service-accounts", csrf, map[string]any{"name": "external-ci", "owner_id": adminID, "purpose": "deployment acceptance", "active": true}, nil, http.StatusCreated))
	serviceID := service["id"].(string)
	grantBody["principal_id"] = serviceID
	externalData(t, externalCall(t, admin, p.baseURL, http.MethodPut, "/api/v2/projects/"+projectID+targetPath+"/"+deploymentID+"/grants", "", csrf, grantBody, nil), http.StatusOK)
	grants := externalData(t, externalCall(t, admin, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+targetPath+"/"+deploymentID+"/grants", "", "", nil, nil), http.StatusOK)
	if !bytes.Contains(grants, []byte(serviceID)) {
		t.Fatal("deployment grant listing omitted the configured service principal")
	}
	legacyGrant := nativeJSONCall(t, admin, p.baseURL, http.MethodPut, "/access/service-accounts/"+serviceID+"/grants", csrf, map[string]any{"grants": []map[string]any{{"target_id": targetID, "schema_id": schemaID, "read": true, "operate": true, "prod": false}}}, nil, http.StatusOK)
	_ = legacyGrant
	issue := func(name string, execute bool) string {
		t.Helper()
		issued := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/access/service-accounts/"+serviceID+"/tokens", csrf, map[string]any{"name": name, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "deployment_scopes": []map[string]any{{"deployment_target_id": deploymentID, "read": true, "upload": true, "preflight": true, "execute": execute}}}, nil, http.StatusCreated))
		return issued["token"].(string)
	}
	token := issue("external-ci", true)
	noExecuteToken := issue("external-read-preflight", false)
	// Use an independent bearer-only HTTP client for authentication and discovery.
	missingToken := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects", "", "", nil, nil)
	if missingToken.status != http.StatusUnauthorized || !strings.HasPrefix(missingToken.header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("missing token: %d %s", missingToken.status, missingToken.body)
	}
	projectList := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects", token, "", nil, nil)
	visibleProjects := externalData(t, projectList, http.StatusOK)
	if !bytes.Contains(visibleProjects, []byte(projectID)) || bytes.Contains(visibleProjects, []byte(otherProjectID)) {
		t.Fatalf("project isolation failed: %s", visibleProjects)
	}
	projectResponse := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID, token, "", nil, nil)
	externalData(t, projectResponse, http.StatusOK)
	targetList := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+targetPath, token, "", nil, nil)
	if !bytes.Contains(externalData(t, targetList, http.StatusOK), []byte(deploymentID)) {
		t.Fatal("authorized deployment target absent")
	}
	targetResponse := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+targetPath+"/"+deploymentID, token, "", nil, nil)
	externalData(t, targetResponse, http.StatusOK)
	crossProject := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+otherProjectID, token, "", nil, nil)
	wrongTarget := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+otherProjectID+targetPath+"/"+deploymentID, token, "", nil, nil)
	hiddenTargets := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+otherProjectID+targetPath, token, "", nil, nil)
	if crossProject.status != 404 || wrongTarget.status != 404 || hiddenTargets.status != 404 {
		t.Fatalf("cross-project visibility: %d %d %d", crossProject.status, wrongTarget.status, hiddenTargets.status)
	}

	zipBytes := externalZIP(t, "V1__native.sql", "CREATE TABLE external_acceptance(id NUMBER);\n")
	zipHash := sha256.Sum256(zipBytes)
	digest := hex.EncodeToString(zipHash[:])
	source := `{"system":"acceptance","execution_id":"native-1","revision":"abc123"}`
	artifactPath := "/api/v2/projects/" + projectID + targetPath + "/" + deploymentID + "/artifacts"
	// A wrong digest is rejected without creating an artifact.
	bad := externalMultipart(t, client, p.baseURL, artifactPath, token, "", "external-upload-wrong", zipBytes, 1, strings.Repeat("0", 64), source)
	if bad.status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong upload digest status %d: %s", bad.status, bad.body)
	}
	const uploadKey = "external-native-upload-key"
	var uploadResponses [2]externalResponse
	var uploadWG sync.WaitGroup
	for i := range uploadResponses {
		uploadWG.Add(1)
		go func(i int) {
			defer uploadWG.Done()
			uploadResponses[i] = externalMultipart(t, client, p.baseURL, artifactPath, token, "", uploadKey, zipBytes, 1, digest, source)
		}(i)
	}
	uploadWG.Wait()
	first := externalData(t, uploadResponses[0], http.StatusCreated)
	second := externalData(t, uploadResponses[1], http.StatusCreated)
	artifact := decodeNative[map[string]any](t, first)
	artifactID := artifact["id"].(string)
	if artifactID != decodeNative[map[string]any](t, second)["id"] {
		t.Fatal("concurrent upload retry created multiple artifacts")
	}
	if artifact["input_sha256"] != digest {
		t.Fatalf("artifact checksum mismatch: %+v", artifact)
	}
	files := artifact["files"].([]any)
	if len(files) != 1 || !strings.Contains(fmt.Sprint(files[0]), "V1__native.sql") {
		t.Fatalf("artifact migration inventory mismatch: %+v", files)
	}
	otherArtifact := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+otherProjectID+"/artifacts/"+artifactID, token, "", nil, nil)
	if otherArtifact.status != http.StatusNotFound {
		t.Fatalf("cross-project artifact lookup returned %d: %s", otherArtifact.status, otherArtifact.body)
	}
	preflightInput := map[string]any{"artifact_id": artifactID, "deployment_target_revision": 1, "external_source": json.RawMessage(source)}
	preflightPath := "/api/v2/projects/" + projectID + targetPath + "/" + deploymentID + "/preflights"
	preflight := decodeNative[map[string]any](t, externalData(t, externalCall(t, client, p.baseURL, http.MethodPost, preflightPath, token, "", preflightInput, nil), http.StatusOK))
	destination := preflight["destination"].(map[string]any)
	if destination["target_id"] != targetID || destination["schema_id"] != schemaID || destination["execution_account_id"] != accountID || preflight["live_status"] != "VERIFIED" {
		t.Fatalf("preflight destination/readiness incorrect: %+v", preflight)
	}
	if preflight["ready"] != true {
		t.Fatalf("native preflight was not ready: %+v", preflight)
	}
	preflightID := preflight["id"].(string)
	runInput := map[string]any{"artifact_id": artifactID, "preflight_id": preflightID, "deployment_target_revision": 1, "external_source": json.RawMessage(source)}
	runPath := "/api/v2/projects/" + projectID + targetPath + "/" + deploymentID + "/migration-runs"
	denied := externalCall(t, client, p.baseURL, http.MethodPost, runPath, noExecuteToken, "", runInput, map[string]string{"Idempotency-Key": "external-denied"})
	if denied.status != http.StatusForbidden {
		t.Fatalf("token without execute scope returned %d: %s", denied.status, denied.body)
	}
	// A project-scoped token cannot reach the legacy executor through v1.
	v1req, _ := http.NewRequest(http.MethodPost, p.baseURL+"/api/v1/migration-runs", bytes.NewReader(mustJSON(t, map[string]any{"target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "artifact_id": artifactID})))
	v1req.Header.Set("Authorization", "Bearer "+token)
	v1req.Header.Set("Content-Type", "application/json")
	v1res, err := client.Do(v1req)
	if err != nil {
		t.Fatal(err)
	}
	v1Body, _ := io.ReadAll(v1res.Body)
	v1res.Body.Close()
	if v1res.StatusCode != http.StatusForbidden {
		t.Fatalf("scoped token used v1 executor: %d %s", v1res.StatusCode, v1Body)
	}

	// Existing unscoped v1 tokens remain usable only on unadopted streams.
	legacyIssued := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/access/service-accounts/"+serviceID+"/tokens", csrf, map[string]any{"name": "external-legacy", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, nil, http.StatusCreated))
	legacyToken := legacyIssued["token"].(string)
	legacyBypass := externalCall(t, client, p.baseURL, http.MethodPost, "/api/v1/migration-runs", legacyToken, "", map[string]any{"target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "artifact_id": artifactID}, map[string]string{"Idempotency-Key": "legacy-bypass"})
	if legacyBypass.status != http.StatusConflict || !bytes.Contains(legacyBypass.body, []byte("PROJECT_TARGET_REQUIRED")) {
		t.Fatalf("legacy token bypass: %d %s", legacyBypass.status, legacyBypass.body)
	}
	const runKey = "external-native-run-key"
	var runResponses [2]externalResponse
	var runWG sync.WaitGroup
	for i := range runResponses {
		runWG.Add(1)
		go func(i int) {
			defer runWG.Done()
			runResponses[i] = externalCall(t, client, p.baseURL, http.MethodPost, runPath, token, "", runInput, map[string]string{"Idempotency-Key": runKey})
		}(i)
	}
	runWG.Wait()
	runData := externalData(t, runResponses[0], http.StatusAccepted)
	run := decodeNative[map[string]any](t, runData)
	runID := run["id"].(string)
	if runID != decodeNative[map[string]any](t, externalData(t, runResponses[1], http.StatusAccepted))["id"] {
		t.Fatal("concurrent submit replay returned another run")
	}
	if runResponses[0].header.Get("Location") == "" || runResponses[0].header.Get("Retry-After") == "" {
		t.Fatal("202 response omitted Location or Retry-After")
	}
	runURL := "/api/v2/projects/" + projectID + "/migration-runs/" + runID
	deadline := time.Now().Add(20 * time.Second)
	var finished map[string]any
	for time.Now().Before(deadline) {
		finished = decodeNative[map[string]any](t, externalData(t, externalCall(t, client, p.baseURL, http.MethodGet, runURL, token, "", nil, nil), http.StatusOK))
		if finished["completed"] == true {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finished["completed"] != true || finished["status"] != "SUCCEEDED" || finished["record_available"] != true {
		t.Fatalf("run did not settle with immutable record: %+v", finished)
	}
	history := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+"/migration-runs?limit=1", token, "", nil, nil)
	if !bytes.Contains(externalData(t, history, http.StatusOK), []byte(runID)) {
		t.Fatal("project history omitted its attributed run")
	}
	if strings.Contains(fmt.Sprint(finished), "native-oracle-password") {
		t.Fatal("run response exposed execution credential")
	}
	recordResponse := externalCall(t, client, p.baseURL, http.MethodGet, runURL+"/record", token, "", nil, nil)
	if recordResponse.status != http.StatusOK {
		t.Fatalf("record response %d: %s", recordResponse.status, recordResponse.body)
	}
	if recordResponse.header.Get("ETag") == "" || !strings.Contains(recordResponse.header.Get("Content-Disposition"), "run-") || recordResponse.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("record response headers are incomplete: %v", recordResponse.header)
	}
	for _, values := range recordResponse.header {
		for _, value := range values {
			if strings.Contains(value, token) {
				t.Fatal("record response header exposed service token")
			}
		}
	}
	var record map[string]any
	if err = json.Unmarshal(recordResponse.body, &record); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if record["run"] == nil || record["executions"] == nil || record["logs"] == nil || record["repair"] == nil {
		t.Fatalf("record omitted evidence: keys=%v", record)
	}
	if strings.Contains(string(recordResponse.body), "native-oracle-password") {
		t.Fatal("execution record exposed credential")
	}
	immutableBytes := append([]byte(nil), recordResponse.body...)
	// Exercise the published Python client's retry state against the real server.
	// Seed the exact accepted request without a run id to model a lost 202 response.
	var sourceObject map[string]any
	if err = json.Unmarshal([]byte(source), &sourceObject); err != nil {
		t.Fatal(err)
	}
	clientZip := filepath.Join(dir, "client-release.zip")
	if err = os.WriteFile(clientZip, zipBytes, 0600); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "external-client-state.json")
	state := map[string]any{
		"url": p.baseURL, "project": "external-main", "deployment_target": "uat", "key": runKey,
		"input_sha256": digest, "external_source": sourceObject,
		"project_id": projectID, "deployment_target_id": deploymentID, "deployment_target_revision": 1,
		"artifact_id": artifactID, "preflight_id": preflightID,
		"run_input": map[string]any{"artifact_id": artifactID, "preflight_id": preflightID, "deployment_target_revision": 1, "external_source": sourceObject},
	}
	if err = os.WriteFile(stateFile, mustJSON(t, state), 0600); err != nil {
		t.Fatal(err)
	}
	clientCmd := exec.Command("python3", "../../scripts/external-api-client.py", "--url", p.baseURL, "--project", "external-main", "--deployment-target", "uat", "--zip", clientZip, "--key", runKey, "--external-source", source, "--state-file", stateFile, "--poll-seconds", "0.01", "--max-polls", "2")
	clientCmd.Env = append(os.Environ(), "FM_TOKEN="+token)
	clientOutput, clientErr := clientCmd.CombinedOutput()
	if clientErr != nil {
		t.Fatalf("generic client could not replay the accepted request: %v: %s", clientErr, clientOutput)
	}
	if bytes.Contains(clientOutput, []byte(token)) {
		t.Fatal("generic client printed its service token")
	}
	stateInfo, err := os.Stat(stateFile)
	if err != nil || stateInfo.Mode().Perm() != 0600 {
		t.Fatalf("retry state file is not private: %v", err)
	}
	var savedState map[string]any
	if err = json.Unmarshal(mustRead(t, stateFile), &savedState); err != nil || savedState["run_id"] != runID {
		t.Fatalf("client did not persist the replayed run id: %v %v", savedState, err)
	}
	// Changing active metadata advances the mapping revision. Existing immutable run evidence remains readable.
	updated := map[string]any{"name": "uat-renamed", "environment": "UAT", "target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "active": true, "revision": 1}
	deploymentURL := "/api/v2/projects/" + projectID + targetPath + "/" + deploymentID
	missingPrecondition := externalCall(t, admin, p.baseURL, http.MethodPatch, deploymentURL, "", csrf, updated, nil)
	if missingPrecondition.status != http.StatusPreconditionRequired {
		t.Fatalf("PATCH without If-Match returned %d, want 428: %s", missingPrecondition.status, missingPrecondition.body)
	}
	stalePrecondition := externalCall(t, admin, p.baseURL, http.MethodPatch, deploymentURL, "", csrf, updated, map[string]string{"If-Match": `"0"`})
	if stalePrecondition.status != http.StatusPreconditionFailed {
		t.Fatalf("PATCH with stale If-Match returned %d, want 412: %s", stalePrecondition.status, stalePrecondition.body)
	}
	updatedResponse := externalData(t, externalCall(t, admin, p.baseURL, http.MethodPatch, deploymentURL, "", csrf, updated, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	updatedDeployment := decodeNative[map[string]any](t, updatedResponse)
	if updatedDeployment["revision"] != float64(2) {
		t.Fatalf("valid PATCH did not advance revision: %s", updatedResponse)
	}
	staleInput := map[string]any{"artifact_id": artifactID, "preflight_id": preflightID, "deployment_target_revision": 1, "external_source": json.RawMessage(source)}
	stale := externalCall(t, client, p.baseURL, http.MethodPost, runPath, token, "", staleInput, map[string]string{"Idempotency-Key": "external-stale-run"})
	if stale.status != http.StatusConflict {
		t.Fatalf("stale deployment revision returned %d: %s", stale.status, stale.body)
	}
	logResponse := externalCall(t, client, p.baseURL, http.MethodGet, runURL+"/logs?limit=1", token, "", nil, nil)
	logs := externalData(t, logResponse, http.StatusOK)
	if !bytes.Contains(logs, []byte(`"sequence"`)) {
		t.Fatal("run logs did not contain command output")
	}
	if !strings.HasPrefix(logResponse.header.Get("Content-Type"), "application/json") || strings.Contains(string(logResponse.body), token) {
		t.Fatal("log response headers/content invalid or leaked the service token")
	}
	var logPage struct {
		Data []json.RawMessage `json:"data"`
		Page struct {
			NextCursor string `json:"next_cursor"`
		} `json:"page"`
	}
	if err = json.Unmarshal(logResponse.body, &logPage); err != nil {
		t.Fatal(err)
	}
	if logPage.Page.NextCursor != "" {
		next := externalCall(t, client, p.baseURL, http.MethodGet, runURL+"/logs?limit=1&cursor="+logPage.Page.NextCursor, token, "", nil, nil)
		if len(externalData(t, next, http.StatusOK)) == 0 {
			t.Fatal("next log cursor returned no evidence")
		}
		misused := externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+"/migration-runs?cursor="+logPage.Page.NextCursor, token, "", nil, nil)
		if misused.status != http.StatusBadRequest {
			t.Fatalf("cursor was accepted for another resource: %d %s", misused.status, misused.body)
		}
	}
	// Retirement is revisioned. Existing idempotency keys still retrieve the
	// original immutable artifact and run, while new writes are denied.
	retire := map[string]any{"name": "uat-renamed", "environment": "UAT", "target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "active": false, "revision": 2}
	retiredResponse := externalData(t, externalCall(t, admin, p.baseURL, http.MethodPatch, deploymentURL, "", csrf, retire, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	retired := decodeNative[map[string]any](t, retiredResponse)
	if retired["active"] != false || retired["revision"] != float64(3) {
		t.Fatalf("Deployment Target was not retired at revision 3: %s", retiredResponse)
	}
	replayedUpload := externalMultipart(t, client, p.baseURL, artifactPath, token, "", uploadKey, zipBytes, 1, digest, source)
	replayedArtifact := decodeNative[map[string]any](t, externalData(t, replayedUpload, http.StatusCreated))
	if replayedArtifact["id"] != artifactID {
		t.Fatalf("retired-target upload replay returned another artifact: %s", replayedUpload.body)
	}
	replayedRun := externalCall(t, client, p.baseURL, http.MethodPost, runPath, token, "", runInput, map[string]string{"Idempotency-Key": runKey})
	replayedRunData := decodeNative[map[string]any](t, externalData(t, replayedRun, http.StatusAccepted))
	if replayedRunData["id"] != runID {
		t.Fatalf("retired-target submit replay returned another run: %s", replayedRun.body)
	}
	newUpload := externalMultipart(t, client, p.baseURL, artifactPath, token, "", "external-retired-new-upload", zipBytes, 3, digest, source)
	if newUpload.status != http.StatusForbidden {
		t.Fatalf("new upload to retired target returned %d, want 403: %s", newUpload.status, newUpload.body)
	}
	newRunInput := map[string]any{"artifact_id": artifactID, "preflight_id": preflightID, "deployment_target_revision": 3, "external_source": json.RawMessage(source)}
	newRun := externalCall(t, client, p.baseURL, http.MethodPost, runPath, token, "", newRunInput, map[string]string{"Idempotency-Key": "external-retired-new-run"})
	if newRun.status != http.StatusForbidden {
		t.Fatalf("new run on retired target returned %d, want 403: %s", newRun.status, newRun.body)
	}
	migrationsAfterReplay, err := os.ReadFile(filepath.Join(fake.stateDir, "migrations"))
	if err != nil || strings.Count(string(migrationsAfterReplay), "M\n") != 1 {
		t.Fatalf("retired-target replay caused another migration: %q: %v", migrationsAfterReplay, err)
	}
	// Restart the actual Manager process and fetch the same artifact, run and frozen record.
	p.stop(t)
	p = nil
	p = launchNative(t, dir, true, fake.configPath)
	artifactAfter := externalData(t, externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+"/artifacts/"+artifactID, token, "", nil, nil), http.StatusOK)
	if !bytes.Contains(artifactAfter, []byte(digest)) {
		t.Fatal("artifact attribution/checksum missing after restart")
	}
	preflightAfter := externalData(t, externalCall(t, client, p.baseURL, http.MethodGet, "/api/v2/projects/"+projectID+"/preflights/"+preflightID, token, "", nil, nil), http.StatusOK)
	if !bytes.Contains(preflightAfter, []byte(artifactID)) {
		t.Fatal("preflight evidence missing after restart")
	}
	runAfter := externalData(t, externalCall(t, client, p.baseURL, http.MethodGet, runURL, token, "", nil, nil), http.StatusOK)
	if !bytes.Contains(runAfter, []byte(runID)) || !bytes.Contains(runAfter, []byte("SUCCEEDED")) {
		t.Fatal("project run state missing after restart")
	}
	if string(immutableBytes) != string(externalCall(t, client, p.baseURL, http.MethodGet, runURL+"/record", token, "", nil, nil).body) {
		t.Fatal("frozen execution record changed after restart")
	}
	// Token revocation takes effect on the next request.
	tokens := decodeNative[[]map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodGet, "/access/service-accounts/"+serviceID+"/tokens", csrf, nil, nil, http.StatusOK))
	mainTokenID := ""
	for _, item := range tokens {
		if item["name"] == "external-ci" {
			mainTokenID = fmt.Sprint(item["id"])
		}
	}
	if mainTokenID == "" {
		t.Fatal("issued service token metadata not found")
	}
	nativeJSONCall(t, admin, p.baseURL, http.MethodDelete, "/access/service-accounts/"+serviceID+"/tokens/"+mainTokenID, csrf, nil, nil, http.StatusOK)
	revoked := externalCall(t, client, p.baseURL, http.MethodGet, runURL, token, "", nil, nil)
	if revoked.status != http.StatusUnauthorized {
		t.Fatalf("revoked token remained usable: %d %s", revoked.status, revoked.body)
	}
	commands, err := os.ReadFile(filepath.Join(fake.stateDir, "commands"))
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := os.ReadFile(filepath.Join(fake.stateDir, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(migrations), "M\n") != 1 {
		t.Fatalf("SQL execution was replayed: %q", migrations)
	}
	if strings.Contains(string(commands), "native-oracle-password") {
		t.Fatal("native process command log exposed credentials")
	}

	if reportPath := os.Getenv("FWM_EXTERNAL_EVIDENCE_FILE"); reportPath != "" {
		evidence := map[string]any{
			"acceptance":    "real Manager HTTP process, isolated SQLite, native subprocess Flyway fixture",
			"real_http_api": "PASS", "flyway_runtime": "SIMULATED_FIXTURE", "real_oracle_execution": "NOT_RUN",
			"project_id": projectID, "deployment_target_id": deploymentID, "artifact_id": artifactID, "artifact_sha256": digest, "preflight_id": preflightID, "run_id": runID,
			"requests": []map[string]any{
				{"method": "GET", "path": "/api/v2/projects", "status": projectList.status, "response": json.RawMessage(projectList.body)},
				{"method": "GET", "path": "/api/v2/projects/" + projectID, "status": projectResponse.status, "response": json.RawMessage(projectResponse.body)},
				{"method": "GET", "path": "/api/v2/projects/" + projectID + targetPath, "status": targetList.status, "response": json.RawMessage(targetList.body)},
				{"method": "GET", "path": "/api/v2/projects/" + projectID + targetPath + "/" + deploymentID, "status": targetResponse.status, "response": json.RawMessage(targetResponse.body)},
				{"method": "POST", "path": artifactPath, "content_type": "multipart/form-data", "status": 201, "response": json.RawMessage(uploadResponses[0].body)},
				{"method": "POST", "path": preflightPath, "request": preflightInput, "status": 200, "response": map[string]any{"data": preflight}},
				{"method": "POST", "path": runPath, "request": runInput, "status": 202, "location": runResponses[0].header.Get("Location"), "retry_after": runResponses[0].header.Get("Retry-After"), "response": json.RawMessage(runResponses[0].body)},
				{"method": "GET", "path": runURL, "status": 200, "response": map[string]any{"data": finished}},
				{"method": "GET", "path": runURL + "/logs?limit=1", "status": 200, "response": json.RawMessage(logResponse.body)},
				{"method": "GET", "path": runURL + "/record", "status": 200, "etag": recordResponse.header.Get("ETag"), "response": json.RawMessage(immutableBytes)},
			},
			"negative_statuses":                 map[string]int{"missing_token": missingToken.status, "revoked_token": revoked.status, "cross_project": crossProject.status, "wrong_project_target": wrongTarget.status, "cross_project_target_list": hiddenTargets.status, "cross_project_artifact": otherArtifact.status, "wrong_checksum": bad.status, "token_without_execute": denied.status, "scoped_token_v1_bypass": v1res.StatusCode, "legacy_token_adopted_stream_bypass": legacyBypass.status, "stale_revision": stale.status},
			"restart":                           map[string]any{"artifact": json.RawMessage(artifactAfter), "preflight": json.RawMessage(preflightAfter), "run": json.RawMessage(runAfter), "record_bytes_unchanged": true},
			"concurrent_upload_single_artifact": true, "concurrent_submit_single_run": true, "migrate_invocations": 1, "client_lost_response_replay": "PASS", "no_credentials_exposed": true,
		}
		encoded, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{token, noExecuteToken, legacyToken, "native-oracle-password", "external-admin-password"} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatal("acceptance evidence contains a secret")
			}
		}
		if err = os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(reportPath, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("native HTTP acceptance: project=%s deployment_target=%s artifact=%s artifact_sha256=%s preflight=%s run=%s state=%s; fake Flyway fixture only, no Oracle SQL executed", projectID, deploymentID, artifactID, digest, preflightID, runID, finished["status"])
}

func TestExternalGenericClientFreshWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	dir := t.TempDir()
	fake := makeNativeFlyway(t, filepath.Join(dir, "distribution"), false)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	admin := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	p := launchNative(t, dir, true, fake.configPath)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	setup, err := os.ReadFile(filepath.Join(dir, "secrets", "bootstrap.token"))
	if err != nil {
		t.Fatal(err)
	}
	identity := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/auth/bootstrap", "", map[string]string{
		"username": "client-admin", "password": "client-admin-password-2026", "setup_token": strings.TrimSpace(string(setup)),
	}, nil, http.StatusCreated))
	adminID := identity["user"].(map[string]any)["id"].(string)
	csrf := identity["csrf_token"].(string)

	targetID := "generic-client-target"
	nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/database-targets", csrf, map[string]any{
		"id": targetID, "name": "Generic Client Oracle", "environment": "UAT", "host": "native.example", "port": 1521, "service": "APP", "transport": "TCP",
	}, nil, http.StatusCreated)
	schemaID := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/database-targets/"+targetID+"/schemas", csrf, map[string]string{"name": "CLIENT_APP"}, nil, http.StatusCreated))["id"].(string)
	account := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/execution-accounts", csrf, map[string]any{
		"target_id": targetID, "name": "client-deployer", "username": "DEPLOY_ADMIN", "schema_ids": []string{schemaID}, "password": "client-oracle-password-2026!",
	}, nil, http.StatusCreated))
	accountID := account["id"].(string)
	nativeJSONCall(t, admin, p.baseURL, http.MethodPut, "/access/users/"+adminID+"/grants", csrf, map[string]any{
		"grants": []map[string]any{{"target_id": targetID, "schema_id": schemaID, "read": true, "operate": true, "prod": false}},
	}, nil, http.StatusOK)
	project := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/projects", csrf, map[string]any{
		"name": "generic-client-project", "status": "active", "members": []map[string]string{{"target_id": targetID, "schema_id": schemaID}},
	}, nil, http.StatusCreated))
	projectID := project["id"].(string)
	deployment := decodeNative[map[string]any](t, externalData(t, externalCall(t, admin, p.baseURL, http.MethodPost,
		"/api/v2/projects/"+projectID+"/deployment-targets", "", csrf,
		map[string]any{"name": "uat", "environment": "UAT", "target_id": targetID, "schema_id": schemaID, "execution_account_id": accountID, "active": true}, nil), http.StatusCreated))
	deploymentID := deployment["id"].(string)
	service := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/access/service-accounts", csrf, map[string]any{
		"name": "generic-client-ci", "owner_id": adminID, "purpose": "generic client acceptance", "active": true,
	}, nil, http.StatusCreated))
	serviceID := service["id"].(string)
	nativeJSONCall(t, admin, p.baseURL, http.MethodPut, "/access/service-accounts/"+serviceID+"/grants", csrf, map[string]any{
		"grants": []map[string]any{{"target_id": targetID, "schema_id": schemaID, "read": true, "operate": true, "prod": false}},
	}, nil, http.StatusOK)
	externalData(t, externalCall(t, admin, p.baseURL, http.MethodPut, "/api/v2/projects/"+projectID+"/deployment-targets/"+deploymentID+"/grants", "", csrf,
		map[string]any{"principal_kind": "service", "principal_id": serviceID, "read": true, "upload": true, "preflight": true, "execute": true}, nil), http.StatusOK)
	issued := decodeNative[map[string]any](t, nativeJSONCall(t, admin, p.baseURL, http.MethodPost, "/access/service-accounts/"+serviceID+"/tokens", csrf, map[string]any{
		"name": "generic-client-token", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"deployment_scopes": []map[string]any{{"deployment_target_id": deploymentID, "read": true, "upload": true, "preflight": true, "execute": true}},
	}, nil, http.StatusCreated))
	token := issued["token"].(string)

	archivePath := filepath.Join(dir, "client-release.zip")
	if err = os.WriteFile(archivePath, externalZIP(t, "V1__native.sql", "CREATE TABLE client_acceptance(id NUMBER);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "private", "client-state.json")
	if _, err = os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file should start absent, stat err=%v", err)
	}
	key := "generic-client-release-1"
	source := `{"system":"acceptance","execution_id":"fresh-client-1","revision":"abc123"}`
	args := []string{
		"../../scripts/external-api-client.py", "--url", p.baseURL, "--project", "generic-client-project", "--deployment-target", "uat",
		"--zip", archivePath, "--key", key, "--external-source", source, "--state-file", statePath, "--poll-seconds", "0.02", "--max-polls", "30",
	}
	runClient := func() []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "python3", args...)
		cmd.Env = append(os.Environ(), "FM_TOKEN="+token)
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("generic client workflow failed: %v: %s", runErr, output)
		}
		if bytes.Contains(output, []byte(token)) {
			t.Fatal("generic client output exposed service token")
		}
		return output
	}
	firstOutput := runClient()
	info, err := os.Stat(statePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("retry state should be private mode 0600: %v", err)
	}
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err = json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("decode saved retry state: %v", err)
	}
	for _, field := range []string{"project_id", "deployment_target_id", "deployment_target_revision", "artifact_id", "preflight_id", "run_input", "run_id"} {
		if state[field] == nil || state[field] == "" {
			t.Fatalf("fresh client state omitted %s: %s", field, stateBytes)
		}
	}
	if state["project_id"] != projectID || state["deployment_target_id"] != deploymentID {
		t.Fatalf("client discovery saved unexpected resources: %s", stateBytes)
	}
	var first struct {
		Run struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Completed bool   `json:"completed"`
		} `json:"run"`
		Logs       []json.RawMessage `json:"logs"`
		Record     json.RawMessage   `json:"record"`
		RecordETag string            `json:"record_etag"`
	}
	if err = json.Unmarshal(firstOutput, &first); err != nil {
		t.Fatalf("decode client workflow result: %v: %s", err, firstOutput)
	}
	if first.Run.ID != state["run_id"] || first.Run.Status != "SUCCEEDED" || !first.Run.Completed || len(first.Logs) == 0 || !json.Valid(first.Record) || first.RecordETag == "" {
		t.Fatalf("client did not retrieve terminal evidence: %+v", first)
	}
	if bytes.Contains(first.Record, []byte("client-oracle-password-2026!")) {
		t.Fatal("client record exposed the execution credential")
	}
	migrationPath := filepath.Join(fake.stateDir, "migrations")
	migrations, err := os.ReadFile(migrationPath)
	if err != nil || strings.Count(string(migrations), "M\n") != 1 {
		t.Fatalf("fresh workflow should execute one migration, got %q: %v", migrations, err)
	}
	secondOutput := runClient()
	if !json.Valid(secondOutput) {
		t.Fatalf("client retry output is not JSON: %s", secondOutput)
	}
	migrations, err = os.ReadFile(migrationPath)
	if err != nil || strings.Count(string(migrations), "M\n") != 1 {
		t.Fatalf("re-running client from saved state replayed SQL: %q: %v", migrations, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
