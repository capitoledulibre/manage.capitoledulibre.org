package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePath(t *testing.T) {
	write := func(path, content string) {
		t.Helper()

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	read := func(path string) string {
		t.Helper()

		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		return string(b)
	}

	t.Run("a partners database is renamed with its WAL files", func(t *testing.T) {
		dir := t.TempDir()
		write(filepath.Join(dir, "partners.db"), "db")
		write(filepath.Join(dir, "partners.db-wal"), "wal")
		write(filepath.Join(dir, "partners.db-shm"), "shm")

		path, err := databasePath(dir)
		if err != nil || path != filepath.Join(dir, "manage.db") {
			t.Fatalf("%q %v", path, err)
		}

		for suffix, want := range map[string]string{"": "db", "-wal": "wal", "-shm": "shm"} {
			if got := read(path + suffix); got != want {
				t.Errorf("manage.db%s = %q, want %q", suffix, got, want)
			}

			if _, err := os.Stat(filepath.Join(dir, "partners.db"+suffix)); !os.IsNotExist(err) {
				t.Errorf("partners.db%s still there", suffix)
			}
		}
	})

	t.Run("an existing manage.db is never overwritten", func(t *testing.T) {
		dir := t.TempDir()
		write(filepath.Join(dir, "manage.db"), "current")
		write(filepath.Join(dir, "partners.db"), "old")

		path, err := databasePath(dir)
		if err != nil || read(path) != "current" || read(filepath.Join(dir, "partners.db")) != "old" {
			t.Fatalf("manage.db overwritten or partners.db moved: %v", err)
		}
	})

	t.Run("an empty directory gets manage.db", func(t *testing.T) {
		dir := t.TempDir()

		path, err := databasePath(dir)
		if err != nil || path != filepath.Join(dir, "manage.db") {
			t.Fatalf("%q %v", path, err)
		}
	})
}
