package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestDeleteProjectRequiresAdminCurrentNameAndEmptyMembership(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, "delete-project", "DEV")
	schema := f.schema(t, target.ID, "APP")
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "Confirm Exactly", Members: []ProjectMember{{TargetID: target.ID, SchemaID: schema.ID}}})
	must(t, e)
	for _, actor := range []Actor{{Kind: "user", Role: "Viewer"}, {Kind: "user", Role: "Operator"}, {Kind: "service", Role: "Admin"}} {
		status(t, f.s.DeleteProject(testCtx, Trace{Actor: actor}, p.ID, ProjectDeleteInput{p.Name}), 403)
	}
	for _, name := range []string{"", "confirm exactly", "Confirm Exactly "} {
		status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{name}), 400)
	}
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}), 409)
	// Even inactive members remain references and must never be detached by delete.
	_, e = f.db.SQL.Exec(`UPDATE target_schemas SET active=0 WHERE id=?`, schema.ID)
	must(t, e)
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}), 409)
	p, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: p.Name, Status: "archived", Members: p.Members})
	must(t, e)
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}), 409)
	// Archived membership remains read-only; it cannot be silently dropped.
	_, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: p.Name, Status: "archived"})
	status(t, e, 409)
	_, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: p.Name, Members: p.Members})
	must(t, e)
	p, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: "Renamed", Status: "archived"})
	must(t, e)
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{"Confirm Exactly"}), 400)
	must(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}))
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}), 404)
	_, e = f.s.Project(testCtx, f.admin, p.ID)
	status(t, e, 404)
	_, e = f.s.ProjectRuns(testCtx, f.admin, p.ID, RunFilter{})
	status(t, e, 404)
	projects, e := f.s.Projects(testCtx, f.admin, "")
	must(t, e)
	if len(projects) != 0 {
		t.Fatalf("deleted project still listed: %+v", projects)
	}
	var n int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM audit_events WHERE action='project.delete' AND target=? AND outcome='success' AND actor_id=? AND request_id=?`, p.ID, f.admin.ID, f.trace().RequestID).Scan(&n))
	if n != 1 {
		t.Fatalf("successful deletion audits = %d", n)
	}
}

func TestDeleteProjectAuditFailureAndRevokedAuthorityRollBack(t *testing.T) {
	f := newFixture(t)
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "Rollback"})
	must(t, e)
	_, e = f.db.SQL.Exec(`CREATE TRIGGER reject_project_delete_audit BEFORE INSERT ON audit_events WHEN NEW.action='project.delete' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	must(t, e)
	if e = f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}); e == nil {
		t.Fatal("deletion committed despite audit failure")
	}
	_, e = f.s.Project(testCtx, f.admin, p.ID)
	must(t, e)
	_, e = f.db.SQL.Exec(`DROP TRIGGER reject_project_delete_audit; UPDATE sessions SET revoked=1 WHERE id=?`, f.admin.SessionID)
	must(t, e)
	status(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}), 401)
	var n int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM projects WHERE id=?`, p.ID).Scan(&n))
	if n != 1 {
		t.Fatal("revoked Admin deleted project")
	}
}

func TestDeleteProjectSerializesConcurrentMembershipChanges(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, "concurrent-delete", "DEV")
	for i := 0; i < 20; i++ {
		p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: fmt.Sprintf("Race %d", i)})
		must(t, e)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var deletion, update error
		go func() {
			defer wg.Done()
			<-start
			deletion = f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, update = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: p.Name, Members: []ProjectMember{{TargetID: target.ID}}})
		}()
		close(start)
		wg.Wait()
		if deletion == nil {
			status(t, update, 404)
		} else {
			status(t, deletion, 409)
			must(t, update)
		}
		var members int
		must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM project_members WHERE project_id=?`, p.ID).Scan(&members))
		if (deletion == nil && members != 0) || (deletion != nil && members != 1) {
			t.Fatalf("race lost membership or left orphan: deleted=%v members=%d", deletion, members)
		}
	}
	rows, e := f.db.SQL.Query(`PRAGMA foreign_key_check`)
	must(t, e)
	defer rows.Close()
	if rows.Next() {
		t.Fatal("orphaned references after race")
	}
	must(t, rows.Err())
}

func TestDeleteProjectPreservesAllOtherRegistryDataAndOriginalAudits(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	in := uploadRun(t, f, target, schema, account, "V1__retained.sql", "SELECT 1 FROM dual;")
	run, e := f.s.SubmitRun(testCtx, f.trace(), in, "retained-project-history")
	must(t, e)
	if waitRun(t, f, run.ID).Status != "SUCCEEDED" {
		t.Fatal("fixture run did not succeed")
	}
	shared, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "Shared", Members: []ProjectMember{{TargetID: target.ID}}})
	must(t, e)
	p, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "Former member", Members: []ProjectMember{{TargetID: target.ID, SchemaID: schema.ID}}})
	must(t, e)
	p, e = f.s.SaveProject(testCtx, f.trace(), p.ID, ProjectInput{Name: p.Name})
	must(t, e)
	var auditMax int64
	must(t, f.db.SQL.QueryRow(`SELECT max(id) FROM audit_events`).Scan(&auditMax))
	before := projectDeleteSnapshot(t, f.db.SQL, p.ID, auditMax)
	must(t, f.s.DeleteProject(testCtx, f.trace(), p.ID, ProjectDeleteInput{p.Name}))
	after := projectDeleteSnapshot(t, f.db.SQL, p.ID, auditMax)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("project deletion changed unrelated registry data or original audits")
	}
	history, e := f.s.ProjectRuns(testCtx, f.admin, shared.ID, RunFilter{})
	must(t, e)
	if len(history) != 1 || history[0].ID != run.ID {
		t.Fatal("shared project's run history changed")
	}
	var integrity string
	must(t, f.db.SQL.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
}

// Snapshot every application table, retaining all original audit rows and every
// other project. The sole allowed changes are this project row and one new audit.
func projectDeleteSnapshot(t *testing.T, db *sql.DB, deletedID string, auditMax int64) map[string]string {
	t.Helper()
	rows, e := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	must(t, e)
	tables := []string{}
	for rows.Next() {
		var table string
		must(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	must(t, rows.Err())
	rows.Close()
	out := map[string]string{}
	for _, table := range tables {
		query := `SELECT * FROM "` + table + `"`
		args := []any{}
		if table == "projects" {
			query += ` WHERE id<>?`
			args = append(args, deletedID)
		}
		if table == "audit_events" {
			query += ` WHERE id<=?`
			args = append(args, auditMax)
		}
		rows, e := db.Query(query+` ORDER BY rowid`, args...)
		must(t, e)
		cols, e := rows.Columns()
		must(t, e)
		all := [][]any{}
		for rows.Next() {
			values, pointers := make([]any, len(cols)), make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			must(t, rows.Scan(pointers...))
			all = append(all, values)
		}
		must(t, rows.Err())
		rows.Close()
		encoded, e := json.Marshal(all)
		must(t, e)
		out[table] = string(encoded)
	}
	return out
}
