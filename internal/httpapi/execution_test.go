package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
	"flyway-manager/internal/flyway"
)

const testFlywayVersion = "10.20.0"

type httpFakeRunner struct {
	mu       sync.Mutex
	commands []string
	applied  bool
}

func (r *httpFakeRunner) Check(context.Context) flyway.Readiness {
	return flyway.Readiness{Ready: true, Version: testFlywayVersion, Integrity: "verified", Dependency: "java_and_oracle_jdbc_available", Blockers: []string{}}
}
func (r *httpFakeRunner) Execute(_ context.Context, req flyway.Request) flyway.Result {
	r.mu.Lock()
	r.commands = append(r.commands, req.Command)
	defer r.mu.Unlock()
	result := flyway.Result{Success: true, ExitCode: 0, Version: testFlywayVersion, Stdout: req.Command + " complete"}
	switch req.Command {
	case "info":
		rows := []map[string]any{}
		for name := range req.Files {
			state := "Pending"
			if r.applied {
				state = "Success"
			}
			rows = append(rows, map[string]any{"script": name, "version": "1", "state": state})
		}
		result.JSON, _ = json.Marshal(map[string]any{"migrations": rows})
	case "validate":
		result.JSON = json.RawMessage(`{"validationSuccessful":true}`)
	case "migrate":
		r.applied = true
		result.JSON = json.RawMessage(`{"migrationsExecuted":1}`)
	default:
		result.Success = false
		result.ExitCode = 1
	}
	return result
}

func executionFixture(t *testing.T, limits artifact.Limits) (*apiFixture, *httpFakeRunner) {
	t.Helper()
	f := apiSetup(t)
	runner := &httpFakeRunner{}
	if err := f.service.StartExecution(context.Background(), runner, testFlywayVersion, t.TempDir(), limits); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.service.StopExecution)
	return f, runner
}

type executionResources struct {
	target  core.Target
	schema  core.Schema
	account core.ExecutionAccount
}

func createExecutionResources(t *testing.T, f *apiFixture) executionResources {
	t.Helper()
	target := decodeData[core.Target](t, f.call("POST", "/database-targets", map[string]any{"id": "exec-test", "name": "Execution test", "environment": "UAT", "host": "oracle.invalid", "port": 1521, "service": "TESTPDB", "transport": "TCP"}, f.cookie, nil), 201)
	schema := decodeData[core.Schema](t, f.call("POST", "/database-targets/exec-test/schemas", map[string]string{"name": "APP_SCHEMA"}, f.cookie, nil), 201)
	account := decodeData[core.ExecutionAccount](t, f.call("POST", "/execution-accounts", map[string]any{"target_id": target.ID, "name": "Deployer", "username": "APP_DEPLOY", "schema_ids": []string{schema.ID}, "password": "oracle-test-secret"}, f.cookie, nil), 201)
	grants := []core.Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true}}
	decodeData[[]core.Grant](t, f.call("PUT", "/access/users/"+f.admin.ID+"/grants", map[string]any{"grants": grants}, f.cookie, nil), 200)
	return executionResources{target, schema, account}
}

func multipartRequest(t *testing.T, f *apiFixture, targetID, schemaID string, files map[string][]byte, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, data := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": name}))
		h.Set("Content-Type", "application/octet-stream")
		part, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://manager.test/api/v1/artifacts?target_id="+targetID+"&schema_id="+schemaID, &body)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Origin", "http://manager.test")
	req.Header.Set("Content-Type", w.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", f.csrf)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestExecutionHTTPArtifactPreflightRunAndScopedRead(t *testing.T) {
	f, _ := executionFixture(t, artifact.DefaultLimits())
	resources := createExecutionResources(t, f)
	assertStatus(t, f.call("GET", "/runtime", nil, f.cookie, nil), 200)
	accounts := decodeData[[]core.ExecutionAccount](t, f.call("GET", fmt.Sprintf("/database-targets/%s/schemas/%s/execution-accounts", resources.target.ID, resources.schema.ID), nil, f.cookie, nil), 200)
	if len(accounts) != 1 || accounts[0].ID != resources.account.ID || !accounts[0].Credential.Configured {
		t.Fatalf("unexpected sanitized stream accounts: %+v", accounts)
	}

	content := []byte("-- preserve every byte\r\nSELECT 1 FROM dual;\r\n")
	created := decodeData[core.Artifact](t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"nested/V1__init.sql": content}, f.cookie, nil), 201)
	if len(created.Files) != 1 || created.Files[0].Path != "nested/V1__init.sql" || created.Files[0].Size != len(content) {
		t.Fatalf("relative path/manifest lost: %+v", created)
	}
	raw := f.call("GET", "/artifacts/"+created.ID+"/files?path=nested%2FV1__init.sql", nil, f.cookie, nil)
	assertStatus(t, raw, 200)
	if !bytes.Equal(raw.Body.Bytes(), content) {
		t.Fatal("download did not preserve SQL bytes")
	}
	manifest := decodeData[core.Artifact](t, f.call("GET", "/artifacts/"+created.ID, nil, f.cookie, nil), 200)
	if manifest.SHA256 != created.SHA256 || strings.Contains(string(raw.Body.Bytes()), "password") {
		t.Fatal("artifact manifest/content integrity mismatch")
	}

	input := core.RunInput{TargetID: resources.target.ID, SchemaID: resources.schema.ID, ExecutionAccountID: resources.account.ID, ArtifactID: created.ID, Context: core.RunContext{InitiatedBy: "untrusted requester claim", ExternalRunID: "ci-77"}}
	body, _ := json.Marshal(input)
	assertStatus(t, f.call("POST", "/migration-preflight", string(body), f.cookie, map[string]string{"X-CSRF-Token": ""}), 403)
	preflight := decodeData[core.Preflight](t, f.call("POST", "/migration-preflight", input, f.cookie, nil), 200)
	if !preflight.Ready || preflight.LiveStatus != "VERIFIED" || !preflight.LocalReady {
		t.Fatalf("fake live validation did not reach expected verified result: %+v", preflight)
	}
	assertStatus(t, f.call("POST", "/migration-preflight", `{"target_id":"x","schema_id":"x","execution_account_id":"x","artifact_id":"x","actor":"admin"}`, f.cookie, nil), 400)

	key := "execution-http-idempotency"
	run := decodeData[core.Run](t, f.call("POST", "/migration-runs", input, f.cookie, map[string]string{"Idempotency-Key": key}), 202)
	if run.ActorID != f.admin.ID || run.ActorName == input.Context.InitiatedBy || run.Context.InitiatedBy != input.Context.InitiatedBy {
		t.Fatalf("request context replaced authenticated actor attribution: %+v", run)
	}
	replayed := decodeData[core.Run](t, f.call("POST", "/migration-runs", input, f.cookie, map[string]string{"Idempotency-Key": key}), 202)
	if replayed.ID != run.ID {
		t.Fatalf("idempotent replay created a new run: %s / %s", run.ID, replayed.ID)
	}
	changed := input
	changed.Context.CommitSHA = "different"
	assertStatus(t, f.call("POST", "/migration-runs", changed, f.cookie, map[string]string{"Idempotency-Key": key}), 409)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got := decodeData[core.Run](t, f.call("GET", "/migration-runs/"+run.ID, nil, f.cookie, nil), 200)
		if got.Status == "SUCCEEDED" {
			run = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "SUCCEEDED" {
		t.Fatalf("deterministic runner did not finish successfully: %s", run.Status)
	}
	logs := decodeData[[]core.RunLog](t, f.call("GET", "/migration-runs/"+run.ID+"/logs?limit=100", nil, f.cookie, nil), 200)
	execs := decodeData[[]core.RunExecution](t, f.call("GET", "/migration-runs/"+run.ID+"/executions", nil, f.cookie, nil), 200)
	repairs := decodeData[core.Repair](t, f.call("GET", "/migration-runs/"+run.ID+"/repair", nil, f.cookie, nil), 200)
	artifacts := decodeData[[]core.Artifact](t, f.call("GET", "/migration-runs/"+run.ID+"/artifacts", nil, f.cookie, nil), 200)
	if len(logs) == 0 || len(execs) < 5 || repairs.Outcome != "SKIPPED" || len(artifacts) != 1 {
		t.Fatalf("run evidence incomplete: logs=%d executions=%d repair=%+v artifacts=%d", len(logs), len(execs), repairs, len(artifacts))
	}
	record := f.call("GET", "/migration-runs/"+run.ID+"/record", nil, f.cookie, nil)
	assertStatus(t, record, 200)
	if !strings.Contains(record.Body.String(), `"repair"`) || !strings.Contains(record.Body.String(), `"executions"`) {
		t.Fatal("download record omitted execution evidence")
	}

	service := decodeData[core.ServiceAccount](t, f.call("POST", "/access/service-accounts", map[string]string{"name": "ungranted", "purpose": "scope test", "owner_id": f.admin.ID}, f.cookie, nil), 201)
	scopeSchema := decodeData[core.Schema](t, f.call("POST", "/database-targets/exec-test/schemas", map[string]string{"name": "OTHER_SCHEMA"}, f.cookie, nil), 201)
	decodeData[[]core.Grant](t, f.call("PUT", "/access/service-accounts/"+service.ID+"/grants", map[string]any{"grants": []core.Grant{{TargetID: resources.target.ID, SchemaID: scopeSchema.ID, Read: true, Operate: true}}}, f.cookie, nil), 200)
	issued := decodeData[core.IssuedToken](t, f.call("POST", "/access/service-accounts/"+service.ID+"/tokens", map[string]string{"name": "scope", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, f.cookie, nil), 201)
	bearer := map[string]string{"Authorization": "Bearer " + issued.Token}
	for _, suffix := range []string{"", "/logs", "/artifacts", "/executions", "/repair", "/record"} {
		assertStatus(t, f.call("GET", "/migration-runs/"+run.ID+suffix, nil, nil, bearer), 403)
	}
	assertStatus(t, f.call("GET", "/artifacts/"+created.ID, nil, nil, bearer), 403)
	assertStatus(t, f.call("GET", "/artifacts/"+created.ID+"/files?path=nested%2FV1__init.sql", nil, nil, bearer), 403)
}

func TestExecutionHTTPRejectsUnsafeMultipartAndEnforcesLimits(t *testing.T) {
	limits := artifact.Limits{MaxUploadBytes: 1024, MaxExpandedBytes: 2048, MaxFiles: 4, MaxFileBytes: 512, MaxExpansionRatio: 10}
	f, _ := executionFixture(t, limits)
	resources := createExecutionResources(t, f)
	for name, payload := range map[string][]byte{"../V1__escape.sql": []byte("SELECT 1;"), "V1__bad.zip": []byte("PK\x03\x04broken"), "notes.txt": []byte("not sql")} {
		assertStatus(t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{name: payload}, f.cookie, nil), 400)
	}
	large := bytes.Repeat([]byte("x"), 1536)
	assertStatus(t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"V1__large.sql": large}, f.cookie, nil), 413)
	assertStatus(t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"V1__valid.sql": []byte("SELECT 1;")}, f.cookie, map[string]string{"X-CSRF-Token": ""}), 403)
	assertStatus(t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"V1__valid.sql": []byte("SELECT 1;")}, nil, nil), 401)
	// Bearer auth bypasses cookie CSRF, but still needs its own explicit schema grant.
	service := decodeData[core.ServiceAccount](t, f.call("POST", "/access/service-accounts", map[string]string{"name": "artifact-uploader", "purpose": "upload test", "owner_id": f.admin.ID}, f.cookie, nil), 201)
	otherSchema := decodeData[core.Schema](t, f.call("POST", "/database-targets/exec-test/schemas", map[string]string{"name": "UPLOAD_SCHEMA"}, f.cookie, nil), 201)
	decodeData[[]core.Grant](t, f.call("PUT", "/access/service-accounts/"+service.ID+"/grants", map[string]any{"grants": []core.Grant{{TargetID: resources.target.ID, SchemaID: otherSchema.ID, Read: true, Operate: true}}}, f.cookie, nil), 200)
	issued := decodeData[core.IssuedToken](t, f.call("POST", "/access/service-accounts/"+service.ID+"/tokens", map[string]string{"name": "upload", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, f.cookie, nil), 201)
	response := multipartRequest(t, f, resources.target.ID, otherSchema.ID, map[string][]byte{"nested/V1__valid.sql": []byte("SELECT 1;")}, nil, map[string]string{"Authorization": "Bearer " + issued.Token})
	created := decodeData[core.Artifact](t, response, 201)
	if created.UploaderKind != "service" || created.UploaderID != service.ID {
		t.Fatalf("bearer upload actor attribution mismatch: %+v", created)
	}
	// A bearer cannot upload to a stream outside its explicit schema grant.
	other := decodeData[core.Target](t, f.call("POST", "/database-targets", map[string]any{"id": "other", "name": "Other", "environment": "DEV", "host": "oracle.invalid", "port": 1521, "service": "OTHER", "transport": "TCP"}, f.cookie, nil), 201)
	foreignSchema := decodeData[core.Schema](t, f.call("POST", "/database-targets/other/schemas", map[string]string{"name": "OTHER_SCHEMA"}, f.cookie, nil), 201)
	assertStatus(t, multipartRequest(t, f, other.ID, foreignSchema.ID, map[string][]byte{"V1__x.sql": []byte("x")}, nil, map[string]string{"Authorization": "Bearer " + issued.Token}), 403)
}
