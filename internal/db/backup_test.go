package db

import (
	"path/filepath"
	"testing"
)

// TestBackupTo checks the snapshot is a usable database, not just a file: the
// point of the command is that a copy taken during the event can be restored.
func TestBackupTo(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetOrCreateUser("vol@example.test"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := d.BackupTo(dest); err != nil {
		t.Fatal(err)
	}
	// Writing again after the snapshot must not change what was captured.
	if _, err := d.GetOrCreateUser("late@example.test"); err != nil {
		t.Fatal(err)
	}

	copyDB, err := Open(dest)
	if err != nil {
		t.Fatalf("the backup is not a readable database: %v", err)
	}
	defer copyDB.Close()
	n, err := copyDB.CountUsers("")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected the 1 account present at snapshot time, got %d", n)
	}
	if _, err := copyDB.GetUserByEmail("vol@example.test"); err != nil {
		t.Fatalf("the snapshot lost its data: %v", err)
	}

	// VACUUM INTO refuses an existing destination, which is what stops a cron
	// job from silently overwriting yesterday's backup.
	if err := d.BackupTo(dest); err == nil {
		t.Fatal("overwriting an existing backup should fail")
	}
}
