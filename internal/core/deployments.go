package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type ExternalSource struct {
	System        string `json:"system,omitempty"`
	ExecutionID   string `json:"execution_id,omitempty"`
	ExecutionURL  string `json:"execution_url,omitempty"`
	Workflow      string `json:"workflow,omitempty"`
	RepositoryURL string `json:"repository_url,omitempty"`
	Revision      string `json:"revision,omitempty"`
}

func (x ExternalSource) validate() error {
	b, _ := json.Marshal(x)
	if len(b) > 4096 {
		return invalid("external_source", "External provenance exceeds 4096 bytes.")
	}
	for _, v := range []string{x.System, x.ExecutionID, x.ExecutionURL, x.Workflow, x.RepositoryURL, x.Revision} {
		if strings.ContainsAny(v, "\x00\r\n") {
			return invalid("external_source", "Provenance must contain single-line text.")
		}
	}
	return nil
}

type DeploymentTargetInput struct {
	Name               string `json:"name"`
	Environment        string `json:"environment"`
	TargetID           string `json:"target_id"`
	SchemaID           string `json:"schema_id"`
	ExecutionAccountID string `json:"execution_account_id"`
	Active             bool   `json:"active"`
	Revision           int    `json:"revision"`
}
type DeploymentTarget struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	DeploymentTargetInput
	DatabaseName string `json:"database_name"`
	SchemaName   string `json:"schema_name"`
	AccountName  string `json:"account_name"`
	CanRead      bool   `json:"can_read"`
	CanUpload    bool   `json:"can_upload"`
	CanPreflight bool   `json:"can_preflight"`
	CanExecute   bool   `json:"can_execute"`
}
type DeploymentGrantInput struct {
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id"`
	Read          bool   `json:"read"`
	Upload        bool   `json:"upload"`
	Preflight     bool   `json:"preflight"`
	Execute       bool   `json:"execute"`
}
type TokenDeploymentScope struct {
	DeploymentTargetID string `json:"deployment_target_id"`
	Read               bool   `json:"read"`
	Upload             bool   `json:"upload"`
	Preflight          bool   `json:"preflight"`
	Execute            bool   `json:"execute"`
}

func deploymentFrom(ctx context.Context, q queryer, p, d string) (DeploymentTarget, error) {
	var x DeploymentTarget
	e := q.QueryRowContext(ctx, `SELECT d.id,d.project_id,d.name,d.environment,d.target_id,d.schema_id,d.execution_account_id,d.active,d.revision,t.name,s.name,a.name FROM deployment_targets d JOIN database_targets t ON t.id=d.target_id JOIN target_schemas s ON s.id=d.schema_id JOIN execution_accounts a ON a.id=d.execution_account_id WHERE d.id=? AND d.project_id=?`, d, p).Scan(&x.ID, &x.ProjectID, &x.Name, &x.Environment, &x.TargetID, &x.SchemaID, &x.ExecutionAccountID, &x.Active, &x.Revision, &x.DatabaseName, &x.SchemaName, &x.AccountName)
	return x, translate(e)
}
func principalCurrent(ctx context.Context, q queryer, a Actor) error {
	var n int
	if a.Kind == "service" {
		if e := q.QueryRowContext(ctx, `SELECT count(*) FROM service_accounts s JOIN users u ON u.id=s.owner_id JOIN api_tokens t ON t.service_id=s.id WHERE s.id=? AND s.active=1 AND u.active=1 AND t.id=? AND t.revoked=0 AND t.expires_at>?`, a.ID, a.TokenID, now()).Scan(&n); e != nil {
			return e
		}
	} else if a.Kind == "user" {
		if e := q.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND active=1 AND role=?`, a.ID, a.Role).Scan(&n); e != nil {
			return e
		}
		if a.SessionID != "" {
			if e := q.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id=? AND user_id=? AND revoked=0 AND expires_at>? AND last_used_at>?`, a.SessionID, a.ID, now(), now()-1800).Scan(&n); e != nil {
				return e
			}
		}
	}
	if n != 1 {
		return unauthorized()
	}
	return nil
}
func (s *Service) deploymentAccess(ctx context.Context, q queryer, a Actor, p, d, action string) (DeploymentTarget, error) {
	x, e := deploymentFrom(ctx, q, p, d)
	if e != nil {
		return x, e
	}
	if e = principalCurrent(ctx, q, a); e != nil {
		return DeploymentTarget{}, e
	}
	var status string
	if e = q.QueryRowContext(ctx, `SELECT status FROM projects WHERE id=?`, p).Scan(&status); e != nil {
		return x, e
	}
	if !a.Admin() {
		col := "user_id"
		if a.Kind == "service" {
			col = "service_id"
		}
		var n int
		if e = q.QueryRowContext(ctx, `SELECT count(*) FROM project_identity_memberships WHERE project_id=? AND `+col+`=?`, p, a.ID).Scan(&n); e != nil {
			return x, e
		}
		if n != 1 {
			return DeploymentTarget{}, missing()
		}
		if e = q.QueryRowContext(ctx, `SELECT can_read,can_upload,can_preflight,can_execute FROM deployment_grants WHERE deployment_target_id=? AND `+col+`=?`, d, a.ID).Scan(&x.CanRead, &x.CanUpload, &x.CanPreflight, &x.CanExecute); e != nil {
			if e == sql.ErrNoRows {
				return DeploymentTarget{}, missing()
			}
			return x, e
		}
	} else {
		x.CanRead = true
		x.CanUpload = true
		x.CanPreflight = true
		x.CanExecute = true
	}
	if a.Kind == "service" {
		var r, u, f, e bool
		err := q.QueryRowContext(ctx, `SELECT can_read,can_upload,can_preflight,can_execute FROM token_deployment_scopes WHERE token_id=? AND deployment_target_id=?`, a.TokenID, d).Scan(&r, &u, &f, &e)
		if err == sql.ErrNoRows {
			return DeploymentTarget{}, missing()
		}
		if err != nil {
			return x, err
		}
		x.CanRead = x.CanRead && r
		x.CanUpload = x.CanUpload && u
		x.CanPreflight = x.CanPreflight && f
		x.CanExecute = x.CanExecute && e
	}
	gs, e := grantsFrom(ctx, q, a.Kind, a.ID)
	if e != nil {
		return x, e
	}
	var env string
	var active, sactive bool
	if e = q.QueryRowContext(ctx, `SELECT t.environment,t.active,s.active FROM database_targets t JOIN target_schemas s ON s.target_id=t.id WHERE t.id=? AND s.id=?`, x.TargetID, x.SchemaID).Scan(&env, &active, &sactive); e != nil {
		return x, e
	}
	var mapped int
	if e = q.QueryRowContext(ctx, `SELECT count(*) FROM execution_accounts a JOIN credential_secrets c ON c.account_id=a.id JOIN account_schema_grants g ON g.account_id=a.id WHERE a.id=? AND a.target_id=? AND g.schema_id=? AND a.active=1 AND c.revoked=0`, x.ExecutionAccountID, x.TargetID, x.SchemaID).Scan(&mapped); e != nil {
		return x, e
	}
	if e = connectionAccountAllowed(ctx, q, x.TargetID, x.SchemaID, x.ExecutionAccountID); e != nil {
		if ce, ok := e.(*Error); ok && ce.Status == 403 {
			mapped = 0
		} else {
			return x, e
		}
	}
	// Environment is part of the immutable destination and must still agree with registry.
	operate := Allowed(a, "operate", x.TargetID, x.SchemaID, env, gs) && env == x.Environment && active && sactive && x.Active && status == "active" && mapped == 1
	x.CanRead = x.CanRead && Allowed(a, "read", x.TargetID, x.SchemaID, env, gs)
	x.CanUpload = x.CanUpload && operate
	// Read-only assessment remains available when a credential or mapping is unavailable.
	x.CanPreflight = x.CanPreflight && Allowed(a, "operate", x.TargetID, x.SchemaID, env, gs) && env == x.Environment && active && sactive && x.Active && status == "active"
	x.CanExecute = x.CanExecute && operate
	if !x.CanRead {
		return DeploymentTarget{}, missing()
	}
	allowed := map[string]bool{"read": x.CanRead, "upload": x.CanUpload, "preflight": x.CanPreflight, "execute": x.CanExecute}[action]
	if !allowed {
		return DeploymentTarget{}, denied()
	}
	return x, nil
}
func (s *Service) DeploymentTarget(ctx context.Context, a Actor, p, d, action string) (DeploymentTarget, error) {
	return s.deploymentAccess(ctx, s.db, a, p, d, action)
}
func (s *Service) ExternalProject(ctx context.Context, a Actor, p string) (Project, error) {
	if e := principalCurrent(ctx, s.db, a); e != nil {
		return Project{}, e
	}
	if !a.Admin() {
		ds, _, e := s.DeploymentTargetsPage(ctx, a, p, "", 1)
		if e != nil {
			return Project{}, e
		}
		if len(ds) == 0 {
			return Project{}, missing()
		}
	}
	var x Project
	var c, u int64
	e := s.db.QueryRowContext(ctx, `SELECT id,name,description,status,created_at,updated_at FROM projects WHERE id=?`, p).Scan(&x.ID, &x.Name, &x.Description, &x.Status, &c, &u)
	x.CreatedAt = stamp(c)
	x.UpdatedAt = stamp(u)
	x.Members = []ProjectMember{}
	return x, translate(e)
}
func (s *Service) SaveDeploymentTarget(ctx context.Context, t Trace, p, d string, in DeploymentTargetInput) (DeploymentTarget, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return DeploymentTarget{}, e
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validText(in.Name, 100) {
		return DeploymentTarget{}, invalid("name", "Name is required (max 100 bytes).")
	}
	create := d == ""
	if create {
		d = id()
	}
	e := s.mutation(ctx, t, "deployment_target.save", d, func(tx *sql.Tx) error {
		var status, env string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM projects WHERE id=?`, p).Scan(&status); e != nil {
			return e
		}
		if status != "active" {
			return conflict("Project is archived.")
		}
		if create {
			if e := connectionAccountAllowed(ctx, tx, in.TargetID, in.SchemaID, in.ExecutionAccountID); e != nil {
				return e
			}
			if e := connectionSchemaAllowed(ctx, tx, in.TargetID, in.SchemaID); e != nil {
				return e
			}
			var mapped int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM account_schema_grants g JOIN execution_accounts a ON a.id=g.account_id WHERE g.target_id=? AND g.schema_id=? AND g.account_id=? AND a.active=1`, in.TargetID, in.SchemaID, in.ExecutionAccountID).Scan(&mapped); e != nil {
				return e
			}
			if mapped != 1 {
				return conflict("An active execution account mapped to this schema is required.")
			}

			if e := tx.QueryRowContext(ctx, `SELECT environment FROM database_targets WHERE id=?`, in.TargetID).Scan(&env); e != nil {
				return e
			}
			if env != in.Environment {
				return invalid("environment", "Environment must match the existing database target.")
			}
			// Ownership adoption requires all earlier work to have settled. Historical bytes remain unchanged.
			var pending int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.target_id=? AND r.schema_id=? AND (r.status IN ('QUEUED','PREFLIGHT','RUNNING','REPAIRING') OR j.claimed=1)`, in.TargetID, in.SchemaID).Scan(&pending); e != nil {
				return e
			}
			if pending > 0 {
				return conflict("Wait for existing stream jobs to settle before adopting ownership.")
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO project_stream_owners VALUES(?,?,?) ON CONFLICT(target_id,schema_id) DO NOTHING`, in.TargetID, in.SchemaID, p); e != nil {
				return e
			}
			var owner string
			if e := tx.QueryRowContext(ctx, `SELECT project_id FROM project_stream_owners WHERE target_id=? AND schema_id=?`, in.TargetID, in.SchemaID).Scan(&owner); e != nil {
				return e
			}
			if owner != p {
				return conflict("Migration stream is already owned by another Project.")
			}
			_, e := tx.ExecContext(ctx, `INSERT INTO deployment_targets VALUES(?,?,?,?,?,?,?,?,1,?,?)`, d, p, in.Name, in.Environment, in.TargetID, in.SchemaID, in.ExecutionAccountID, in.Active, now(), now())
			return e
		}
		old, e := deploymentFrom(ctx, tx, p, d)
		if e != nil {
			return e
		}
		if in.Revision != old.Revision {
			return conflict("Deployment Target revision is stale.")
		}
		if in.TargetID != old.TargetID || in.SchemaID != old.SchemaID || in.ExecutionAccountID != old.ExecutionAccountID || in.Environment != old.Environment {
			return conflict("Deployment destination is immutable; create a new Deployment Target.")
		}
		_, e = tx.ExecContext(ctx, `UPDATE deployment_targets SET name=?,active=?,revision=revision+1,updated_at=? WHERE id=?`, in.Name, in.Active, now(), d)
		return e
	})
	if e != nil {
		return DeploymentTarget{}, e
	}
	return s.DeploymentTarget(ctx, t.Actor, p, d, "read")
}
func (s *Service) SetDeploymentGrant(ctx context.Context, t Trace, p, d string, in DeploymentGrantInput) error {
	if e := requireAdmin(t.Actor); e != nil {
		return e
	}
	col, table := "user_id", "users"
	if in.PrincipalKind == "service" {
		col, table = "service_id", "service_accounts"
	} else if in.PrincipalKind != "user" {
		return invalid("principal_kind", "Use user or service.")
	}
	if !in.Read && (in.Upload || in.Preflight || in.Execute) {
		return invalid("read", "Actions require read permission.")
	}
	return s.mutation(ctx, t, "deployment_target.grant", d, func(tx *sql.Tx) error {
		if _, e := deploymentFrom(ctx, tx, p, d); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=?", in.PrincipalID).Scan(&n); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO project_identity_memberships(project_id,`+col+`) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM project_identity_memberships WHERE project_id=? AND `+col+`=?)`, p, in.PrincipalID, p, in.PrincipalID); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM deployment_grants WHERE deployment_target_id=? AND `+col+`=?`, d, in.PrincipalID); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO deployment_grants(deployment_target_id,`+col+`,can_read,can_upload,can_preflight,can_execute) VALUES(?,?,?,?,?,?)`, d, in.PrincipalID, in.Read, in.Upload, in.Preflight, in.Execute)
		return e
	})
}

// Enforced at transaction admission for legacy mutations. V2 calls carry an internal admission, never a client-supplied destination.
func legacyStreamAllowed(ctx context.Context, q queryer, target, schema string) error {
	var n int
	if e := q.QueryRowContext(ctx, `SELECT count(*) FROM project_stream_owners WHERE target_id=? AND schema_id=?`, target, schema).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return &Error{409, "PROJECT_TARGET_REQUIRED", "Use the Project-owned Deployment Target API for this stream.", ""}
	}
	return nil
}
