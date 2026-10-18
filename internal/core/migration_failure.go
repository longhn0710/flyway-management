package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"

	"flyway-manager/internal/flyway"
)

// MigrationFailure is a view of retained native evidence, independent of Repair.
// No identity is inferred from the uploaded filename or an expected version.
type MigrationFailure struct {
	ExecutionID    string `json:"execution_id"`
	Script         string `json:"script"`
	Version        string `json:"version"`
	ErrorCode      string `json:"error_code"`
	Message        string `json:"message"`
	ExitCode       int    `json:"exit_code"`
	IdentitySource string `json:"identity_source"`
	IdentityReason string `json:"identity_reason"`
}

func (f *MigrationFailure) reason() string {
	return "Flyway migrate failed: " + f.Message + " Oracle DDL may have partially committed."
}

type nativeError struct {
	Code     string `json:"errorCode"`
	Message  string `json:"message"`
	Script   string `json:"script"`
	Filepath string `json:"filepath"`
	Path     string `json:"path"`
	Version  string `json:"version"`
}

func migrationError(payload json.RawMessage) nativeError {
	var body struct {
		Error        *nativeError `json:"error"`
		ErrorDetails *nativeError `json:"errorDetails"`
	}
	if json.Unmarshal(payload, &body) == nil {
		if body.Error != nil {
			return *body.Error
		}
		if body.ErrorDetails != nil {
			return *body.ErrorDetails
		}
	}
	return nativeError{}
}

var failedScriptMessage = regexp.MustCompile(`(?m)^Migration (.+\.sql) failed`)

func migrationFailure(executionID string, result flyway.Result, failedInfo json.RawMessage) *MigrationFailure {
	e := migrationError(result.JSON)
	f := &MigrationFailure{ExecutionID: executionID, ExitCode: result.ExitCode, ErrorCode: e.Code, Message: e.Message, Version: e.Version}
	f.Script = e.Script
	if f.Script == "" {
		f.Script = e.Filepath
	}
	if f.Script == "" {
		f.Script = e.Path
	}
	if f.Script == "" {
		if match := failedScriptMessage.FindStringSubmatch(e.Message); len(match) == 2 {
			f.Script = match[1]
		}
	}
	if f.Script != "" {
		f.Script = path.Base(strings.ReplaceAll(f.Script, "\\", "/"))
	}
	if f.Script != "" || f.Version != "" {
		f.IdentitySource = "migrate"
	}
	rows, err := history(failedInfo)
	if err == nil {
		failed := []historyEntry{}
		for _, row := range rows {
			if strings.EqualFold(row.State, "Failed") {
				failed = append(failed, row)
			}
		}
		if len(failed) == 1 {
			row := failed[0]
			version := ""
			if row.Version != nil {
				version = *row.Version
			}
			if f.Script != "" && row.Script != "" && f.Script != row.Script || f.Version != "" && version != "" && f.Version != version {
				f.Script, f.Version, f.IdentitySource = "", "", ""
				f.IdentityReason = "Native migrate and failed history report conflicting migration identities."
			} else {
				if f.Script == "" {
					f.Script = row.Script
				}
				if f.Version == "" {
					f.Version = version
				}
				if f.Script != "" || f.Version != "" {
					if f.IdentitySource == "" {
						f.IdentitySource = "failed_history"
					} else {
						f.IdentitySource = "migrate_and_failed_history"
					}
				}
			}
		}
	}
	if f.Script == "" && f.Version == "" && f.IdentityReason == "" {
		f.IdentityReason = "Flyway evidence did not identify a precise failed SQL filename or version."
	}
	if f.Message == "" {
		f.Message = result.Stderr
	}
	if f.Message == "" {
		f.Message = "Flyway did not report a structured migration error; inspect the retained CLI output."
	}
	return f
}

// Read only original migrate evidence and the first subsequent history capture,
// never a repair error or the repaired resolver view. Both are durable before
// the worker is allowed to invoke Repair; legacy records need no schema upgrade.
func (s *Service) runMigrationFailure(ctx context.Context, rid, pin string) (*MigrationFailure, error) {
	var executionID, payload string
	var result flyway.Result
	err := s.db.QueryRowContext(ctx, `SELECT id,result,stdout,stderr,exit_code,ambiguous FROM run_executions WHERE run_id=? AND command='migrate' AND success=0 ORDER BY rowid LIMIT 1`, rid).Scan(&executionID, &payload, &result.Stdout, &result.Stderr, &result.ExitCode, &result.Ambiguous)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result.JSON = json.RawMessage(payload)
	if result.Ambiguous || !flyway.ConfirmedMigrationFailure(result.JSON, pin) {
		// Generic/ambiguous CLI output cannot be attributed to a later history
		// row discovered during startup reconciliation. Retain direct native
		// identity if any, otherwise explicitly report that it is unknown.
		return migrationFailure(executionID, result, nil), nil
	}
	var after string
	err = s.db.QueryRowContext(ctx, `SELECT before_history FROM repair_operations WHERE run_id=?`, rid).Scan(&after)
	if errors.Is(err, sql.ErrNoRows) {
		// During assessment there is no completed repair record yet. Use native
		// insertion order, independent of wall-clock corrections, and never use
		// the repaired resolver view to identify the original failed script.
		err = s.db.QueryRowContext(ctx, `SELECT result FROM run_executions WHERE run_id=? AND command='info' AND success=1 AND rowid>(SELECT rowid FROM run_executions WHERE id=?) AND rowid<COALESCE((SELECT min(rowid) FROM run_executions WHERE run_id=? AND command='repair'),9223372036854775807) ORDER BY rowid LIMIT 1`, rid, executionID, rid).Scan(&after)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return migrationFailure(executionID, result, json.RawMessage(after)), nil
}
