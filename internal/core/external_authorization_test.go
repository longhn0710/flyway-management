package core

import (
	"testing"
	"time"
)

func externalService(t *testing.T, f *fixture, d DeploymentTarget, scopes []TokenDeploymentScope) (Actor, IssuedToken) {
	t.Helper()
	service, e := f.s.SaveService(testCtx, f.trace(), "", ServiceInput{Name: "ci-" + id(), OwnerID: f.admin.ID, Purpose: "isolated API test"})
	must(t, e)
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", service.ID, []Grant{{TargetID: d.TargetID, SchemaID: d.SchemaID, Read: true, Operate: true}}))
	must(t, f.s.SetDeploymentGrant(testCtx, f.trace(), d.ProjectID, d.ID, DeploymentGrantInput{PrincipalKind: "service", PrincipalID: service.ID, Read: true, Upload: true, Preflight: true, Execute: true}))
	token, e := f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "scoped", ExpiresAt: stamp(now() + 3600), DeploymentScopes: scopes})
	must(t, e)
	actor, e := f.s.AuthenticateToken(testCtx, token.Token, "test-service")
	must(t, e)
	return actor, token
}
func TestExternalAuthorizationIntersectionsAndRotation(t *testing.T) {
	f, _, d := externalFixture(t)
	scope := TokenDeploymentScope{DeploymentTargetID: d.ID, Read: true, Upload: true, Preflight: true, Execute: true}
	a, token := externalService(t, f, d, []TokenDeploymentScope{scope})
	if project, e := f.s.Project(testCtx, a, d.ProjectID); e != nil || project.ID != d.ProjectID {
		t.Fatalf("deployment member cannot see browser project: %+v %v", project, e)
	}
	if _, e := f.s.DeploymentTarget(testCtx, a, d.ProjectID, d.ID, "execute"); e != nil {
		t.Fatal(e)
	}
	// The same service's unscoped legacy token cannot inherit v2 permissions.
	legacy, e := f.s.IssueToken(testCtx, f.trace(), a.ID, "", TokenInput{Name: "legacy", ExpiresAt: stamp(now() + 3600)})
	must(t, e)
	legacyActor, e := f.s.AuthenticateToken(testCtx, legacy.Token, "legacy")
	must(t, e)
	_, e = f.s.DeploymentTarget(testCtx, legacyActor, d.ProjectID, d.ID, "read")
	status(t, e, 404)
	rotated, e := f.s.IssueToken(testCtx, f.trace(), a.ID, token.Metadata.ID, TokenInput{ExpiresAt: stamp(now() + 3600)})
	must(t, e)
	a, e = f.s.AuthenticateToken(testCtx, rotated.Token, "rotated")
	must(t, e)
	_, e = f.s.DeploymentTarget(testCtx, a, d.ProjectID, d.ID, "execute")
	must(t, e)
	readOnly, e := f.s.IssueToken(testCtx, f.trace(), a.ID, "", TokenInput{Name: "read", ExpiresAt: stamp(now() + 3600), DeploymentScopes: []TokenDeploymentScope{{DeploymentTargetID: d.ID, Read: true}}})
	must(t, e)
	reader, e := f.s.AuthenticateToken(testCtx, readOnly.Token, "reader")
	must(t, e)
	_, e = f.s.DeploymentTarget(testCtx, reader, d.ProjectID, d.ID, "upload")
	status(t, e, 403)
	// Existing database permissions remain a separate intersection.
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", a.ID, []Grant{{TargetID: d.TargetID, SchemaID: d.SchemaID, Read: true}}))
	_, e = f.s.DeploymentTarget(testCtx, a, d.ProjectID, d.ID, "execute")
	status(t, e, 403)
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", a.ID, []Grant{{TargetID: d.TargetID, SchemaID: d.SchemaID, Read: true, Operate: true}}))
	// Removing project membership hides resources even while target grants survive.
	_, e = f.db.SQL.Exec(`DELETE FROM project_identity_memberships WHERE project_id=? AND service_id=?`, d.ProjectID, a.ID)
	must(t, e)
	_, e = f.s.DeploymentTarget(testCtx, a, d.ProjectID, d.ID, "read")
	status(t, e, 404)
	must(t, f.s.SetDeploymentGrant(testCtx, f.trace(), d.ProjectID, d.ID, DeploymentGrantInput{PrincipalKind: "service", PrincipalID: a.ID, Read: true, Upload: true, Preflight: true, Execute: true}))
	must(t, f.s.RevokeToken(testCtx, f.trace(), a.ID, rotated.Metadata.ID))
	_, e = f.s.DeploymentTarget(testCtx, a, d.ProjectID, d.ID, "read")
	status(t, e, 401)
}
func TestExternalProductionRequiresExplicitAuthority(t *testing.T) {
	f, _, d := externalFixture(t)
	target := f.target(t, "production", "PROD")
	schema := f.schema(t, target.ID, "PRODAPP")
	account, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "prod", Username: "DEPLOY", SchemaIDs: []string{schema.ID}, Password: "test-private-password"})
	must(t, e)
	prod, e := f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, "", DeploymentTargetInput{Name: "prod", Environment: "PROD", TargetID: target.ID, SchemaID: schema.ID, ExecutionAccountID: account.ID, Active: true})
	must(t, e)
	a, _ := externalService(t, f, prod, []TokenDeploymentScope{{DeploymentTargetID: prod.ID, Read: true, Upload: true, Preflight: true, Execute: true}})
	_, e = f.s.DeploymentTarget(testCtx, a, prod.ProjectID, prod.ID, "execute")
	status(t, e, 403)
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", a.ID, []Grant{{TargetID: prod.TargetID, SchemaID: prod.SchemaID, Read: true, Operate: true, Prod: true}}))
	_, e = f.s.DeploymentTarget(testCtx, a, prod.ProjectID, prod.ID, "execute")
	must(t, e)
}
func TestExternalRevocationBeforeRepairHoldsFailure(t *testing.T) {
	f, engine, d := externalFixture(t)
	scope := TokenDeploymentScope{DeploymentTargetID: d.ID, Read: true, Upload: true, Preflight: true, Execute: true}
	a, _ := externalService(t, f, d, []TokenDeploymentScope{scope})
	aid := externalUpload(t, f, d, "V1__fail.sql")
	tr := Trace{Actor: a, RequestID: "revoked-before-repair"}
	in := externalAssess(t, f, tr, d, aid)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 1)
	out, e := f.s.SubmitExternalRun(testCtx, tr, d.ProjectID, d.ID, in, "revoke")
	must(t, e)
	select {
	case <-engine.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	must(t, f.s.SetDeploymentGrant(testCtx, f.trace(), d.ProjectID, d.ID, DeploymentGrantInput{PrincipalKind: "service", PrincipalID: a.ID, Read: true, Upload: true, Preflight: true, Execute: false}))
	close(gate)
	rid := out["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, e := f.s.Run(testCtx, f.admin, rid)
		must(t, e)
		if run.Completed {
			if run.Status != "NEEDS_REVIEW" || run.MigrationOutcome != "FAILED" || run.RepairOutcome != "UNSAFE" {
				t.Fatalf("revoked repair %+v", run)
			}
			for _, cmd := range engine.calls {
				if cmd == d.SchemaName+":repair" {
					t.Fatal("repair ran after revocation")
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not settle")
}
