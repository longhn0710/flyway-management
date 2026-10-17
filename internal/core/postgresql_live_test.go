package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

// Opt-in only: this test uses the explicitly prepared disposable local lab.
// Never point these variables at an existing database service.
func TestPostgreSQLRealConnectionAndInfo(t *testing.T) {
	if os.Getenv("FWM_POSTGRES_LAB") != "1" {
		t.Skip("requires disposable PostgreSQL 17 TLS/SCRAM lab")
	}
	f := newFixture(t)
	ca, e := os.ReadFile(os.Getenv("PGSSLROOTCERT"))
	must(t, e)
	port, e := strconv.Atoi(os.Getenv("PGPORT"))
	must(t, e)
	runtime := flyway.New(flyway.Config{Executable: os.Getenv("FWM_LAB_FLYWAY"), Version: "13.10.3", SHA256: "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c", TimeoutSeconds: 25}, filepath.Join(f.dir, "runtime"))
	must(t, f.s.StartExecution(testCtx, runtime, "postgres-lab-pin", f.dir, artifact.Limits{}))
	t.Cleanup(f.s.StopExecution)
	in := ConnectionInput{Target: TargetInput{Engine: "postgresql", Name: "PostgreSQL17 lab", Environment: "DEV", Host: os.Getenv("PGHOST"), Port: port, DatabaseName: os.Getenv("PGDATABASE"), Transport: "TCPS", Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: string(ca)}}, Username: os.Getenv("PGUSER"), Password: os.Getenv("PGPASSWORD"), Schemas: []string{"app"}, GrantCurrentUser: true}
	before := postgresDump(t)
	result, e := f.s.TestConnection(testCtx, f.trace(), "", in)
	must(t, e)
	if result.AuthenticationState != "verified" || result.ActualUser != in.Username || result.DatabaseName != in.Target.DatabaseName || !strings.HasPrefix(result.ServerVersion, "17") {
		t.Fatalf("identity verification failed: state=%s code=%s version=%s", result.AuthenticationState, result.ErrorCode, result.ServerVersion)
	}
	for _, schema := range result.Schemas {
		if schema.Name == "restricted" || strings.HasPrefix(schema.Name, "pg_") {
			t.Fatal("inaccessible/system schema disclosed")
		}
	}
	if postgresDump(t) != before {
		t.Fatal("connection metadata probe mutated PostgreSQL")
	}
	saved, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	if saved.Target.Engine != "postgresql" || saved.Schemas[0].Name != "app" || saved.Account.Username != in.Username || saved.Profile == nil {
		t.Fatal("engine profile normalization failed")
	}
	// A second database with the same host, port and schema is an independent target.
	second := in
	second.Target.Name = "PostgreSQL17 second"
	second.Target.DatabaseName = os.Getenv("PGSECOND_DATABASE")
	secondSaved, e := f.s.SaveConnection(testCtx, f.trace(), "", second)
	must(t, e)
	if secondSaved.Schemas[0].ID == saved.Schemas[0].ID {
		t.Fatal("cross-database schema identity collision")
	}
	// Empty-password edit keeps exact credential ciphertext and revision.
	var ciphertext []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM credential_secrets WHERE account_id=?", saved.Account.ID).Scan(&ciphertext))
	edit := in
	edit.AccountID = saved.Account.ID
	edit.Password = ""
	edit.Target.Profile = nil
	unchanged, e := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, edit)
	must(t, e)
	var afterCipher []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM credential_secrets WHERE account_id=?", saved.Account.ID).Scan(&afterCipher))
	if string(ciphertext) != string(afterCipher) || unchanged.Account.Credential.Version != 1 {
		t.Fatal("empty edit rotated credential")
	}
	// Explicit replacement increments version without rewriting earlier evidence.
	edit.Password = in.Password
	rotated, e := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, edit)
	must(t, e)
	if rotated.Account.Credential.Version != 2 {
		t.Fatal("credential rotation missing")
	}
	v1 := []byte("CREATE TABLE foundation_probe(id integer PRIMARY KEY); INSERT INTO foundation_probe VALUES (1);\n")
	v2 := []byte("INSERT INTO foundation_probe VALUES (2);\n")
	artifact1, e := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__foundation.sql", Bytes: v1}})
	must(t, e)
	run := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: artifact1.ID}
	// An independent nonexistent history location makes absent-history acceptance
	// repeatable even when the controlled fixture history already exists.
	_, e = f.s.SaveSchema(testCtx, f.trace(), run.TargetID, run.SchemaID, SchemaInput{Name: "app", HistoryTable: "absent_info_history", DefaultAccountID: saved.Account.ID, Active: true})
	must(t, e)
	initial, e := f.s.RefreshInfo(testCtx, f.trace(), run)
	must(t, e)
	if !initial.Success {
		t.Fatalf("absent-history Info failed: %s", initial.Stderr)
	}
	if postgresDump(t) != before {
		t.Fatal("absent-history Info mutated database")
	}
	_, e = f.s.SaveSchema(testCtx, f.trace(), run.TargetID, run.SchemaID, SchemaInput{Name: "app", HistoryTable: "flyway_schema_history", DefaultAccountID: saved.Account.ID, Active: true})
	must(t, e)
	// These are controlled lab fixtures, never a Manager migration submission.
	plan, e := f.s.localPlan(testCtx, f.db.SQL, f.admin, run, "13.10.3", true)
	must(t, e)
	fixture := requestFor(plan, "migrate")
	fixture.Files = map[string][]byte{"V1__foundation.sql": v1, "V2__second.sql": v2}
	applied := runtime.Execute(testCtx, fixture)
	clear(plan.password)
	if !applied.Success {
		b, _ := json.MarshalIndent(applied, "", "  ")
		_ = os.WriteFile("/tmp/fwm-phase7-baseline/fixture-failure.json", b, 0600)
		t.Fatalf("controlled fixture setup failed; sanitized result in private fixture-failure.json")
	}
	populated := postgresDump(t)
	variants := []struct {
		name  string
		files []artifact.Input
		state string
	}{
		{"success", []artifact.Input{{Name: "V1__foundation.sql", Bytes: v1}, {Name: "V2__second.sql", Bytes: v2}}, "Success"},
		{"pending", []artifact.Input{{Name: "V1__foundation.sql", Bytes: v1}, {Name: "V2__second.sql", Bytes: v2}, {Name: "V3__pending.sql", Bytes: []byte("SELECT 3;\n")}}, "Pending"},
		{"future", []artifact.Input{{Name: "V1__foundation.sql", Bytes: v1}}, "Future"},
		{"missing", []artifact.Input{{Name: "V2__second.sql", Bytes: v2}}, "Missing"},
	}
	evidence := map[string]any{"identity": result, "fixture_migrate": applied, "initial_info": initial}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			a, e := f.s.UploadArtifact(testCtx, f.trace(), run.TargetID, run.SchemaID, variant.files)
			must(t, e)
			input := run
			input.ArtifactID = a.ID
			capture, e := f.s.RefreshInfo(testCtx, f.trace(), input)
			must(t, e)
			if !capture.Success || !strings.Contains(string(capture.JSON), `"state": "`+variant.state+`"`) && !strings.Contains(string(capture.JSON), `"state":"`+variant.state+`"`) {
				t.Fatalf("native %s state missing", variant.state)
			}
			if postgresDump(t) != populated {
				t.Fatal("native Info mutated database objects/data/history")
			}
			evidence[variant.name] = capture
		})
	}
	bad := run
	bad.ArtifactID = "absent"
	_, e = f.s.RefreshInfo(testCtx, f.trace(), bad)
	status(t, e, 404)
	_, viewer, _ := f.user(t, "pg-viewer", "Viewer")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", viewer.ID, []Grant{{TargetID: run.TargetID, SchemaID: run.SchemaID, Read: true}}))
	_, e = f.s.RefreshInfo(testCtx, Trace{Actor: viewer}, run)
	status(t, e, 403)
	_, e = f.s.LatestInfo(testCtx, viewer, run.TargetID, run.SchemaID, "")
	must(t, e)
	// An unmapped account is rejected before CLI. Stored-content fault injection
	// stays in unit fixtures; live acceptance does not edit the Manager registry.
	bad = run
	bad.ExecutionAccountID = secondSaved.Account.ID
	_, e = f.s.RefreshInfo(testCtx, f.trace(), bad)
	status(t, e, 409)
	if !postgresqlMigrationAccepted {
		pre, e := f.s.Preflight(testCtx, f.trace(), run)
		must(t, e)
		if pre.Ready {
			t.Fatal("PostgreSQL migration gate bypassed")
		}
		_, e = f.s.SubmitRun(testCtx, f.trace(), run, "pg-must-stay-disabled")
		status(t, e, 409)
	}
	for _, table := range []string{"migration_runs", "migration_streams", "stream_files", "run_executions", "repair_operations"} {
		var n int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Fatalf("Info created %s", table)
		}
	}
	// Read-only account can inspect populated history without schema CREATE.
	readonly := in
	readonly.AccountID = ""
	readonly.Username = os.Getenv("PGREADONLY_USER")
	readonly.Password = os.Getenv("PGREADONLY_PASSWORD")
	readonly.AcknowledgeUnknown = true
	ro, e := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, readonly)
	must(t, e)
	roRun := run
	roRun.ExecutionAccountID = ro.Account.ID
	// A fresh immutable package avoids intentionally corrupted earlier fixture.
	roArtifact, e := f.s.UploadArtifact(testCtx, f.trace(), run.TargetID, run.SchemaID, variants[0].files)
	must(t, e)
	roRun.ArtifactID = roArtifact.ID
	roCapture, e := f.s.RefreshInfo(testCtx, f.trace(), roRun)
	must(t, e)
	if !roCapture.Success {
		t.Fatalf("read-only Info failed: %s", roCapture.Stderr)
	}
	if postgresDump(t) != populated {
		t.Fatal("read-only Info mutated database")
	}
	evidence["readonly_info"] = roCapture
	sum := sha256.Sum256([]byte(populated))
	evidence["database_before_after_sha256"] = hex.EncodeToString(sum[:])
	evidence["migration_policy_enabled"] = postgresqlMigrationAccepted
	evidence["info_created_migrations"] = false
	if out := os.Getenv("FWM_LAB_EVIDENCE"); out != "" {
		must(t, os.MkdirAll(out, 0700))
		b, e := json.MarshalIndent(evidence, "", "  ")
		must(t, e)
		for _, secret := range []string{in.Password, readonly.Password} {
			if secret != "" && strings.Contains(string(b), secret) {
				t.Fatal("credential found in evidence")
			}
		}
		must(t, os.WriteFile(filepath.Join(out, "core-postgresql.json"), b, 0600))
	}
}
func postgresDump(t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "docker", "exec", "flyway-manager-postgres17-lab", "pg_dump", "-U", "lab_admin", "--no-owner", "--no-privileges", os.Getenv("PGDATABASE"))
	b, e := cmd.Output()
	must(t, e)
	lines := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, `\restrict`) && !strings.HasPrefix(line, `\unrestrict`) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
