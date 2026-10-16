package core

import (
	"context"
	"encoding/json"
	"flyway-manager/internal/flyway"
	"strings"
	"testing"
)

func TestInfoRefreshOnlyInspectsAndPersistsSeparateEvidence(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__info.sql", "CREATE TABLE never_executed(id NUMBER);")
	capture, e := f.s.RefreshInfo(testCtx, f.trace(), in)
	must(t, e)
	if !capture.Success || capture.Source != "read-only-refresh" || len(capture.Files) != 1 || capture.ArtifactSHA256 == "" {
		t.Fatalf("invalid capture %+v", capture)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "APP:info" || engine.migrateCount("APP") != 0 {
		t.Fatalf("unexpected commands %v", engine.calls)
	}
	for _, table := range []string{"migration_runs", "migration_streams", "stream_files", "run_executions", "repair_operations"} {
		var n int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Fatalf("inspection changed %s", table)
		}
	}
	latest, e := f.s.LatestInfo(testCtx, f.admin, target.ID, schema.ID, "")
	must(t, e)
	if latest == nil || latest.ID != capture.ID {
		t.Fatal("capture not returned")
	}
	if _, e = f.db.SQL.Exec("UPDATE flyway_info_captures SET record='{}'"); e == nil {
		t.Fatal("capture mutable")
	}
	if strings.Contains(string(capture.JSON)+capture.Stdout+capture.Stderr, "oracle-password-never-returned") {
		t.Fatal("secret leaked")
	}
}

func TestInfoRefreshEnforcesScopeCredentialsAndHistoryHolds(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__info.sql", "SELECT 1 FROM dual;")
	_, viewer, _ := f.user(t, "info-viewer", "Viewer")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", viewer.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true}}))
	if _, e := f.s.RefreshInfo(testCtx, Trace{Actor: viewer}, in); e == nil {
		t.Fatal("viewer refreshed")
	}
	other := f.schema(t, target.ID, "OTHER")
	bad := in
	bad.SchemaID = other.ID
	if _, e := f.s.RefreshInfo(testCtx, f.trace(), bad); e == nil {
		t.Fatal("unmapped account refreshed")
	}
	bad = in
	bad.ArtifactID = "unknown"
	if _, e := f.s.RefreshInfo(testCtx, f.trace(), bad); e == nil {
		t.Fatal("unknown artifact refreshed")
	}
	plan, e := f.s.localPlan(testCtx, f.s.db, f.admin, in, "11.20.0", true)
	must(t, e)
	defer clear(plan.password)
	_, e = f.s.db.Exec(`INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin,held,reason) VALUES(?,?,?,?,1,'HISTORY_UNSAFE')`, target.ID, schema.ID, plan.configuration, f.s.execution.pin)
	must(t, e)
	if _, e = f.s.RefreshInfo(testCtx, f.trace(), in); e == nil {
		t.Fatal("history hold bypassed")
	}
	if len(engine.calls) != 0 {
		t.Fatalf("blocked request invoked CLI: %v", engine.calls)
	}
}

func TestInfoPreservesAllNativeStatesAndPartialFailure(t *testing.T) {
	states := []string{"Success", "Pending", "Failed", "Missing", "Superseded", "Ignored", "Out of Order", "Baseline", "Below Baseline", "Above Target", "Future", "Outdated", "Deleted", "Available", "Undone", "New Native State"}
	rows := []map[string]any{}
	for _, state := range states {
		rows = append(rows, map[string]any{"state": state, "version": nil, "undoable": "No", "installedBy": "APP", "executionTime": 42, "filepath": "V1__example.sql"})
	}
	payload, e := json.Marshal(map[string]any{"migrations": rows, "schemaVersion": "1"})
	must(t, e)
	x := InfoCapture{JSON: payload, Success: false}
	infoWarnings(&x)
	var decoded map[string]any
	must(t, json.Unmarshal(x.JSON, &decoded))
	if len(decoded["migrations"].([]any)) != len(states) {
		t.Fatal("native states collapsed")
	}
	if len(x.Warnings) < 3 {
		t.Fatal("missing partial/history warnings")
	}
}

func TestInfoRecordedRunSourceAndDestinationChange(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__record.sql", "CREATE TABLE fixture(id NUMBER);")
	r, e := f.s.SubmitRun(testCtx, f.trace(), in, "record-info")
	must(t, e)
	waitRun(t, f, r.ID)
	capture, e := f.s.LatestInfo(testCtx, f.admin, target.ID, schema.ID, r.ID)
	must(t, e)
	if capture == nil || capture.SourceRunID != r.ID || len(capture.Files) != 1 {
		t.Fatalf("source lost %+v", capture)
	}
	_, e = f.db.SQL.Exec("UPDATE database_targets SET host='changed.example' WHERE id=?", target.ID)
	must(t, e)
	latest, e := f.s.LatestInfo(testCtx, f.admin, target.ID, schema.ID, "")
	must(t, e)
	if !strings.Contains(strings.Join(latest.Warnings, " "), "configuration has changed") {
		t.Fatal("destination drift not explicit")
	}
	if latest.Snapshot.Target.Host == "changed.example" {
		t.Fatal("historical snapshot rewritten")
	}
}

func TestInfoCaptureOrderingUsesInstants(t *testing.T) {
	if !captureAfter("2026-10-16T00:00:00.1234Z", "2026-10-16T00:00:00.123Z") {
		t.Fatal("fractional precision misorders captures")
	}
	if captureAfter("2026-10-16T00:00:00Z", "2026-10-16T00:00:00.123Z") {
		t.Fatal("whole seconds misorder captures")
	}
}

type fixedInfoRunner struct {
	*fakeEngine
	result flyway.Result
}

func (r *fixedInfoRunner) Execute(_ context.Context, request flyway.Request) flyway.Result {
	if request.Command != "info" {
		panic("unexpected command")
	}
	return r.result
}

func TestInfoFailedAndMismatchedRefreshRemainVisibleWithoutJobs(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__expected.sql", "CREATE TABLE never_execute(id NUMBER);")
	empty, e := f.s.LatestInfo(testCtx, f.admin, target.ID, schema.ID, "")
	must(t, e)
	if empty != nil {
		t.Fatal("invented empty evidence")
	}
	f.s.execution.runner = &fixedInfoRunner{fakeEngine: newEngine(), result: flyway.Result{Success: false, ExitCode: 1, JSON: json.RawMessage(`{"migrations":[{"state":"Missing","description":"Unresolved"}],"schemaVersion":null}`), Stderr: "network interrupted"}}
	capture, e := f.s.RefreshInfo(testCtx, f.trace(), in)
	must(t, e)
	if capture.Success || !strings.Contains(strings.Join(capture.Warnings, " "), "Artifact mismatch") {
		t.Fatalf("missing partial/mismatch result %+v", capture)
	}
	latest, e := f.s.LatestInfo(testCtx, f.admin, target.ID, schema.ID, "")
	must(t, e)
	if latest == nil || latest.ID != capture.ID || latest.Success || latest.Stderr != "network interrupted" {
		t.Fatal("latest failed evidence lost")
	}
	var n int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs").Scan(&n))
	if n != 0 {
		t.Fatal("inspection created job")
	}
}

func TestInfoOperatorRequiresOperateAndExplicitProd(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__operator.sql", "SELECT 1 FROM dual;")
	_, operator, _ := f.user(t, "info-operator", "Operator")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", operator.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true}}))
	if _, e := f.s.RefreshInfo(testCtx, Trace{Actor: operator}, in); e == nil {
		t.Fatal("read-only operator refreshed")
	}
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", operator.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true}}))
	if _, e := f.s.RefreshInfo(testCtx, Trace{Actor: operator}, in); e != nil {
		t.Fatal(e)
	}
	_, e := f.db.SQL.Exec("UPDATE database_targets SET environment='PROD' WHERE id=?", target.ID)
	must(t, e)
	if _, e = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, in); e == nil {
		t.Fatal("PROD permit inferred")
	}
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", operator.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true, Prod: true}}))
	if _, e = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, in); e != nil {
		t.Fatal(e)
	}
}
