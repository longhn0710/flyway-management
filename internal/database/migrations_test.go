package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"

	"flyway-manager/internal/secrets"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseMigrationsSortsNumerically(t *testing.T) {
	files := fstest.MapFS{
		"migrations/003_later.sql":  &fstest.MapFile{},
		"migrations/002_middle.sql": &fstest.MapFile{},
		"migrations/001_first.sql":  &fstest.MapFile{},
	}
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := parseMigrations(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 3 || migrations[0].version != 1 || migrations[1].version != 2 || migrations[2].version != 3 {
		t.Fatalf("migration order = %+v", migrations)
	}
}

func TestParseMigrationsRejectsDuplicateFutureVersions(t *testing.T) {
	files := fstest.MapFS{
		"migrations/001_initial.sql":     &fstest.MapFile{},
		"migrations/002_future.sql":      &fstest.MapFile{},
		"migrations/002_also_future.sql": &fstest.MapFile{},
	}
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseMigrations(entries); err == nil {
		t.Fatal("duplicate future migration version was accepted")
	}
}

func migrationFixture(t *testing.T, additions map[string]string) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		contents, e := migrationFiles.ReadFile("migrations/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		files["migrations/"+entry.Name()] = &fstest.MapFile{Data: contents}
	}
	for name, contents := range additions {
		files["migrations/"+name] = &fstest.MapFile{Data: []byte(contents)}
	}
	return files
}

func TestParseMigrationsRejectsMissingAndInvalidVersions(t *testing.T) {
	for _, name := range []string{"012_gap.sql", "000_zero.sql", "002_.sql", "bad.sql"} {
		t.Run(name, func(t *testing.T) {
			entries, err := fs.ReadDir(migrationFixture(t, map[string]string{name: "SELECT 1"}), "migrations")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseMigrations(entries); err == nil {
				t.Fatal("accepted invalid migration set")
			}
		})
	}
}

func TestMigrationsRejectModifiedAppliedSQLAndNames(t *testing.T) {
	for _, change := range []string{"contents", "name"} {
		t.Run(change, func(t *testing.T) {
			db, err := Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			files := migrationFixture(t, nil)
			if change == "contents" {
				files["migrations/001_initial.sql"].Data = append(files["migrations/001_initial.sql"].Data, []byte("\n-- edited")...)
			} else {
				files["migrations/001_renamed.sql"] = files["migrations/001_initial.sql"]
				delete(files, "migrations/001_initial.sql")
			}
			if _, err := migrateFS(context.Background(), db.SQL, files); err == nil || !strings.Contains(err.Error(), "modified") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMigrationsRejectInconsistentHistory(t *testing.T) {
	for name, sql := range map[string]string{
		"empty ledger":      "DELETE FROM schema_migrations",
		"missing first":     "DELETE FROM schema_migrations WHERE version=1",
		"missing last":      "DELETE FROM schema_migrations WHERE version=11",
		"wrong checkpoint":  "PRAGMA user_version=9",
		"missing middle":    "DELETE FROM schema_migrations WHERE version=2",
		"zero version":      "UPDATE schema_migrations SET version=0 WHERE version=1",
		"missing checksum":  "UPDATE schema_migrations SET checksum=NULL WHERE version=1",
		"wrong checksum":    "UPDATE schema_migrations SET checksum='untrusted' WHERE version=1",
		"invalid timestamp": "UPDATE schema_migrations SET applied_at='' WHERE version=1",
		"partial ledger":    "ALTER TABLE schema_migrations DROP COLUMN name",
	} {
		t.Run(name, func(t *testing.T) {
			db, err := Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			files := migrationFixture(t, map[string]string{"011_second.sql": "CREATE TABLE second(value TEXT)", "012_third.sql": "CREATE TABLE IF NOT EXISTS third(value TEXT)"})
			if _, err := migrateFS(context.Background(), db.SQL, files); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL.Exec(sql); err != nil {
				t.Fatal(err)
			}
			if _, err := migrateFS(context.Background(), db.SQL, files); err == nil {
				t.Fatal("accepted inconsistent history")
			}
		})
	}
}

func TestLegacyLedgerUpgradePreservesDataAndRejectsEditedBaseline(t *testing.T) {
	for _, edited := range []bool{false, true} {
		t.Run(fmt.Sprint(edited), func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			files := migrationFixture(t, nil)
			if _, err := db.Exec(string(files["migrations/001_initial.sql"].Data)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO schema_migrations VALUES(1, '2026-10-15T00:00:00Z'); INSERT INTO installation_metadata VALUES('installation_id', 'existing-installation')"); err != nil {
				t.Fatal(err)
			}
			if edited {
				files["migrations/001_initial.sql"].Data = append(files["migrations/001_initial.sql"].Data, []byte("\n-- changed")...)
			}
			_, err = migrateFS(context.Background(), db, files)
			if edited {
				if err == nil {
					t.Fatal("accepted edited legacy migration")
				}
				var columns int
				if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('schema_migrations')").Scan(&columns); err != nil || columns != 2 {
					t.Fatalf("legacy upgrade was not rolled back: %d %v", columns, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var id, checksum string
			if err := db.QueryRow("SELECT value FROM installation_metadata WHERE key='installation_id'").Scan(&id); err != nil || id != "existing-installation" {
				t.Fatalf("installation changed: %s %v", id, err)
			}
			if err := db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); err != nil || checksum != legacyInitialChecksum {
				t.Fatalf("checksum = %q, %v", checksum, err)
			}
			if _, err := migrateFS(context.Background(), db, files); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationFailureRollsBackSchemaAndLedgerThenRecovers(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	files := migrationFixture(t, map[string]string{"011_second.sql": "CREATE TABLE second(value TEXT); INSERT INTO second VALUES('persisted');", "012_third.sql": "CREATE TABLE partial(value TEXT); INVALID SQL"})
	if _, err := migrateFS(context.Background(), db.SQL, files); err == nil {
		t.Fatal("expected SQL failure")
	}
	var count int
	if err := db.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('second', 'partial')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial schema survived: %d %v", count, err)
	}
	if err := db.SQL.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 10 {
		t.Fatalf("partial ledger survived: %d %v", count, err)
	}
	// A failed startup must be safe to reopen using the previous schema.
	id := db.InstallationID
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if db.InstallationID != id {
		t.Fatal("failed migration changed installation metadata")
	}
	files["migrations/012_third.sql"].Data = []byte("CREATE TABLE third(value TEXT)")
	if version, err := migrateFS(context.Background(), db.SQL, files); err != nil || version != 12 {
		t.Fatalf("retry: version %d, %v", version, err)
	}
	if _, err := migrateFS(context.Background(), db.SQL, files); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.SQL.QueryRow("SELECT value FROM second").Scan(&value); err != nil || value != "persisted" {
		t.Fatalf("value = %q, %v", value, err)
	}
}

func TestVersionThreeUpgradePreservesRegistryAndAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(t, nil)
	delete(files, "migrations/004_connection_wizard.sql")
	delete(files, "migrations/005_projects.sql")
	delete(files, "migrations/006_flyway_info.sql")
	delete(files, "migrations/007_sit_environment.sql")
	delete(files, "migrations/008_engine_profiles.sql")
	delete(files, "migrations/009_project_deployments.sql")
	delete(files, "migrations/010_oracle_corrections.sql")
	if version, err := migrateFS(context.Background(), legacy, files); err != nil || version != 3 {
		t.Fatal(version, err)
	}
	if _, err = legacy.Exec(`INSERT INTO installation_metadata VALUES('installation_id','prior-installation');
 INSERT INTO database_targets VALUES('prior','Prior','DEV','oracle.example',1521,'APP','TCP',1,1);
 INSERT INTO execution_accounts VALUES('account','prior','Prior account','APP',1,1);
 INSERT INTO target_schemas VALUES('schema','prior','APP','APP','flyway_schema_history','account',1,1);
 INSERT INTO account_schema_grants VALUES('account','schema','prior');
 INSERT INTO credential_secrets VALUES('account',X'010203',7,1,0);
 INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES('actor','user','Actor','prior.audit','prior',1,'success','request');`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if upgraded.SchemaVersion != 10 || upgraded.InstallationID != "prior-installation" {
		t.Fatal("installation changed")
	}
	var version int
	var payload []byte
	if err = upgraded.SQL.QueryRow("SELECT payload,version FROM credential_secrets WHERE account_id='account'").Scan(&payload, &version); err != nil || version != 7 || !bytes.Equal(payload, []byte{1, 2, 3}) {
		t.Fatal("credential changed", err)
	}
	for _, table := range []string{"database_targets", "target_schemas", "execution_accounts", "account_schema_grants", "audit_events"} {
		var count int
		if err = upgraded.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatal(table, count, err)
		}
	}
}

func openVersionSix(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v6.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(t, nil)
	delete(files, "migrations/007_sit_environment.sql")
	delete(files, "migrations/008_engine_profiles.sql")
	delete(files, "migrations/009_project_deployments.sql")
	delete(files, "migrations/010_oracle_corrections.sql")
	if v, err := migrateFS(context.Background(), db, files); err != nil || v != 6 {
		t.Fatalf("create v6 fixture: version=%d err=%v", v, err)
	}
	return db
}

func TestSITMigrationPreservesRegistryProjectsAndImmutableRunEvidence(t *testing.T) {
	db := openVersionSix(t)
	defer db.Close()
	_, err := db.Exec(`INSERT INTO database_targets VALUES('target','Target','UAT','db.example',1521,'APP','TCP',1,1);
INSERT INTO execution_accounts VALUES('account','target','Deployer','APP',1,1);
INSERT INTO credential_secrets VALUES('account',X'DEADBEEF',9,123,0);
INSERT INTO target_schemas VALUES('schema','target','APP','APP','flyway_schema_history','account',1,1);
INSERT INTO account_schema_grants VALUES('account','schema','target');
INSERT INTO users(id,username,display_name,provider,subject,password_hash,role,active,created_at) VALUES('user','user','User','local','user','hash','Admin',1,1);
INSERT INTO permission_grants(user_id,target_id,schema_id,can_read,can_operate,allow_prod) VALUES('user','target','schema',1,1,0);
INSERT INTO projects VALUES('project','Project','description','active',1,1);
INSERT INTO project_members(project_id,target_id,schema_id) VALUES('project','target',NULL),('project','target','schema');
INSERT INTO database_targets VALUES('cascade','Cascade','DEV','cascade.example',1521,'APP','TCP',1,1);
INSERT INTO project_members(project_id,target_id,schema_id) VALUES('project','cascade',NULL);
INSERT INTO artifacts VALUES('artifact','target','schema','user','user','User',1,'hash',0,'[]');
INSERT INTO artifact_blobs VALUES('artifact',0,'V1__one.sql','hash',X'01020304');
INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin) VALUES('target','schema','{}','pin');
INSERT INTO migration_runs(id,target_id,schema_id,artifact_id,execution_account_id,actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,source,request_id,request,snapshot,runtime_pin,created_at,status,migration_outcome,repair_outcome,reason,flyway_version)
VALUES('run','target','schema','artifact','account','user','user','User','Admin','','','WEB','request','{"target_id":"target"}','{"snapshot":"immutable"}','pin',2,'SUCCEEDED','','SKIPPED','','11.20.0');`)
	if err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(t, nil)
	if v, err := migrateFS(context.Background(), db, files); err != nil || v != 10 {
		t.Fatalf("upgrade: version=%d err=%v", v, err)
	}
	var env string
	if err = db.QueryRow("SELECT environment FROM database_targets WHERE id='target'").Scan(&env); err != nil || env != "UAT" {
		t.Fatalf("target preserved: %q %v", env, err)
	}
	if _, err = db.Exec("UPDATE database_targets SET environment='SIT' WHERE id='target'"); err != nil {
		t.Fatalf("SIT rejected: %v", err)
	}
	var secret []byte
	var secretVersion int
	var updated int64
	if err = db.QueryRow("SELECT payload,version,updated_at FROM credential_secrets WHERE account_id='account'").Scan(&secret, &secretVersion, &updated); err != nil || !bytes.Equal(secret, []byte{0xde, 0xad, 0xbe, 0xef}) || secretVersion != 9 || updated != 123 {
		t.Fatalf("credential changed: %x %d %d %v", secret, secretVersion, updated, err)
	}
	var request, snapshot string
	if err = db.QueryRow("SELECT request,snapshot FROM migration_runs WHERE id='run'").Scan(&request, &snapshot); err != nil || request != "{\"target_id\":\"target\"}" || snapshot != "{\"snapshot\":\"immutable\"}" {
		t.Fatalf("run snapshot changed: %s %s %v", request, snapshot, err)
	}
	var blob []byte
	if err = db.QueryRow("SELECT content FROM artifact_blobs WHERE artifact_id='artifact' AND input_index=0").Scan(&blob); err != nil || !bytes.Equal(blob, []byte{1, 2, 3, 4}) {
		t.Fatalf("artifact blob changed: %x %v", blob, err)
	}
	for _, table := range []string{"execution_accounts", "target_schemas", "account_schema_grants", "permission_grants", "projects", "artifacts", "artifact_blobs", "migration_runs"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("%s lost: %d %v", table, n, err)
		}
	}
	var members int
	if err = db.QueryRow("SELECT count(*) FROM project_members WHERE project_id='project'").Scan(&members); err != nil || members != 3 {
		t.Fatalf("project membership changed: %d %v", members, err)
	}
	var triggers int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name IN ('run_snapshot_immutable','artifact_immutable_update')").Scan(&triggers); err != nil || triggers != 2 {
		t.Fatalf("immutable triggers changed: %d %v", triggers, err)
	}
	checks, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if checks.Next() {
		checks.Close()
		t.Fatal("foreign_key_check found a violation")
	}
	if err = checks.Err(); err != nil {
		checks.Close()
		t.Fatal(err)
	}
	if err = checks.Close(); err != nil {
		t.Fatal(err)
	}
	var fkEnabled int
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil || fkEnabled != 1 {
		t.Fatalf("foreign key enforcement not restored: %d %v", fkEnabled, err)
	}
	if _, err = db.Exec("DELETE FROM database_targets WHERE id='cascade'"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM project_members WHERE target_id='cascade'").Scan(&members); err != nil || members != 0 {
		t.Fatalf("membership cascade lost: %d %v", members, err)
	}
}

func TestSITMigrationRejectsCustomTargetIndexWithoutDroppingIt(t *testing.T) {
	db := openVersionSix(t)
	defer db.Close()
	if _, err := db.Exec("CREATE INDEX custom_target_name ON database_targets(name)"); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateFS(context.Background(), db, migrationFixture(t, nil)); err == nil || !strings.Contains(err.Error(), "custom indexes or triggers") {
		t.Fatalf("migration error = %v", err)
	}
	var count, version int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='custom_target_name'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("custom index lost: %d %v", count, err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("migration committed: version=%d %v", version, err)
	}
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys not restored: %d %v", enabled, err)
	}
}

func TestSITMigrationForeignKeyFailureRollsBackAndRestoresEnforcement(t *testing.T) {
	db := openVersionSix(t)
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects VALUES('project','Project','','active',1,1); INSERT INTO project_members(project_id,target_id,schema_id) VALUES('project','missing-target',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(t, nil)
	if _, err := migrateFS(context.Background(), db, files); err == nil || !strings.Contains(err.Error(), "foreign key violation") {
		t.Fatalf("migration error = %v", err)
	}
	var enabled, version, members int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys not restored: %d %v", enabled, err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("schema version changed after rollback: %d %v", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM project_members WHERE target_id='missing-target'").Scan(&members); err != nil || members != 1 {
		t.Fatalf("preexisting row changed: %d %v", members, err)
	}
	var sqlText string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='database_targets'").Scan(&sqlText); err != nil || strings.Contains(sqlText, "'SIT'") {
		t.Fatalf("table rebuild escaped rollback: %s %v", sqlText, err)
	}
}

func TestV9ProjectDeploymentsPreserveLegacyDataAndEnforceAttribution(t *testing.T) {
	db := legacyV7DB(t)
	defer db.Close()
	if version, err := migrateFS(context.Background(), db, migrationSet(t, 8)); err != nil || version != 8 {
		t.Fatalf("create v8 fixture: version=%d err=%v", version, err)
	}
	if _, err := db.Exec(`INSERT INTO account_schema_grants(account_id,schema_id,target_id) VALUES('account-1','schema-1','target-1');
INSERT INTO users(id,username,display_name,provider,subject,password_hash,role,active,created_at) VALUES('user','user','User','local','user','hash','Admin',1,1);
INSERT INTO projects VALUES('project','Project','description','active',1,1),('other-project','Other','','active',1,1);
INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES('user','user','User','legacy.event','run',1,'success','legacy-request');
INSERT INTO project_members(project_id,target_id,schema_id) VALUES('project','target-1','schema-1');
INSERT INTO artifacts VALUES('artifact','target-1','schema-1','user','user','User',1,'hash',0,'[]');
INSERT INTO artifact_blobs VALUES('artifact',0,'V1__one.sql','hash',X'01020304');
INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin) VALUES('target-1','schema-1','{}','pin');
INSERT INTO migration_runs(id,target_id,schema_id,artifact_id,execution_account_id,actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,source,request_id,request,snapshot,runtime_pin,created_at,status,migration_outcome,repair_outcome,reason,flyway_version)
VALUES('run','target-1','schema-1','artifact','account-1','user','user','User','Admin','','','WEB','request','{"target_id":"target-1"}','{"snapshot":"before-009"}','pin',2,'SUCCEEDED','','SKIPPED','','11.20.0');`); err != nil {
		t.Fatal(err)
	}
	if version, err := migrateFS(context.Background(), db, migrationSet(t, 9)); err != nil || version != 9 {
		t.Fatalf("upgrade v8 to v9: version=%d err=%v", version, err)
	}
	for table, want := range map[string]int{"projects": 2, "project_members": 1, "artifacts": 1, "artifact_blobs": 1, "migration_runs": 1, "project_stream_owners": 0, "project_identity_memberships": 0, "audit_events": 1} {
		var got int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s after upgrade = %d, want %d: %v", table, got, want, err)
		}
	}
	var integrity, payload, blob, legacyRequest string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	if err := db.QueryRow(`SELECT hex(payload) FROM credential_secrets WHERE account_id='account-1'`).Scan(&payload); err != nil || payload != "00FF7F80" {
		t.Fatalf("credential bytes changed: %s %v", payload, err)
	}
	if err := db.QueryRow(`SELECT hex(content) FROM artifact_blobs WHERE artifact_id='artifact'`).Scan(&blob); err != nil || blob != "01020304" {
		t.Fatalf("artifact bytes changed: %s %v", blob, err)
	}
	if err := db.QueryRow(`SELECT request_id FROM audit_events WHERE action='legacy.event'`).Scan(&legacyRequest); err != nil || legacyRequest != "legacy-request" {
		t.Fatalf("audit changed: %s %v", legacyRequest, err)
	}
	var snapshot string
	if err := db.QueryRow("SELECT snapshot FROM migration_runs WHERE id='run'").Scan(&snapshot); err != nil || snapshot != `{"snapshot":"before-009"}` {
		t.Fatalf("legacy run snapshot changed: %s %v", snapshot, err)
	}
	violations, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if violations.Next() {
		violations.Close()
		t.Fatal("foreign_key_check found violations after v9 migration")
	}
	if err = violations.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project_stream_owners(target_id,schema_id,project_id) VALUES('target-1','schema-1','project')`); err != nil {
		t.Fatal("first stream owner rejected:", err)
	}
	if _, err := db.Exec(`INSERT INTO project_stream_owners(target_id,schema_id,project_id) VALUES('target-1','schema-1','other-project')`); err == nil {
		t.Fatal("second project claimed an already-owned migration stream")
	}
	if _, err := db.Exec(`INSERT INTO deployment_targets VALUES('dt','project','Production','PROD','target-1','schema-1','account-1',1,1,3,3)`); err != nil {
		t.Fatal("valid deployment target rejected:", err)
	}
	if _, err := db.Exec(`UPDATE deployment_targets SET schema_id='other-schema' WHERE id='dt'`); err == nil || !strings.Contains(err.Error(), "immutable deployment destination") {
		t.Fatalf("deployment destination update error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO project_artifacts VALUES('artifact','project','dt',1,'input-hash','{}','user','user','upload-key','request-hash')`); err != nil {
		t.Fatal("artifact attribution rejected:", err)
	}
	if _, err := db.Exec(`INSERT INTO migration_preflights VALUES('preflight','project','dt','artifact',1,'user','user','fingerprint',1,4,5,'{}')`); err != nil {
		t.Fatal("preflight rejected:", err)
	}
	if _, err := db.Exec(`INSERT INTO deployment_targets VALUES('dt2','project','Second','DEV','target-1','schema-1','account-1',1,1,3,3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO migration_preflights VALUES('wrong-target','project','dt2','artifact',1,'user','user','fingerprint',1,4,5,'{}')`); err == nil {
		t.Fatal("preflight accepted an artifact attributed to a different deployment target")
	}
	if _, err := db.Exec(`INSERT INTO project_migration_runs VALUES('run','project','dt',2,'preflight','{}','request-hash')`); err == nil {
		t.Fatal("run accepted a different preflight revision")
	}
	if _, err := db.Exec(`INSERT INTO project_migration_runs VALUES('run','project','dt',1,'preflight','{}','request-hash')`); err != nil {
		t.Fatal("run attribution rejected:", err)
	}
	if _, err := db.Exec(`INSERT INTO execution_records VALUES('run',6,'record-hash','{}')`); err != nil {
		t.Fatal("execution record rejected:", err)
	}
	for _, statement := range []string{`UPDATE execution_records SET payload='changed' WHERE run_id='run'`, `DELETE FROM execution_records WHERE run_id='run'`} {
		if _, err := db.Exec(statement); err == nil || !strings.Contains(err.Error(), "immutable execution record") {
			t.Fatalf("execution record guard for %q returned %v", statement, err)
		}
	}
}

// Exercise the deployed v9 boundary separately from the older v8 attribution
// fixture. VACUUM INTO creates a consistent backup; only its isolated copy upgrades.
func TestV10BackupRestorePreservesV9AndEncryptedCredentials(t *testing.T) {
	db := legacyV7DB(t)
	defer db.Close()
	if _, err := migrateFS(context.Background(), db, migrationSet(t, 9)); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "master.key")
	store, err := secrets.NewLocal(keyPath, false)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("disposable-fixture-password")
	sealed, err := store.Seal("account-1", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE credential_secrets SET payload=? WHERE account_id='account-1'`, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(id,username,display_name,provider,subject,password_hash,role,active,created_at) VALUES('user','user','User','local','user','hash','Admin',1,1);
 INSERT INTO account_schema_grants(account_id,schema_id,target_id) VALUES('account-1','schema-1','target-1');
 INSERT INTO projects VALUES('project','Project','','active',1,1);
 INSERT INTO project_stream_owners VALUES('target-1','schema-1','project');
 INSERT INTO deployment_targets VALUES('dt','project','DEV','DEV','target-1','schema-1','account-1',1,1,1,1);
 INSERT INTO artifacts VALUES('artifact','target-1','schema-1','user','user','User',1,'hash',0,'[]');
 INSERT INTO artifact_blobs VALUES('artifact',0,'V6__failed.sql','hash',X'01020304');
 INSERT INTO project_artifacts VALUES('artifact','project','dt',1,'input-hash','{}','user','user','upload','request-hash');
 INSERT INTO migration_preflights VALUES('preflight','project','dt','artifact',1,'user','user','fingerprint',1,1,2,'{}');
 INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin,held,reason) VALUES('target-1','schema-1','{}','pin',1,'historical UNSAFE');
 INSERT INTO migration_runs(id,target_id,schema_id,artifact_id,execution_account_id,actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,source,request_id,request,snapshot,runtime_pin,created_at,status,migration_outcome,repair_outcome,reason,flyway_version)
 VALUES('run','target-1','schema-1','artifact','account-1','user','user','User','Admin','','','WEB','request','{}','{"snapshot":"immutable"}','pin',2,'NEEDS_REVIEW','FAILED','UNSAFE','original ORA-00957','13.10.3');
 INSERT INTO project_migration_runs VALUES('run','project','dt',1,'preflight','{}','request-hash');
 INSERT INTO execution_records VALUES('run',3,'record-hash','{"original":"failed"}');
 INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES('user','user','User','migration.repair','run',3,'UNSAFE','request');`); err != nil {
		t.Fatal(err)
	}
	if _, err = installationID(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	before := snapshotTables(t, db)
	sourceBefore := snapshotTables(t, db)
	dir := t.TempDir()
	backup := filepath.Join(dir, "manager.db")
	if _, err = db.Exec(`VACUUM INTO ?`, backup); err != nil {
		t.Fatal(err)
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredKey := filepath.Join(dir, "master.key")
	if err = os.WriteFile(restoredKey, keyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	clear(keyBytes)
	restored, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.SchemaVersion != 10 {
		t.Fatal(restored.SchemaVersion)
	}
	after := snapshotTables(t, restored.SQL)
	delete(before, "schema_migrations")
	delete(after, "schema_migrations")
	if got := after["oracle_correction_approvals"]; string(got) != "[]" {
		t.Fatalf("upgrade invented approvals: %s", got)
	}
	delete(after, "oracle_correction_approvals")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("v10 changed pre-existing rows, credential bytes, held state or frozen evidence")
	}
	restoredStore, err := secrets.NewLocal(restoredKey, true)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err = restored.SQL.QueryRow(`SELECT payload FROM credential_secrets WHERE account_id='account-1'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	decrypted, err := restoredStore.Open("account-1", ciphertext)
	if err != nil || !bytes.Equal(decrypted, plain) {
		t.Fatal("restored original key did not decrypt preserved credential", err)
	}
	clear(decrypted)
	var integrity string
	if err = restored.SQL.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	var violations int
	if err = restored.SQL.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatal(violations, err)
	}
	if _, err = migrateFS(context.Background(), restored.SQL, migrationSet(t, 9)); err == nil {
		t.Fatal("older v9 binary accepted upgraded database")
	}
	if !reflect.DeepEqual(snapshotTables(t, db), sourceBefore) {
		t.Fatal("source changed")
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 9 {
		t.Fatal("backup source changed", version, err)
	}
}

func snapshotTables(t *testing.T, db *sql.DB) map[string]json.RawMessage {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := map[string]json.RawMessage{}
	for _, name := range names {
		rows, err = db.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		records := [][]any{}
		for rows.Next() {
			record := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range record {
				dest[i] = &record[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			records = append(records, record)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		result[name], err = json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}
