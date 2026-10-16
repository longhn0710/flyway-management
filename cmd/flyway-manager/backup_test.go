package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"flyway-manager/internal/database"
)

func TestBackupIncludesLiveWALAndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLYWAY_MANAGER_CONFIG", "")
	t.Setenv("FLYWAY_MANAGER_DATA_DIR", dir)
	db, err := database.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.Exec("CREATE TABLE backup_probe(value TEXT); INSERT INTO backup_probe VALUES('committed-in-wal')"); err != nil {
		t.Fatal(err)
	}
	if info, e := os.Stat(filepath.Join(dir, "manager.db-wal")); e != nil || info.Size() == 0 {
		t.Fatal("expected live WAL", e)
	}
	destination := filepath.Join(dir, "snapshot.db")
	if err = backupData([]string{"--output", destination}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	copyDB, err := sql.Open("sqlite", destination+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var value string
	if err = copyDB.QueryRow("SELECT value FROM backup_probe").Scan(&value); err != nil || value != "committed-in-wal" {
		t.Fatal(value, err)
	}
	if _, err = db.SQL.Exec("INSERT INTO backup_probe VALUES('after-snapshot')"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = copyDB.QueryRow("SELECT count(*) FROM backup_probe").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = backupData([]string{"--output", destination}); err == nil {
		t.Fatal("must refuse existing destination")
	}
}

func TestBackupDoesNotCreateMissingSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLYWAY_MANAGER_CONFIG", "")
	t.Setenv("FLYWAY_MANAGER_DATA_DIR", dir)
	if err := backupData([]string{"--output", filepath.Join(dir, "snapshot.db")}); err == nil {
		t.Fatal("must reject missing source")
	}
	if _, err := os.Stat(filepath.Join(dir, "manager.db")); !os.IsNotExist(err) {
		t.Fatal("backup created a source database")
	}
}
