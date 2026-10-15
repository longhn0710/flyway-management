package database

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestParseMigrationsSortsNumerically(t *testing.T) {
	files := fstest.MapFS{
		"migrations/010_later.sql":  &fstest.MapFile{},
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
	if len(migrations) != 3 || migrations[0].version != 1 || migrations[1].version != 2 || migrations[2].version != 10 {
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
