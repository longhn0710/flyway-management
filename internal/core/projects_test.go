package core

import (
	"testing"
)

func TestProjectRunMembershipDeduplicatesAndPaginatesInRunOrder(t *testing.T) {
	scope := map[string]map[string]bool{"db": {"": true, "schema": true}}
	runs := []Run{{ID: "new", RunInput: RunInput{TargetID: "db", SchemaID: "schema"}, CreatedAt: "same"}, {ID: "older", RunInput: RunInput{TargetID: "db", SchemaID: "other"}, CreatedAt: "same"}, {ID: "outside", RunInput: RunInput{TargetID: "else", SchemaID: "schema"}}}
	filtered := appendProjectRuns(nil, runs, scope, "", "", map[string]bool{})
	filtered = appendProjectRuns(filtered, runs[:1], scope, "", "", map[string]bool{"new": true})
	// A schema member overlaps the instance member; deduplication keeps the first
	// occurrence and the database's preexisting order (including equal timestamps).
	if len(filtered) != 2 {
		t.Fatalf("overlapping members duplicated history: %+v", filtered)
	}
	page := paginateProjectRuns(filtered, 1, 1)
	if len(page) != 1 || page[0].ID != "older" {
		t.Fatalf("stable paginated history = %+v", page)
	}
	if got := projectRunMatches(runs[0], scope, "other", ""); got {
		t.Fatal("target filter ignored")
	}
}

func TestProjectsUseCurrentAuthorizedMembershipAndAllowSharedInstances(t *testing.T) {
	f := newFixture(t)
	dev := f.target(t, "projects-dev", "DEV")
	prod := f.target(t, "projects-prod", "PROD")
	a := f.schema(t, dev.ID, "APP_A")
	b := f.schema(t, dev.ID, "APP_B")
	c := f.schema(t, prod.ID, "APP_A")
	_, viewer, _ := f.user(t, "project-viewer", "Viewer")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", viewer.ID, []Grant{{TargetID: dev.ID, SchemaID: a.ID, Read: true}}))
	pa, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "A", Members: []ProjectMember{{TargetID: dev.ID, SchemaID: a.ID}, {TargetID: prod.ID, SchemaID: c.ID}}})
	must(t, e)
	_, e = f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "B", Members: []ProjectMember{{TargetID: dev.ID, SchemaID: b.ID}}})
	must(t, e)
	visible, e := f.s.Projects(testCtx, viewer, "")
	must(t, e)
	if len(visible) != 1 || visible[0].ID != pa.ID || len(visible[0].Members) != 1 || visible[0].Members[0].SchemaID != a.ID {
		t.Fatalf("visible project leaked member: %+v", visible)
	}
	if _, e = f.s.Project(testCtx, viewer, visible[0].ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Project(testCtx, viewer, "non-visible"); e == nil {
		t.Fatal("expected unavailable")
	}
	archived, e := f.s.SaveProject(testCtx, f.trace(), pa.ID, ProjectInput{Name: "A", Status: "archived", Members: []ProjectMember{{TargetID: dev.ID, SchemaID: a.ID}}})
	must(t, e)
	if archived.Status != "archived" {
		t.Fatal(archived)
	}
	// Metadata edits and unarchive are allowed when membership remains identical.
	archived, e = f.s.SaveProject(testCtx, f.trace(), pa.ID, ProjectInput{Name: "A renamed", Description: "edited while archived", Status: "archived", Members: []ProjectMember{{TargetID: dev.ID, SchemaID: a.ID}}})
	must(t, e)
	if _, e = f.s.SaveProject(testCtx, f.trace(), pa.ID, ProjectInput{Name: "A renamed", Status: "archived", Members: []ProjectMember{}}); e == nil {
		t.Fatal("archived membership changed")
	}
	_, e = f.s.SaveProject(testCtx, f.trace(), pa.ID, ProjectInput{Name: "A renamed", Status: "active", Members: []ProjectMember{{TargetID: dev.ID, SchemaID: a.ID}}})
	must(t, e)
	if _, e = f.s.SaveProject(testCtx, Trace{Actor: viewer}, "", ProjectInput{Name: "no"}); e == nil {
		t.Fatal("viewer created a project")
	}
}

func TestProjectHistoryUsesCurrentMembersAndGloballyPaginatesRuns(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	other := f.schema(t, target.ID, "OTHER")
	account2, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "other deployer", Username: "OTHER_DEPLOY", SchemaIDs: []string{other.ID}, Password: "other-password"})
	must(t, e)
	in1 := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	r1, e := f.s.SubmitRun(testCtx, f.trace(), in1, "project-one")
	must(t, e)
	in2 := uploadRun(t, f, target, other, account2, "V1__two.sql", "SELECT 2 FROM dual;")
	r2, e := f.s.SubmitRun(testCtx, f.trace(), in2, "project-two")
	must(t, e)
	if waitRun(t, f, r1.ID).Status != "SUCCEEDED" || waitRun(t, f, r2.ID).Status != "SUCCEEDED" {
		t.Fatal("fixture executions failed")
	}
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "history", Members: []ProjectMember{{TargetID: target.ID}, {TargetID: target.ID, SchemaID: schema.ID}}})
	must(t, e)
	first, e := f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{Page: Page{Limit: 1}})
	must(t, e)
	second, e := f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{Page: Page{Limit: 1, Offset: 1}})
	must(t, e)
	if len(first) != 1 || len(second) != 1 || first[0].ID == second[0].ID || first[0].ID != r2.ID || second[0].ID != r1.ID {
		t.Fatalf("global stable pages: first=%v second=%v expected newest %s then %s", first, second, r2.ID, r1.ID)
	}
	p, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: "history", Members: []ProjectMember{{TargetID: target.ID, SchemaID: other.ID}}})
	must(t, e)
	history, e := f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{})
	must(t, e)
	if len(history) != 1 || history[0].ID != r2.ID {
		t.Fatalf("history did not follow current membership: %+v", history)
	}
}

func TestProjectRejectsMismatchedSchemaAndProjectHistoryUsesCurrentMembership(t *testing.T) {
	f := newFixture(t)
	t1 := f.target(t, "history-dev", "DEV")
	t2 := f.target(t, "history-sit", "UAT")
	s1 := f.schema(t, t1.ID, "APP")
	s2 := f.schema(t, t2.ID, "APP")
	if _, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "bad", Members: []ProjectMember{{TargetID: t1.ID, SchemaID: s2.ID}}}); e == nil {
		t.Fatal("accepted schema from another instance")
	}
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "multi", Members: []ProjectMember{{TargetID: t1.ID, SchemaID: s1.ID}, {TargetID: t2.ID, SchemaID: s2.ID}}})
	must(t, e)
	runs, e := f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{})
	must(t, e)
	if len(runs) != 0 {
		t.Fatalf("unexpected historical runs: %v", runs)
	}
	_, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: "multi", Status: "active", Members: nil})
	must(t, e)
	runs, e = f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{})
	must(t, e)
	if len(runs) != 0 {
		t.Fatalf("empty current membership returned runs: %v", runs)
	}
}
