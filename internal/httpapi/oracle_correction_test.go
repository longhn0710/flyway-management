package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
	"flyway-manager/internal/flyway"
	"flyway-manager/internal/oracle"
)

// A stateful V1–V7 Oracle/Flyway fixture. SQL is NOT executed against Oracle.
// Physical history is independent of info/validate JSON and records native CRCs.
type correctionOracle struct {
	mu                 sync.Mutex
	rows               []oracle.HistoryRow
	attempts           map[string]int
	applied            map[string]int
	repairCount        int
	dbid, objects      string
	driftAfterValidate bool
	repairFault        string
	evidenceHook       func(context.Context) error
	executeHook        func(context.Context, flyway.Request) error
}

func newCorrectionOracle() *correctionOracle {
	return &correctionOracle{rows: []oracle.HistoryRow{}, attempts: map[string]int{}, applied: map[string]int{}, dbid: "1514744704", objects: "catalogue"}
}
func (*correctionOracle) Check(context.Context) flyway.Readiness {
	return flyway.Readiness{Ready: true, Version: testFlywayVersion, Integrity: "verified", Blockers: []string{}}
}
func sqlCRC(b []byte) int64 {
	return int64(int32(crc32.ChecksumIEEE([]byte(strings.ReplaceAll(strings.ReplaceAll(string(b), "\r", ""), "\n", "")))))
}
func sqlVersion(name string) string {
	if !strings.HasPrefix(name, "V") {
		return ""
	}
	return strings.TrimPrefix(strings.Split(name, "__")[0], "V")
}
func (r *correctionOracle) OracleEvidence(ctx context.Context, req flyway.Request) (oracle.Evidence, error) {
	r.mu.Lock()
	hook := r.evidenceHook
	r.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return oracle.Evidence{}, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := append([]oracle.HistoryRow{}, r.rows...)
	return oracle.Evidence{DBID: r.dbid, DatabaseUniqueName: "fixture", PDBName: req.Service, Service: req.Service, SessionUser: req.Username, CurrentSchema: req.Schema, ObjectsSHA256: r.objects, History: rows}, nil
}
func (r *correctionOracle) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	r.mu.Lock()
	hook := r.executeHook
	r.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, req); err != nil {
			return flyway.Result{ExitCode: -1, TimedOut: errors.Is(err, context.DeadlineExceeded)}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	names := []string{}
	for name := range req.Files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		vi, vj := sqlVersion(names[i]), sqlVersion(names[j])
		if vi != "" && vj == "" {
			return true
		}
		if vi == "" && vj != "" {
			return false
		}
		return names[i] < names[j]
	})
	find := func(name string) *oracle.HistoryRow {
		for i := range r.rows {
			if r.rows[i].Script == name {
				return &r.rows[i]
			}
		}
		return nil
	}
	payload := map[string]any{"operation": req.Command, "flywayVersion": testFlywayVersion}
	result := flyway.Result{Success: true, Version: testFlywayVersion}
	switch req.Command {
	case "info":
		rows := []map[string]any{}
		for _, name := range names {
			row := map[string]any{"version": sqlVersion(name), "script": name, "type": "SQL", "description": name, "state": "Pending", "installedOn": "", "installedBy": ""}
			if h := find(name); h != nil {
				row["state"] = "Success"
				if !h.Success {
					row["state"] = "Failed"
				}
				row["installedOn"] = h.InstalledOn
				row["installedBy"] = h.InstalledBy
			}
			rows = append(rows, row)
		}
		payload["migrations"] = rows
		payload["schemaName"] = req.Schema
	case "validate":
		invalid := []any{}
		for _, h := range r.rows {
			code := ""
			if !h.Success {
				code = "FAILED_VERSIONED_MIGRATION"
			} else if data, ok := req.Files[h.Script]; !ok || h.Checksum == nil || *h.Checksum != sqlCRC(data) {
				code = "CHECKSUM_MISMATCH"
			}
			if code != "" {
				invalid = append(invalid, map[string]any{"version": h.Version, "filepath": h.Script, "errorDetails": map[string]any{"errorCode": code}})
			}
		}
		payload["invalidMigrations"] = invalid
		payload["validationSuccessful"] = len(invalid) == 0
		payload["validateCount"] = len(names)
		if len(invalid) > 0 {
			result.Success = false
			result.ExitCode = 1
		}
		if r.driftAfterValidate {
			r.objects = "changed after preflight"
			r.driftAfterValidate = false
		}
	case "migrate":
		count := 0
		completed := []any{}
		for _, name := range names {
			if find(name) != nil {
				continue
			}
			r.attempts[name]++
			crc := sqlCRC(req.Files[name])
			ok := !strings.Contains(string(req.Files[name]), "id NUMBER, id NUMBER")
			r.rows = append(r.rows, oracle.HistoryRow{Rank: len(r.rows) + 1, Version: sqlVersion(name), Script: name, Description: name, Type: "SQL", Checksum: &crc, InstalledBy: req.Username, InstalledOn: fmt.Sprintf("2026-10-19T01:00:%02dZ", len(r.rows)), Success: ok})
			if !ok {
				result.Success = false
				result.ExitCode = 1
				result.Stderr = "ORA-00957: duplicate column name"
				payload["error"] = map[string]any{"errorCode": "FAILED_VERSIONED_MIGRATION", "message": "ORA-00957: duplicate column name", "path": name}
				break
			}
			r.applied[name]++
			completed = append(completed, map[string]any{"version": sqlVersion(name), "filepath": name})
			count++
		}
		payload["success"] = result.Success
		payload["migrationsExecuted"] = count
		payload["migrations"] = completed
		if result.Success && r.repairFault == "corrected-history-drift" && len(r.rows) > 0 {
			r.rows[0].InstalledOn = "tampered after correction"
		}

	case "repair":
		r.repairCount++
		if r.repairFault == "failure" {
			result.Success = false
			result.ExitCode = 7
			payload["error"] = map[string]any{"message": "native Repair failure"}
			break
		}
		removed := []any{}
		rows := []oracle.HistoryRow{}
		for _, h := range r.rows {
			if h.Success {
				rows = append(rows, h)
			} else {
				removed = append(removed, map[string]any{"version": h.Version})
			}
		}
		r.rows = rows
		if r.repairFault == "realigned" && len(rows) > 0 {
			crc := int64(999)
			r.rows[0].Checksum = &crc
		}
		payload["migrationsRemoved"] = removed
		payload["migrationsAligned"] = []any{}
		payload["migrationsDeleted"] = []any{}
	default:
		panic(req.Command)
	}
	result.JSON, _ = json.Marshal(payload)
	result.Stdout = string(result.JSON)
	return result
}

func correctionZIP(t *testing.T, files map[string]string, comment ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	if len(comment) > 0 {
		if e := z.SetComment(comment[0]); e != nil {
			t.Fatal(e)
		}
	}
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(files[name])); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (f *apiFixture) v2(method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://manager.test/api/v2"+path, bytes.NewReader(b))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Origin", "http://manager.test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "fixture-submit-once")
	req.AddCookie(f.cookie)
	req.Header.Set("X-CSRF-Token", f.csrf)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if name := os.Getenv("FWM_CORRECTION_EVIDENCE_FILE"); name != "" && (rec.Code == 200 || rec.Code == 201) {
		var response any
		if e := json.Unmarshal(rec.Body.Bytes(), &response); e != nil {
			panic(e)
		}
		evidence := map[string][]any{"requests": {}}
		if raw, e := os.ReadFile(name); e == nil {
			if e = json.Unmarshal(raw, &evidence); e != nil {
				panic(e)
			}
		}
		item := map[string]any{"method": method, "path": "/api/v2" + path, "status": rec.Code, "response": response}
		if method == "POST" {
			item["request"] = body
		}
		evidence["requests"] = append(evidence["requests"], item)
		raw, _ := json.Marshal(evidence)
		if e := os.WriteFile(name, raw, 0600); e != nil {
			panic(e)
		}
	}
	return rec
}

type correctionHTTPFixture struct {
	f       *apiFixture
	runner  *correctionOracle
	d       core.DeploymentTarget
	tr      core.Trace
	token   string
	server  *httptest.Server
	dir     string
	commits map[string]string
}

func newCorrectionHTTPFixture(t *testing.T) *correctionHTTPFixture {
	t.Helper()
	f := apiSetup(t)
	runner := newCorrectionOracle()
	if e := f.service.StartExecution(context.Background(), runner, testFlywayVersion, t.TempDir(), artifact.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(f.service.StopExecution)
	resources := createExecutionResources(t, f)
	tr := core.Trace{Actor: core.Actor{ID: f.admin.ID, Kind: "user", Role: "Admin", Name: f.admin.Username}, RequestID: "test-dba"}
	p, e := f.service.SaveProject(context.Background(), tr, "", core.ProjectInput{Name: "recovery", Status: "active"})
	if e != nil {
		t.Fatal(e)
	}
	d, e := f.service.SaveDeploymentTarget(context.Background(), tr, p.ID, "", core.DeploymentTargetInput{Name: "isolated", Environment: resources.target.Environment, TargetID: resources.target.ID, SchemaID: resources.schema.ID, ExecutionAccountID: resources.account.ID, Active: true})
	if e != nil {
		t.Fatal(e)
	}
	sa, e := f.service.SaveService(context.Background(), tr, "", core.ServiceInput{Name: "jenkins-fixture", OwnerID: f.admin.ID, Purpose: "contract"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.service.SetGrants(context.Background(), tr, "service", sa.ID, []core.Grant{{TargetID: d.TargetID, SchemaID: d.SchemaID, Read: true, Operate: true}}); e != nil {
		t.Fatal(e)
	}
	if e = f.service.SetDeploymentGrant(context.Background(), tr, p.ID, d.ID, core.DeploymentGrantInput{PrincipalKind: "service", PrincipalID: sa.ID, Read: true, Upload: true, Preflight: true, Execute: true}); e != nil {
		t.Fatal(e)
	}
	token, e := f.service.IssueToken(context.Background(), tr, sa.ID, "", core.TokenInput{Name: "fixture", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), DeploymentScopes: []core.TokenDeploymentScope{{DeploymentTargetID: d.ID, Read: true, Upload: true, Preflight: true, Execute: true}}})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return &correctionHTTPFixture{f: f, runner: runner, d: d, tr: tr, token: token.Token, server: server, dir: t.TempDir()}
}
func (f *correctionHTTPFixture) upload(t *testing.T, files map[string]string, key string) (string, string) {
	t.Helper()
	raw := correctionZIP(t, files)
	a, e := f.f.service.UploadExternalArtifact(context.Background(), f.tr, f.d.ProjectID, f.d.ID, f.d.Revision, hashBytes(raw), key, core.ExternalSource{System: "DBA review"}, artifact.Input{Name: "sql.zip", Bytes: raw})
	if e != nil {
		t.Fatal(e)
	}
	return a["id"].(string), hashBytes(raw)
}
func (f *correctionHTTPFixture) client(t *testing.T, files map[string]string, key string, want int) map[string]any {
	t.Helper()
	commit := f.gitRelease(t, files, key)
	archive := filepath.Join(f.dir, key+".zip")
	if e := os.WriteFile(archive, correctionZIP(t, files), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("python3", "../../scripts/external-api-client.py", "--url", f.server.URL, "--project", "recovery", "--deployment-target", "isolated", "--zip", archive, "--key", key, "--state-file", filepath.Join(f.dir, key+".json"), "--poll-seconds", "0.01", "--max-polls", "30")
	source, _ := json.Marshal(core.ExternalSource{System: "Jenkins", Workflow: "fixture", Revision: commit, ExecutionID: key, ExecutionURL: "https://jenkins.example/job/fixture/" + key + "/"})
	cmd.Args = append(cmd.Args, "--external-source", string(source))
	cmd.Env = append(os.Environ(), "FM_TOKEN="+f.token)
	out, e := cmd.CombinedOutput()
	code := 0
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(e)
		}
	}
	if code != want {
		t.Fatalf("external CI client exit=%d want=%d: %s", code, want, out)
	}
	var result map[string]any
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatalf("CI client failed to collect evidence: %v: %s", e, out)
	}
	return result
}
func initialCorrectionFiles() map[string]string {
	f := map[string]string{"R__view.sql": "CREATE OR REPLACE VIEW demo_view AS SELECT 1 n FROM dual;"}
	for i := 1; i <= 4; i++ {
		f[fmt.Sprintf("R__view_%d.sql", i)] = fmt.Sprintf("CREATE OR REPLACE VIEW demo_view_%d AS SELECT %d n FROM dual;", i, i)
	}
	for i := 1; i <= 5; i++ {
		f[fmt.Sprintf("V%d__step.sql", i)] = fmt.Sprintf("CREATE TABLE t%d (id NUMBER);", i)
	}
	return f
}
func failCorrectionFiles() map[string]string {
	f := initialCorrectionFiles()
	f["V6__step.sql"] = "CREATE TABLE t6 (id NUMBER, id NUMBER);"
	f["V7__step.sql"] = "CREATE TABLE t7 (id NUMBER);"
	return f
}
func fixedCorrectionFiles() map[string]string {
	f := failCorrectionFiles()
	f["V6__step.sql"] = "CREATE TABLE t6 (id NUMBER);"
	return f
}
func mapData(x any) map[string]any { return x.(map[string]any) }
func (f *correctionHTTPFixture) review(t *testing.T, failed, aid string) *httptest.ResponseRecorder {
	return f.f.v2("POST", fmt.Sprintf("/projects/%s/deployment-targets/%s/correction-reviews", f.d.ProjectID, f.d.ID), core.CorrectionReviewInput{FailedRunID: failed, ArtifactID: aid, DeploymentTargetRevision: f.d.Revision})
}
func (f *correctionHTTPFixture) approve(t *testing.T, pref, sha string) *httptest.ResponseRecorder {
	return f.f.v2("POST", fmt.Sprintf("/projects/%s/deployment-targets/%s/correction-approvals", f.d.ProjectID, f.d.ID), core.CorrectionApprovalInput{PreflightID: pref, InputSHA256: sha, PartialDDLReviewed: true, Note: "DBA inspected native failure and current schema; no partial DDL remains in this fixture."})
}
func TestOracleV1V7CorrectionThroughHTTPAndPublishedCIClient(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	first := f.client(t, initialCorrectionFiles(), "seed-git-commit", 0)
	if mapData(first["run"])["status"] != "SUCCEEDED" {
		t.Fatal(first)
	}
	failure := f.client(t, failCorrectionFiles(), "invalid-git-commit", 1)
	failed := mapData(failure["run"])
	rid := failed["id"].(string)
	if failed["status"] != "FAILED" || failed["migration_outcome"] != "FAILED" || failed["repair_outcome"] != "SUCCEEDED" || failed["completed"] != true {
		t.Fatal(failed)
	}
	afterRepair := mapData(mapData(failure["record"])["repair"])["after"].(map[string]any)
	pending := map[string]bool{}
	for _, item := range afterRepair["migrations"].([]any) {
		row := mapData(item)
		if row["state"] == "Pending" {
			pending[row["version"].(string)] = true
		}
	}
	if !pending["6"] || !pending["7"] || len(pending) != 2 {
		t.Fatal("V6/V7 did not remain pending after Repair", pending)
	}
	nativeFailure := mapData(failed["failure"])
	if nativeFailure["version"] != "6" || !strings.Contains(nativeFailure["message"].(string), "ORA-00957") {
		t.Fatal(nativeFailure)
	}
	record, e := f.f.service.ExternalRecord(context.Background(), f.tr.Actor, f.d.ProjectID, rid)
	if e != nil {
		t.Fatal(e)
	}
	f.runner.mu.Lock()
	baseline := append([]oracle.HistoryRow{}, f.runner.rows...)
	if len(baseline) != len(initialCorrectionFiles()) || f.runner.attempts["V7__step.sql"] != 0 || f.runner.repairCount != 1 {
		t.Fatal("V7 ran or Repair did not isolate V6")
	}
	f.runner.mu.Unlock()
	var held bool
	if e = f.f.db.SQL.QueryRow(`SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, f.d.TargetID, f.d.SchemaID).Scan(&held); e != nil || !held {
		t.Fatal("Repair released the stream", e)
	}
	// Model a DBA's independent partial-DDL inspection/handling before review:
	// history must remain exact, while the new current catalogue is approval-bound.
	f.runner.mu.Lock()
	f.runner.objects = "DBA-reviewed catalogue"
	f.runner.mu.Unlock()
	corrected, checksum := f.upload(t, fixedCorrectionFiles(), "review-zip")
	blocked, e := f.f.service.ExternalPreflight(context.Background(), f.tr, f.d.ProjectID, f.d.ID, core.ExternalRunInput{ArtifactID: corrected, DeploymentTargetRevision: 1})
	if e != nil || blocked["ready"] != false {
		t.Fatal("replacement allowed without approval", blocked, e)
	}
	review := decodeData[map[string]any](t, f.review(t, rid, corrected), 200)
	if review["ready"] != true || review["live_status"] != "VERIFIED" {
		t.Fatal(review)
	}
	assertStatus(t, f.f.v2("GET", fmt.Sprintf("/projects/%s/preflights/%s", f.d.ProjectID, review["id"]), nil), 200)
	assertStatus(t, f.approve(t, review["id"].(string), strings.Repeat("0", 64)), 409)
	approval := decodeData[map[string]any](t, f.approve(t, review["id"].(string), checksum), 201)
	if approval["stream_held"] != true || approval["single_use"] != true {
		t.Fatal(approval)
	}
	// Jenkins uploads a new artifact with identical approved ZIP bytes and uses the
	// unmodified v2 preflight/submit contract. Its artifact ID differs from review.
	result := f.client(t, fixedCorrectionFiles(), "fixed-git-commit", 0)
	fixed := mapData(result["run"])
	if mapData(fixed["external_source"])["revision"] != f.commits["fixed-git-commit"] || f.commits["invalid-git-commit"] == f.commits["fixed-git-commit"] {
		t.Fatal("corrected Git push provenance lost")
	}
	correction := mapData(mapData(fixed["preflight"])["correction_evidence"])
	if correction["approval_id"] != approval["id"] || correction["approved_by"] != f.tr.Actor.ID {
		t.Fatal("frozen approval evidence missing", correction)
	}

	if fixed["status"] != "SUCCEEDED" || fixed["artifact_id"] == corrected {
		t.Fatal(fixed)
	}
	f.runner.mu.Lock()
	if f.runner.rows[len(baseline)].Version != "6" || f.runner.rows[len(baseline)+1].Version != "7" {
		t.Fatal("corrected V6/V7 executed out of order")
	}
	if !reflect.DeepEqual(f.runner.rows[:len(baseline)], baseline) {
		t.Fatal("successful physical history changed")
	}
	for name := range fixedCorrectionFiles() {
		if f.runner.applied[name] != 1 {
			t.Fatal("migration rerun or missing", name, f.runner.applied)
		}
	}
	if f.runner.attempts["V6__step.sql"] != 2 || f.runner.repairCount != 1 {
		t.Fatal("implicit retry/repair")
	}
	f.runner.mu.Unlock()
	repeated := f.client(t, failCorrectionFiles(), "invalid-git-commit", 1)
	if mapData(repeated["run"])["id"] != rid {
		t.Fatal("original failed CI job became green or was resubmitted")
	}
	after, e := f.f.service.ExternalRecord(context.Background(), f.tr.Actor, f.d.ProjectID, rid)
	if e != nil || !bytes.Equal(record, after) {
		t.Fatal("original failed audit/SQL was rewritten")
	}
	var consumed string
	if e = f.f.db.SQL.QueryRow(`SELECT consumed_run_id FROM oracle_correction_approvals WHERE id=?`, approval["id"]).Scan(&consumed); e != nil || consumed != fixed["id"] {
		t.Fatal("approval not consumed once", e)
	}
	if e = f.f.db.SQL.QueryRow(`SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, f.d.TargetID, f.d.SchemaID).Scan(&held); e != nil || held {
		t.Fatal("verified correction did not release hold", e)
	}
}

func prepareCorrectionFailure(t *testing.T, f *correctionHTTPFixture) string {
	t.Helper()
	f.client(t, initialCorrectionFiles(), "seed", 0)
	result := f.client(t, failCorrectionFiles(), "failed", 1)
	return mapData(result["run"])["id"].(string)
}
func TestOracleCorrectionRejectsUnrelatedChangesAndServiceApproval(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	for _, change := range []string{"successful", "repeatable", "pending", "missing", "added", "renamed"} {
		t.Run(change, func(t *testing.T) {
			files := fixedCorrectionFiles()
			switch change {
			case "successful":
				files["V1__step.sql"] += "-- changed"
			case "repeatable":
				files["R__view.sql"] += "-- changed"
			case "pending":
				files["V7__step.sql"] += "-- changed"
			case "missing":
				delete(files, "V7__step.sql")
			case "added":
				files["V8__step.sql"] = "SELECT 8 FROM dual;"
			case "renamed":
				files["V6__renamed.sql"] = files["V6__step.sql"]
				delete(files, "V6__step.sql")
			}
			aid, _ := f.upload(t, files, "bad-"+change)
			assertStatus(t, f.review(t, rid, aid), 409)
		})
	}
	aid, _ := f.upload(t, fixedCorrectionFiles(), "service-review")
	request := httptest.NewRequest("POST", "http://manager.test/api/v2"+fmt.Sprintf("/projects/%s/deployment-targets/%s/correction-reviews", f.d.ProjectID, f.d.ID), strings.NewReader(fmt.Sprintf(`{"failed_run_id":%q,"artifact_id":%q,"deployment_target_revision":1}`, rid, aid)))
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.f.handler.ServeHTTP(rec, request)
	assertStatus(t, rec, 403)
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	original := correctionZIP(t, fixedCorrectionFiles())
	assertStatus(t, f.approve(t, review["id"].(string), hashBytes(original)), 201)
	alternate := correctionZIP(t, fixedCorrectionFiles(), "same SQL, different ZIP bytes")
	if hashBytes(original) == hashBytes(alternate) {
		t.Fatal("fixture ZIP bytes did not differ")
	}
	art, e := f.f.service.UploadExternalArtifact(context.Background(), f.tr, f.d.ProjectID, f.d.ID, 1, hashBytes(alternate), "different-zip", core.ExternalSource{}, artifact.Input{Name: "sql.zip", Bytes: alternate})
	if e != nil {
		t.Fatal(e)
	}
	blocked, e := f.f.service.ExternalPreflight(context.Background(), f.tr, f.d.ProjectID, f.d.ID, core.ExternalRunInput{ArtifactID: art["id"].(string), DeploymentTargetRevision: 1})
	if e != nil || blocked["ready"] != false {
		t.Fatal("equivalent extracted SQL bypassed exact ZIP approval", blocked, e)
	}
}
func TestOracleCorrectionApprovalRejectsStaleDatabaseEvidence(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed")
	for _, change := range []string{"objects", "dbid", "history"} {
		t.Run(change, func(t *testing.T) {
			review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
			f.runner.mu.Lock()
			saved := append([]oracle.HistoryRow{}, f.runner.rows...)
			switch change {
			case "objects":
				f.runner.objects = "DBA changed schema"
			case "dbid":
				f.runner.dbid = "another database"
			case "history":
				row := f.runner.rows[0]
				row.InstalledBy = "OTHER"
				f.runner.rows[0] = row
			}
			f.runner.mu.Unlock()
			assertStatus(t, f.approve(t, review["id"].(string), sha), 409)
			f.runner.mu.Lock()
			f.runner.objects = "catalogue"
			f.runner.dbid = "1514744704"
			f.runner.rows = saved
			f.runner.mu.Unlock()
		})
	}
}
func TestOracleCorrectionApprovalRevocationBlocksService(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed")
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	assertStatus(t, f.approve(t, review["id"].(string), sha), 201)
	// A role change after approval must revoke the DBA's authority to authorize it.
	if _, e := f.f.db.SQL.Exec(`UPDATE users SET role='Viewer' WHERE id=?`, f.tr.Actor.ID); e != nil {
		t.Fatal(e)
	}
	actor, e := f.f.service.AuthenticateToken(context.Background(), f.token, "service")
	if e != nil {
		t.Fatal(e)
	}
	pf, e := f.f.service.ExternalPreflight(context.Background(), core.Trace{Actor: actor}, f.d.ProjectID, f.d.ID, core.ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: 1})
	if e == nil && pf["ready"] != false {
		t.Fatal("revoked DBA approval remained executable")
	}
}
func TestOracleUnsafeOrFailedRepairCannotAuthorizeCorrection(t *testing.T) {
	for _, fault := range []string{"failure", "realigned"} {
		t.Run(fault, func(t *testing.T) {
			f := newCorrectionHTTPFixture(t)
			f.client(t, initialCorrectionFiles(), "seed", 0)
			f.runner.mu.Lock()
			f.runner.repairFault = fault
			f.runner.mu.Unlock()
			result := f.client(t, failCorrectionFiles(), "failed", 1)
			run := mapData(result["run"])
			if run["status"] != "NEEDS_REVIEW" || run["migration_outcome"] != "FAILED" || run["repair_outcome"] != "FAILED" {
				t.Fatal(run)
			}
			if !strings.Contains(mapData(run["failure"])["message"].(string), "ORA-00957") {
				t.Fatal("original error lost")
			}
			aid, _ := f.upload(t, fixedCorrectionFiles(), "fix-blocked")
			assertStatus(t, f.review(t, run["id"].(string), aid), 409)
			f.runner.mu.Lock()
			defer f.runner.mu.Unlock()
			if f.runner.repairCount != 1 || f.runner.attempts["V7__step.sql"] != 0 {
				t.Fatal("Repair or SQL automatically retried")
			}
		})
	}
}

func TestOracleCorrectionWorkerRechecksEvidenceAndConsumesOnce(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed")
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	approval := decodeData[map[string]any](t, f.approve(t, review["id"].(string), sha), 201)
	base := fmt.Sprintf("/projects/%s/deployment-targets/%s", f.d.ProjectID, f.d.ID)
	input := core.ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: 1}
	pre := decodeData[map[string]any](t, f.f.v2("POST", base+"/preflights", input), 200)
	if pre["ready"] != true {
		t.Fatal(pre)
	}
	input.PreflightID = pre["id"].(string)
	f.runner.mu.Lock()
	f.runner.driftAfterValidate = true
	f.runner.mu.Unlock()
	run := decodeData[map[string]any](t, f.f.v2("POST", base+"/migration-runs", input), 202)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		run = decodeData[map[string]any](t, f.f.v2("GET", fmt.Sprintf("/projects/%s/migration-runs/%s", f.d.ProjectID, run["id"]), nil), 200)
		if run["completed"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run["status"] != "BLOCKED" || !strings.Contains(run["reason"].(string), "stale") {
		t.Fatal(run)
	}
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	if f.runner.attempts["V6__step.sql"] != 1 || f.runner.attempts["V7__step.sql"] != 0 {
		t.Fatal("SQL ran after approval evidence drift")
	}
	if _, e := f.f.db.SQL.Exec(`UPDATE oracle_correction_approvals SET consumed_run_id=NULL WHERE id=?`, approval["id"]); e == nil {
		t.Fatal("consumed approval was reusable")
	}
	if _, e := f.f.db.SQL.Exec(`UPDATE oracle_correction_approvals SET input_sha256=? WHERE id=?`, strings.Repeat("0", 64), approval["id"]); e == nil {
		t.Fatal("approval checksum was mutable")
	}
}
func TestOracleExpiredCorrectionApprovalCannotReleaseHold(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed")
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	_, e := f.f.db.SQL.Exec(`INSERT INTO oracle_correction_approvals(id,preflight_id,failed_run_id,project_id,deployment_target_id,input_sha256,approved_by,note,created_at,expires_at) VALUES('expired',?,?,?,?,?,?,?,1,2)`, review["id"], rid, f.d.ProjectID, f.d.ID, sha, f.tr.Actor.ID, "historical expired approval")
	if e != nil {
		t.Fatal(e)
	}
	pf, e := f.f.service.ExternalPreflight(context.Background(), f.tr, f.d.ProjectID, f.d.ID, core.ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: 1})
	if e != nil || pf["ready"] != false {
		t.Fatal("expired approval released stream", pf, e)
	}
}

func TestOracleRepairPreservesEarlierSuccessesInSameV1V7Batch(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	f.client(t, map[string]string{"R__view.sql": initialCorrectionFiles()["R__view.sql"]}, "repeatable-history", 0)
	result := f.client(t, failCorrectionFiles(), "batch-v1-v7", 1)
	failed := mapData(result["run"])
	if failed["status"] != "FAILED" || failed["repair_outcome"] != "SUCCEEDED" {
		t.Fatal(failed)
	}
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed-review")
	review := decodeData[map[string]any](t, f.review(t, failed["id"].(string), aid), 200)
	assertStatus(t, f.approve(t, review["id"].(string), sha), 201)
	f.client(t, fixedCorrectionFiles(), "fixed-v6-v7", 0)
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	if len(f.runner.rows) != len(fixedCorrectionFiles()) || f.runner.repairCount != 1 {
		t.Fatal("incomplete final history")
	}
	for name, count := range f.runner.applied {
		if count != 1 {
			t.Fatal("successful migration replayed", name, count)
		}
	}
}

func TestOracleCorrectionDoesNotReleaseHoldAfterHiddenHistoryMutation(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed")
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	assertStatus(t, f.approve(t, review["id"].(string), sha), 201)
	f.runner.mu.Lock()
	f.runner.repairFault = "corrected-history-drift"
	f.runner.mu.Unlock()
	result := f.client(t, fixedCorrectionFiles(), "corrected", 1)
	run := mapData(result["run"])
	if run["status"] != "NEEDS_REVIEW" || run["migration_outcome"] != "SUCCEEDED" {
		t.Fatal("physical history mutation was accepted", run)
	}
	var held bool
	if e := f.f.db.SQL.QueryRow(`SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, f.d.TargetID, f.d.SchemaID).Scan(&held); e != nil || !held {
		t.Fatal("unverified correction released hold")
	}
}

// Git operations are confined to disposable local repositories. No GitHub demo
// branch, Jenkins instance or Oracle database is contacted by this fixture.
func (f *correctionHTTPFixture) gitRelease(t *testing.T, files map[string]string, key string) string {
	t.Helper()
	repo := filepath.Join(f.dir, "developer")
	remote := filepath.Join(f.dir, "remote.git")
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if f.commits == nil {
		f.commits = map[string]string{}
		if err := os.MkdirAll(repo, 0700); err != nil {
			t.Fatal(err)
		}
		git(f.dir, "init", "--bare", remote)
		git(repo, "init", "-b", "demo")
		git(repo, "config", "user.name", "Fixture Developer")
		git(repo, "config", "user.email", "fixture@example.invalid")
		git(repo, "remote", "add", "origin", remote)
	}
	if existing := f.commits[key]; existing != "" {
		return existing
	}
	migrations := filepath.Join(repo, "migrations")
	if err := os.RemoveAll(migrations); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(migrations, 0700); err != nil {
		t.Fatal(err)
	}
	for name, sql := range files {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git(repo, "add", "migrations")
	git(repo, "commit", "--allow-empty", "-m", key)
	commit := git(repo, "rev-parse", "HEAD")
	git(repo, "push", "origin", "HEAD:refs/heads/demo")
	if git(f.dir, "--git-dir="+remote, "rev-parse", "refs/heads/demo") != commit {
		t.Fatal("local Git push did not preserve exact commit")
	}
	for name, sql := range files {
		cmd := exec.Command("git", "show", commit+":migrations/"+name)
		cmd.Dir = repo
		raw, err := cmd.Output()
		if err != nil || string(raw) != sql {
			t.Fatal("ZIP input differs from pushed SQL bytes", name, err)
		}
	}
	f.commits[key] = commit
	return commit
}

func TestOracleCorrectionConcurrentSubmissionsConsumeApprovalOnce(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	failed := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "fixed-concurrent")
	review := decodeData[map[string]any](t, f.review(t, failed, aid), 200)
	approval := decodeData[map[string]any](t, f.approve(t, review["id"].(string), sha), 201)
	actor, err := f.f.service.AuthenticateToken(context.Background(), f.token, "service")
	if err != nil {
		t.Fatal(err)
	}
	input := core.ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: 1}
	pre, err := f.f.service.ExternalPreflight(context.Background(), core.Trace{Actor: actor}, f.d.ProjectID, f.d.ID, input)
	if err != nil || pre["ready"] != true {
		t.Fatal(pre, err)
	}
	input.PreflightID = pre["id"].(string)
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		key    string
		status int
		id     string
		err    error
	}
	submit := func(key string) result {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+fmt.Sprintf("/api/v2/projects/%s/deployment-targets/%s/migration-runs", f.d.ProjectID, f.d.ID), bytes.NewReader(payload))
		if err != nil {
			return result{err: err}
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return result{err: err}
		}
		defer response.Body.Close()
		var body struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		return result{key: key, status: response.StatusCode, id: body.Data.ID, err: err}
	}
	// Hold the native fixture so the winner cannot finish/release the stream
	// before the competing request tests the already consumed approval.
	results := func() []result {
		f.runner.mu.Lock()
		defer f.runner.mu.Unlock()
		start := make(chan struct{})
		done := make(chan result, 2)
		for _, key := range []string{"correction-concurrent-a", "correction-concurrent-b"} {
			go func(key string) { <-start; done <- submit(key) }(key)
		}
		close(start)
		return []result{<-done, <-done}
	}()
	var winner result
	accepted, rejected := 0, 0
	for _, r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch r.status {
		case 202:
			accepted++
			winner = r
		case 409:
			rejected++
		default:
			t.Fatalf("unexpected concurrent response: %+v", r)
		}
	}
	if accepted != 1 || rejected != 1 || winner.id == "" {
		t.Fatal(results)
	}
	var run map[string]any
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		run = decodeData[map[string]any](t, f.f.v2("GET", fmt.Sprintf("/projects/%s/migration-runs/%s", f.d.ProjectID, winner.id), nil), 200)
		if run["completed"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run["status"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	replay := submit(winner.key)
	if replay.err != nil || replay.status != 202 || replay.id != winner.id {
		t.Fatalf("same-key replay changed run: %+v", replay)
	}
	var consumed string
	if err = f.f.db.SQL.QueryRow(`SELECT consumed_run_id FROM oracle_correction_approvals WHERE id=?`, approval["id"]).Scan(&consumed); err != nil || consumed != winner.id {
		t.Fatal(consumed, err)
	}
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	if f.runner.attempts["V6__step.sql"] != 2 || f.runner.attempts["V7__step.sql"] != 1 {
		t.Fatal("correction SQL repeated", f.runner.attempts)
	}
	for name, count := range f.runner.applied {
		if count != 1 {
			t.Fatal("successful SQL repeated", name, count)
		}
	}
}
