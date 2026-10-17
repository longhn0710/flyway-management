package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"flyway-manager/internal/database"
)

// TestPostgreSQLFoundationV7UpgradeRehearsal is an opt-in upgrade rehearsal
// against a private SQLite backup. It never starts execution workers or uses
// the Oracle probe/runtime.
func TestPostgreSQLFoundationV7UpgradeRehearsal(t *testing.T) {
	if os.Getenv("FWM_RUN_V7_UPGRADE_REHEARSAL") != "1" {
		t.Skip("set FWM_RUN_V7_UPGRADE_REHEARSAL=1 for the private populated-v7 rehearsal")
	}
	dataDir := os.Getenv("FWM_V7_UPGRADE_DATA_DIR")
	keyPath := os.Getenv("FWM_V7_UPGRADE_MASTER_KEY")
	if dataDir == "" || keyPath == "" {
		t.Fatal("private rehearsal paths are required")
	}
	dataInfo, err := os.Stat(dataDir)
	if err != nil || !dataInfo.IsDir() || dataInfo.Mode().Perm()&0077 != 0 {
		t.Fatal("rehearsal data directory must exist and be private")
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm()&0077 != 0 || keyInfo.Size() != 32 {
		t.Fatal("original external key must be a private regular 32-byte file")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("open and upgrade private v7 copy: %v", err)
	}
	defer db.Close()
	if db.SchemaVersion != 8 {
		t.Fatalf("upgraded schema version = %d, want 8", db.SchemaVersion)
	}
	service, err := NewWithMasterKeyFile(ctx, db, dataDir, "", keyPath)
	if err != nil {
		t.Fatalf("validate original encryption key and decrypt stored credentials: %v", err)
	}
	if service.execution != nil {
		t.Fatal("upgrade rehearsal unexpectedly configured execution")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "secrets", "master.key")); !os.IsNotExist(err) {
		t.Fatal("external-key rehearsal created a replacement local master key")
	}
	var credentialCount, ledgerCount int
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM credential_secrets").Scan(&credentialCount); err != nil {
		t.Fatal("count preserved credentials")
	}
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version BETWEEN 1 AND 8").Scan(&ledgerCount); err != nil {
		t.Fatal("count schema migration ledger")
	}
	if ledgerCount != 8 {
		t.Fatalf("migration ledger rows 1 through 8 = %d", ledgerCount)
	}
	var integrity string
	if err := db.SQL.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("upgraded SQLite integrity check failed")
	}
	var foreignKeyViolations int
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreignKeyViolations); err != nil || foreignKeyViolations != 0 {
		t.Fatal("upgraded SQLite foreign-key check failed")
	}
	t.Logf("schema v8 opened; encrypted credentials validated=%d; execution configured=false", credentialCount)
}
