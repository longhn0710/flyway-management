package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

type oracleRepairFaultRunner struct {
	*fakeEngine
	fault        string
	cancel       context.CancelFunc
	afterMigrate func()
}

func (r *oracleRepairFaultRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	if req.Command == "repair" && r.fault == "repair-failure" {
		r.mu.Lock()
		r.calls = append(r.calls, req.Schema+":repair")
		r.mu.Unlock()
		return flyway.Result{ExitCode: 7, JSON: json.RawMessage(`{"error":{"errorCode":"REPAIR_ERROR","message":"different repair error"}}`), Stderr: "different repair error"}
	}
	result := r.fakeEngine.Execute(ctx, req)
	if req.Command == "validate" && r.fault == "validation-identity" && !result.Success {
		result.JSON = json.RawMessage(`{"operation":"validate","flywayVersion":"11.20.0","validationSuccessful":false,"invalidMigrations":[{"version":"1","filepath":"V99__other.sql","errorDetails":{"errorCode":"FAILED_VERSIONED_MIGRATION"}}]}`)
	}
	if req.Command == "validate" && r.fault == "cancel-before-repair" && !result.Success {
		r.cancel()
	}
	if req.Command != "migrate" {
		return result
	}
	result.Stderr = "original Oracle CLI diagnostic"
	if r.afterMigrate != nil {
		r.afterMigrate()
	}
	switch r.fault {
	case "ambiguous":
		result.Ambiguous = true
	case "timeout":
		result.Ambiguous, result.TimedOut, result.ExitCode = true, true, -1
	case "signal":
		result.ExitCode = 143
	case "malformed":
		result.JSON, result.Stdout, result.Ambiguous = nil, `{"truncated":`, true
	case "generic-error":
		result.JSON = json.RawMessage(`{"error":{"errorCode":"ERROR","message":"original generic CLI error"}}`)
	case "exit-zero":
		result.ExitCode = 0
	case "conflicting-identity":
		result.JSON = json.RawMessage(strings.ReplaceAll(string(result.JSON), "V1__fail.sql", "V99__other.sql"))
	case "cancelled":
		r.cancel()
	}
	return result
}

func TestOracleMandatoryRepairOutcomesPreserveOriginalFailure(t *testing.T) {
	for _, fault := range []string{"eligible", "repair-failure", "unsafe-history", "validation-identity", "diagnostic-persistence-failure", "ambiguous", "timeout", "signal", "malformed", "generic-error", "exit-zero", "conflicting-identity", "cancelled", "cancel-before-repair"} {
		t.Run(fault, func(t *testing.T) {
			f, engine, target, schema, account := executionFixture(t)
			f.s.StopExecution()
			ctx, cancel := context.WithCancel(testCtx)
			defer cancel()
			runner := &oracleRepairFaultRunner{fakeEngine: engine, fault: fault, cancel: cancel}
			f.s.execution.runner = runner
			if fault == "diagnostic-persistence-failure" {
				runner.afterMigrate = func() {
					_, err := f.db.SQL.Exec(`CREATE TRIGGER fail_diagnostic BEFORE INSERT ON run_executions WHEN NEW.command='info' BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END`)
					must(t, err)
				}
			}
			engine.unsafe = fault == "unsafe-history"
			const sql = "CREATE TABLE partial_fixture(id NUMBER); invalid SQL;"
			in := uploadRun(t, f, target, schema, account, "V1__fail.sql", sql)
			run, err := f.s.SubmitRun(testCtx, f.trace(), in, "original-failure")
			must(t, err)
			claimed, err := f.s.claim(testCtx)
			must(t, err)
			if claimed != run.ID {
				t.Fatal(claimed)
			}
			f.s.executeJob(ctx, run.ID)
			final := waitRun(t, f, run.ID)
			wantState, wantRepair, wantRepairs := "NEEDS_REVIEW", "UNSAFE", 0
			if fault == "eligible" {
				wantState, wantRepair, wantRepairs = "FAILED", "SUCCEEDED", 1
			}
			if fault == "repair-failure" {
				wantRepair, wantRepairs = "FAILED", 1
			}
			if final.Status != wantState || final.MigrationOutcome != "FAILED" || final.RepairOutcome != wantRepair {
				t.Fatalf("wrong independent outcomes: %+v", final)
			}
			if final.Failure == nil || final.Failure.ExecutionID == "" || final.ExitCode == nil || final.Failure.ExitCode != *final.ExitCode {
				t.Fatal("original failure missing", final)
			}
			if fault == "malformed" || fault == "generic-error" || fault == "conflicting-identity" {
				if final.Failure.IdentityReason == "" || final.Failure.Script != "" || final.Failure.Version != "" {
					t.Fatal("failed script fabricated", final.Failure)
				}
			} else if final.Failure.Script != "V1__fail.sql" || final.Failure.ErrorCode != "FAULT" {
				t.Fatal("native failure identity/code lost", final.Failure)
			}
			if fault == "eligible" || fault == "repair-failure" || fault == "unsafe-history" {
				if final.Failure.Version != "1" || final.Failure.Message != "fixture failure" || !strings.Contains(final.Reason, "fixture failure") {
					t.Fatal("Repair replaced the root cause", final)
				}
			}
			var held bool
			must(t, f.db.SQL.QueryRow(`SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, target.ID, schema.ID).Scan(&held))
			if !held {
				t.Fatal("wrong stream hold", held)
			}
			repair, err := f.s.RunRepair(testCtx, f.admin, run.ID)
			must(t, err)
			if repair.Outcome != wantRepair || repair.Reason == "" {
				t.Fatal("missing Repair result/reason", repair)
			}
			if fault == "cancel-before-repair" && !strings.Contains(repair.Reason, "Application stopped") {
				t.Fatal("incorrect cancellation reason", repair)
			}
			if wantRepairs == 1 && !strings.Contains(string(repair.Before), `"Failed"`) {
				t.Fatal("pre-repair history missing", repair)
			}
			if fault == "eligible" && (!strings.Contains(string(repair.After), `"Pending"`) || engine.failed[schema.Name] != "") {
				t.Fatal("eligible Repair did not restore history", repair)
			}
			if wantRepair != "SUCCEEDED" && engine.failed[schema.Name] == "" {
				t.Fatal("unsafe failure silently repaired")
			}
			executions, err := f.s.RunExecutions(testCtx, f.admin, run.ID)
			must(t, err)
			migrations, repairs := 0, 0
			for _, execution := range executions {
				switch execution.Command {
				case "migrate":
					migrations++
					if execution.Stderr != "original Oracle CLI diagnostic" || execution.ExitCode != final.Failure.ExitCode || execution.ID != final.Failure.ExecutionID {
						t.Fatal("original CLI evidence changed", execution)
					}
				case "repair":
					repairs++
				case "info", "validate":
				default:
					t.Fatal("unexpected recovery command", execution.Command)
				}
			}
			if migrations != 1 || repairs != wantRepairs {
				t.Fatal("Repair skipped/duplicated or SQL replayed", migrations, repairs)
			}
			files, err := f.s.RunFiles(testCtx, f.admin, run.ID)
			must(t, err)
			_, content, err := f.s.RunFileBytes(testCtx, f.admin, run.ID, files[0].ArtifactID, files[0].Path)
			must(t, err)
			if string(content) != sql {
				t.Fatal("original SQL changed")
			}
			if held {
				_, err = f.s.SubmitRun(testCtx, f.trace(), in, "unsafe-retry")
				status(t, err, 409)
			}
		})
	}
}

func TestOracleRepairNeverReplaysSQLOrQueuedSuccessorAcrossRestart(t *testing.T) {
	for _, fault := range []string{"eligible", "repair-failure", "unsafe-history", "ambiguous"} {
		t.Run(fault, func(t *testing.T) {
			f, engine, target, schema, account := executionFixture(t)
			gate := make(chan struct{})
			engine.gate, engine.started = gate, make(chan string, 1)
			engine.unsafe = fault == "unsafe-history"
			f.s.execution.runner = &oracleRepairFaultRunner{fakeEngine: engine, fault: fault}
			firstInput := uploadRun(t, f, target, schema, account, "V1__fail.sql", "PARTIAL DDL")
			first, err := f.s.SubmitRun(testCtx, f.trace(), firstInput, "first-failure")
			must(t, err)
			<-engine.started
			next := uploadRun(t, f, target, schema, account, "V2__next.sql", "SELECT 2 FROM dual;")
			queued, err := f.s.SubmitRun(testCtx, f.trace(), next, "queued-successor")
			must(t, err)
			close(gate)
			original := waitRun(t, f, first.ID)
			if final := waitRun(t, f, queued.ID); final.Status != "BLOCKED" {
				t.Fatal("queued successor ran", final)
			}
			f.s.StopExecution()
			restarted, err := New(testCtx, f.db, f.dir, "")
			must(t, err)
			must(t, restarted.StartExecution(testCtx, engine, "approved-fixture-pin", f.dir, artifact.DefaultLimits()))
			restarted.StopExecution()
			defer restarted.StopExecution()
			replayed, err := restarted.SubmitRun(testCtx, f.trace(), firstInput, "first-failure")
			must(t, err)
			if replayed.ID != first.ID || replayed.Status != original.Status || replayed.Failure == nil || replayed.Failure.Message != "fixture failure" {
				t.Fatal("restart changed original failure", replayed)
			}
			if engine.migrateCount(schema.Name) != 1 {
				t.Fatal("migration SQL replayed")
			}
			repairs := 0
			for _, call := range engine.calls {
				if strings.HasSuffix(call, ":repair") {
					repairs++
				}
			}
			wantRepairs := 1
			if fault == "unsafe-history" || fault == "ambiguous" {
				wantRepairs = 0
			}
			if repairs != wantRepairs {
				t.Fatal("restart repeated Repair", repairs)
			}
		})
	}
}

func TestOracleRestartDuringRepairAssessmentRetainsFailureAndHold(t *testing.T) {
	for _, interrupted := range []string{"RUNNING", "FAILED", "REPAIRING"} {
		t.Run(interrupted, func(t *testing.T) {
			f, engine, target, schema, account := executionFixture(t)
			f.s.StopExecution()
			input := uploadRun(t, f, target, schema, account, "V1__fail.sql", "PARTIAL DDL")
			run, err := f.s.SubmitRun(testCtx, f.trace(), input, "interrupted-original")
			must(t, err)
			_, err = f.s.claim(testCtx)
			must(t, err)
			plan, err := f.s.planJob(testCtx, f.db.SQL, f.admin, run)
			must(t, err)
			defer clear(plan.password)
			must(t, f.s.changeRun(testCtx, run.ID, "RUNNING", "fixture running", "", "", nil, false, false))
			migrated, _, err := f.s.runCommand(testCtx, testCtx, run.ID, "migrate", plan)
			must(t, err)
			if interrupted != "RUNNING" {
				must(t, f.s.changeRun(testCtx, run.ID, "FAILED", "original fixture failure", "FAILED", "PENDING", &migrated.ExitCode, false, false))
				_, _, err = f.s.runCommand(testCtx, testCtx, run.ID, "info", plan)
				must(t, err)
			}
			if interrupted == "REPAIRING" {
				must(t, f.s.changeRun(testCtx, run.ID, "REPAIRING", "original fixture failure", "FAILED", "PENDING", nil, false, false))
			}
			restarted, err := New(testCtx, f.db, f.dir, "")
			must(t, err)
			must(t, restarted.StartExecution(testCtx, engine, "approved-fixture-pin", f.dir, artifact.DefaultLimits()))
			restarted.StopExecution()
			defer restarted.StopExecution()
			final, err := restarted.Run(testCtx, f.admin, run.ID)
			must(t, err)
			wantRepair := "UNSAFE"
			if interrupted == "REPAIRING" {
				wantRepair = "FAILED"
			}
			if final.Status != "NEEDS_REVIEW" || final.MigrationOutcome != "FAILED" || final.RepairOutcome != wantRepair || final.Failure == nil || final.Failure.Message != "fixture failure" || final.Failure.Script != "V1__fail.sql" {
				t.Fatal("restart lost original failure or left repair pending", final)
			}
			repair, err := restarted.RunRepair(testCtx, f.admin, run.ID)
			must(t, err)
			if repair.Outcome != wantRepair || repair.Reason == "" {
				t.Fatal("restart misreported Repair", repair)
			}
			_, err = restarted.SubmitRun(testCtx, f.trace(), input, "unsafe-retry-after-restart")
			status(t, err, 409)
			if engine.migrateCount(schema.Name) != 1 {
				t.Fatal("restart replayed failed SQL")
			}
			for _, call := range engine.calls {
				if strings.HasSuffix(call, ":repair") {
					t.Fatal("restart retried Repair")
				}
			}
		})
	}
}
