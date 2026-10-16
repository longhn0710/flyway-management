package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flyway-manager/internal/artifact"
	"fmt"
)

type ArtifactFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Size    int    `json:"size"`
}
type ArtifactInput struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}
type Artifact struct {
	ID           string          `json:"id"`
	TargetID     string          `json:"target_id"`
	SchemaID     string          `json:"schema_id"`
	UploaderID   string          `json:"uploader_id"`
	UploaderKind string          `json:"uploader_kind"`
	UploaderName string          `json:"uploader_name"`
	CreatedAt    string          `json:"created_at"`
	SHA256       string          `json:"sha256"`
	TotalBytes   int64           `json:"total_bytes"`
	Files        []ArtifactFile  `json:"files"`
	Inputs       []ArtifactInput `json:"inputs"`
}

func (s *Service) UploadArtifact(ctx context.Context, t Trace, target, schema string, inputs []artifact.Input) (Artifact, error) {
	if schema == "" {
		return Artifact{}, invalid("schema_id", "A target schema is required.")
	}
	if e := s.Authorize(ctx, t.Actor, "operate", target, schema); e != nil {
		return Artifact{}, e
	}
	bundle, e := artifact.Normalize(inputs, s.artifactLimits())
	if e != nil {
		if errors.Is(e, artifact.ErrLimit) {
			return Artifact{}, &Error{413, "ARTIFACT_LIMIT", "Artifact exceeds configured limits.", "files"}
		}
		return Artifact{}, invalid("files", e.Error())
	}
	out := Artifact{ID: id(), TargetID: target, SchemaID: schema, UploaderID: t.Actor.ID, UploaderKind: t.Actor.Kind, UploaderName: t.Actor.Name, CreatedAt: stamp(now()), SHA256: bundle.SHA256, TotalBytes: bundle.TotalBytes, Files: []ArtifactFile{}, Inputs: []ArtifactInput{}}
	for _, f := range bundle.Files {
		out.Files = append(out.Files, ArtifactFile{f.Path, f.SHA256, f.Kind, f.Version, len(f.Bytes)})
	}
	for _, f := range bundle.Inputs {
		out.Inputs = append(out.Inputs, ArtifactInput{f.Name, digest(string(f.Bytes)), len(f.Bytes)})
	}
	payload, _ := json.Marshal(out)
	e = s.mutation(ctx, t, "artifact.upload", out.ID, func(tx *sql.Tx) error {
		if e := s.authorizeExecution(ctx, tx, t.Actor, target, schema); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO artifacts(id,target_id,schema_id,uploader_id,uploader_kind,uploader_name,created_at,sha256,total_bytes,manifest) VALUES(?,?,?,?,?,?,?,?,?,?)`, out.ID, target, schema, t.Actor.ID, t.Actor.Kind, t.Actor.Name, now(), out.SHA256, out.TotalBytes, string(payload)); e != nil {
			return e
		}
		for i, f := range bundle.Inputs {
			if _, e := tx.ExecContext(ctx, `INSERT INTO artifact_blobs VALUES(?,?,?,?,?)`, out.ID, i, f.Name, digest(string(f.Bytes)), f.Bytes); e != nil {
				return e
			}
		}
		for _, f := range bundle.Files {
			if _, e := tx.ExecContext(ctx, `INSERT INTO migration_files VALUES(?,?,?,?,?,?)`, out.ID, f.Path, f.SHA256, f.Kind, f.Version, f.Bytes); e != nil {
				return e
			}
		}
		return nil
	})
	return out, e
}
func (s *Service) artifactLimits() artifact.Limits {
	if s.execution != nil {
		return artifact.EffectiveLimits(s.execution.limits)
	}
	return artifact.DefaultLimits()
}

func (s *Service) Artifact(ctx context.Context, a Actor, aid string) (Artifact, error) {
	var payload string
	e := s.db.QueryRowContext(ctx, "SELECT manifest FROM artifacts WHERE id=?", aid).Scan(&payload)
	if e != nil {
		return Artifact{}, translate(e)
	}
	var out Artifact
	if e = json.Unmarshal([]byte(payload), &out); e != nil {
		return Artifact{}, e
	}
	if e = s.Authorize(ctx, a, "read", out.TargetID, out.SchemaID); e != nil {
		return Artifact{}, e
	}
	return out, nil
}
func (s *Service) ArtifactBytes(ctx context.Context, a Actor, aid, path string, index int, original bool) (string, []byte, error) {
	if _, e := s.Artifact(ctx, a, aid); e != nil {
		return "", nil, e
	}
	var name, hash string
	var content []byte
	var e error
	if original {
		e = s.db.QueryRowContext(ctx, "SELECT name,sha256,content FROM artifact_blobs WHERE artifact_id=? AND input_index=?", aid, index).Scan(&name, &hash, &content)
	} else {
		e = s.db.QueryRowContext(ctx, "SELECT path,sha256,content FROM migration_files WHERE artifact_id=? AND path=?", aid, path).Scan(&name, &hash, &content)
	}
	if e != nil {
		return "", nil, translate(e)
	}
	if digest(string(content)) != hash {
		return "", nil, fmt.Errorf("artifact integrity failure")
	}
	return name, content, nil
}

// Recheck current principal, token/session and grants inside every execution write/claim.
func (s *Service) authorizeExecution(ctx context.Context, q queryer, a Actor, target, schema string) error {
	var env string
	var active, enabled bool
	if e := q.QueryRowContext(ctx, `SELECT t.environment,t.active,s.active FROM database_targets t JOIN target_schemas s ON s.target_id=t.id WHERE t.id=? AND s.id=?`, target, schema).Scan(&env, &active, &enabled); e != nil {
		return translate(e)
	}
	if !active || !enabled {
		return conflict("Target and schema must be active.")
	}
	if a.Kind == "user" {
		var role string
		var userActive bool
		if e := q.QueryRowContext(ctx, "SELECT role,active FROM users WHERE id=?", a.ID).Scan(&role, &userActive); e != nil {
			return unauthorized()
		}
		if !userActive || role != a.Role {
			return denied()
		}
		if a.SessionID != "" {
			var n int
			if e := q.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE id=? AND user_id=? AND revoked=0 AND expires_at>? AND last_used_at>?", a.SessionID, a.ID, now(), now()-1800).Scan(&n); e != nil {
				return e
			}
			if n != 1 {
				return unauthorized()
			}
		}
	} else if a.Kind == "service" {
		var n int
		if e := q.QueryRowContext(ctx, `SELECT count(*) FROM service_accounts a JOIN users u ON u.id=a.owner_id JOIN api_tokens t ON t.service_id=a.id WHERE a.id=? AND a.active=1 AND u.active=1 AND t.id=? AND t.revoked=0 AND t.expires_at>?`, a.ID, a.TokenID, now()).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return unauthorized()
		}
	} else {
		return denied()
	}
	gs, e := grantsFrom(ctx, q, a.Kind, a.ID)
	if e != nil {
		return e
	}
	if !Allowed(a, "operate", target, schema, env, gs) {
		return denied()
	}
	return nil
}

// ArtifactLimits exposes the effective request and normalization limits.
func (s *Service) ArtifactLimits() artifact.Limits { return s.artifactLimits() }
