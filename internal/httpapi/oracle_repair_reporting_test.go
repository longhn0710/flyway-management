package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
	"flyway-manager/internal/flyway"
)

// This runner follows Flyway's native JSON shape closely and pauses inside
// Repair so the API can be inspected while recovery is still in progress.
type oracleRepairHTTPRunner struct {
	mu             sync.Mutex
	failed         bool
	repairStarted  chan struct{}
	repairContinue chan struct{}
	repairFails    bool
}

func (*oracleRepairHTTPRunner) Check(context.Context) flyway.Readiness {
	return flyway.Readiness{Ready: true, Version: testFlywayVersion, Integrity: "verified", Dependency: "java_and_oracle_jdbc_available", Blockers: []string{}}
}

func (r *oracleRepairHTTPRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := flyway.Result{Success: true, ExitCode: 0, Version: testFlywayVersion}
	switch req.Command {
	case "info":
		state := "Pending"
		if r.failed {
			state = "Failed"
		}
		result.JSON = json.RawMessage(`{"operation":"info","flywayVersion":"` + testFlywayVersion + `","migrations":[{"version":"1","script":"V1__fail.sql","state":"` + state + `"}]}`)
	case "validate":
		if r.failed {
			result.JSON = json.RawMessage(`{"operation":"validate","flywayVersion":"` + testFlywayVersion + `","validationSuccessful":false,"invalidMigrations":[{"version":"1","errorDetails":{"errorCode":"FAILED_VERSIONED_MIGRATION"}}]}`)
			result.Success, result.ExitCode = false, 1
		} else {
			result.JSON = json.RawMessage(`{"operation":"validate","flywayVersion":"` + testFlywayVersion + `","validationSuccessful":true,"invalidMigrations":[]}`)
		}
	case "migrate":
		r.failed = true
		result.Success, result.ExitCode = false, 1
		result.Stderr = "ORIGINAL_SQL_FAILURE"
		result.JSON = json.RawMessage(`{"operation":"migrate","flywayVersion":"` + testFlywayVersion + `","success":false,"migrations":[],"error":{"errorCode":"FAILED_VERSIONED_MIGRATION","message":"ORIGINAL_SQL_FAILURE","path":"V1__fail.sql","version":"1"}}`)
	case "repair":
		if r.repairStarted != nil {
			close(r.repairStarted)
		}
		if r.repairContinue != nil {
			select {
			case <-r.repairContinue:
			case <-ctx.Done():
				return flyway.Result{ExitCode: -1, TimedOut: true}
			}
		}
		if r.repairFails {
			result.Success, result.ExitCode = false, 1
			result.JSON = json.RawMessage(`{"operation":"repair","flywayVersion":"` + testFlywayVersion + `","success":false,"error":{"errorCode":"REPAIR_FAILED","message":"repair failed"}}`)
		} else {
			r.failed = false
			result.JSON = json.RawMessage(`{"operation":"repair","flywayVersion":"` + testFlywayVersion + `","migrationsRemoved":[{"version":"1","description":"fail"}],"migrationsAligned":[],"migrationsDeleted":[]}`)
		}
	default:
		result.JSON = json.RawMessage(`{}`)
	}
	return result
}

func oracleRepairHTTPFixture(t *testing.T, runner *oracleRepairHTTPRunner) (*apiFixture, executionResources) {
	t.Helper()
	f := apiSetup(t)
	if err := f.service.StartExecution(context.Background(), runner, testFlywayVersion, t.TempDir(), artifact.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.service.StopExecution)
	return f, createExecutionResources(t, f)
}

func TestOracleRepairReportingPreservesFailedMigrationEvidence(t *testing.T) {
	runner := &oracleRepairHTTPRunner{repairStarted: make(chan struct{}), repairContinue: make(chan struct{})}
	f, resources := oracleRepairHTTPFixture(t, runner)
	created := decodeData[core.Artifact](t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"V1__fail.sql": []byte("CREATE TABLE partial_fixture(id NUMBER); invalid SQL;")}, f.cookie, nil), http.StatusCreated)
	input := core.RunInput{TargetID: resources.target.ID, SchemaID: resources.schema.ID, ExecutionAccountID: resources.account.ID, ArtifactID: created.ID}
	run := decodeData[core.Run](t, f.call(http.MethodPost, "/migration-runs", input, f.cookie, map[string]string{"Idempotency-Key": "oracle-repair-api-reporting"}), http.StatusAccepted)
	select {
	case <-runner.repairStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("eligible failed migration did not reach automatic Repair")
	}
	observed := decodeData[core.Run](t, f.call(http.MethodGet, "/migration-runs/"+run.ID, nil, f.cookie, nil), http.StatusOK)
	if observed.Status != "REPAIRING" || observed.MigrationOutcome != "FAILED" || observed.Failure == nil {
		t.Fatalf("original failure was not published before Repair completed: %+v", observed)
	}
	failure := observed.Failure
	if failure.Script != "V1__fail.sql" || failure.Version != "1" || failure.ErrorCode != "FAILED_VERSIONED_MIGRATION" || failure.Message != "ORIGINAL_SQL_FAILURE" || failure.ExitCode != 1 || failure.ExecutionID == "" {
		t.Fatalf("API failure identity/evidence incomplete: %+v", failure)
	}
	close(runner.repairContinue)
	settled := waitHTTPRun(t, f, run.ID)
	if settled.Status != "FAILED" || settled.MigrationOutcome != "FAILED" || settled.RepairOutcome != "SUCCEEDED" || settled.Reason == "" {
		t.Fatalf("Repair changed or hid the migration outcome: %+v", settled)
	}
	if settled.Failure == nil || *settled.Failure != *failure || settled.ExitCode == nil || *settled.ExitCode != 1 {
		t.Fatalf("original failure details changed after Repair: before=%+v after=%+v", failure, settled)
	}
	repair := decodeData[core.Repair](t, f.call(http.MethodGet, "/migration-runs/"+run.ID+"/repair", nil, f.cookie, nil), http.StatusOK)
	if repair.Outcome != "SUCCEEDED" || repair.ExecutionID == "" || len(repair.Before) == 0 || len(repair.After) == 0 || repair.Reason == "" {
		t.Fatalf("independent repair record missing reason/history/evidence: %+v", repair)
	}
	record := f.call(http.MethodGet, "/migration-runs/"+run.ID+"/record", nil, f.cookie, nil)
	assertStatus(t, record, http.StatusOK)
	var envelope struct {
		Data struct {
			Run        core.Run            `json:"run"`
			Executions []core.RunExecution `json:"executions"`
			Repair     core.Repair         `json:"repair"`
		} `json:"data"`
	}
	if err := json.Unmarshal(record.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Run.Failure == nil || envelope.Data.Run.Failure.Message != "ORIGINAL_SQL_FAILURE" || envelope.Data.Repair.Outcome != "SUCCEEDED" {
		t.Fatalf("record did not retain independent outcomes: %+v", envelope.Data)
	}
	foundNativeMigrate := false
	for _, execution := range envelope.Data.Executions {
		if execution.Command == "migrate" {
			foundNativeMigrate = !execution.Success && execution.ExitCode == 1 && execution.JSON != nil && string(execution.JSON) == string(runnerMigrateEvidence())
		}
	}
	if !foundNativeMigrate {
		t.Fatal("run record lost the original native migrate evidence")
	}
}

func runnerMigrateEvidence() json.RawMessage {
	return json.RawMessage(`{"operation":"migrate","flywayVersion":"` + testFlywayVersion + `","success":false,"migrations":[],"error":{"errorCode":"FAILED_VERSIONED_MIGRATION","message":"ORIGINAL_SQL_FAILURE","path":"V1__fail.sql","version":"1"}}`)
}

func waitHTTPRun(t *testing.T, f *apiFixture, id string) core.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run := decodeData[core.Run](t, f.call(http.MethodGet, "/migration-runs/"+id, nil, f.cookie, nil), http.StatusOK)
		if (run.Status == "FAILED" || run.Status == "NEEDS_REVIEW") && run.RepairOutcome != "PENDING" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run did not settle after automatic Repair: %s", id)
	return core.Run{}
}
