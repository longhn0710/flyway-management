package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

type recoveryInspectionRunner struct {
	*postgresCoreRunner
	delay                time.Duration
	cancel               context.CancelFunc
	cancellationObserved bool
}

func (r *recoveryInspectionRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	if req.Command != "info" && req.Command != "validate" {
		panic("recovery attempted a mutating command")
	}
	if r.cancel != nil {
		r.cancel()
		r.cancellationObserved = ctx.Err() == context.Canceled
		return flyway.Result{ExitCode: -1, Ambiguous: true}
	}
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return flyway.Result{ExitCode: -1, Ambiguous: true}
	}
	result := r.postgresCoreRunner.Execute(ctx, req)
	if req.Command == "info" {
		// Model the externally observed V1 commit after an uncertain migration.
		// This fixture does not contact PostgreSQL or claim a database observation.
		var body map[string]any
		if err := json.Unmarshal(result.JSON, &body); err != nil {
			panic(err)
		}
		for _, migration := range body["migrations"].([]any) {
			migration.(map[string]any)["state"] = "Success"
		}
		result.JSON, _ = json.Marshal(body)
		result.Stdout = string(result.JSON)
	}
	return result
}

func TestRecoveredInspectionPersistsAfterSlowCommands(t *testing.T) {
	// Each command exceeds the former shared ten-second persistence deadline.
	testRecoveredInspectionPersistence(t, 11*time.Second, false)
}

func TestRecoveredInspectionPreservesProcessCancellation(t *testing.T) {
	testRecoveredInspectionPersistence(t, 0, true)
}

func testRecoveredInspectionPersistence(t *testing.T, delay time.Duration, cancelProcess bool) {
	t.Helper()
	f, base, connection := postgresCoreFixture(t, "DEV")
	f.s.execution.runner = &postgresqlFaultRunner{base, flyway.Result{ExitCode: -1, Ambiguous: true}}
	artifactRecord, err := f.s.UploadArtifact(testCtx, f.trace(), connection.Target.ID, connection.Schemas[0].ID,
		[]artifact.Input{{Name: "V1__committed.sql", Bytes: []byte("CREATE TABLE committed(id int);")}})
	must(t, err)
	input := RunInput{TargetID: connection.Target.ID, SchemaID: connection.Schemas[0].ID,
		ExecutionAccountID: connection.Account.ID, ArtifactID: artifactRecord.ID}
	run, err := f.s.SubmitRun(testCtx, f.trace(), input, "recovery-persistence")
	must(t, err)
	if final := waitRun(t, f, run.ID); final.Status != "NEEDS_REVIEW" {
		t.Fatalf("fixture did not become held: %s", final.Status)
	}
	f.s.StopExecution()

	counts := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		rows, queryErr := f.db.SQL.Query("SELECT command,count(*) FROM run_executions WHERE run_id=? GROUP BY command", run.ID)
		must(t, queryErr)
		defer rows.Close()
		for rows.Next() {
			var command string
			var count int
			must(t, rows.Scan(&command, &count))
			out[command] = count
		}
		must(t, rows.Err())
		return out
	}
	before := counts()
	ctx, cancel := context.WithCancel(testCtx)
	defer cancel()
	runner := &recoveryInspectionRunner{postgresCoreRunner: base, delay: delay}
	if cancelProcess {
		runner.cancel = cancel
	}
	f.s.execution.runner = runner
	f.s.reconcileRecovered(ctx)
	after := counts()
	if after["info"] != before["info"]+1 || after["validate"] != before["validate"]+1 {
		t.Fatalf("recovered inspection completion evidence was lost: before=%v after=%v", before, after)
	}
	if after["migrate"] != before["migrate"] || after["repair"] != before["repair"] {
		t.Fatal("reconciliation replayed or repaired SQL", before, after)
	}
	var held bool
	must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", input.TargetID, input.SchemaID).Scan(&held))
	final, err := f.s.Run(testCtx, f.admin, run.ID)
	must(t, err)
	if !held || final.Status != "NEEDS_REVIEW" {
		t.Fatal("inspection released an ambiguous outcome", held, final.Status)
	}
	var outcome string
	must(t, f.db.SQL.QueryRow("SELECT outcome FROM audit_events WHERE action='migration.reconciliation' AND target=? ORDER BY id DESC LIMIT 1", run.ID).Scan(&outcome))
	if cancelProcess {
		if !runner.cancellationObserved || outcome != "NOT_VERIFIED" {
			t.Fatal("process cancellation was detached or misreported", runner.cancellationObserved, outcome)
		}
	} else {
		if outcome != "HISTORY_INSPECTED_REVIEW_REQUIRED" {
			t.Fatal("successful inspection audit was lost", outcome)
		}
		capture, captureErr := f.s.LatestInfo(testCtx, f.admin, input.TargetID, input.SchemaID, run.ID)
		must(t, captureErr)
		var native struct {
			Migrations []struct{ State string } `json:"migrations"`
		}
		must(t, json.Unmarshal(capture.JSON, &native))
		if !capture.Success || len(native.Migrations) != 1 || native.Migrations[0].State != "Success" || capture.Stdout != string(capture.JSON) {
			t.Fatal("latest native capture remains stale or differs from original JSON", capture.Success, string(capture.JSON))
		}
	}
	_, err = f.s.SubmitRun(testCtx, f.trace(), input, "held-after-inspection")
	status(t, err, 409)
}
