package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

// Keep the same failed-history fixture that formerly qualified for automatic
// Repair. Faults change only the native result or diagnostic persistence.
type oracleFaultRunner struct {
	*fakeEngine
	fault        *flyway.Result
	afterMigrate func()
}

func (r *oracleFaultRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	result := r.fakeEngine.Execute(ctx, req)
	if req.Command == "migrate" {
		if r.afterMigrate != nil {
			r.afterMigrate()
		}
		if r.fault != nil {
			return *r.fault
		}
	}
	return result
}

func assertNoRecoveryCommands(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		command := strings.SplitN(call, ":", 2)[1]
		if command != "info" && command != "validate" && command != "migrate" {
			t.Fatalf("unexpected recovery command: %s", call)
		}
	}
}

func TestOracleFailurePreservesEvidenceAndHistoryWithoutRepair(t *testing.T) {
	for _, scenario := range []string{"formerly-eligible", "checksum-mismatch", "timeout", "terminated", "malformed-json", "exit-zero", "ambiguous-json", "cancelled", "diagnostic-persistence-failure"} {
		t.Run(scenario, func(t *testing.T) {
			f, engine, target, schema, account := executionFixture(t)
			f.s.StopExecution()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := &oracleFaultRunner{fakeEngine: engine}
			fault := flyway.Result{ExitCode: 1, Version: "11.20.0", JSON: json.RawMessage(`{"operation":"migrate","error":{"message":"original failure"}}`), Stdout: "original stdout", Stderr: "original stderr"}
			switch scenario {
			case "checksum-mismatch":
				engine.unsafe = true
			case "timeout":
				fault.ExitCode, fault.TimedOut, fault.Ambiguous = -1, true, true
			case "terminated":
				fault.ExitCode, fault.Ambiguous = 143, true
			case "malformed-json":
				fault.JSON, fault.Stdout, fault.Ambiguous = nil, `{"truncated":`, true
			case "exit-zero":
				fault.ExitCode = 0
			case "ambiguous-json":
				fault.Ambiguous = true
			case "cancelled":
				runner.afterMigrate = cancel
			case "diagnostic-persistence-failure":
				runner.afterMigrate = func() {
					_, err := f.db.SQL.Exec(`CREATE TRIGGER fail_diagnostics BEFORE INSERT ON run_executions WHEN NEW.command='info' BEGIN SELECT RAISE(ABORT,'fixture diagnostic persistence failure'); END`)
					must(t, err)
				}
			}
			runner.fault = &fault
			f.s.execution.runner = runner
			const sql = "CREATE TABLE partial_fixture(id NUMBER); invalid SQL;"
			in := uploadRun(t, f, target, schema, account, "V1__fail.sql", sql)
			run, err := f.s.SubmitRun(testCtx, f.trace(), in, "original-failure")
			must(t, err)
			claimed, err := f.s.claim(testCtx)
			must(t, err)
			if claimed != run.ID {
				t.Fatal("fixture did not claim the queued run", claimed)
			}
			f.s.executeJob(ctx, run.ID)
			final := waitRun(t, f, run.ID)
			f.s.StopExecution() // diagnostics may finish after the durable hold
			if final.Status != "NEEDS_REVIEW" || final.MigrationOutcome != "FAILED" || final.RepairOutcome != "SKIPPED" || final.ExitCode == nil || *final.ExitCode != fault.ExitCode {
				t.Fatalf("failure outcome lost: %+v", final)
			}
			var held bool
			must(t, f.db.SQL.QueryRow(`SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, target.ID, schema.ID).Scan(&held))
			if !held || engine.failed[schema.Name] != "V1__fail.sql" || engine.migrateCount(schema.Name) != 1 {
				t.Fatal("failure history changed, stream released or SQL replayed")
			}
			assertNoRecoveryCommands(t, engine.calls)
			executions, err := f.s.RunExecutions(testCtx, f.admin, run.ID)
			must(t, err)
			migrations := 0
			for _, execution := range executions {
				if execution.Command != "migrate" {
					continue
				}
				migrations++
				if execution.Success || execution.ExitCode != fault.ExitCode || execution.Stdout != fault.Stdout || execution.Stderr != fault.Stderr || execution.Ambiguous != fault.Ambiguous || len(fault.JSON) > 0 && !reflect.DeepEqual(execution.JSON, fault.JSON) {
					t.Fatalf("original execution evidence changed: %+v", execution)
				}
			}
			if migrations != 1 {
				t.Fatalf("expected one original migration execution: %d", migrations)
			}
			files, err := f.s.RunFiles(testCtx, f.admin, run.ID)
			must(t, err)
			_, content, err := f.s.RunFileBytes(testCtx, f.admin, run.ID, files[0].ArtifactID, files[0].Path)
			must(t, err)
			if string(content) != sql {
				t.Fatal("original SQL evidence changed")
			}
			var repairs int
			must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM repair_operations`).Scan(&repairs))
			if repairs != 0 {
				t.Fatal("automatic repair operation recorded")
			}
			repair, err := f.s.RunRepair(testCtx, f.admin, run.ID)
			must(t, err)
			if repair.Outcome != "SKIPPED" {
				t.Fatal("repair read API reported an operation", repair)
			}
			_, err = f.s.SubmitRun(testCtx, f.trace(), in, "unsafe-new-retry")
			status(t, err, 409)
		})
	}
}

func TestOracleFailedStreamBlocksQueuedAndNewSQLAcrossRestart(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	gate := make(chan struct{})
	engine.gate, engine.started = gate, make(chan string, 1)
	firstInput := uploadRun(t, f, target, schema, account, "V1__fail.sql", "PARTIAL DDL")
	first, err := f.s.SubmitRun(testCtx, f.trace(), firstInput, "first-failure")
	must(t, err)
	<-engine.started
	next := uploadRun(t, f, target, schema, account, "V2__next.sql", "SELECT 2 FROM dual;")
	queued, err := f.s.SubmitRun(testCtx, f.trace(), next, "queued-successor")
	must(t, err)
	close(gate)
	if final := waitRun(t, f, first.ID); final.Status != "NEEDS_REVIEW" {
		t.Fatal(final)
	}
	if final := waitRun(t, f, queued.ID); final.Status != "BLOCKED" {
		t.Fatal("queued successor was not blocked", final)
	}
	f.s.StopExecution()
	restarted, err := New(testCtx, f.db, f.dir, "")
	must(t, err)
	f.s = restarted
	must(t, f.s.StartExecution(testCtx, engine, "approved-fixture-pin", f.dir, artifact.DefaultLimits()))
	defer f.s.StopExecution()
	f.s.StopExecution() // wait for read-only startup reconciliation
	replayed, err := f.s.SubmitRun(testCtx, f.trace(), firstInput, "first-failure")
	must(t, err)
	if replayed.ID != first.ID || replayed.Status != "NEEDS_REVIEW" {
		t.Fatal("idempotent lookup changed failed run", replayed)
	}
	pre, err := f.s.Preflight(testCtx, f.trace(), next)
	must(t, err)
	if pre.Ready || !strings.Contains(pre.Blockers[0].Message, "reconciliation") {
		t.Fatal("held stream passed preflight", pre)
	}
	_, err = f.s.SubmitRun(testCtx, f.trace(), next, "new-successor")
	status(t, err, 409)
	f.s.StopExecution()
	assertNoRecoveryCommands(t, engine.calls)
	if engine.migrateCount(schema.Name) != 1 || engine.failed[schema.Name] != "V1__fail.sql" {
		t.Fatal("restart repaired or replayed SQL")
	}
}

func TestLegacyRepairedFailureNeverBecomesImplicitSQLRetry(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	f.s.StopExecution()
	failed := uploadRun(t, f, target, schema, account, "V1__fail.sql", "PARTIAL DDL")
	first, err := f.s.SubmitRun(testCtx, f.trace(), failed, "legacy-failure")
	must(t, err)
	_, err = f.s.claim(testCtx)
	must(t, err)
	must(t, f.s.changeRun(testCtx, first.ID, "RUNNING", "legacy fixture", "", "", nil, false, false))
	// Model a historical release that removed failed history and released the
	// stream. The current worker must still reject an implicit replay of V1.
	exit := 1
	must(t, f.s.changeRun(testCtx, first.ID, "FAILED", "legacy repaired failure", "FAILED", "SUCCEEDED", &exit, false, true))
	next := uploadRun(t, f, target, schema, account, "V2__next.sql", "SELECT 2 FROM dual;")
	pre, err := f.s.Preflight(testCtx, f.trace(), next)
	must(t, err)
	if pre.Ready || !strings.Contains(pre.Blockers[0].Message, "automatic SQL retry") {
		t.Fatal("legacy failure passed implicit retry guard", pre)
	}
	second, err := f.s.SubmitRun(testCtx, f.trace(), next, "no-legacy-retry")
	must(t, err)
	claimed, err := f.s.claim(testCtx)
	must(t, err)
	if claimed != second.ID {
		t.Fatal("fixture did not claim successor", claimed)
	}
	f.s.executeJob(testCtx, second.ID)
	if final := waitRun(t, f, second.ID); final.Status != "BLOCKED" {
		t.Fatal("legacy failed SQL could be replayed", final)
	}
	if engine.migrateCount(schema.Name) != 0 {
		t.Fatal("legacy failure replayed SQL")
	}
}
