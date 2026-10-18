// Package observability projects committed Manager events without access to the
// vault, SQL artifacts, CLI output or authentication credentials.
package observability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const BatchSize = 100

// Event is the complete export allowlist. It deliberately has no free-form
// message, request, target, username, issuer, subject, error or CLI result field.
type Event struct {
	SchemaVersion          int    `json:"schema_version"`
	EventID                string `json:"event_id"`
	Timestamp              string `json:"timestamp"`
	EventType              string `json:"event_type"`
	Severity               string `json:"severity"`
	ProjectID              string `json:"project_id,omitempty"`
	InstanceID             string `json:"instance_id,omitempty"`
	Engine                 string `json:"engine,omitempty"`
	Database               string `json:"database,omitempty"`
	Schema                 string `json:"schema,omitempty"`
	RunID                  string `json:"run_id,omitempty"`
	CorrelationID          string `json:"correlation_id,omitempty"`
	ActorID                string `json:"actor_id,omitempty"`
	AuthenticationProvider string `json:"authentication_provider"`
	Action                 string `json:"action"`
	Outcome                string `json:"outcome"`
	DurationMS             *int64 `json:"duration_ms,omitempty"`
}

type Source struct {
	db           *sql.DB
	installation string
	cursor       int64
}

// OpenSource starts at the current audit high-water mark: enabling telemetry
// never silently exports historical data. Its connection cannot write/migrate.
func OpenSource(ctx context.Context, dataDir, installation string) (*Source, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(installation) {
		return nil, errors.New("invalid telemetry installation identity")
	}
	path, err := filepath.Abs(filepath.Join(dataDir, "manager.db"))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("telemetry requires an existing regular registry")
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	db, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(100)")
	if err != nil {
		return nil, errors.New("telemetry reader unavailable")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Source{db: db, installation: installation}
	var actual string
	if err = db.QueryRowContext(ctx, `SELECT value FROM installation_metadata WHERE key='installation_id'`).Scan(&actual); err != nil || actual != installation {
		db.Close()
		return nil, errors.New("telemetry registry identity mismatch")
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM audit_events`).Scan(&s.cursor); err != nil {
		db.Close()
		return nil, errors.New("telemetry cursor unavailable")
	}
	return s, nil
}

func (s *Source) Close() error { return s.db.Close() }

// Poll reads at most 100 rows with caller-bounded time. Only selected scalar
// fields enter memory. Result rows close before any exporter work begins.
// Invalid/unknown records advance the cursor and are counted as rejected.
func (s *Source) Poll(ctx context.Context) (events []Event, rejected int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,substr(a.actor_id,1,81),substr(a.actor_type,1,16),
substr(a.action,1,65),substr(a.target,1,81),a.timestamp,substr(a.outcome,1,65),substr(a.request_id,1,129),
COALESCE(substr(u.provider,1,16),''),COALESCE(r.id,''),COALESCE(r.target_id,''),COALESCE(json_valid(r.snapshot),0),
CASE WHEN json_valid(r.snapshot) THEN substr(json_extract(r.snapshot,'$.target.engine'),1,129) END,
CASE WHEN json_valid(r.snapshot) THEN substr(json_extract(r.snapshot,'$.target.database_name'),1,129) END,
CASE WHEN json_valid(r.snapshot) THEN substr(json_extract(r.snapshot,'$.target.service'),1,129) END,
CASE WHEN json_valid(r.snapshot) THEN substr(json_extract(r.snapshot,'$.schema.name'),1,129) END,
CASE WHEN r.finished_at>=r.started_at THEN (r.finished_at-r.started_at)*1000 END
FROM audit_events a LEFT JOIN users u ON a.actor_type='user' AND u.id=a.actor_id
LEFT JOIN migration_runs r ON a.action IN ('migration.submit','migration.running','migration.state','migration.recovery','migration.cancel','migration.reconciliation') AND r.id=a.target
WHERE a.id>? ORDER BY a.id LIMIT ?`, s.cursor, BatchSize)
	if err != nil {
		return nil, 0, errors.New("telemetry read failed")
	}
	defer rows.Close()
	last := s.cursor
	for rows.Next() {
		var in persistedEvent
		if err = rows.Scan(&in.id, &in.actorID, &in.actorKind, &in.action, &in.target, &in.timestamp, &in.outcome, &in.correlation, &in.provider, &in.runID, &in.instanceID, &in.snapshotValid, &in.engine, &in.database, &in.service, &in.schema, &in.duration); err != nil {
			return nil, 0, errors.New("telemetry record unreadable")
		}
		last = in.id
		if event, ok := project(s.installation, in); ok {
			events = append(events, event)
		} else {
			rejected++
		}
	}
	if err = rows.Err(); err != nil {
		return nil, 0, errors.New("telemetry read interrupted")
	}
	s.cursor = last
	return events, rejected, nil
}

type persistedEvent struct {
	snapshotValid                                                                         bool
	id, timestamp                                                                         int64
	actorID, actorKind, action, target, outcome, correlation, provider, runID, instanceID string
	engine, database, service, schema                                                     sql.NullString
	duration                                                                              sql.NullInt64
}

var opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
var instanceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
var scopeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#.-]{0,127}$`)
var correlationID = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,128}$`)

// User provider/issuer/subject cannot be changed by supported UserUpdate paths.
// We project only that immutable provider enum, never current roles or claims.
func project(installation string, in persistedEvent) (Event, bool) {
	kind := eventCategory(in.action)
	if kind == "" || !knownOutcome(in.outcome) || in.timestamp <= 0 || in.timestamp > 253402300799 {
		return Event{}, false
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:audit:%d", installation, in.id)))
	e := Event{SchemaVersion: 1, EventID: hex.EncodeToString(hash[:]), Timestamp: time.Unix(in.timestamp, 0).UTC().Format(time.RFC3339), EventType: kind, Severity: "INFO", AuthenticationProvider: "unknown", Action: in.action, Outcome: in.outcome}
	if in.outcome == "failure" || in.outcome == "FAILED" || in.outcome == "NEEDS_REVIEW" || in.outcome == "BLOCKED" || in.outcome == "NOT_VERIFIED" || in.outcome == "UNSUPPORTED" {
		e.Severity = "WARN"
	}
	if in.outcome == "FAILED" || in.outcome == "NEEDS_REVIEW" {
		e.Severity = "ERROR"
	}
	if opaqueID.MatchString(in.actorID) {
		e.ActorID = in.actorID
	}
	if correlationID.MatchString(in.correlation) {
		e.CorrelationID = in.correlation
	}
	switch {
	case in.actorKind == "service" || in.action == "auth.token":
		e.AuthenticationProvider = "service"
	case in.action == "auth.oidc":
		e.AuthenticationProvider = "oidc"
	case in.provider == "local" || in.provider == "oidc" || in.provider == "ldap":
		e.AuthenticationProvider = in.provider
	case in.actorKind == "system":
		e.AuthenticationProvider = "system"
	}
	if kind == "project" && opaqueID.MatchString(in.target) {
		e.ProjectID = in.target
	}
	if (in.action == "connection.save" || in.action == "connection.test" || strings.HasPrefix(in.action, "target.")) && instanceID.MatchString(in.target) {
		e.InstanceID = in.target
	}
	if opaqueID.MatchString(in.runID) && instanceID.MatchString(in.instanceID) {
		e.RunID = in.runID
		e.InstanceID = in.instanceID
		if in.engine.String == "postgresql" || in.engine.String == "oracle" {
			e.Engine = in.engine.String
		} else if in.engine.String == "" && in.snapshotValid {
			e.Engine = "oracle"
		}
		db := in.database.String
		if e.Engine == "oracle" {
			db = in.service.String
		}
		if scopeName.MatchString(db) {
			e.Database = db
		}
		if scopeName.MatchString(in.schema.String) {
			e.Schema = in.schema.String
		}
		if terminalOutcome(in.outcome) && in.duration.Valid && in.duration.Int64 >= 0 && in.duration.Int64 <= 86400000 {
			n := in.duration.Int64
			e.DurationMS = &n
		}
	}
	b, err := json.Marshal(e)
	return e, err == nil && len(b) <= 4096
}

func eventCategory(action string) string {
	switch action {
	case "migration.submit", "migration.running", "migration.state", "migration.recovery", "migration.cancel", "migration.reconciliation", "migration.preflight":
		return "migration"
	case "auth.bootstrap", "auth.login", "auth.oidc", "auth.logout", "auth.token", "auth.rate_limit", "authentication.update", "password.change", "session.revoke":
		return "authentication"
	case "request.denied", "permissions.update", "permissions.prod_reset", "permissions.role_update", "permissions.wizard_grant":
		return "authorization"
	case "project.create", "project.update", "project.delete":
		return "project"
	case "connection.test", "connection.save", "target.create", "target.update", "schema.create", "schema.update", "execution_account.create", "execution_account.update", "credential.create", "credential.replace", "credential.revoke":
		return "connection"
	case "artifact.upload", "flyway.info.requested", "flyway.info.captured":
		return "inspection"
	case "user.create", "user.update", "service_account.create", "service_account.update", "token.issue", "token.rotate", "token.revoke", "token.revoke_service", "token.revoke_owner":
		return "identity"
	}
	return ""
}
func knownOutcome(outcome string) bool {
	switch outcome {
	case "success", "failure", "VERIFIED", "NOT_VERIFIED", "UNSUPPORTED", "QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING", "SUCCEEDED", "FAILED", "BLOCKED", "NEEDS_REVIEW", "CANCELLED":
		return true
	}
	return false
}

func terminalOutcome(outcome string) bool {
	switch outcome {
	case "SUCCEEDED", "FAILED", "BLOCKED", "NEEDS_REVIEW", "CANCELLED":
		return true
	}
	return false
}
