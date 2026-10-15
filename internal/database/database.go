package database

import (
	"context"
	"crypto/rand"
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
	version int
	name    string
}

func Open(ctx context.Context, dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("restrict data directory permissions: %w", err)
	}
	dbPath := filepath.Join(dataDir, "manager.db")
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}).String()
	dsn := fileURL + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
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
	if err := os.Chmod(dbPath, 0600); err != nil && !os.IsNotExist(err) {
		sqlDB.Close()
		return nil, fmt.Errorf("restrict database permissions: %w", err)
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

func migrate(ctx context.Context, db *sql.DB) (int, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return 0, fmt.Errorf("read migrations: %w", err)
	}
	migrations, err := parseMigrations(entries)
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return 0, fmt.Errorf("initialize migration ledger: %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	if len(migrations) > 0 && current > migrations[len(migrations)-1].version {
		return current, fmt.Errorf("database schema version %d is newer than supported version %d", current, migrations[len(migrations)-1].version)
	}
	for _, item := range migrations {
		if item.version <= current {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + item.name)
		if err != nil {
			return current, fmt.Errorf("read migration %q: %w", item.name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return current, fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err = tx.ExecContext(ctx, string(contents)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)", item.version, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err != nil {
			tx.Rollback()
			return current, fmt.Errorf("apply migration %d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return current, fmt.Errorf("commit migration %d: %w", item.version, err)
		}
		current = item.version
	}
	return current, nil
}

func parseMigrations(entries []fs.DirEntry) ([]migration, error) {
	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		prefix, description, ok := strings.Cut(name, "_")
		if !ok || description == "" || !strings.HasSuffix(name, ".sql") {
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
