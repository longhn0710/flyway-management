package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

type postgresqlJournalRecoveryRunner struct {
	*postgresCoreRunner
	fault          flyway.Result
	blockInfo      atomic.Bool
	infoStarted    chan struct{}
	releaseInfo    <-chan struct{}
	infoOnce       sync.Once
	journalReads   atomic.Int32
	blockJournal   atomic.Bool
	journalStarted chan struct{}
	releaseJournal <-chan struct{}
	journalOnce    sync.Once
	migrateStarted chan struct{}
	migrateOnce    sync.Once
	cancelJournal  context.CancelFunc
}

func (r *postgresqlJournalRecoveryRunner) Execute(ctx context.Context, req flyway.Request) flyway.Result {
	if req.Command == "migrate" {
		if r.migrateStarted != nil {
			r.migrateOnce.Do(func() { close(r.migrateStarted) })
		}
		return r.fault
	}
	if req.Command == "info" && req.Host != "pg-queued.example" && r.blockInfo.Load() {
		r.infoOnce.Do(func() { close(r.infoStarted) })
		select {
		case <-r.releaseInfo:
		case <-ctx.Done():
			return flyway.Result{}
		}
	}
	return r.postgresCoreRunner.Execute(ctx, req)
}

func (r *postgresqlJournalRecoveryRunner) ReadPostgreSQLLifecycle(tag string) (json.RawMessage, error) {
	r.journalReads.Add(1)
	if r.blockJournal.Load() {
		r.journalOnce.Do(func() { close(r.journalStarted) })
		<-r.releaseJournal
	}
	if r.cancelJournal != nil {
		r.cancelJournal()
		r.cancelJournal = nil
	}
	return json.RawMessage(fmt.Sprintf(`{"application_name":%q,"events":[{"event":"finished","application_name":%q,"outcome":"cancelled_unknown","gone":false,"cleanup_verified":false}]}`, tag, tag)), nil
}

func TestPostgreSQLRecoveryImportsAllHeldJournalsBeforeInspection(t *testing.T) {
	if !postgresqlMigrationAccepted {
		t.Skip("runs against the isolated PostgreSQL migration candidate")
	}
	f, baseRunner, firstConnection := postgresCoreFixture(t, "DEV")
	fault := flyway.Result{
		ExitCode: 1,
		JSON:     json.RawMessage(`{"operation":"migrate","error":{"message":"permission denied"}}`),
	}
	f.s.execution.runner = &postgresqlFaultRunner{baseRunner, fault}

	secondConnection, err := f.s.SaveConnection(testCtx, f.trace(), "", ConnectionInput{
		Target: TargetInput{
			Engine: "postgresql", Name: "PG second recovery target", Environment: "DEV",
			Host: "pg-second.example", Port: 5432, DatabaseName: "app_db", Transport: "TCPS",
			Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 2)},
		},
		Username: "app_role", Password: "test-postgres-password", Schemas: []string{"app"}, GrantCurrentUser: true,
	})
	must(t, err)
	if secondConnection.Target.ID == firstConnection.Target.ID || secondConnection.Schemas[0].ID == firstConnection.Schemas[0].ID {
		t.Fatal("recovery fixtures must use distinct targets and streams")
	}

	submitFailure := func(connection Connection, key string) Run {
		t.Helper()
		artifactRecord, uploadErr := f.s.UploadArtifact(testCtx, f.trace(), connection.Target.ID, connection.Schemas[0].ID, []artifact.Input{{Name: "V1__held.sql", Bytes: []byte("CREATE TABLE held(id int);")}})
		must(t, uploadErr)
		input := RunInput{TargetID: connection.Target.ID, SchemaID: connection.Schemas[0].ID, ExecutionAccountID: connection.Account.ID, ArtifactID: artifactRecord.ID}
		run, submitErr := f.s.SubmitRun(testCtx, f.trace(), input, key)
		must(t, submitErr)
		final := waitRun(t, f, run.ID)
		if final.Status != "NEEDS_REVIEW" || final.RepairOutcome != "SKIPPED" {
			t.Fatalf("failed fixture run must remain held without Repair: %+v", final)
		}
		var held bool
		must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", input.TargetID, input.SchemaID).Scan(&held))
		if !held {
			t.Fatal("failed fixture stream was released")
		}
		return final
	}
	firstRun := submitFailure(firstConnection, "journal-recovery-first")
	secondRun := submitFailure(secondConnection, "journal-recovery-second")

	var firstMigrations, secondMigrations, repairs int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id=? AND command='migrate'", firstRun.ID).Scan(&firstMigrations))
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id=? AND command='migrate'", secondRun.ID).Scan(&secondMigrations))
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id IN (?,?) AND command='repair'", firstRun.ID, secondRun.ID).Scan(&repairs))
	if firstMigrations != 1 || secondMigrations != 1 || repairs != 0 {
		t.Fatal("fixture failures replayed SQL or attempted Repair", firstMigrations, secondMigrations, repairs)
	}

	f.s.StopExecution()
	// Queue independent work while the old dispatcher is stopped. Startup must
	// finish importing held-run journals before its dispatcher can claim this run.
	thirdConnection, err := f.s.SaveConnection(testCtx, f.trace(), "", ConnectionInput{
		Target: TargetInput{
			Engine: "postgresql", Name: "PG queued recovery target", Environment: "DEV",
			Host: "pg-queued.example", Port: 5432, DatabaseName: "app_db", Transport: "TCPS",
			Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 3)},
		},
		Username: "app_role", Password: "test-postgres-password", Schemas: []string{"app"}, GrantCurrentUser: true,
	})
	must(t, err)
	queuedArtifact, err := f.s.UploadArtifact(testCtx, f.trace(), thirdConnection.Target.ID, thirdConnection.Schemas[0].ID, []artifact.Input{{Name: "V1__queued.sql", Bytes: []byte("CREATE TABLE queued(id int);")}})
	must(t, err)
	queuedRun, err := f.s.SubmitRun(testCtx, f.trace(), RunInput{TargetID: thirdConnection.Target.ID, SchemaID: thirdConnection.Schemas[0].ID, ExecutionAccountID: thirdConnection.Account.ID, ArtifactID: queuedArtifact.ID}, "journal-recovery-queued")
	must(t, err)
	if queuedRun.Status != "QUEUED" {
		t.Fatalf("work queued while dispatcher was stopped has unexpected status %s", queuedRun.Status)
	}
	failedStart, err := New(testCtx, f.db, f.dir, "")
	must(t, err)
	startupCtx, cancelStartup := context.WithCancel(testCtx)
	failingReader := &postgresqlJournalRecoveryRunner{postgresCoreRunner: baseRunner, cancelJournal: cancelStartup}
	if err = failedStart.StartExecution(startupCtx, failingReader, "postgres-profile-test-pin", f.dir, artifact.DefaultLimits()); err == nil {
		failedStart.StopExecution()
		t.Fatal("startup succeeded after journal import persistence context was cancelled")
	}
	cancelStartup()
	if failedStart.execution != nil {
		t.Fatal("failed journal import left execution enabled")
	}
	releaseFailedLock, err := executionLock(filepath.Join(f.dir, "execution.lock"))
	must(t, err)
	must(t, releaseFailedLock())
	restarted, err := New(testCtx, f.db, f.dir, "")
	must(t, err)
	f.s = restarted
	releaseInfo := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseInfo) }) }
	var restartedRunner *postgresqlJournalRecoveryRunner
	releaseJournal := make(chan struct{})
	var releaseJournalOnce sync.Once
	finishJournalImport := func() { releaseJournalOnce.Do(func() { close(releaseJournal) }) }
	t.Cleanup(func() {
		release()
		finishJournalImport()
		if restarted != nil {
			restarted.StopExecution()
		}
	})
	restartedRunner = &postgresqlJournalRecoveryRunner{
		postgresCoreRunner: baseRunner,
		fault:              fault,
		infoStarted:        make(chan struct{}),
		releaseInfo:        releaseInfo,
		journalStarted:     make(chan struct{}),
		releaseJournal:     releaseJournal,
		migrateStarted:     make(chan struct{}),
	}
	restartedRunner.blockInfo.Store(true)
	restartedRunner.blockJournal.Store(true)
	startResult := make(chan error, 1)
	go func() {
		startResult <- restarted.StartExecution(testCtx, restartedRunner, "postgres-profile-test-pin", f.dir, artifact.DefaultLimits())
	}()
	select {
	case <-restartedRunner.journalStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not begin local journal import")
	}
	select {
	case err := <-startResult:
		t.Fatalf("StartExecution returned before journal import completed: %v", err)
	case <-restartedRunner.infoStarted:
		t.Fatal("database inspection began before local journal import completed")
	case <-time.After(100 * time.Millisecond):
	}
	time.Sleep(400 * time.Millisecond)
	var queuedStatus string
	var claimed, executions int
	must(t, f.db.SQL.QueryRow(`SELECT r.status,j.claimed FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.id=?`, queuedRun.ID).Scan(&queuedStatus, &claimed))
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM run_executions WHERE run_id=?`, queuedRun.ID).Scan(&executions))
	if queuedStatus != "QUEUED" || claimed != 0 || executions != 0 {
		t.Fatalf("queued independent work was dispatched before journal import: status=%s claimed=%d executions=%d", queuedStatus, claimed, executions)
	}
	finishJournalImport()
	must(t, <-startResult)
	select {
	case <-restartedRunner.migrateStarted:
	case <-time.After(3 * time.Second):
		current, runErr := restarted.Run(testCtx, f.admin, queuedRun.ID)
		var claimed int
		_ = f.db.SQL.QueryRow(`SELECT claimed FROM jobs WHERE run_id=?`, queuedRun.ID).Scan(&claimed)
		t.Fatalf("queued independent work was not dispatched after journal import: run=%+v claimed=%d err=%v", current, claimed, runErr)
	}
	if final := waitRun(t, f, queuedRun.ID); final.Status != "NEEDS_REVIEW" {
		t.Fatalf("queued fixture migration did not finish through the fake runner: %+v", final)
	}

	select {
	case <-restartedRunner.infoStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("startup reconciliation did not reach its first database Info inspection")
	}
	for _, run := range []Run{firstRun, secondRun} {
		var journals int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_logs WHERE run_id=? AND message LIKE 'PostgreSQL recovered lifecycle journal:%'", run.ID).Scan(&journals))
		if journals == 0 {
			t.Fatalf("held PostgreSQL run %s has no imported lifecycle evidence while first Info is blocked", run.ID)
		}
		current, e := restarted.Run(testCtx, f.admin, run.ID)
		must(t, e)
		if current.Status != "NEEDS_REVIEW" {
			t.Fatalf("journal import changed held run outcome: %s", current.Status)
		}
		var held bool
		must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", run.TargetID, run.SchemaID).Scan(&held))
		if !held {
			t.Fatalf("journal import released held stream for run %s", run.ID)
		}
	}
	if restartedRunner.journalReads.Load() < 2 {
		t.Fatal("recovery did not read durable journals for both held PostgreSQL runs", restartedRunner.journalReads.Load())
	}
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id IN (?,?) AND command='migrate'", firstRun.ID, secondRun.ID).Scan(&firstMigrations))
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id IN (?,?) AND command='repair'", firstRun.ID, secondRun.ID).Scan(&repairs))
	if firstMigrations != 2 || repairs != 0 {
		t.Fatal("startup reconciliation replayed migration SQL or attempted Repair", firstMigrations, repairs)
	}
	release()
	restarted.StopExecution()
}
