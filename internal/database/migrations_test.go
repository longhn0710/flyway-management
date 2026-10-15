package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
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
	initial, err := migrationFiles.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"migrations/001_initial.sql": &fstest.MapFile{Data: initial}}
	for name, contents := range additions {
		files["migrations/"+name] = &fstest.MapFile{Data: []byte(contents)}
	}
	return files
}

func TestParseMigrationsRejectsMissingAndInvalidVersions(t *testing.T) {
	for _, name := range []string{"003_gap.sql", "000_zero.sql", "002_.sql", "bad.sql"} {
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
		"missing last":      "DELETE FROM schema_migrations WHERE version=3",
		"wrong checkpoint":  "PRAGMA user_version=2",
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
			files := migrationFixture(t, map[string]string{"002_second.sql": "CREATE TABLE second(value TEXT)", "003_third.sql": "CREATE TABLE IF NOT EXISTS third(value TEXT)"})
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
	files := migrationFixture(t, map[string]string{"002_second.sql": "CREATE TABLE second(value TEXT); INSERT INTO second VALUES('persisted');", "003_third.sql": "CREATE TABLE partial(value TEXT); INVALID SQL"})
	if _, err := migrateFS(context.Background(), db.SQL, files); err == nil {
		t.Fatal("expected SQL failure")
	}
	var count int
	if err := db.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('second', 'partial')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial schema survived: %d %v", count, err)
	}
	if err := db.SQL.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
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
	files["migrations/003_third.sql"].Data = []byte("CREATE TABLE third(value TEXT)")
	if version, err := migrateFS(context.Background(), db.SQL, files); err != nil || version != 3 {
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
