package core

import (
	"fmt"
	"testing"

	"flyway-manager/internal/artifact"
)

func TestPostgreSQLMaintenanceSQLCannotCreateJobs(t *testing.T) {
	if !postgresqlMigrationAccepted {
		t.Skip("requires accepted PostgreSQL policy")
	}
	f, _, saved := postgresCoreFixture(t, "DEV")
	for i, sql := range []string{
		"ALTER TABLE t DETACH PARTITION p CONCURRENTLY",
		"CLUSTER", "CLUSTER partitioned_table",
		"ALTER SUBSCRIPTION s REFRESH PUBLICATION",
		"ALTER SUBSCRIPTION s SET PUBLICATION p",
	} {
		a, err := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__maintenance.sql", Bytes: []byte(sql)}})
		must(t, err)
		input := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: a.ID}
		pre, err := f.s.Preflight(testCtx, f.trace(), input)
		must(t, err)
		if pre.Ready || pre.LocalReady || len(pre.Blockers) == 0 || pre.Blockers[0].Code != "UNSUPPORTED_MIGRATION_SQL" {
			t.Fatalf("unsupported maintenance preflight admitted %q: %+v", sql, pre)
		}
		_, err = f.s.SubmitRun(testCtx, f.trace(), input, fmt.Sprintf("maintenance-%d", i))
		status(t, err, 409)
	}
	var jobs, executions int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs").Scan(&jobs))
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions").Scan(&executions))
	if jobs != 0 || executions != 0 {
		t.Fatal("unsupported maintenance created a job or dispatched a command", jobs, executions)
	}
}
