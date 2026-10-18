package core

import (
	"context"
	"encoding/json"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"sync"
	"time"
)

type FlywayRunner interface {
	Check(context.Context) flyway.Readiness
	Execute(context.Context, flyway.Request) flyway.Result
}
type RunFile struct {
	ArtifactID string `json:"artifact_id"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	Size       int    `json:"size"`
}
type Execution struct {
	runner      FlywayRunner
	pin         string
	limits      artifact.Limits
	mu          sync.Mutex
	streams     map[string]*sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	lockRelease func() error
}

func (e *Execution) stream(target, schema string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := target + "/" + schema
	if e.streams[key] == nil {
		e.streams[key] = &sync.Mutex{}
	}
	return e.streams[key]
}

type RunContext struct {
	ExternalRunID string `json:"external_run_id,omitempty"`
	CommitSHA     string `json:"commit_sha,omitempty"`
	InitiatedBy   string `json:"initiated_by,omitempty"`
}
type RunInput struct {
	external *externalAdmission

	TargetID           string     `json:"target_id"`
	SchemaID           string     `json:"schema_id"`
	ExecutionAccountID string     `json:"execution_account_id"`
	ArtifactID         string     `json:"artifact_id"`
	Context            RunContext `json:"context"`
}
type AccountSnapshot struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Username          string `json:"username"`
	CredentialVersion int    `json:"credential_version"`
}
type RunSnapshot struct {
	SnapshotVersion     int               `json:"snapshot_version,omitempty"`
	Profile             *ProfileSnapshot  `json:"profile,omitempty"`
	FlywayConfiguration map[string]string `json:"flyway_configuration"`
	Target              Target            `json:"target"`
	Schema              Schema            `json:"schema"`
	Account             AccountSnapshot   `json:"account"`
}
type Run struct {
	ProjectID                string         `json:"project_id,omitempty"`
	DeploymentTargetID       string         `json:"deployment_target_id,omitempty"`
	DeploymentTargetRevision int            `json:"deployment_target_revision,omitempty"`
	ExternalSource           ExternalSource `json:"external_source,omitempty"`
	Completed                bool           `json:"completed"`
	RecordAvailable          bool           `json:"record_available"`

	ArtifactName string `json:"artifact_name"`
	StatusURL    string `json:"status_url"`
	LogsURL      string `json:"logs_url"`
	ID           string `json:"id"`
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	RunInput
	RunSnapshot
	ActorID          string            `json:"actor_id"`
	ActorKind        string            `json:"actor_kind"`
	ActorName        string            `json:"actor_name"`
	Source           string            `json:"source"`
	CreatedAt        string            `json:"created_at"`
	StartedAt        string            `json:"started_at"`
	FinishedAt       string            `json:"finished_at"`
	FlywayVersion    string            `json:"flyway_version"`
	ExitCode         *int              `json:"exit_code"`
	MigrationOutcome string            `json:"migration_outcome"`
	RepairOutcome    string            `json:"repair_outcome"`
	Reason           string            `json:"reason"`
	Preflight        *Preflight        `json:"preflight,omitempty"`
	Failure          *MigrationFailure `json:"failure,omitempty"`
}
type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Diagnostic struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Preflight struct {
	Ready          bool            `json:"ready"`
	LocalReady     bool            `json:"local_ready"`
	LiveStatus     string          `json:"live_status"`
	Blockers       []Blocker       `json:"blockers"`
	Warnings       []string        `json:"warnings"`
	RuntimeVersion string          `json:"runtime_version"`
	Files          []ArtifactFile  `json:"files"`
	Diagnostics    []Diagnostic    `json:"diagnostics"`
	Info           json.RawMessage `json:"info,omitempty"`
	Validation     json.RawMessage `json:"validation,omitempty"`
}

func (p *Preflight) block(code, message string) {
	p.Blockers = append(p.Blockers, Blocker{code, message})
	p.Ready = false
}
func preflightBase() Preflight {
	return Preflight{LiveStatus: "NOT_VERIFIED", Blockers: []Blocker{}, Files: []ArtifactFile{}, Diagnostics: []Diagnostic{}, Warnings: []string{"Oracle DDL can commit partially. Flyway repair changes history and never rolls back SQL.", "Oracle privileges govern cross-schema access; schema configuration is not a security boundary."}}
}

type RunFilter struct {
	Page
	Status, TargetID, SchemaID, Actor, Source, From, To string
}
type RunLog struct {
	Sequence  int64  `json:"sequence"`
	Command   string `json:"command"`
	Channel   string `json:"channel"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}
type RunExecution struct {
	ID         string          `json:"id"`
	Command    string          `json:"command"`
	Success    bool            `json:"success"`
	ExitCode   int             `json:"exit_code"`
	Stdout     string          `json:"stdout"`
	Stderr     string          `json:"stderr"`
	JSON       json.RawMessage `json:"json"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at"`
	Ambiguous  bool            `json:"ambiguous"`
}
type Repair struct {
	Outcome     string          `json:"outcome"`
	Reason      string          `json:"reason"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	ExecutionID string          `json:"execution_id"`
}

func utcNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }
