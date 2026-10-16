package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"flyway-manager/internal/config"
)

// backupData takes a consistent SQLite snapshot, including committed WAL data.
// Encryption keys are deliberately backed up separately by the operator.
func backupData(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	output := flags.String("output", "", "new private SQLite snapshot path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || flags.NArg() != 0 {
		return errors.New("backup requires --output pointing to a new file")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	source, err := filepath.Abs(filepath.Join(cfg.DataDir, "manager.db"))
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("backup source must be an existing regular manager.db")
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	// Exclusive private creation prevents overwriting a backup or exposing bytes.
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	if err = f.Close(); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	sourceURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(source)}).String()
	db, err := sql.Open("sqlite", sourceURL+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err = db.ExecContext(ctx, "VACUUM main INTO ?", destination); err != nil {
		return fmt.Errorf("snapshot SQLite: %w", err)
	}
	snapshotURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(destination)}).String()
	copyDB, err := sql.Open("sqlite", snapshotURL+"?mode=ro")
	if err != nil {
		return err
	}
	defer copyDB.Close()
	var integrity string
	if err = copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("backup integrity check failed: %s: %v", integrity, err)
	}
	// Explicitly sync the snapshot independently of SQLite's connection settings.
	f, err = os.OpenFile(destination, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	parent, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	err = parent.Sync()
	_ = parent.Close()
	if err != nil {
		return err
	}
	complete = true
	return nil
}
