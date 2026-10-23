package core

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

type Project struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Members     []ProjectMember `json:"members"`
}
type ProjectMember struct {
	TargetID string `json:"target_id"`
	SchemaID string `json:"schema_id,omitempty"`
}
type ProjectInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	Members     []ProjectMember `json:"members"`
}

type ProjectDeleteInput struct {
	ConfirmationName string `json:"confirmation_name"`
}

// DeleteProject removes only empty project metadata. Runs are target/schema-scoped
// and audit rows retain their target identifiers. Never detach memberships here:
// their absence is checked under the same write lock as deletion/audit.
func (s *Service) DeleteProject(ctx context.Context, t Trace, pid string, in ProjectDeleteInput) error {
	if e := requireAdmin(t.Actor); e != nil {
		return e
	}
	return s.mutation(ctx, t, "project.delete", pid, func(tx *sql.Tx) error {
		var name string
		if e := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE id=?`, pid).Scan(&name); e != nil {
			return e
		}
		if in.ConfirmationName != name {
			return invalid("confirmation_name", "Type the current project name exactly to confirm deletion.")
		}
		var members int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_members WHERE project_id=?`, pid).Scan(&members); e != nil {
			return e
		}
		if members != 0 {
			return conflict("Remove all project memberships before deleting. Unarchive the project first if its membership is read-only.")
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, pid)
		return e
	})
}

func (s *Service) projectVisible(ctx context.Context, a Actor, id string) (bool, error) {
	if a.Admin() {
		return true, nil
	}
	// Identity membership and a readable deployment also make a project visible
	// in the existing browser registry. Legacy resource grouping remains supported.
	if targets, _, e := s.DeploymentTargetsPage(ctx, a, id, "", 1); e == nil && len(targets) > 0 {
		return true, nil
	} else if e != nil {
		if ce, ok := e.(*Error); !ok || ce.Status != 404 {
			return false, e
		}
	}
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return false, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT m.target_id,IFNULL(m.schema_id,''),t.environment,t.active,s.active FROM project_members m JOIN database_targets t ON t.id=m.target_id LEFT JOIN target_schemas s ON s.id=m.schema_id AND s.target_id=m.target_id WHERE m.project_id=?`, id)
	if e != nil {
		return false, e
	}
	defer rows.Close()
	type memberRow struct {
		tid, sid, env string
		ta            bool
		sa            sql.NullBool
	}
	items := []memberRow{}
	for rows.Next() {
		var x memberRow
		if e = rows.Scan(&x.tid, &x.sid, &x.env, &x.ta, &x.sa); e != nil {
			return false, e
		}
		items = append(items, x)
	}
	if e = rows.Err(); e != nil {
		return false, e
	}
	rows.Close()
	for _, x := range items {
		if !a.Admin() && (!x.ta || x.sid != "" && !x.sa.Bool) {
			continue
		}
		readable := Allowed(a, "read", x.tid, x.sid, x.env, gs)
		if !readable && x.sid == "" {
			readable, e = targetHasReadableSchema(ctx, s.db, a, x.tid, x.env, gs)
			if e != nil {
				return false, e
			}
		}
		if readable {
			return true, nil
		}
	}
	return false, nil
}

func targetHasReadableSchema(ctx context.Context, q queryer, a Actor, tid, env string, gs []Grant) (bool, error) {
	rows, e := q.QueryContext(ctx, `SELECT id,active FROM target_schemas WHERE target_id=?`, tid)
	if e != nil {
		return false, e
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var active bool
		if e = rows.Scan(&sid, &active); e != nil {
			return false, e
		}
		if (active || a.Admin()) && Allowed(a, "read", tid, sid, env, gs) {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (s *Service) Projects(ctx context.Context, a Actor, q string) ([]Project, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,description,status,created_at,updated_at FROM projects WHERE name LIKE ? OR description LIKE ? ORDER BY name,id`, `%`+q+`%`, `%`+q+`%`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Project{}
	projects := []Project{}
	for rows.Next() {
		var p Project
		var c, u int64
		if e = rows.Scan(&p.ID, &p.Name, &p.Description, &p.Status, &c, &u); e != nil {
			return nil, e
		}
		p.CreatedAt = stamp(c)
		p.UpdatedAt = stamp(u)
		projects = append(projects, p)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	for _, p := range projects {
		ok, e := s.projectVisible(ctx, a, p.ID)
		if e != nil {
			return nil, e
		}
		if !ok {
			continue
		}
		p.Members, e = s.projectMembers(ctx, p.ID, a)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Service) Project(ctx context.Context, a Actor, id string) (Project, error) {
	var p Project
	var c, u int64
	e := s.db.QueryRowContext(ctx, `SELECT id,name,description,status,created_at,updated_at FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Description, &p.Status, &c, &u)
	if e != nil {
		return p, translate(e)
	}
	ok, e := s.projectVisible(ctx, a, id)
	if e != nil {
		return p, e
	}
	if !ok {
		return p, missing()
	}
	p.CreatedAt = stamp(c)
	p.UpdatedAt = stamp(u)
	p.Members, e = s.projectMembers(ctx, id, a)
	return p, e
}
func (s *Service) projectMembers(ctx context.Context, id string, a Actor) ([]ProjectMember, error) {
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT m.target_id,IFNULL(m.schema_id,''),t.environment,t.active,s.active FROM project_members m JOIN database_targets t ON t.id=m.target_id LEFT JOIN target_schemas s ON s.id=m.schema_id AND s.target_id=m.target_id WHERE m.project_id=? ORDER BY t.name,m.schema_id`, id)
	if e != nil {
		return nil, e
	}
	out := []ProjectMember{}
	type memberRow struct {
		x   ProjectMember
		env string
		ta  bool
		sa  sql.NullBool
	}
	items := []memberRow{}
	for rows.Next() {
		var x memberRow
		if e = rows.Scan(&x.x.TargetID, &x.x.SchemaID, &x.env, &x.ta, &x.sa); e != nil {
			return nil, e
		}
		items = append(items, x)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	for _, x := range items {
		readable := Allowed(a, "read", x.x.TargetID, x.x.SchemaID, x.env, gs)
		if !readable && x.x.SchemaID == "" {
			readable, e = targetHasReadableSchema(ctx, s.db, a, x.x.TargetID, x.env, gs)
			if e != nil {
				return nil, e
			}
		}
		if a.Admin() || x.ta && (x.x.SchemaID == "" || x.sa.Bool) && readable {
			out = append(out, x.x)
		}
	}
	return out, nil
}
func (s *Service) SaveProject(ctx context.Context, t Trace, pid string, in ProjectInput) (Project, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return Project{}, e
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if !validText(in.Name, 120) || len(in.Description) > 2000 || strings.ContainsAny(in.Description, "\x00") {
		return Project{}, invalid("project", "Name and description are invalid.")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "archived" {
		return Project{}, invalid("status", "Status must be active or archived.")
	}
	if len(in.Members) > 1000 {
		return Project{}, invalid("members", "A project can contain at most 1000 members.")
	}
	create := pid == ""
	if create {
		pid = id()
	}
	members := map[string]ProjectMember{}
	for _, m := range in.Members {
		if m.TargetID == "" {
			return Project{}, invalid("members", "Member target is required.")
		}
		if m.SchemaID != "" {
			var n int
			if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM target_schemas WHERE id=? AND target_id=?`, m.SchemaID, m.TargetID).Scan(&n); e != nil {
				return Project{}, e
			}
			if n == 0 {
				return Project{}, invalid("members", "Schema must belong to the selected instance.")
			}
		} else {
			var n int
			if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM database_targets WHERE id=?`, m.TargetID).Scan(&n); e != nil {
				return Project{}, e
			}
			if n == 0 {
				return Project{}, invalid("members", "Instance does not exist.")
			}
		}
		members[m.TargetID+"/"+m.SchemaID] = m
	}
	ordered := make([]ProjectMember, 0, len(members))
	for _, m := range members {
		ordered = append(ordered, m)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].TargetID+ordered[i].SchemaID < ordered[j].TargetID+ordered[j].SchemaID
	})
	action := "project.update"
	if create {
		action = "project.create"
	}
	replaceMembers := create
	e := s.mutation(ctx, t, action, pid, func(tx *sql.Tx) error {
		ts := now()
		if create {
			if _, e := tx.ExecContext(ctx, `INSERT INTO projects(id,name,description,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, pid, in.Name, in.Description, in.Status, ts, ts); e != nil {
				return e
			}
		} else {
			var oldStatus string
			if e := tx.QueryRowContext(ctx, `SELECT status FROM projects WHERE id=?`, pid).Scan(&oldStatus); e != nil {
				return translate(e)
			}
			if oldStatus == "archived" {
				replaceMembers = false
				rows, e := tx.QueryContext(ctx, `SELECT target_id,IFNULL(schema_id,'') FROM project_members WHERE project_id=?`, pid)
				if e != nil {
					return e
				}
				stored := map[string]bool{}
				for rows.Next() {
					var tid, sid string
					if e = rows.Scan(&tid, &sid); e != nil {
						rows.Close()
						return e
					}
					stored[tid+"/"+sid] = true
				}
				if e = rows.Err(); e != nil {
					rows.Close()
					return e
				}
				rows.Close()
				if len(stored) != len(members) {
					return conflict("Archived project membership is read-only; unarchive it before changing members.")
				}
				for key := range members {
					if !stored[key] {
						return conflict("Archived project membership is read-only; unarchive it before changing members.")
					}
				}
			}
			if _, e := tx.ExecContext(ctx, `UPDATE projects SET name=?,description=?,status=?,updated_at=? WHERE id=?`, in.Name, in.Description, in.Status, ts, pid); e != nil {
				return e
			}
			if oldStatus != "archived" {
				replaceMembers = true
				if _, e := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id=?`, pid); e != nil {
					return e
				}
			}
		}
		if replaceMembers {
			for _, m := range ordered {
				var sid any
				if m.SchemaID != "" {
					sid = m.SchemaID
				}
				if _, e := tx.ExecContext(ctx, `INSERT INTO project_members(project_id,target_id,schema_id) VALUES(?,?,?)`, pid, m.TargetID, sid); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		return Project{}, e
	}
	return s.Project(ctx, t.Actor, pid)
}

// ProjectRuns applies current project membership first, then the existing run-level authorization policy.
// Historical run snapshots remain untouched; former members no longer appear in this view.
func (s *Service) ProjectRuns(ctx context.Context, a Actor, pid string, f RunFilter) ([]Run, error) {
	p, e := s.Project(ctx, a, pid)
	if e != nil {
		return nil, e
	}
	if len(p.Members) == 0 {
		return []Run{}, nil
	}
	// Build a current membership predicate. An instance member includes all its schemas;
	// a schema member remains specific even when another member covers that instance.
	scope := map[string]map[string]bool{}
	for _, m := range p.Members {
		if scope[m.TargetID] == nil {
			scope[m.TargetID] = map[string]bool{}
		}
		scope[m.TargetID][m.SchemaID] = true
	}
	limit, offset := f.Limit, f.Offset
	f.Limit = 200
	f.Offset = 0
	all := []Run{}
	seen := map[string]bool{}
	for pageOffset := 0; ; pageOffset += 200 {
		f.Offset = pageOffset
		batch, e := s.Runs(ctx, a, f)
		if e != nil {
			return nil, e
		}
		all = appendProjectRuns(all, batch, scope, f.TargetID, f.SchemaID, seen)
		if len(batch) < 200 {
			break
		}
	}
	return paginateProjectRuns(all, limit, offset), nil
}

func projectRunMatches(run Run, scope map[string]map[string]bool, targetID, schemaID string) bool {
	if targetID != "" && run.TargetID != targetID || schemaID != "" && run.SchemaID != schemaID {
		return false
	}
	members, ok := scope[run.TargetID]
	return ok && (members[""] || members[run.SchemaID])
}
func appendProjectRuns(all, batch []Run, scope map[string]map[string]bool, targetID, schemaID string, seen map[string]bool) []Run {
	for _, run := range batch {
		if projectRunMatches(run, scope, targetID, schemaID) && !seen[run.ID] {
			seen[run.ID] = true
			all = append(all, run)
		}
	}
	return all
}
func paginateProjectRuns(runs []Run, limit, offset int) []Run {
	l, o := (Page{Limit: limit, Offset: offset}).bounds()
	if o >= len(runs) {
		return []Run{}
	}
	end := o + l
	if end > len(runs) {
		end = len(runs)
	}
	return runs[o:end]
}
