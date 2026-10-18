package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
)

func externalPackage(t *testing.T, name string) artifact.Input {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, e := z.Create(name)
	must(t, e)
	_, e = w.Write([]byte("select 1 from dual;"))
	must(t, e)
	must(t, z.Close())
	return artifact.Input{Name: "sql.zip", Bytes: b.Bytes()}
}
func externalFixture(t *testing.T) (*fixture, *fakeEngine, DeploymentTarget) {
	t.Helper()
	f, engine, target, schema, account := executionFixture(t)
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "external", Status: "active"})
	must(t, e)
	d, e := f.s.SaveDeploymentTarget(testCtx, f.trace(), p.ID, "", DeploymentTargetInput{Name: "uat", TargetID: target.ID, SchemaID: schema.ID, ExecutionAccountID: account.ID, Environment: "UAT", Active: true})
	must(t, e)
	return f, engine, d
}
func externalUpload(t *testing.T, f *fixture, d DeploymentTarget, name string) string {
	t.Helper()
	input := externalPackage(t, name)
	out, e := f.s.UploadExternalArtifact(testCtx, f.trace(), d.ProjectID, d.ID, d.Revision, digest(string(input.Bytes)), "upload-"+name, ExternalSource{System: "CLI"}, input)
	must(t, e)
	return out["id"].(string)
}
func externalAssess(t *testing.T, f *fixture, tr Trace, d DeploymentTarget, aid string) ExternalRunInput {
	t.Helper()
	in := ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: d.Revision}
	p, e := f.s.ExternalPreflight(testCtx, tr, d.ProjectID, d.ID, in)
	must(t, e)
	if p["ready"] != true {
		t.Fatalf("preflight blocked: %+v", p)
	}
	in.PreflightID = p["id"].(string)
	return in
}
func TestExternalOwnershipIdentityAndLegacyBoundary(t *testing.T) {
	f, _, d := externalFixture(t)
	_, e := f.s.UploadArtifact(testCtx, f.trace(), d.TargetID, d.SchemaID, []artifact.Input{{Name: "V1__x.sql", Bytes: []byte("select 1;")}})
	status(t, e, 409)
	_, e = f.s.Preflight(testCtx, f.trace(), RunInput{TargetID: d.TargetID, SchemaID: d.SchemaID, ExecutionAccountID: d.ExecutionAccountID})
	status(t, e, 409)
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "other", Status: "active"})
	must(t, e)
	_, e = f.s.SaveDeploymentTarget(testCtx, f.trace(), p.ID, "", d.DeploymentTargetInput)
	status(t, e, 409)
	next := d.DeploymentTargetInput
	next.Name = "second-same-environment"
	_, e = f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, "", next)
	must(t, e)
	next = d.DeploymentTargetInput
	next.ExecutionAccountID = "wrong"
	_, e = f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, d.ID, next)
	status(t, e, 409)
	next = d.DeploymentTargetInput
	next.Active = false
	updated, e := f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, d.ID, next)
	must(t, e)
	if updated.Active || updated.Revision != 2 {
		t.Fatal(updated)
	}
	_, e = f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, d.ID, next)
	status(t, e, 409)
}
func TestExternalConcurrentUploadSubmitAndFrozenRecord(t *testing.T) {
	f, engine, d := externalFixture(t)
	input := externalPackage(t, "V1__ok.sql")
	var wg sync.WaitGroup
	ids := make(chan string, 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := f.s.UploadExternalArtifact(testCtx, f.trace(), d.ProjectID, d.ID, d.Revision, digest(string(input.Bytes)), "same", ExternalSource{}, input)
			if e != nil {
				errs <- e
				return
			}
			ids <- out["id"].(string)
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		must(t, e)
	}
	aid := ""
	for v := range ids {
		if aid != "" && aid != v {
			t.Fatal("duplicate artifacts")
		}
		aid = v
	}
	var n int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM artifacts`).Scan(&n))
	if n != 1 {
		t.Fatalf("artifacts=%d", n)
	}
	in := externalAssess(t, f, f.trace(), d, aid)
	runids := make(chan string, 6)
	errs = make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "same-run")
			if e != nil {
				errs <- e
				return
			}
			runids <- out["id"].(string)
		}()
	}
	wg.Wait()
	close(errs)
	close(runids)
	for e := range errs {
		must(t, e)
	}
	rid := ""
	for v := range runids {
		if rid != "" && rid != v {
			t.Fatal("duplicate runs")
		}
		rid = v
	}
	run := waitRun(t, f, rid)
	if run.Status != "SUCCEEDED" || !run.Completed || !run.RecordAvailable {
		t.Fatalf("run %+v", run)
	}
	record, e := f.s.ExternalRecord(testCtx, f.admin, d.ProjectID, rid)
	must(t, e)
	if !json.Valid(record) || !bytes.Contains(record, []byte(`"audit"`)) || bytes.Contains(record, []byte("oracle-password-never-returned")) {
		t.Fatal("invalid record")
	}
	must(t, f.s.Event(testCtx, f.trace(), "later.inspection", rid, "inspected"))
	after, e := f.s.ExternalRecord(testCtx, f.admin, d.ProjectID, rid)
	must(t, e)
	if !bytes.Equal(record, after) {
		t.Fatal("record mutated")
	}
	if engine.migrateCount(d.SchemaName) != 1 {
		t.Fatalf("migrations=%d", engine.migrateCount(d.SchemaName))
	}
	in.ExternalSource.System = "changed"
	_, e = f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "same-run")
	status(t, e, 409)
}
func TestExternalPreflightBindingExpiryAndAuditAtomicity(t *testing.T) {
	f, _, d := externalFixture(t)
	aid := externalUpload(t, f, d, "V1__ok.sql")
	in := externalAssess(t, f, f.trace(), d, aid)
	_, e := f.db.SQL.ExecContext(testCtx, `CREATE TRIGGER reject_project_audit BEFORE INSERT ON audit_events WHEN NEW.action='migration.submit' BEGIN SELECT RAISE(ABORT,'test audit failure'); END;`)
	must(t, e)
	_, e = f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "rollback")
	if e == nil {
		t.Fatal("accepted without durable audit")
	}
	var n int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM migration_runs`).Scan(&n))
	if n != 0 {
		t.Fatal("partial run")
	}
	_, e = f.db.SQL.Exec(`DROP TRIGGER reject_project_audit`)
	must(t, e)
	// Expiry is immutable in storage; simulate time through an already-expired assessment row.
	var fingerprint, payload string
	must(t, f.db.SQL.QueryRow(`SELECT fingerprint,assessment FROM migration_preflights WHERE id=?`, in.PreflightID).Scan(&fingerprint, &payload))
	_, e = f.db.SQL.Exec(`INSERT INTO migration_preflights VALUES('expired',?,?,?,?,?,?,?,?,?,?,?)`, d.ProjectID, d.ID, aid, d.Revision, f.admin.Kind, f.admin.ID, fingerprint, true, now()-700, now()-100, payload)
	must(t, e)
	in.PreflightID = "expired"
	_, e = f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "expired")
	status(t, e, 409)
	in = externalAssess(t, f, f.trace(), d, aid)
	updated := d.DeploymentTargetInput
	updated.Name = "renamed"
	_, e = f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, d.ID, updated)
	must(t, e)
	_, e = f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "stale")
	status(t, e, 409)
}
func TestExternalFailedMigrationRepairRemainsIndependent(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "safe", true: "unsafe"}[unsafe], func(t *testing.T) {
			f, engine, d := externalFixture(t)
			engine.unsafe = unsafe
			aid := externalUpload(t, f, d, "V1__fail.sql")
			in := externalAssess(t, f, f.trace(), d, aid)
			out, e := f.s.SubmitExternalRun(testCtx, f.trace(), d.ProjectID, d.ID, in, "failure")
			must(t, e)
			rid := out["id"].(string)
			deadline := time.Now().Add(10 * time.Second)
			var run Run
			for time.Now().Before(deadline) {
				run, e = f.s.Run(testCtx, f.admin, rid)
				must(t, e)
				if run.Completed {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !run.Completed || !run.RecordAvailable || run.MigrationOutcome != "FAILED" || run.Failure == nil {
				t.Fatalf("failure %+v", run)
			}
			if unsafe {
				if run.Status != "NEEDS_REVIEW" || run.RepairOutcome != "UNSAFE" {
					t.Fatal(run)
				}
			} else if run.Status != "FAILED" || run.RepairOutcome != "SUCCEEDED" {
				t.Fatal(run)
			}
			record, e := f.s.ExternalRecord(testCtx, f.admin, d.ProjectID, rid)
			must(t, e)
			if !strings.Contains(string(record), `"migration_outcome":"FAILED"`) {
				t.Fatal(string(record))
			}
			if engine.migrateCount(d.SchemaName) != 1 {
				t.Fatal("migration replayed")
			}
		})
	}
}

func TestExternalPreflightPersistsTruthfulRuntimeBlocker(t *testing.T) {
	f, _, d := externalFixture(t)
	aid := externalUpload(t, f, d, "V1__pending.sql")
	f.s.StopExecution()
	f.s.execution = nil
	assessment, e := f.s.ExternalPreflight(testCtx, f.trace(), d.ProjectID, d.ID, ExternalRunInput{ArtifactID: aid, DeploymentTargetRevision: d.Revision})
	must(t, e)
	if assessment["ready"] != false || assessment["live_status"] != "NOT_VERIFIED" {
		t.Fatalf("unverified runtime reported ready: %+v", assessment)
	}
	stored, e := f.s.GetExternalPreflight(testCtx, f.admin, d.ProjectID, assessment["id"].(string))
	must(t, e)
	encoded, e := json.Marshal(stored)
	must(t, e)
	if !bytes.Contains(encoded, []byte("RUNTIME_UNAVAILABLE")) {
		t.Fatalf("runtime blocker lost: %s", encoded)
	}
}
