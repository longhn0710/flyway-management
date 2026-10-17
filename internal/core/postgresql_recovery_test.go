package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

type postgresqlFaultRunner struct {
	*postgresCoreRunner
	fault flyway.Result
}

type postgresqlUnsafeHistoryRunner struct {
	*postgresCoreRunner
	state string
}

func (r *postgresqlUnsafeHistoryRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	result := r.postgresCoreRunner.Execute(ctx, req)
	if req.Command == "info" {
		var body map[string]any
		_ = json.Unmarshal(result.JSON, &body)
		for _, entry := range body["migrations"].([]any) {
			entry.(map[string]any)["state"] = r.state
		}
		result.JSON, _ = json.Marshal(body)
	}
	return result
}

func (r *postgresqlFaultRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	if req.Command == "migrate" {
		return r.fault
	}
	return r.postgresCoreRunner.Execute(ctx, req)
}

// Fault envelopes supplement real PostgreSQL acceptance. They do not claim
// database observations and never change the shipping engine policy.
func TestPostgreSQLCandidateFailureEnvelopes(t *testing.T) {
	if !postgresqlMigrationAccepted {
		t.Skip("runs against the isolated migration candidate")
	}
	for name, fault := range map[string]flyway.Result{
		"timeout":                    {TimedOut: true, Ambiguous: true, ExitCode: -1},
		"terminated":                 {Ambiguous: true, ExitCode: 143},
		"truncated":                  {Ambiguous: true, ExitCode: 0, Stdout: `{"success":`},
		"semantic-failure-exit-zero": {ExitCode: 0, JSON: json.RawMessage(`{"operation":"migrate","success":false}`)},
		"permission-error":           {ExitCode: 1, JSON: json.RawMessage(`{"operation":"migrate","error":{"message":"permission denied"}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			f, runner, saved := postgresCoreFixture(t, "DEV")
			f.s.execution.runner = &postgresqlFaultRunner{runner, fault}
			a, e := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__safe.sql", Bytes: []byte("CREATE TABLE safe(id int);")}})
			must(t, e)
			input := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: a.ID}
			r, e := f.s.SubmitRun(testCtx, f.trace(), input, "fault-"+name)
			must(t, e)
			final := waitRun(t, f, r.ID)
			if final.Status != "NEEDS_REVIEW" || final.RepairOutcome != "SKIPPED" {
				t.Fatalf("fault was not held without repair: %+v", final)
			}
			indeterminate := fault.Ambiguous || fault.TimedOut || len(fault.JSON) == 0
			if strings.Contains(final.Reason, "indeterminate") != indeterminate {
				t.Fatal("status did not distinguish missing/ambiguous evidence from a native failure", final.Reason)
			}
			var held bool
			must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", input.TargetID, input.SchemaID).Scan(&held))
			if !held {
				t.Fatal("fault released stream")
			}
			var repairs, migrations int
			must(t, f.db.SQL.QueryRow("SELECT count(*) FROM repair_operations").Scan(&repairs))
			must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE command='migrate'").Scan(&migrations))
			if repairs != 0 || migrations != 1 {
				t.Fatal("repair or replay introduced", repairs, migrations)
			}
			_, e = f.s.SubmitRun(testCtx, f.trace(), input, "fault-retry")
			status(t, e, 409)
		})
	}
}

func TestPostgreSQLCandidateUnsafeHistoryCannotMigrate(t *testing.T) {
	if !postgresqlMigrationAccepted {
		t.Skip("runs against isolated migration candidate")
	}
	for _, state := range []string{"Failed", "Missing", "Future", "UnknownNativeState"} {
		t.Run(state, func(t *testing.T) {
			f, runner, saved := postgresCoreFixture(t, "DEV")
			f.s.execution.runner = &postgresqlUnsafeHistoryRunner{runner, state}
			a, e := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__safe.sql", Bytes: []byte("CREATE TABLE safe(id int);")}})
			must(t, e)
			input := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: a.ID}
			pre, e := f.s.Preflight(testCtx, f.trace(), input)
			must(t, e)
			if pre.Ready || len(pre.Blockers) == 0 || pre.Blockers[0].Code != "HISTORY_UNSAFE" {
				t.Fatal("unsafe history bypassed preflight", pre.Blockers)
			}
			r, e := f.s.SubmitRun(testCtx, f.trace(), input, "unsafe-history")
			must(t, e)
			if final := waitRun(t, f, r.ID); final.Status != "BLOCKED" {
				t.Fatal("unsafe history was not blocked at worker", final.Status)
			}
			var migrations int
			must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE command IN ('migrate','repair')").Scan(&migrations))
			if migrations != 0 {
				t.Fatal("unsafe native history caused mutation")
			}
		})
	}
}
