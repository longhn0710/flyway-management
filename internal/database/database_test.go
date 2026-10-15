package database

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigratesAndPersistsInstallationMetadata(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	first, err := Open(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.InstallationID
	if first.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", first.SchemaVersion)
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
	if second.SchemaVersion != 1 {
		t.Fatalf("schema version after restart = %d, want 1", second.SchemaVersion)
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
	if _, err := db.SQL.Exec("UPDATE schema_migrations SET version=99"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), dataDir); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Open error = %v, want newer schema rejection", err)
	}
}
