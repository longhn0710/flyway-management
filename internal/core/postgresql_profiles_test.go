package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/oracle"
	"flyway-manager/internal/postgresql"
)

// postgresCoreRunner supplies deterministic metadata for service tests. It does
// not claim a database connection or exercise the JDBC adapter.
type postgresCoreRunner struct {
	*fakeEngine
	probe postgresql.Result
}

func (r *postgresCoreRunner) ProbePostgreSQL(context.Context, postgresql.Config) (postgresql.Result, error) {
	return r.probe, nil
}

func newPostgresCoreRunner() *postgresCoreRunner {
	return &postgresCoreRunner{fakeEngine: newEngine(), probe: postgresql.Result{
		Result: oracle.Result{
			NetworkState: "reachable", AuthenticationState: "verified", ActualUser: "app_role",
			Schemas:  []oracle.SchemaCandidate{{Name: "app", Own: true, MigrationCapability: "potential"}},
			Warnings: []string{},
		},
		DatabaseName: "app_db", ServerVersion: "170011", SessionUser: "app_role",
		TLSMode: "verify-full", JDBCDriver: "PostgreSQL JDBC 42.7.14",
	}}
}

func postgresTestCA(t *testing.T, serial int64) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	must(t, err)
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "test PostgreSQL CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	must(t, err)
	return "-----BEGIN CERTIFICATE-----\n" + encodeBase64(der) + "\n-----END CERTIFICATE-----\n"
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func postgresCoreFixture(t *testing.T, environment string) (*fixture, *postgresCoreRunner, Connection) {
	t.Helper()
	f := newFixture(t)
	runner := newPostgresCoreRunner()
	must(t, f.s.StartExecution(testCtx, runner, "postgres-profile-test-pin", f.dir, artifact.DefaultLimits()))
	t.Cleanup(f.s.StopExecution)
	input := ConnectionInput{
		Target: TargetInput{Engine: "postgresql", Name: "PG fixture", Environment: environment,
			Host: "localhost", Port: 5432, DatabaseName: "app_db", Transport: "TCPS",
			Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 1)}},
		Username: "app_role", Password: "test-postgres-password", Schemas: []string{"app"}, GrantCurrentUser: true,
	}
	saved, err := f.s.SaveConnection(testCtx, f.trace(), "", input)
	must(t, err)
	return f, runner, saved
}

func TestPostgreSQLConnectionSaveIsAtomicAndPreservesLowercaseIdentity(t *testing.T) {
	f := newFixture(t)
	runner := newPostgresCoreRunner()
	must(t, f.s.StartExecution(testCtx, runner, "postgres-profile-test-pin", f.dir, artifact.DefaultLimits()))
	t.Cleanup(f.s.StopExecution)
	in := ConnectionInput{
		Target: TargetInput{Engine: "postgresql", Name: "PG fixture", Environment: "DEV", Host: "localhost", Port: 5432,
			DatabaseName: "app_db", Transport: "TCPS", Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 2)}},
		Username: "app_role", Password: "test-postgres-password", Schemas: []string{"app"}, GrantCurrentUser: true,
	}
	saved, err := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, err)
	if saved.Target.Engine != "postgresql" || saved.Target.DatabaseName != "app_db" || saved.Target.Service != "" || saved.Target.Transport != "TCPS" || saved.Account.Username != "app_role" || len(saved.Schemas) != 1 || saved.Schemas[0].Name != "app" {
		t.Fatalf("PostgreSQL identity was not preserved: target=%+v account=%+v schemas=%+v", saved.Target, saved.Account, saved.Schemas)
	}
	var dbName, engine, username string
	must(t, f.db.SQL.QueryRow(`SELECT t.engine,t.database_name,a.username FROM database_targets t JOIN execution_accounts a ON a.target_id=t.id WHERE t.id=?`, saved.Target.ID).Scan(&engine, &dbName, &username))
	if engine != "postgresql" || dbName != "app_db" || username != "app_role" {
		t.Fatalf("stored identity changed: %s %s %s", engine, dbName, username)
	}

	_, err = f.db.SQL.Exec(`CREATE TRIGGER reject_pg_schema BEFORE INSERT ON target_schemas BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`)
	must(t, err)
	failed := in
	failed.Target.Name = "must roll back"
	failed.Target.DatabaseName = "other_db"
	failed.Target.Profile = &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 3)}
	_, err = f.s.SaveConnection(testCtx, f.trace(), "", failed)
	if err == nil {
		t.Fatal("injected schema insert failure unexpectedly saved")
	}
	var count int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM database_targets WHERE name='must roll back'`).Scan(&count))
	if count != 0 {
		t.Fatal("failed PostgreSQL save left a target behind")
	}
	for _, table := range []string{"execution_accounts", "target_connection_profiles"} {
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM "+table+" WHERE target_id=?", "nonexistent").Scan(&count))
		if count != 0 {
			t.Fatalf("unexpected fixture row in %s", table)
		}
	}
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM credential_secrets c JOIN execution_accounts a ON a.id=c.account_id WHERE a.name='must roll back'`).Scan(&count))
	if count != 0 {
		t.Fatal("failed PostgreSQL save left credential material behind")
	}
}

func TestPostgreSQLSaveRejectsUnsupportedIdentityAndEngineChanges(t *testing.T) {
	f, runner, saved := postgresCoreFixture(t, "DEV")
	f.s.oracleProbe = func(context.Context, oracle.Config) (oracle.Result, error) {
		return oracle.Result{AuthenticationState: "verified", Schemas: []oracle.SchemaCandidate{{Name: "APP", MigrationCapability: "supported"}}, Warnings: []string{}}, nil
	}
	bad := newPostgresCoreRunner()
	bad.probe.ErrorCode = "UNSUPPORTED_POSTGRESQL_VERSION"
	bad.probe.AuthenticationState = "verified"
	f.s.execution.runner = bad
	edit := ConnectionInput{Target: TargetInput{Engine: "postgresql", Name: "changed", Environment: "DEV", Host: "localhost", Port: 5432, DatabaseName: "app_db", Transport: "TCPS"}, AccountID: saved.Account.ID, Username: "app_role", Schemas: []string{"app"}}
	_, err := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, edit)
	status(t, err, 409)

	f.s.execution.runner = runner
	toOracle := edit
	toOracle.Target = TargetInput{Name: "changed", Environment: "DEV", Host: "localhost", Port: 1521, Service: "APP", Transport: "TCP"}
	_, err = f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, toOracle)
	status(t, err, 400)

	oracleTarget := f.target(t, "oracle-immutable", "DEV")
	_, err = f.s.SaveConnection(testCtx, f.trace(), oracleTarget.ID, ConnectionInput{Target: TargetInput{Engine: "postgresql", Name: "oracle-immutable", Environment: "DEV", Host: "localhost", Port: 5432, DatabaseName: "app_db", Transport: "TCPS", Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 4)}}, Username: "app_role", Password: "pw", Schemas: []string{"app"}})
	status(t, err, 400)
}

func TestPostgreSQLProfileRevisionCiphertextAndStreamIdentity(t *testing.T) {
	f, _, saved := postgresCoreFixture(t, "DEV")
	var beforeCipher []byte
	must(t, f.db.SQL.QueryRow(`SELECT payload FROM credential_secrets WHERE account_id=?`, saved.Account.ID).Scan(&beforeCipher))
	var beforeProfileRevision, targetRevision int
	must(t, f.db.SQL.QueryRow(`SELECT revision FROM target_connection_profiles WHERE target_id=?`, saved.Target.ID).Scan(&beforeProfileRevision))
	must(t, f.db.SQL.QueryRow(`SELECT profile_revision FROM database_targets WHERE id=?`, saved.Target.ID).Scan(&targetRevision))

	edit := ConnectionInput{Target: saved.Target.TargetInputForTest(), AccountID: saved.Account.ID, Username: "app_role", Schemas: []string{"app"}}
	edit.Target.Name = "metadata only"
	unchanged, err := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, edit)
	must(t, err)
	var afterCipher []byte
	must(t, f.db.SQL.QueryRow(`SELECT payload FROM credential_secrets WHERE account_id=?`, saved.Account.ID).Scan(&afterCipher))
	var unchangedRevision int
	must(t, f.db.SQL.QueryRow(`SELECT revision FROM target_connection_profiles WHERE target_id=?`, saved.Target.ID).Scan(&unchangedRevision))
	if string(beforeCipher) != string(afterCipher) || unchanged.Account.Credential.Version != 1 || unchangedRevision != beforeProfileRevision {
		t.Fatal("metadata-only edit changed credential ciphertext/version or trust revision")
	}

	artifactRecord, err := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__profile.sql", Bytes: []byte("SELECT 1;\n")}})
	must(t, err)
	run := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: artifactRecord.ID}
	plan, err := f.s.localPlan(testCtx, f.db.SQL, f.admin, run, "13.10.3", true)
	must(t, err)
	defer clear(plan.password)
	snapshotJSON, err := json.Marshal(plan.snapshot)
	must(t, err)
	text := string(snapshotJSON) + plan.configuration
	if plan.snapshot.SnapshotVersion != 2 || plan.snapshot.Target.DatabaseName != "app_db" || plan.snapshot.Profile == nil || plan.snapshot.Profile.CASHA256 == "" || !strings.Contains(plan.configuration, "app_db") || !strings.Contains(plan.configuration, "flyway_schema_history") || !strings.Contains(plan.configuration, "verify-full") || !strings.Contains(plan.configuration, "password") {
		t.Fatalf("incomplete PostgreSQL v2 stream identity: %+v config=%s", plan.snapshot, plan.configuration)
	}
	if (saved.Profile.CAPEM != "" && strings.Contains(text, saved.Profile.CAPEM)) || strings.Contains(text, "test-postgres-password") || strings.Contains(text, "ca.pem=") || strings.Contains(text, "sslrootcert=") || strings.Contains(text, "/tmp/") {
		t.Fatal("snapshot or stream identity contains secret bytes or private CA paths")
	}
	oldConfiguration := plan.configuration
	_, err = f.db.SQL.Exec(`INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin,held,reason) VALUES(?,?,?, ?,0,'')`, saved.Target.ID, saved.Schemas[0].ID, oldConfiguration, f.s.execution.pin)
	must(t, err)

	changed := edit
	changed.Target.Profile = &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: postgresTestCA(t, 5)}
	_, err = f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, changed)
	status(t, err, 409)

	// Without a stream, changing trust advances both profile revisions and the
	// v2 configuration identity. The same change is intentionally blocked above.
	_, err = f.db.SQL.Exec(`DELETE FROM migration_streams WHERE target_id=? AND schema_id=?`, saved.Target.ID, saved.Schemas[0].ID)
	must(t, err)
	updated, err := f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, changed)
	must(t, err)
	if updated.Profile == nil || updated.Profile.Revision != beforeProfileRevision+1 || updated.Target.ProfileRevision != targetRevision+1 {
		t.Fatalf("trust edit did not advance profile revision: profile=%+v target=%+v", updated.Profile, updated.Target)
	}
	newPlan, err := f.s.localPlan(testCtx, f.db.SQL, f.admin, run, "13.10.3", true)
	must(t, err)
	defer clear(newPlan.password)
	if newPlan.configuration == oldConfiguration || newPlan.snapshot.Profile.CASHA256 == plan.snapshot.Profile.CASHA256 {
		t.Fatal("CA trust change did not alter PostgreSQL v2 stream identity")
	}
}

func (x Target) TargetInputForTest() TargetInput {
	return TargetInput{Engine: x.Engine, Name: x.Name, Environment: x.Environment, Host: x.Host, Port: x.Port, DatabaseName: x.DatabaseName, Transport: x.Transport}
}

func TestPostgreSQLInfoRejectsCorruptTrustAndEnforcesProdGrant(t *testing.T) {
	f, runner, saved := postgresCoreFixture(t, "PROD")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", f.admin.ID, []Grant{{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, Read: true, Operate: true, Prod: true}}))
	artifactRecord, err := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, saved.Schemas[0].ID, []artifact.Input{{Name: "V1__info.sql", Bytes: []byte("SELECT 1;\n")}})
	must(t, err)
	run := RunInput{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: artifactRecord.ID}
	_, viewer, _ := f.user(t, "postgres-viewer", "Viewer")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", viewer.ID, []Grant{{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, Read: true}}))
	if _, err = f.s.RefreshInfo(testCtx, Trace{Actor: viewer}, run); err == nil {
		t.Fatal("PostgreSQL Viewer refreshed native Info")
	}
	if _, err = f.s.LatestInfo(testCtx, viewer, saved.Target.ID, saved.Schemas[0].ID, ""); err != nil {
		t.Fatalf("read-only Viewer could not read PostgreSQL Info: %v", err)
	}

	_, operator, _ := f.user(t, "postgres-operator", "Operator")
	grant := Grant{TargetID: saved.Target.ID, SchemaID: saved.Schemas[0].ID, Read: true, Operate: true}
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", operator.ID, []Grant{grant}))
	if _, err = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, run); err == nil {
		t.Fatal("PROD operation succeeded without explicit allow-prod grant")
	}
	grant.Prod = true
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", operator.ID, []Grant{grant}))
	if _, err = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, run); err != nil {
		t.Fatalf("explicit PROD grant did not authorize PostgreSQL Info: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "app:info" {
		t.Fatalf("unexpected runtime operations: %v", runner.calls)
	}
	unmapped, err := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: saved.Target.ID, Name: "unmapped role", Username: "other_role"})
	must(t, err)
	_, err = f.s.ReplaceCredential(testCtx, f.trace(), unmapped.ID, "other-role-password", false)
	must(t, err)
	badAccount := run
	badAccount.ExecutionAccountID = unmapped.ID
	if _, err = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, badAccount); err == nil {
		t.Fatal("PostgreSQL Info accepted an unmapped execution account")
	}
	if len(runner.calls) != 1 {
		t.Fatal("unmapped account reached the runtime")
	}

	_, err = f.db.SQL.Exec(`UPDATE target_connection_profiles SET ca_sha256=? WHERE target_id=?`, strings.Repeat("0", 64), saved.Target.ID)
	must(t, err)
	if _, err = f.s.RefreshInfo(testCtx, Trace{Actor: operator}, run); err == nil {
		t.Fatal("Info accepted a corrupted stored trust hash")
	}
	if len(runner.calls) != 1 {
		t.Fatal("corrupted profile reached the runtime")
	}
}
