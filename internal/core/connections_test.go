package core

import (
	"context"
	"encoding/json"
	"errors"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"flyway-manager/internal/oracle"
	"fmt"
	"strings"
	"testing"
)

func connectionFixture(t *testing.T) (*fixture, ConnectionInput, *int) {
	f := newFixture(t)
	calls := new(int)
	f.s.oracleProbe = func(ctx context.Context, c oracle.Config) (oracle.Result, error) {
		*calls++
		if c.Password != "oracle-private-password" {
			return oracle.Result{AuthenticationState: "failed"}, nil
		}
		return oracle.Result{AuthenticationState: "verified", ActualUser: "APP", Schemas: []oracle.SchemaCandidate{{Name: "APP", Own: true, MigrationCapability: "potential"}, {Name: "OTHER", MigrationCapability: "unknown"}, {Name: "SYS", MigrationCapability: "unsupported"}}}, nil
	}
	return f, ConnectionInput{Target: TargetInput{Name: "Wizard", Host: "oracle.example", Port: 1521, Service: "APP", Environment: "DEV", Transport: "TCP"}, Username: "APP", Password: "oracle-private-password", Schemas: []string{"APP"}}, calls
}
func TestConnectionTestIsTransientAndAdminOnly(t *testing.T) {
	f, in, calls := connectionFixture(t)
	result, e := f.s.TestConnection(testCtx, f.trace(), "", in)
	must(t, e)
	if result.AuthenticationState != "verified" || *calls != 1 {
		t.Fatal(result, *calls)
	}
	for _, table := range []string{"database_targets", "execution_accounts", "credential_secrets", "target_schemas", "connection_settings"} {
		var count int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM "+table).Scan(&count))
		if count != 0 {
			t.Fatalf("test persisted %s", table)
		}
	}
	_, a, _ := f.user(t, "operator", "Operator")
	_, e = f.s.TestConnection(testCtx, Trace{Actor: a}, "", in)
	status(t, e, 403)
	if *calls != 1 {
		t.Fatal("unauthorized request reached Oracle")
	}
}
func TestConnectionSaveSelectionValidationAndAtomicRollback(t *testing.T) {
	f, in, _ := connectionFixture(t)
	for _, schemas := range [][]string{nil, {"MISSING"}, {"SYS"}, {"OTHER"}, {"APP", "APP"}} {
		bad := in
		bad.Schemas = schemas
		_, e := f.s.SaveConnection(testCtx, f.trace(), "", bad)
		status(t, e, 400)
	}
	in.Schemas = []string{"OTHER"}
	in.AcknowledgeUnknown = true
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	if len(out.Schemas) != 1 || out.Schemas[0].Name != "OTHER" || !out.Account.Credential.Configured {
		t.Fatal(out)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), in.Password) {
		t.Fatal("password exposed")
	}
	var ciphertext []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM credential_secrets WHERE account_id=?", out.Account.ID).Scan(&ciphertext))
	if strings.Contains(string(ciphertext), in.Password) {
		t.Fatal("plaintext credential persisted")
	}
	// Conflict after the target write rolls the entire registration back.
	duplicate := in
	duplicate.Target.Host = "different.example"
	_, e = f.s.SaveConnection(testCtx, f.trace(), "", duplicate)
	status(t, e, 409)
	var count int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM database_targets").Scan(&count))
	if count != 1 {
		t.Fatal("partial target persisted", count)
	}
	// A late failure in the selected-schema writes rolls back target/account/secret too.
	must(t, execSQL(f, "CREATE TRIGGER reject_new_schema BEFORE INSERT ON target_schemas BEGIN SELECT RAISE(ABORT,'fail'); END"))
	duplicate.Target.Name = "Late failure"
	_, e = f.s.SaveConnection(testCtx, f.trace(), "", duplicate)
	if e == nil {
		t.Fatal("expected injected failure")
	}
	for _, table := range []string{"database_targets", "execution_accounts", "credential_secrets", "connection_settings"} {
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM "+table).Scan(&count))
		if count != 1 {
			t.Fatalf("partial %s persisted", table)
		}
	}
}
func execSQL(f *fixture, sql string) error { _, e := f.db.SQL.Exec(sql); return e }
func TestConnectionAllowlistRevocationPreservesRecordsAndAdvancedAccounts(t *testing.T) {
	f, in, _ := connectionFixture(t)
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	sid := out.Schemas[0].ID
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", f.admin.ID, []Grant{{TargetID: out.Target.ID, Read: true, Operate: true}}))
	extra := f.schema(t, out.Target.ID, "OTHER")
	advanced, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: out.Target.ID, Name: "advanced", Username: "ADVANCED", SchemaIDs: []string{sid}, Password: "advanced-private"})
	must(t, e)
	must(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, sid))
	status(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, extra.ID), 403)
	_, e = f.s.StreamAccounts(testCtx, f.admin, out.Target.ID, extra.ID)
	status(t, e, 403)
	_, e = f.s.UploadArtifact(testCtx, f.trace(), out.Target.ID, extra.ID, []artifact.Input{{Name: "V1__probe.sql", Bytes: []byte("SELECT 1 FROM dual;")}})
	status(t, e, 403)
	input := in
	input.AccountID = out.Account.ID
	input.Password = ""
	input.Schemas = []string{"OTHER"}
	input.AcknowledgeUnknown = true
	saved, e := f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, input)
	must(t, e)
	if saved.Account.Credential.Version != 1 {
		t.Fatal("empty edit rotated credential")
	}
	status(t, connectionAccountAllowed(testCtx, f.db.SQL, out.Target.ID, sid, out.Account.ID), 403)
	must(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, sid))
	must(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, extra.ID))
	// Historical schema remains the same row, without deleting account data.
	schema, e := f.s.Schema(testCtx, f.admin, out.Target.ID, sid)
	must(t, e)
	if schema.ID != sid {
		t.Fatal("historical row changed")
	}
	account, e := f.s.Account(testCtx, f.admin, advanced.ID)
	must(t, e)
	if len(account.SchemaIDs) != 1 {
		t.Fatal("advanced mappings lost")
	}
	visible, e := f.s.MigrationSchemas(testCtx, f.admin, out.Target.ID)
	must(t, e)
	if len(visible) != 2 {
		t.Fatal(visible)
	}
	// Existing row/history location survives re-adding the schema.
	input.Schemas = []string{"APP", "OTHER"}
	input.Password = in.Password
	saved, e = f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, input)
	must(t, e)
	if saved.Account.Credential.Version != 2 {
		t.Fatal("rotation did not increment version")
	}
	must(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, sid))
}
func TestConnectionSaveRechecksMetadataAndAuthentication(t *testing.T) {
	f, in, calls := connectionFixture(t)
	_, e := f.s.TestConnection(testCtx, f.trace(), "", in)
	must(t, e)
	in.Password = "wrong"
	_, e = f.s.SaveConnection(testCtx, f.trace(), "", in)
	status(t, e, 409)
	if *calls != 2 {
		t.Fatal("save trusted earlier test")
	}
	f.s.oracleProbe = func(context.Context, oracle.Config) (oracle.Result, error) {
		return oracle.Result{}, errors.New("internal probe failure")
	}
	in.Password = "oracle-private-password"
	_, e = f.s.SaveConnection(testCtx, f.trace(), "", in)
	if e == nil {
		t.Fatal("save ignored failed probe")
	}
}

func TestWizardExplicitOperatorGrantsAndPRODAuthorization(t *testing.T) {
	f, in, _ := connectionFixture(t)
	in.Target.Environment = "PROD"
	in.GrantCurrentUser = true
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	status(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, out.Schemas[0].ID), 403)
	in.AccountID = out.Account.ID
	in.Password = ""
	in.GrantProd = true
	out, e = f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, in)
	must(t, e)
	must(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, out.Schemas[0].ID))
}
func TestQueuedAllowlistRevocationBlocksSQLAndPreservesImmutableHistory(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	// A separate, explicitly mapped account must remain usable, while queued
	// jobs for the revoked account still block through exact account checks.
	_, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "advanced-preserved", Username: "OTHER_DEPLOY", Password: "another-password", SchemaIDs: []string{schema.ID}})
	must(t, e)
	gate := make(chan struct{})
	engine.gate = gate
	engine.started = make(chan string, 10)
	in := uploadRun(t, f, target, schema, account, "V1__one.sql", "SELECT 1 FROM dual;")
	first, e := f.s.SubmitRun(testCtx, f.trace(), in, "first")
	must(t, e)
	<-engine.started
	queued, e := f.s.SubmitRun(testCtx, f.trace(), in, "queued")
	must(t, e)
	// The new empty allowlist revokes future dispatch even though the old account
	// mapping and broad application grant still exist.
	_, e = f.db.SQL.Exec("INSERT INTO connection_settings VALUES(?,?,20)", target.ID, account.ID)
	must(t, e)
	_, e = f.db.SQL.Exec("INSERT INTO connection_managed_accounts VALUES(?,?)", target.ID, account.ID)
	must(t, e)
	close(gate)
	completed := waitRun(t, f, first.ID)
	if completed.Status != "SUCCEEDED" {
		t.Fatal(completed)
	}
	blocked := waitRun(t, f, queued.ID)
	if blocked.Status != "BLOCKED" {
		t.Fatal(blocked)
	}
	if engine.migrateCount(schema.Name) != 1 {
		t.Fatal("revoked stream executed")
	}
	persisted, e := f.s.Run(testCtx, f.admin, first.ID)
	must(t, e)
	if persisted.Status != "SUCCEEDED" || persisted.Schema.ID != schema.ID {
		t.Fatal("history corrupted")
	}
	replay, e := f.s.SubmitRun(testCtx, f.trace(), in, "first")
	must(t, e)
	if replay.ID != first.ID {
		t.Fatal("idempotent history lost")
	}
}

func TestBlankCredentialEditRejectsConcurrentCredentialRotation(t *testing.T) {
	f, in, _ := connectionFixture(t)
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	original := f.s.oracleProbe
	f.s.oracleProbe = func(ctx context.Context, c oracle.Config) (oracle.Result, error) {
		result, e := original(ctx, c)
		_, rotateErr := f.s.ReplaceCredential(ctx, f.trace(), out.Account.ID, "rotated-during-discovery", false)
		must(t, rotateErr)
		return result, e
	}
	in.AccountID = out.Account.ID
	in.Password = ""
	_, e = f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, in)
	status(t, e, 409)
}

func TestOperationalSchemasPaginateAfterAuthorizationWithoutTruncating(t *testing.T) {
	f, in, _ := connectionFixture(t)
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", f.admin.ID, []Grant{{TargetID: out.Target.ID, Read: true, Operate: true}}))
	tx, e := f.db.SQL.Begin()
	must(t, e)
	for n := 0; n < 205; n++ {
		sid := fmt.Sprintf("extra-%03d", n)
		name := fmt.Sprintf("EXTRA_%03d", n)
		_, e = tx.Exec("INSERT INTO target_schemas(id,target_id,name,history_schema,history_table,active,created_at) VALUES(?,?,?,?,?,1,?)", sid, out.Target.ID, name, name, "flyway_schema_history", now())
		must(t, e)
		_, e = tx.Exec("INSERT INTO account_schema_grants VALUES(?,?,?)", out.Account.ID, sid, out.Target.ID)
		must(t, e)
		_, e = tx.Exec("INSERT INTO connection_allowed_schemas VALUES(?,?,?,'potential',0)", out.Target.ID, out.Account.ID, sid)
		must(t, e)
	}
	must(t, tx.Commit())
	first, e := f.s.MigrationSchemas(testCtx, f.admin, out.Target.ID, Page{Limit: 200})
	must(t, e)
	last, e := f.s.MigrationSchemas(testCtx, f.admin, out.Target.ID, Page{Limit: 200, Offset: 200})
	must(t, e)
	empty, e := f.s.MigrationSchemas(testCtx, f.admin, out.Target.ID, Page{Limit: 200, Offset: 400})
	must(t, e)
	if len(first) != 200 || len(last) != 6 || len(empty) != 0 {
		t.Fatal(len(first), len(last), len(empty))
	}
}

func TestConnectionSaveRejectsUnusableDisabledResourcesAndFlywayCredentialBoundary(t *testing.T) {
	f, in, _ := connectionFixture(t)
	bad := in
	bad.Password = "line\nbreak"
	_, e := f.s.SaveConnection(testCtx, f.trace(), "", bad)
	status(t, e, 400)
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	input := in
	input.AccountID = out.Account.ID
	input.Password = ""
	for _, table := range []string{"database_targets", "execution_accounts", "target_schemas"} {
		_, e = f.db.SQL.Exec("UPDATE " + table + " SET active=0")
		must(t, e)
		_, e = f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, input)
		status(t, e, 409)
		_, e = f.db.SQL.Exec("UPDATE " + table + " SET active=1")
		must(t, e)
	}
}

type revokeAfterMigrationRunner struct {
	*fakeEngine
	revoke func()
}

func (r *revokeAfterMigrationRunner) Execute(ctx context.Context, request flyway.Request) flyway.Result {
	result := r.fakeEngine.Execute(ctx, request)
	if request.Command == "migrate" {
		r.revoke()
	}
	return result
}
func TestAllowlistRevocationAfterMigrationPreventsAutomaticRepairSQL(t *testing.T) {
	f, engine, target, schema, account := executionFixture(t)
	f.s.execution.runner = &revokeAfterMigrationRunner{fakeEngine: engine, revoke: func() {
		_, e := f.db.SQL.Exec("INSERT INTO connection_settings VALUES(?,?,20)", target.ID, account.ID)
		must(t, e)
		_, e = f.db.SQL.Exec("INSERT INTO connection_managed_accounts VALUES(?,?)", target.ID, account.ID)
		must(t, e)
	}}
	input := uploadRun(t, f, target, schema, account, "V1__fail.sql", "CREATE TABLE partial_ddl(id NUMBER);")
	run, e := f.s.SubmitRun(testCtx, f.trace(), input, "revoked-before-repair")
	must(t, e)
	final := waitRun(t, f, run.ID)
	if final.Status != "NEEDS_REVIEW" || final.RepairOutcome != "UNSAFE" {
		t.Fatal(final)
	}
	for _, call := range engine.calls {
		if strings.HasSuffix(call, ":repair") {
			t.Fatal("repair SQL ran after revocation")
		}
	}
}

func TestWizardCannotRedirectOtherConfiguredAccounts(t *testing.T) {
	f, in, _ := connectionFixture(t)
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	_, e = f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: out.Target.ID, Name: "Other account", Username: "OTHER", SchemaIDs: []string{out.Schemas[0].ID}, Password: "other-private"})
	must(t, e)
	in.AccountID = out.Account.ID
	in.Target.Host = "another.example"
	_, e = f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, in)
	status(t, e, 409)
	target, e := f.s.Target(testCtx, f.admin, out.Target.ID)
	must(t, e)
	if target.Host == in.Target.Host {
		t.Fatal("redirected another account")
	}
}

func TestEditCanRevokeAllSchemasWithoutRemovingHistoryIdentity(t *testing.T) {
	f, in, _ := connectionFixture(t)
	in.GrantCurrentUser = true
	out, e := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, e)
	sid := out.Schemas[0].ID
	in.AccountID = out.Account.ID
	in.Password = ""
	in.Schemas = []string{}
	saved, e := f.s.SaveConnection(testCtx, f.trace(), out.Target.ID, in)
	must(t, e)
	if len(saved.Schemas) != 0 || len(saved.Account.SchemaIDs) != 0 {
		t.Fatal("mappings not removed")
	}
	status(t, f.s.authorizeExecution(testCtx, f.db.SQL, f.admin, out.Target.ID, sid), 403)
	preserved, e := f.s.Schema(testCtx, f.admin, out.Target.ID, sid)
	must(t, e)
	if preserved.ID != sid {
		t.Fatal("historical schema removed")
	}
}
