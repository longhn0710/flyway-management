package core

import "context"

// Read bounded batches and apply current policy before returning a page. The
// continuation is the last returned immutable ID, never a filtered row offset.
func visibleExternalPage[T any](after string, limit int, fetch func(string) ([]string, error), view func(string) (T, error)) ([]T, string, error) {
	if limit < 1 || limit > 200 {
		return nil, "", invalid("limit", "Use a page size between 1 and 200.")
	}
	out := []T{}
	for {
		ids, e := fetch(after)
		if e != nil {
			return nil, "", e
		}
		for _, id := range ids {
			after = id
			x, e := view(id)
			if e != nil {
				if ce, ok := e.(*Error); ok && (ce.Status == 403 || ce.Status == 404) {
					continue
				}
				return nil, "", e
			}
			out = append(out, x)
			if len(out) == limit {
				return out, id, nil
			}
		}
		if len(ids) < 200 {
			return out, "", nil
		}
	}
}
func externalPageIDs(ctx context.Context, q queryer, query string, args ...any) ([]string, error) {
	rows, e := q.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Service) ExternalProjectsPage(ctx context.Context, a Actor, after string, limit int) ([]Project, string, error) {
	if e := principalCurrent(ctx, s.db, a); e != nil {
		return nil, "", e
	}
	return visibleExternalPage(after, limit, func(cursor string) ([]string, error) {
		return externalPageIDs(ctx, s.db, `SELECT id FROM projects WHERE id>? ORDER BY id LIMIT 200`, cursor)
	}, func(p string) (Project, error) { return s.ExternalProject(ctx, a, p) })
}
func (s *Service) DeploymentTargetsPage(ctx context.Context, a Actor, p, after string, limit int) ([]DeploymentTarget, string, error) {
	if e := principalCurrent(ctx, s.db, a); e != nil {
		return nil, "", e
	}
	var exists int
	if e := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id=?`, p).Scan(&exists); e != nil {
		return nil, "", translate(e)
	}
	if !a.Admin() {
		col := "user_id"
		if a.Kind == "service" {
			col = "service_id"
		}
		if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_identity_memberships WHERE project_id=? AND `+col+`=?`, p, a.ID).Scan(&exists); e != nil {
			return nil, "", e
		}
		if exists != 1 {
			return nil, "", missing()
		}
	}
	out, next, e := visibleExternalPage(after, limit, func(cursor string) ([]string, error) {
		return externalPageIDs(ctx, s.db, `SELECT id FROM deployment_targets WHERE project_id=? AND id>? ORDER BY id LIMIT 200`, p, cursor)
	}, func(d string) (DeploymentTarget, error) { return s.DeploymentTarget(ctx, a, p, d, "read") })
	if e == nil && after == "" && !a.Admin() && len(out) == 0 {
		return nil, "", missing()
	}
	return out, next, e
}
func (s *Service) ExternalRunsPage(ctx context.Context, a Actor, p, after string, limit int) ([]map[string]any, string, error) {
	if _, e := s.ExternalProject(ctx, a, p); e != nil {
		return nil, "", e
	}
	return visibleExternalPage(after, limit, func(cursor string) ([]string, error) {
		return externalPageIDs(ctx, s.db, `SELECT run_id FROM project_migration_runs WHERE project_id=? AND run_id>? ORDER BY run_id LIMIT 200`, p, cursor)
	}, func(r string) (map[string]any, error) { return s.ExternalRun(ctx, a, p, r) })
}
