package core

import (
	"context"
	"encoding/json"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeEngine struct {
	mu             sync.Mutex
	applied        map[string]map[string]bool
	failed         map[string]string
	calls          []string
	migrations     map[string]int
	gate           <-chan struct{}
	started        chan string
	unsafe         bool
	corruptSuccess bool
	checksums      map[string]map[string]string
}

func newEngine() *fakeEngine {
	return &fakeEngine{applied: map[string]map[string]bool{}, failed: map[string]string{}, migrations: map[string]int{}, checksums: map[string]map[string]string{}}
}
func (f *fakeEngine) Check(context.Context) flyway.Readiness {
	return flyway.Readiness{Ready: true, Version: "11.20.0", Integrity: "verified", Dependency: "fixture", Blockers: []string{}}
}
func (f *fakeEngine) Execute(ctx context.Context, r flyway.Request) flyway.Result {
	if r.Command == "migrate" && f.gate != nil {
		if f.started != nil {
			f.started <- r.Schema
		}
		select {
		case <-f.gate:
		case <-ctx.Done():
			return flyway.Result{ExitCode: -1, TimedOut: true}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Schema+":"+r.Command)
	if f.applied[r.Schema] == nil {
		f.applied[r.Schema] = map[string]bool{}
		f.checksums[r.Schema] = map[string]string{}
	}
	obj := map[string]any{"flywayVersion": "11.20.0", "operation": r.Command}
	success := true
	code := 0
	fileNames := []string{}
	for p := range r.Files {
		fileNames = append(fileNames, p)
	}
	sort.Strings(fileNames)
	switch r.Command {
	case "info":
		migrations := []map[string]any{}
		for _, p := range fileNames {
			var version any
			base := strings.Split(p, "/")[len(strings.Split(p, "/"))-1]
			if strings.HasPrefix(base, "V") {
				version = strings.TrimPrefix(strings.Split(base, "__")[0], "V")
			}
			state := "Pending"
			installed := ""
			if f.applied[r.Schema][p] {
				state = "Success"
				if f.checksums[r.Schema][p] != digest(string(r.Files[p])) {
					state = "Outdated"
				}
				installed = "2026-10-16T00:00:00Z"
			}
			if f.failed[r.Schema] == p {
				state = "Failed"
				installed = "2026-10-16T00:00:01Z"
			}
			migrations = append(migrations, map[string]any{"version": version, "script": p, "state": state, "type": "SQL", "description": p, "installedOn": installed})
		}
		obj["migrations"] = migrations
		obj["schemaVersion"] = "1"
		obj["schemaName"] = r.Schema
	case "validate":
		invalid := []any{}
		if p := f.failed[r.Schema]; p != "" {
			success = false
			code = 1
			version := strings.TrimPrefix(strings.Split(p, "__")[0], "V")
			invalid = append(invalid, map[string]any{"version": version, "errorDetails": map[string]any{"errorCode": "FAILED_VERSIONED_MIGRATION"}})
		}
		if f.unsafe && len(invalid) > 0 {
			invalid = append(invalid, map[string]any{"version": "999", "errorDetails": map[string]any{"errorCode": "CHECKSUM_MISMATCH"}})
		}
		obj["validationSuccessful"] = success
		obj["invalidMigrations"] = invalid
		obj["validateCount"] = len(r.Files)
	case "migrate":
		f.migrations[r.Schema]++
		failed := ""
		for _, p := range fileNames {
			if strings.Contains(p, "fail") {
				failed = p
				break
			}
		}
		if failed != "" {
			f.failed[r.Schema] = failed
			success = false
			code = 1
			obj["success"] = false
			obj["migrations"] = []any{}
			obj["migrationsExecuted"] = 0
			obj["error"] = map[string]any{"errorCode": "FAULT", "message": "fixture failure", "path": failed}
		} else {
			for _, p := range fileNames {
				f.applied[r.Schema][p] = true
				f.checksums[r.Schema][p] = digest(string(r.Files[p]))
			}
			obj["migrationsExecuted"] = len(r.Files)
			obj["migrations"] = []any{}
			if f.corruptSuccess {
				obj["migrationsExecuted"] = "invalid"
				success = false
			}
		}
	case "repair":
		p := f.failed[r.Schema]
		delete(f.failed, r.Schema)
		obj["migrationsRemoved"] = []any{map[string]any{"version": "1", "description": p}}
		obj["migrationsAligned"] = []any{}
		obj["migrationsDeleted"] = []any{}
		obj["repairActions"] = []string{"REMOVED FAILED MIGRATIONS"}
	}
	payload, _ := json.Marshal(obj)
	return flyway.Result{Success: success, ExitCode: code, JSON: payload, Stdout: string(payload), Version: "11.20.0"}
}
func (f *fakeEngine) migrateCount(schema string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.migrations[schema]
}

type cancelOnInfoRunner struct {
	cancel context.CancelFunc
	calls  []string
}

func (*cancelOnInfoRunner) Check(context.Context) flyway.Readiness {
	return flyway.Readiness{Ready: true, Version: "11.20.0", Integrity: "verified", Dependency: "fixture"}
}
func (r *cancelOnInfoRunner) Execute(_ context.Context, req flyway.Request) flyway.Result {
	r.calls = append(r.calls, req.Command)
	if req.Command == "info" {
		r.cancel()
		return flyway.Result{ExitCode: -1, TimedOut: true, Stderr: "execution deadline exceeded"}
	}
	return flyway.Result{Success: true, Version: "11.20.0"}
}

func executionFixture(t *testing.T) (*fixture, *fakeEngine, Target, Schema, ExecutionAccount) {
	t.Helper()
	f := newFixture(t)
	engine := newEngine()
	must(t, f.s.StartExecution(testCtx, engine, "approved-fixture-pin", f.dir, artifact.DefaultLimits()))
	t.Cleanup(f.s.StopExecution)
	target := f.target(t, "execution", "UAT")
	schema := f.schema(t, target.ID, "APP")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", f.admin.ID, []Grant{{TargetID: target.ID, Read: true, Operate: true}}))
	account, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "deployer", Username: "DEPLOY", SchemaIDs: []string{schema.ID}, Password: "oracle-password-never-returned"})
	must(t, e)
	return f, engine, target, schema, account
}
func uploadRun(t *testing.T, f *fixture, target Target, schema Schema, account ExecutionAccount, name, sql string) RunInput {
	t.Helper()
	a, e := f.s.UploadArtifact(testCtx, f.trace(), target.ID, schema.ID, []artifact.Input{{Name: name, Bytes: []byte(sql)}})
	must(t, e)
	return RunInput{TargetID: target.ID, SchemaID: schema.ID, ExecutionAccountID: account.ID, ArtifactID: a.ID}
}
func waitRun(t *testing.T, f *fixture, rid string) Run {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, e := f.s.Run(testCtx, f.admin, rid)
		must(t, e)
		if terminal(r.Status) && r.RepairOutcome != "PENDING" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, e := f.s.Run(testCtx, f.admin, rid)
	t.Fatalf("run did not settle: %+v %v", r, e)
	return r
}

func TestPreflightTimeoutReturnsDiagnosticsAndPersistsAudit(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__timeout.sql", "CREATE TABLE timeout_probe(id NUMBER);")
	ctx, cancel := context.WithCancel(context.Background())
	runner := &cancelOnInfoRunner{cancel: cancel}
	f.s.execution.runner = runner

	pre, err := f.s.Preflight(ctx, f.trace(), in)
	must(t, err)
	if pre.Ready || pre.LocalReady == false || pre.LiveStatus != "NOT_VERIFIED" {
		t.Fatalf("timeout preflight should preserve local readiness and fail closed: %+v", pre)
	}
	foundTimeout := false
	for _, blocker := range pre.Blockers {
		if blocker.Code == "REQUEST_TIMEOUT" && strings.Contains(blocker.Message, "No migration was started") {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatalf("missing actionable timeout blocker: %+v", pre.Blockers)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "info" {
		t.Fatalf("timeout must stop read-only preflight without validate or migrate: %v", runner.calls)
	}
	events, err := f.s.Audit(testCtx, f.admin, Page{Action: "migration.preflight", Limit: 20})
	must(t, err)
	foundAudit := false
	for _, event := range events {
		if event.RequestID == f.trace().RequestID && event.Target == schema.ID {
			if event.Outcome != "NOT_VERIFIED" {
				t.Fatalf("timeout audit outcome %q, want NOT_VERIFIED", event.Outcome)
			}
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatal("timeout preflight audit event was not persisted")
	}
}

func TestRunFilesFailsClosedOnMissingReservationAndReturnsEmptyForNoItems(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__missing.sql", "SELECT 1 FROM dual;\r\n")
	run, e := f.s.SubmitRun(testCtx, f.trace(), in, "run-files-missing-row")
	must(t, e)
	if got := waitRun(t, f, run.ID); got.Status != "SUCCEEDED" {
		t.Fatalf("run did not complete: %s", got.Status)
	}

	conn, e := f.db.SQL.Conn(testCtx)
	must(t, e)
	if _, e = conn.ExecContext(testCtx, "PRAGMA foreign_keys=OFF"); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(testCtx, "DROP TRIGGER file_immutable_delete"); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(testCtx, "DELETE FROM migration_files WHERE artifact_id=? AND path=?", in.ArtifactID, "V1__missing.sql"); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(testCtx, "PRAGMA foreign_keys=ON"); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	must(t, conn.Close())

	if _, e = f.s.RunFiles(testCtx, f.admin, run.ID); e == nil {
		t.Fatal("missing reserved content was accepted")
	} else {
		status(t, e, 409)
	}
	// The early error path must close its rows before the next database read.
	if _, e = f.s.Run(testCtx, f.admin, run.ID); e != nil {
		t.Fatalf("database connection remained busy after missing content: %v", e)
	}
	if _, e = f.db.SQL.Exec("DROP TRIGGER item_immutable_delete"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.SQL.Exec("DELETE FROM migration_run_items WHERE run_id=?", run.ID); e != nil {
		t.Fatal(e)
	}
	files, e := f.s.RunFiles(testCtx, f.admin, run.ID)
	must(t, e)
	if files == nil || len(files) != 0 {
		t.Fatalf("empty reservations should return an empty list, got %#v", files)
	}
}

func TestDurableRunContinuityIdempotencyAndImmutableEvidence(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "nested/V1__initial.sql", "CREATE TABLE fixture(id NUMBER);\r\n")
	pre, e := f.s.Preflight(testCtx, f.trace(), in)
	must(t, e)
	if !pre.Ready || pre.LiveStatus != "VERIFIED" {
		t.Fatalf("preflight %+v", pre)
	}
	const n = 12
	ids := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.s.SubmitRun(testCtx, f.trace(), in, "same-key")
			if e != nil {
				errs <- e
			} else {
				ids <- r.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	rid := ""
	for got := range ids {
		if rid == "" {
			rid = got
		}
		if got != rid {
			t.Fatal("duplicate run")
		}
	}
	result := waitRun(t, f, rid)
	if result.FlywayConfiguration["flyway.ignoreMigrationPatterns"] != "*:pending" {
		t.Fatal("exact Flyway policy not frozen")
	}
	if _, ok := result.FlywayConfiguration["flyway.password"]; ok {
		t.Fatal("secret included in frozen configuration")
	}
	if result.Status != "SUCCEEDED" || engine.migrateCount(schema.Name) != 1 {
		t.Fatalf("result %+v", result)
	}
	in2 := uploadRun(t, f, target, schema, account, "V2__second.sql", "ALTER TABLE fixture ADD name VARCHAR2(20);")
	_, e = f.s.SubmitRun(testCtx, f.trace(), in2, "same-key")
	status(t, e, 409)
	r2, e := f.s.SubmitRun(testCtx, f.trace(), in2, "second-key")
	must(t, e)
	must(t, func() error {
		if waitRun(t, f, r2.ID).Status != "SUCCEEDED" {
			return fmt.Errorf("V2 failed")
		}
		return nil
	}())
	manifests, e := f.s.RunArtifacts(testCtx, f.admin, r2.ID)
	must(t, e)
	if len(manifests) != 2 {
		t.Fatalf("continuity lost: %+v", manifests)
	}
	files, e := readStoredFiles(testCtx, f.db.SQL, `SELECT f.artifact_id,f.path,f.sha256,f.kind,f.version,f.content FROM migration_run_items i JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path WHERE i.run_id=?`, r2.ID)
	must(t, e)
	if len(files) != 2 {
		t.Fatal("old SQL hidden")
	}
	_, bytes, e := f.s.ArtifactBytes(testCtx, f.admin, in.ArtifactID, "nested/V1__initial.sql", 0, false)
	must(t, e)
	if string(bytes) != "CREATE TABLE fixture(id NUMBER);\r\n" {
		t.Fatal("SQL was rewritten")
	}
	drift := uploadRun(t, f, target, schema, account, "nested/V1__initial.sql", "changed content")
	_, e = f.s.SubmitRun(testCtx, f.trace(), drift, "drift")
	status(t, e, 409)
	for _, query := range []string{"UPDATE artifacts SET sha256='bad'", "DELETE FROM artifact_blobs", "UPDATE migration_files SET content=X'00'", "DELETE FROM migration_run_items", "DELETE FROM run_executions", "DELETE FROM migration_logs", "UPDATE migration_runs SET snapshot='{}'"} {
		if _, e = f.db.SQL.Exec(query); e == nil {
			t.Fatalf("immutable evidence changed: %s", query)
		}
	}
	logs, e := f.s.RunLogs(testCtx, f.admin, rid, Page{Limit: 200}, "")
	must(t, e)
	for _, l := range logs {
		if strings.Contains(l.Message, "oracle-password") {
			t.Fatal("password leaked")
		}
	}
}
func TestConditionalRepairNeverRetriesOrSucceedsMigration(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(fmt.Sprint(unsafe), func(t *testing.T) {
			f, engine, target, schema, account := executionFixture(t)
			engine.unsafe = unsafe
			in := uploadRun(t, f, target, schema, account, "V1__fail.sql", "CREATE TABLE partial_fixture(id NUMBER); invalid SQL;")
			r, e := f.s.SubmitRun(testCtx, f.trace(), in, "failure")
			must(t, e)
			done := waitRun(t, f, r.ID)
			if done.MigrationOutcome != "FAILED" || engine.migrateCount(schema.Name) != 1 {
				t.Fatalf("failed SQL replayed or reclassified %+v", done)
			}
			if unsafe {
				if done.Status != "NEEDS_REVIEW" || done.RepairOutcome != "UNSAFE" {
					t.Fatalf("unsafe repair %+v", done)
				}
				if _, e = f.s.SubmitRun(testCtx, f.trace(), in, "blocked"); e == nil {
					t.Fatal("held stream accepted")
				}
			} else {
				if done.Status != "FAILED" || done.RepairOutcome != "SUCCEEDED" {
					t.Fatalf("safe repair %+v", done)
				}
			}
			evidence, e := f.s.RunExecutions(testCtx, f.admin, r.ID)
			must(t, e)
			repairs := 0
			for _, x := range evidence {
				if x.Command == "repair" {
					repairs++
				}
			}
			if unsafe && repairs != 0 || !unsafe && repairs != 1 {
				t.Fatalf("repair count %d", repairs)
			}
			repair, e := f.s.RunRepair(testCtx, f.admin, r.ID)
			must(t, e)
			if repair.Outcome != done.RepairOutcome {
				t.Fatal("repair evidence mismatch")
			}
		})
	}
}
func TestStreamSerializationAndIndependentStreams(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 10)
	in := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	r1, e := f.s.SubmitRun(testCtx, f.trace(), in, "one")
	must(t, e)
	select {
	case <-engine.started:
	case <-time.After(4 * time.Second):
		t.Fatal("not dispatched")
	}
	r2, e := f.s.SubmitRun(testCtx, f.trace(), in, "two")
	must(t, e)
	other := f.schema(t, target.ID, "OTHER")
	account2, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "other", Username: "OTHER_DEPLOY", SchemaIDs: []string{other.ID}, Password: "other-password"})
	must(t, e)
	in3 := uploadRun(t, f, target, other, account2, "V1__independent.sql", "SELECT 2 FROM dual;")
	r3, e := f.s.SubmitRun(testCtx, f.trace(), in3, "three")
	must(t, e)
	select {
	case name := <-engine.started:
		if name != other.Name {
			t.Fatalf("same stream ran concurrently: %s", name)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("independent stream unnecessarily blocked")
	}
	close(gate)
	for _, r := range []Run{r1, r2, r3} {
		if waitRun(t, f, r.ID).Status != "SUCCEEDED" {
			t.Fatal("serialized run failed")
		}
	}
}
func TestQueuedAuthoritySnapshotAndCancellation(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 4)
	in := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	first, e := f.s.SubmitRun(testCtx, f.trace(), in, "first")
	must(t, e)
	<-engine.started
	cancelled, e := f.s.SubmitRun(testCtx, f.trace(), in, "cancel")
	must(t, e)
	must(t, f.s.CancelRun(testCtx, f.trace(), cancelled.ID))
	if r := waitRun(t, f, cancelled.ID); r.Status != "CANCELLED" {
		t.Fatal(r.Status)
	}
	queued, e := f.s.SubmitRun(testCtx, f.trace(), in, "queued")
	must(t, e)
	_, e = f.s.ReplaceCredential(testCtx, f.trace(), account.ID, "rotated-before-dispatch", false)
	must(t, e)
	close(gate)
	waitRun(t, f, first.ID)
	if r := waitRun(t, f, queued.ID); r.Status != "BLOCKED" {
		t.Fatalf("changed credential executed %+v", r)
	}
	if engine.migrateCount(schema.Name) != 1 {
		t.Fatal("queued snapshot changed silently")
	}
}
func TestCrashRecoveryHoldsAmbiguousStreamWithoutReplay(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	// Stop dispatch, leaving the durable queue under test control.
	f.s.StopExecution()
	in := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	run, e := f.s.SubmitRun(testCtx, f.trace(), in, "recover")
	must(t, e)
	must(t, func() error {
		_, e := f.db.SQL.Exec(`UPDATE migration_runs SET status='PREFLIGHT' WHERE id=?`, run.ID)
		return e
	}())
	_, e = f.db.SQL.Exec(`UPDATE migration_runs SET status='RUNNING' WHERE id=?`, run.ID)
	must(t, e)
	_, e = f.db.SQL.Exec(`UPDATE jobs SET claimed=1 WHERE run_id=?`, run.ID)
	must(t, e)
	must(t, f.s.recoverExecution(testCtx))
	recovered := waitRun(t, f, run.ID)
	if recovered.Status != "NEEDS_REVIEW" {
		t.Fatal(recovered.Status)
	}
	claimed, e := f.s.claim(testCtx)
	must(t, e)
	if claimed != "" || engine.migrateCount(schema.Name) != 0 {
		t.Fatal("ambiguous migration replayed")
	}
	_, e = f.s.SubmitRun(testCtx, f.trace(), in, "new-after-crash")
	status(t, e, 409)
}
func TestRunStateMachineRejectsTerminalReactivation(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	f.s.StopExecution()
	in := uploadRun(t, f, target, schema, account, "V1__state.sql", "SELECT 1 FROM dual;")
	run, e := f.s.SubmitRun(testCtx, f.trace(), in, "state")
	must(t, e)
	for _, state := range []string{"RUNNING", "REPAIRING", "SUCCEEDED", "FAILED", "NEEDS_REVIEW"} {
		if _, e = f.db.SQL.Exec(`UPDATE migration_runs SET status=? WHERE id=?`, state, run.ID); e == nil {
			t.Fatalf("illegal QUEUED -> %s", state)
		}
	}
	must(t, f.s.CancelRun(testCtx, f.trace(), run.ID))
	for _, state := range []string{"QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING", "SUCCEEDED", "FAILED"} {
		if _, e = f.db.SQL.Exec(`UPDATE migration_runs SET status=? WHERE id=?`, state, run.ID); e == nil {
			t.Fatalf("terminal -> %s", state)
		}
	}
}

func TestCancelledPredecessorCannotBeExecutedByLaterUpload(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 10)
	initial := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	r1, e := f.s.SubmitRun(testCtx, f.trace(), initial, "initial")
	must(t, e)
	<-engine.started
	cancelledInput := uploadRun(t, f, target, schema, account, "V2__cancelled.sql", "DO NOT EXECUTE CANCELLED SQL")
	r2, e := f.s.SubmitRun(testCtx, f.trace(), cancelledInput, "cancel-me")
	must(t, e)
	laterInput := uploadRun(t, f, target, schema, account, "V3__later.sql", "SELECT 3 FROM dual;")
	r3, e := f.s.SubmitRun(testCtx, f.trace(), laterInput, "later")
	must(t, e)
	must(t, f.s.CancelRun(testCtx, f.trace(), r2.ID))
	close(gate)
	if waitRun(t, f, r1.ID).Status != "SUCCEEDED" {
		t.Fatal("initial failed")
	}
	if r := waitRun(t, f, r3.ID); r.Status != "BLOCKED" || !strings.Contains(r.Reason, "earlier migration") {
		t.Fatalf("cancelled SQL inherited %+v", r)
	}
	if engine.migrateCount(schema.Name) != 1 {
		t.Fatal("cancelled predecessor SQL executed")
	}
	// Unused cancelled/blocked reservations must not contaminate a fresh submission.
	fresh, e := f.s.SubmitRun(testCtx, f.trace(), laterInput, "fresh-later")
	must(t, e)
	if r := waitRun(t, f, fresh.ID); r.Status != "SUCCEEDED" {
		t.Fatalf("unused reservation poisoned stream: %+v", r)
	}
}
func TestRepairedFailureIsNeverAnImplicitSQLRetry(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	failed := uploadRun(t, f, target, schema, account, "V1__fail.sql", "PARTIAL DDL")
	first, e := f.s.SubmitRun(testCtx, f.trace(), failed, "first-failure")
	must(t, e)
	if r := waitRun(t, f, first.ID); r.RepairOutcome != "SUCCEEDED" {
		t.Fatal(r)
	}
	next := uploadRun(t, f, target, schema, account, "V2__next.sql", "SELECT 2 FROM dual;")
	pre, e := f.s.Preflight(testCtx, f.trace(), next)
	must(t, e)
	if pre.Ready || !strings.Contains(fmt.Sprint(pre.Blockers), "automatic SQL retry") {
		t.Fatalf("implicit retry preflight %+v", pre)
	}
	second, e := f.s.SubmitRun(testCtx, f.trace(), next, "next-must-not-retry")
	must(t, e)
	if r := waitRun(t, f, second.ID); r.Status != "BLOCKED" {
		t.Fatalf("retried failed history %+v", r)
	}
	if engine.migrateCount(schema.Name) != 1 {
		t.Fatal("migration SQL automatically retried")
	}
}
func TestQueuedUserGrantRevocationBlocksDispatch(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 10)
	initial := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	first, e := f.s.SubmitRun(testCtx, f.trace(), initial, "first")
	must(t, e)
	<-engine.started
	user, operator, _ := f.user(t, "queued-operator", "Operator")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", user.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true}}))
	queued, e := f.s.SubmitRun(testCtx, Trace{RequestID: "operator", Actor: operator}, initial, "operator-queue")
	must(t, e)
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", user.ID, nil))
	close(gate)
	waitRun(t, f, first.ID)
	if r := waitRun(t, f, queued.ID); r.Status != "BLOCKED" {
		t.Fatalf("revoked operator executed %+v", r)
	}
	if engine.migrateCount(schema.Name) != 1 {
		t.Fatal("revoked grant dispatched")
	}
}
func TestRunHistoryFiltersCurrentAuthorizationBeforePagination(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	r, e := f.s.SubmitRun(testCtx, f.trace(), in, "history")
	must(t, e)
	waitRun(t, f, r.ID)
	u, viewer, _ := f.user(t, "history-viewer", "Viewer")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", u.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true}}))
	out, e := f.s.Runs(testCtx, viewer, RunFilter{Page: Page{Limit: 1}})
	must(t, e)
	if len(out) != 1 {
		t.Fatal("granted history missing")
	}
	_, e = f.s.SaveTarget(testCtx, f.trace(), target.ID, TargetInput{ID: target.ID, Name: target.Name, Environment: target.Environment, Host: target.Host, Port: target.Port, Service: target.Service, Transport: target.Transport, Active: false})
	must(t, e)
	out, e = f.s.Runs(testCtx, viewer, RunFilter{Page: Page{Limit: 1}})
	must(t, e)
	if len(out) != 0 {
		t.Fatal("inactive resource leaked through history list")
	}
}

func TestRepairRequiresCompletePendingAndAppliedHistory(t *testing.T) {
	before := json.RawMessage(`{"migrations":[{"version":"1","script":"V1__fail.sql","state":"Pending","type":"SQL"},{"version":"0","script":"V0__old.sql","state":"Success","type":"SQL","installedOn":"2026-10-16T00:00:00Z"}]}`)
	missing := json.RawMessage(`{"migrations":[{"version":"0","script":"V0__old.sql","state":"Success","type":"SQL","installedOn":"2026-10-16T00:00:00Z"}]}`)
	altered := json.RawMessage(`{"migrations":[{"version":"1","script":"V1__fail.sql","state":"Pending","type":"SQL"},{"version":"0","script":"V0__old.sql","state":"Success","type":"SQL","installedOn":"2026-10-16T00:00:01Z"}]}`)
	if repairHistoryRestored(before, missing) || repairHistoryRestored(before, altered) {
		t.Fatal("unsafe post-repair history accepted")
	}
	if !repairHistoryRestored(before, before) {
		t.Fatal("exact restored history rejected")
	}
	for _, payload := range []string{`{}`, `{"migrationsRemoved":[{}],"migrationsAligned":[{}],"migrationsDeleted":[]}`, `{"migrationsRemoved":[{}],"migrationsAligned":[],"migrationsDeleted":[{}]}`} {
		if repairResultSafe(json.RawMessage(payload)) {
			t.Fatal("unrelated history mutation accepted")
		}
	}
}

func TestAllRunStateTransitionsAndQueuedRestart(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	f.s.StopExecution()
	in := uploadRun(t, f, target, schema, account, "V1__states.sql", "SELECT 1 FROM dual;")
	r, e := f.s.SubmitRun(testCtx, f.trace(), in, "state-matrix")
	must(t, e)
	paths := map[string][]string{
		"QUEUED": {}, "PREFLIGHT": {"PREFLIGHT"}, "RUNNING": {"PREFLIGHT", "RUNNING"}, "SUCCEEDED": {"PREFLIGHT", "RUNNING", "SUCCEEDED"}, "FAILED": {"PREFLIGHT", "RUNNING", "FAILED"}, "REPAIRING": {"PREFLIGHT", "RUNNING", "FAILED", "REPAIRING"}, "NEEDS_REVIEW": {"PREFLIGHT", "NEEDS_REVIEW"}, "BLOCKED": {"BLOCKED"}, "CANCELLED": {"CANCELLED"},
	}
	legal := map[string]string{"QUEUED": "PREFLIGHT BLOCKED CANCELLED", "PREFLIGHT": "RUNNING BLOCKED NEEDS_REVIEW", "RUNNING": "SUCCEEDED FAILED NEEDS_REVIEW", "FAILED": "REPAIRING NEEDS_REVIEW BLOCKED", "REPAIRING": "FAILED NEEDS_REVIEW BLOCKED"}
	tx, e := f.db.SQL.Begin()
	must(t, e)
	for from, path := range paths {
		for to := range paths {
			_, e = tx.Exec("SAVEPOINT transition_case")
			must(t, e)
			for _, state := range path {
				_, e = tx.Exec(`UPDATE migration_runs SET status=? WHERE id=?`, state, r.ID)
				must(t, e)
			}
			_, e = tx.Exec(`UPDATE migration_runs SET status=? WHERE id=?`, to, r.ID)
			allowed := from == to || strings.Contains(" "+legal[from]+" ", " "+to+" ")
			if allowed != (e == nil) {
				t.Fatalf("transition %s -> %s allowed=%t err=%v", from, to, allowed, e)
			}
			_, e = tx.Exec("ROLLBACK TO transition_case")
			must(t, e)
			_, e = tx.Exec("RELEASE transition_case")
			must(t, e)
		}
	}
	must(t, tx.Rollback())
	restarted, e := New(testCtx, f.db, f.dir, "")
	must(t, e)
	must(t, restarted.StartExecution(testCtx, engine, "approved-fixture-pin", f.dir, artifact.DefaultLimits()))
	defer restarted.StopExecution()
	// The durable QUEUED row, unlike uncertain RUNNING rows, may execute once.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, e := restarted.Run(testCtx, f.admin, r.ID)
		must(t, e)
		if run.Status == "SUCCEEDED" {
			if engine.migrateCount(schema.Name) != 1 {
				t.Fatal("queued run repeated")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued work not recovered")
}
func TestConnectivityDiagnosticsDoNotInventVerification(t *testing.T) {
	for text, code := range map[string]string{"ORA-01017: invalid credential": "ORACLE_AUTHENTICATION_FAILED", "ORA-12514: service missing": "ORACLE_SERVICE_UNAVAILABLE", "ORA-12541: no listener": "ORACLE_NETWORK_FAILED", "ORA-01031: privilege": "ORACLE_PRIVILEGES_INSUFFICIENT", "ORA-00942: table": "ORACLE_HISTORY_UNAVAILABLE", "timed out": "ORACLE_NOT_VERIFIED"} {
		p := preflightBase()
		applyLivePreflight(&p, flyway.Result{Stderr: text}, flyway.Result{})
		if p.Ready || p.Blockers[0].Code != code {
			t.Fatalf("diagnostic %s %+v", text, p)
		}
	}
	plan := executionPlan{files: []storedFile{{File: artifact.File{Path: "V1__missing.sql", Kind: "versioned", Version: "1"}}}}
	if resolvedSnapshot(plan, json.RawMessage(`{"migrations":[]}`)) == nil {
		t.Fatal("empty resolver accepted uploaded SQL")
	}
}

func TestDocumentedFlywayInfoFieldsPreserveHistoryIdentity(t *testing.T) {
	one := json.RawMessage(`{"migrations":[{"version":"1","filepath":"/private/run-one/sql/folder/V1__a.sql","installedOnUTC":"2026-10-16T00:00:00Z","state":"Success","type":"SQL"},{"version":"2","filepath":"/private/run-one/sql/V2__next.sql","installedOnUTC":"","state":"Pending","type":"SQL"}]}`)
	two := json.RawMessage(`{"migrations":[{"version":"1","filepath":"/private/run-two/sql/folder/V1__a.sql","installedOnUTC":"2026-10-16T00:00:00Z","state":"Success","type":"SQL"},{"version":"2","filepath":"/private/run-two/sql/V2__next.sql","installedOnUTC":"","state":"Pending","type":"SQL"}]}`)
	rows, e := history(one)
	must(t, e)
	if rows[0].Script != "V1__a.sql" || rows[0].InstalledOn != "2026-10-16T00:00:00Z" {
		t.Fatalf("documented fields lost %+v", rows)
	}
	if !repairHistoryRestored(one, two) {
		t.Fatal("ephemeral workspace changed history identity")
	}
	changed := json.RawMessage(strings.ReplaceAll(string(two), "2026-10-16T00:00:00Z", "2026-10-16T00:00:01Z"))
	if repairHistoryRestored(one, changed) {
		t.Fatal("installed UTC evidence ignored")
	}
}

func TestRepeatableUpdatesPreserveVersionedHistoryAndOldArtifact(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	original, e := f.s.UploadArtifact(testCtx, f.trace(), target.ID, schema.ID, []artifact.Input{{Name: "V1__initial.sql", Bytes: []byte("SELECT 1 FROM dual;")}, {Name: "nested/R__refresh.sql", Bytes: []byte("SELECT 'original' FROM dual;")}})
	must(t, e)
	in := RunInput{TargetID: target.ID, SchemaID: schema.ID, ExecutionAccountID: account.ID, ArtifactID: original.ID}
	r, e := f.s.SubmitRun(testCtx, f.trace(), in, "repeatable-first")
	must(t, e)
	if waitRun(t, f, r.ID).Status != "SUCCEEDED" {
		t.Fatal("initial failed")
	}
	update := uploadRun(t, f, target, schema, account, "nested/R__refresh.sql", "SELECT 'updated' FROM dual;")
	pre, e := f.s.Preflight(testCtx, f.trace(), update)
	must(t, e)
	if !pre.Ready {
		t.Fatalf("repeatable update blocked %+v", pre)
	}
	r2, e := f.s.SubmitRun(testCtx, f.trace(), update, "repeatable-update")
	must(t, e)
	if run := waitRun(t, f, r2.ID); run.Status != "SUCCEEDED" {
		t.Fatalf("repeatable update %+v", run)
	}
	manifests, e := f.s.RunArtifacts(testCtx, f.admin, r2.ID)
	must(t, e)
	if len(manifests) != 2 {
		t.Fatal("historical versioned file lost")
	}
	_, content, e := f.s.ArtifactBytes(testCtx, f.admin, original.ID, "nested/R__refresh.sql", 0, false)
	must(t, e)
	if string(content) != "SELECT 'original' FROM dual;" {
		t.Fatal("repeatable replaced immutable bytes")
	}
}
