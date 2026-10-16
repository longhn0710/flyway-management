package database

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"database/sql"
	"fmt"
	"io/fs"
)

func TestOpenMigratesAndPersistsInstallationMetadata(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	first, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.InstallationID
	if first.SchemaVersion != 8 {
		t.Fatalf("schema version = %d, want 8", first.SchemaVersion)
	}
	var tables int
	if err := first.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('schema_migrations','installation_metadata')").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 2 {
		t.Fatalf("migration created %d required tables, want 2", tables)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, "manager.db")
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0644); err != nil {
		t.Fatal(err)
	}
	second, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.InstallationID != firstID {
		t.Fatalf("installation ID changed after restart: %q != %q", second.InstallationID, firstID)
	}
	if second.SchemaVersion != 8 {
		t.Fatalf("schema version after restart = %d, want 8", second.SchemaVersion)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("data dir permissions = %o, want 700", info.Mode().Perm())
	}
	info, err = os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestOpenRejectsDatabaseFromNewerSchemaVersion(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec("UPDATE schema_migrations SET version=99 WHERE version=8"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), dataDir); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Open error = %v, want newer schema rejection", err)
	}
}

func migrationSet(t *testing.T, through int) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	for version := 1; version <= through; version++ {
		name := fmt.Sprintf("%03d_", version)
		entries, err := fs.ReadDir(migrationFiles, "migrations")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), name) {
				data, err := fs.ReadFile(migrationFiles, "migrations/"+entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				files["migrations/"+entry.Name()] = &fstest.MapFile{Data: data}
			}
		}
	}
	return files
}

func legacyV7DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateFS(context.Background(), db, migrationSet(t, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO database_targets(id,name,environment,host,port,service,transport,created_at) VALUES('target-1','Oracle','DEV','db.example',1521,'FREEPDB1','TCPS',123)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO execution_accounts(id,target_id,name,username,created_at) VALUES('account-1','target-1','app','APP',124)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO credential_secrets(account_id,payload,version,updated_at) VALUES('account-1',x'00ff7f80',1,125)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO target_schemas(id,target_id,name,history_schema,history_table,created_at) VALUES('schema-1','target-1','APP','APP','flyway_schema_history',126)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV8UpgradePreservesPopulatedV7AndEnforcesEngineProfiles(t *testing.T) {
	db := legacyV7DB(t)
	defer db.Close()
	var beforeID, beforePayload string
	if err := db.QueryRow(`SELECT t.id||'|'||t.name, hex(c.payload) FROM database_targets t JOIN credential_secrets c ON c.account_id='account-1' WHERE t.id='target-1'`).Scan(&beforeID, &beforePayload); err != nil {
		t.Fatal(err)
	}
	version, err := migrateFS(context.Background(), db, migrationSet(t, 8))
	if err != nil || version != 8 {
		t.Fatalf("v8 migration = %d, %v", version, err)
	}
	var idName, payload, engine, databaseName string
	if err := db.QueryRow(`SELECT t.id||'|'||t.name, hex(c.payload), t.engine, t.database_name FROM database_targets t JOIN credential_secrets c ON c.account_id='account-1' WHERE t.id='target-1'`).Scan(&idName, &payload, &engine, &databaseName); err != nil {
		t.Fatal(err)
	}
	if idName != beforeID || payload != beforePayload || engine != "oracle" || databaseName != "" {
		t.Fatalf("legacy data changed: %q %q %q %q", idName, payload, engine, databaseName)
	}
	if _, err := db.Exec(`INSERT INTO database_targets(id,name,environment,host,port,service,transport,engine,database_name,created_at) VALUES('pg1','PG one','DEV','pg.example',5432,'','TCPS','postgresql','appdb',1),('pg2','PG two','DEV','pg.example',5432,'','TCP','postgresql','otherdb',1)`); err != nil {
		t.Fatalf("distinct database targets rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO target_connection_profiles(target_id,auth_kind,tls_mode,ca_pem,ca_sha256,revision) VALUES('pg1','password','verify-full','-----BEGIN CERTIFICATE-----x',printf('%064d',0),1)`); err != nil {
		t.Fatalf("valid connection profile rejected: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO target_connection_profiles(target_id,auth_kind,tls_mode,ca_pem,ca_sha256,revision) VALUES('missing','password','verify-full','x',printf('%064d',0),1)`,
		`INSERT INTO target_connection_profiles(target_id,auth_kind,tls_mode,ca_pem,ca_sha256,revision) VALUES('pg2','password','disable','x',printf('%064d',0),1)`,
		`INSERT INTO target_connection_profiles(target_id,auth_kind,tls_mode,ca_pem,ca_sha256,revision) VALUES('pg2','password','verify-full','x',printf('%064d',0),0)`,
		`INSERT INTO database_targets(id,name,environment,host,port,service,transport,engine,database_name,created_at) VALUES('pg3','PG dup','DEV','pg.example',5432,'','TCP','postgresql','appdb',1)`,
		`INSERT INTO database_targets(id,name,environment,host,port,service,transport,created_at) VALUES('ora-dup','Oracle duplicate','DEV','db.example',1521,'FREEPDB1','TCP',1)`,
		`INSERT INTO database_targets(id,name,environment,host,port,service,transport,engine,database_name,created_at) VALUES('bad','Bad','DEV','x',5432,'svc','TCP','postgresql','appdb',1)`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("constraint accepted invalid statement: %s", stmt)
		}
	}
	var violations int
	if err := db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign key violations=%d err=%v", violations, err)
	}
}

func TestV8UpgradeRejectsCustomTargetObjectsAndRestoresForeignKeys(t *testing.T) {
	db := legacyV7DB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE INDEX custom_target_idx ON database_targets(host)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateFS(context.Background(), db, migrationSet(t, 8)); err == nil || !strings.Contains(err.Error(), "unexpected custom indexes or triggers") {
		t.Fatalf("migration error = %v", err)
	}
	var version, fk int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if version != 7 || fk != 1 {
		t.Fatalf("rollback state version=%d foreign_keys=%d", version, fk)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM database_targets WHERE id='target-1'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("v7 target after rollback count=%d err=%v", count, err)
	}
}

func TestRelativeDataDirectoryUsesLocalFilesystemPath(t *testing.T) {
	// Do not change the process working directory: tests may run concurrently.
	dataDir, err := filepath.Rel(mustWorkingDir(t), filepath.Join(t.TempDir(), "relative data ? #"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(filepath.Join(dataDir, "manager.db")); err != nil {
		t.Fatal(err)
	}
}

func mustWorkingDir(t *testing.T) string {
	t.Helper()
	path, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLitePragmasAndSidecarPermissions(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for pragma, want := range map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "2"} {
		var value string
		if err := db.SQL.QueryRow("PRAGMA " + pragma).Scan(&value); err != nil || value != want {
			t.Fatalf("%s = %s, want %s: %v", pragma, value, want, err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(filepath.Join(dataDir, "manager.db"+suffix))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %o", suffix, info.Mode().Perm())
		}
	}
}

func TestOpenRejectsSymlinkedSQLitePathsWithoutChangingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	for _, suffix := range []string{"directory", "", "-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			dataDir := filepath.Join(root, "data")
			link := filepath.Join(dataDir, "manager.db"+suffix)
			if suffix == "directory" {
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
				link = dataDir
			} else {
				if err := os.Mkdir(dataDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("preserve me"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if db, err := Open(context.Background(), dataDir); err == nil {
				db.Close()
				t.Fatal("accepted symlink")
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if before.Mode() != after.Mode() || before.Size() != after.Size() {
				t.Fatal("symlink target modified")
			}
		})
	}
}

func TestSQLiteWALRecoveryAfterProcessExit(t *testing.T) {
	// This subprocess intentionally exits without closing SQLite. Only its isolated
	// temporary data directory is affected; committed WAL must survive and recover.
	if dataDir := os.Getenv("FLYWAY_MANAGER_TEST_CRASH_DIR"); dataDir != "" {
		db, err := Open(context.Background(), dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.Exec("PRAGMA wal_autocheckpoint=0; INSERT INTO installation_metadata VALUES('crash_test', 'committed')"); err != nil {
			t.Fatal(err)
		}
		// Leave a second, uncommitted transaction behind as well.
		tx, err := db.SQL.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO installation_metadata VALUES('unfinished', 'discard')"); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	dataDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteWALRecoveryAfterProcessExit$")
	cmd.Env = append(os.Environ(), "FLYWAY_MANAGER_TEST_CRASH_DIR="+dataDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v: %s", err, output)
	}
	info, err := os.Stat(filepath.Join(dataDir, "manager.db-wal"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected unrecovered WAL: %v", err)
	}
	db, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	id := db.InstallationID
	var value string
	if err := db.SQL.QueryRow("SELECT value FROM installation_metadata WHERE key='crash_test'").Scan(&value); err != nil || value != "committed" {
		t.Fatalf("lost WAL commit: %s %v", value, err)
	}
	var count int
	if err := db.SQL.QueryRow("SELECT count(*) FROM installation_metadata WHERE key='unfinished'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncommitted write survived: %d %v", count, err)
	}
	if err := db.SQL.QueryRow("PRAGMA integrity_check").Scan(&value); err != nil || value != "ok" {
		t.Fatalf("integrity = %s %v", value, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.InstallationID != id {
		t.Fatal("installation changed after recovery/restart")
	}
}
