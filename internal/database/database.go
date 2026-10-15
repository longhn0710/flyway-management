package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"database/sql"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type DB struct {
	SQL            *sql.DB
	InstallationID string
	SchemaVersion  int
}

type migration struct {
	version  int
	name     string
	contents []byte
	checksum string
}

func Open(ctx context.Context, dataDir string) (*DB, error) {
	// Resolve before building a file: URL; relative URL paths can be interpreted as hosts.
	var err error
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	if info, err := os.Lstat(dataDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("data directory must not be a symlink: %s", dataDir)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("restrict data directory permissions: %w", err)
	}
	dbPath := filepath.Join(dataDir, "manager.db")
	// Check before SQLite opens or recovers any files. Never unlink WAL/SHM files:
	// committed data may still exist only in the WAL after an interrupted process.
	for _, name := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + "-journal"} {
		info, err := os.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect SQLite file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SQLite path must be a regular file: %s", name)
		}
		if err := os.Chmod(name, 0600); err != nil {
			return nil, fmt.Errorf("restrict SQLite file: %w", err)
		}
	}
	// Create privately before the driver can create the database and its sidecars.
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("prepare SQLite file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}).String()
	dsn := fileURL + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("connect SQLite: %w", err)
	}
	var journalMode string
	if err := sqlDB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "wal" {
		sqlDB.Close()
		return nil, fmt.Errorf("SQLite WAL mode unavailable (%q): %v", journalMode, err)
	}
	version, err := migrate(ctx, sqlDB)
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	id, err := installationID(ctx, sqlDB)
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{SQL: sqlDB, InstallationID: id, SchemaVersion: version}, nil
}

func (d *DB) Close() error { return d.SQL.Close() }

// legacyInitialChecksum pins the SQL shipped before the ledger recorded checksums.
// Do not derive it from current files: doing so would bless an edited migration.
const legacyInitialChecksum = "49c87e8530785fc8ddee33e18ce9beeb49655541add788f52b6dbf824aff922a"

func migrate(ctx context.Context, db *sql.DB) (int, error) {
	return migrateFS(ctx, db, migrationFiles)
}

func migrateFS(ctx context.Context, db *sql.DB, files fs.FS) (int, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return 0, fmt.Errorf("read migrations: %w", err)
	}
	migrations, err := parseMigrations(entries)
	if err != nil {
		return 0, err
	}
	for i := range migrations {
		item := &migrations[i]
		item.contents, err = fs.ReadFile(files, "migrations/"+item.name)
		if err != nil {
			return 0, fmt.Errorf("read migration %q: %w", item.name, err)
		}
		digest := sha256.Sum256(item.contents)
		item.checksum = hex.EncodeToString(digest[:])
	}
	// The ledger upgrade, history checks and pending SQL share one write transaction.
	// Failure leaves both the previous schema and ledger intact and can be retried.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL, name TEXT, checksum TEXT)`); err != nil {
		return 0, fmt.Errorf("initialize migration ledger: %w", err)
	}
	// Detect the original two-column Phase 1 ledger without rewriting migration 001.
	columns, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info('schema_migrations')")
	if err != nil {
		return 0, err
	}
	hasName, hasChecksum := false, false
	for columns.Next() {
		var name string
		if err := columns.Scan(&name); err != nil {
			columns.Close()
			return 0, err
		}
		hasName = hasName || name == "name"
		hasChecksum = hasChecksum || name == "checksum"
	}
	err = columns.Err()
	columns.Close()
	if err != nil {
		return 0, err
	}
	legacy := !hasName && !hasChecksum
	if hasName != hasChecksum {
		return 0, fmt.Errorf("inconsistent migration ledger columns")
	}
	if legacy {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE schema_migrations ADD COLUMN name TEXT; ALTER TABLE schema_migrations ADD COLUMN checksum TEXT"); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT version, applied_at, name, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	current := 0
	for rows.Next() {
		var version int
		var appliedAt string
		var name, checksum sql.NullString
		if err := rows.Scan(&version, &appliedAt, &name, &checksum); err != nil {
			rows.Close()
			return 0, err
		}
		if version > len(migrations) {
			rows.Close()
			return current, fmt.Errorf("database schema version %d is newer than supported version %d", version, len(migrations))
		}
		if version != current+1 {
			rows.Close()
			return current, fmt.Errorf("inconsistent migration history: expected version %d, got %d", current+1, version)
		}
		if _, err := time.Parse(time.RFC3339Nano, appliedAt); err != nil {
			rows.Close()
			return current, fmt.Errorf("invalid applied_at for migration %d", version)
		}
		item := migrations[version-1]
		if legacy {
			if version != 1 || item.name != "001_initial.sql" || item.checksum != legacyInitialChecksum {
				rows.Close()
				return current, fmt.Errorf("cannot verify legacy migration %d", version)
			}
		} else if !name.Valid || !checksum.Valid || name.String != item.name || checksum.String != item.checksum {
			rows.Close()
			return current, fmt.Errorf("applied migration %d was modified or has missing validation metadata", version)
		}
		current = version
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return current, err
	}
	// A separate transactional high-water mark detects deletion of the trailing
	// ledger row, which otherwise looks like a pending (possibly idempotent) SQL file.
	// Original Phase 1 databases did not set user_version and adopt it once here.
	var recordedVersion int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&recordedVersion); err != nil {
		return current, err
	}
	if recordedVersion != current && !(legacy && recordedVersion == 0) {
		return current, fmt.Errorf("inconsistent migration history: ledger version %d differs from SQLite user_version %d", current, recordedVersion)
	}
	if legacy && current == 1 {
		if _, err := tx.ExecContext(ctx, "UPDATE schema_migrations SET name=?, checksum=? WHERE version=1", migrations[0].name, legacyInitialChecksum); err != nil {
			return current, err
		}
	}
	// A surviving application table with an empty ledger is evidence of lost history.
	// Do not reapply CREATE IF NOT EXISTS and silently bless that state.
	if current == 0 {
		var tables int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'").Scan(&tables); err != nil {
			return current, err
		}
		if tables != 0 {
			return current, fmt.Errorf("inconsistent migration history: application tables exist without applied migrations")
		}
	}
	for _, item := range migrations[current:] {
		if _, err := tx.ExecContext(ctx, string(item.contents)); err != nil {
			return current, fmt.Errorf("apply migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at, name, checksum) VALUES(?, ?, ?, ?)", item.version, time.Now().UTC().Format(time.RFC3339Nano), item.name, item.checksum); err != nil {
			return current, fmt.Errorf("record migration %d: %w", item.version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(len(migrations))); err != nil {
		return current, err
	}
	if err := tx.Commit(); err != nil {
		return current, fmt.Errorf("commit schema migrations: %w", err)
	}
	return len(migrations), nil
}

func parseMigrations(entries []fs.DirEntry) ([]migration, error) {
	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		prefix, description, ok := strings.Cut(name, "_")
		if entry.IsDir() || !ok || description == ".sql" || description == "" || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("invalid migration filename %q; expected NNN_description.sql", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in filename %q", name)
		}
		if previous, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, name)
		}
		seen[version] = name
		migrations = append(migrations, migration{version: version, name: name})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i, item := range migrations {
		if item.version != i+1 {
			return nil, fmt.Errorf("missing migration version %d before %q", i+1, item.name)
		}
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no schema migrations found")
	}
	return migrations, nil
}

func installationID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, "SELECT value FROM installation_metadata WHERE key='installation_id'").Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("read installation ID: %w", err)
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return "", fmt.Errorf("create installation ID: %w", err)
	}
	id = hex.EncodeToString(randomID[:])
	if _, err := db.ExecContext(ctx, "INSERT INTO installation_metadata(key, value) VALUES('installation_id', ?)", id); err != nil {
		return "", fmt.Errorf("persist installation ID: %w", err)
	}
	return id, nil
}
