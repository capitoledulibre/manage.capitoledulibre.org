package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestPeopleMigration converts a database at the schema of partners
// (version 7) and checks nothing is lost: one person per address, staff and
// contacts on the same ids, the staff owner of each partnership kept, the
// sessions still valid.
func TestPeopleMigration(t *testing.T) {
	ctx := context.Background()

	s, err := openDB(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.migrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) {
		t.Helper()

		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	t0, t1, t2 := "2025-01-01 10:00:00+00:00", "2025-06-01 10:00:00+00:00", "2025-09-01 10:00:00+00:00"

	exec(`INSERT INTO events (id, slug, name, year, created_at) VALUES (1, 'ec-2025', 'EC 2025', 2025, ?)`, t0)
	exec(`INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES (1, 'acme', 'Acme', ?, ?), (2, 'globex', 'Globex', ?, ?)`, t0, t0, t0, t0)
	// Sam is staff, and also a contact of Acme under the same address.
	exec(`INSERT INTO admins (id, uid, email, name, avatar_file, created_at, last_login_at) VALUES (5, 'admin-uid', 'sam@conf.test', 'Sam Staff', 'sam.png', ?, ?)`, t1, t1)
	// Ada manages two organizations; the Globex row is the most recent login.
	exec(`INSERT INTO managers (id, uid, organization_id, email, name, lang, invited_by, created_at, last_login_at) VALUES
		(10, 'm-acme-ada', 1, 'ada@acme.test', 'Ada A.', 'fr', 'staff', ?, ?),
		(11, 'm-globex-ada', 2, 'ada@acme.test', 'Ada Lovelace', 'en', 'staff', ?, ?),
		(12, 'm-acme-sam', 1, 'sam@conf.test', 'Sam', 'fr', '', ?, NULL)`, t0, t1, t0, t2, t0)
	exec(`INSERT INTO partnerships (id, organization_id, event_id, status, owner_admin_id, created_at, updated_at) VALUES (1, 1, 1, 'confirmed', 5, ?, ?)`, t0, t0)
	exec(`INSERT INTO sessions (token_hash, kind, manager_id, expires_at, created_at) VALUES ('h1', 'manager', 11, '2099-01-01 00:00:00+00:00', ?)`, t0)
	exec(`INSERT INTO sessions (token_hash, kind, admin_id, expires_at, created_at) VALUES ('h2', 'admin', 5, '2099-01-01 00:00:00+00:00', ?)`, t0)

	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	count := func(q string) int {
		t.Helper()

		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	if n := count(`SELECT COUNT(*) FROM people`); n != 2 {
		t.Fatalf("%d people, want 2 (one per address)", n)
	}

	if n := count(`SELECT COUNT(*) FROM partnerships WHERE owner_admin_id = 5`); n != 1 {
		t.Fatal("the staff owner of the partnership was lost")
	}

	if n := count(`SELECT COUNT(*) FROM sessions`); n != 2 {
		t.Fatalf("%d sessions left, want 2", n)
	}

	if n := count(`PRAGMA foreign_keys`); n != 1 {
		t.Fatal("foreign keys were left off after the migration")
	}

	sam, err := s.GetAdmin(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}

	if sam.UID != "admin-uid" || sam.Email != "sam@conf.test" || sam.Name != "Sam Staff" || sam.AvatarFile != "sam.png" {
		t.Fatalf("staff identity not carried over: %+v", sam)
	}

	ada, err := s.ManagersByEmail(ctx, "ada@acme.test")
	if err != nil || len(ada) != 2 {
		t.Fatalf("Ada should manage two organizations: %v %v", ada, err)
	}

	if ada[0].ID != 10 || ada[0].UID != "m-acme-ada" || ada[0].PersonID != ada[1].PersonID {
		t.Fatalf("manager ids or person not kept: %+v %+v", ada[0], ada[1])
	}

	// The most recently active contact row gave the name and language.
	if ada[0].Name != "Ada Lovelace" || ada[0].Lang != "en" {
		t.Fatalf("name or language from the wrong row: %+v", ada[0])
	}

	// Sam the contact is Sam the staff member: one person, the staff name.
	samContact, err := s.GetManager(ctx, 12)
	if err != nil || samContact.PersonID != sam.PersonID || samContact.Name != "Sam Staff" {
		t.Fatalf("Sam as a contact should be the same person: %+v %v", samContact, err)
	}
}

// TestManagerIdentityChange: changing a contact's address attaches the
// contact to the person who owns the new address; it does not rename an
// address the same person may use elsewhere.
func TestManagerIdentityChange(t *testing.T) {
	ctx := context.Background()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	acme := &Organization{Name: "Acme"}
	globex := &Organization{Name: "Globex"}
	_ = s.CreateOrganization(ctx, acme)
	_ = s.CreateOrganization(ctx, globex)

	a := &Manager{OrgID: acme.ID, Email: "ada@acme.test", Name: "Ada"}
	g := &Manager{OrgID: globex.ID, Email: "ada@acme.test"}

	for _, m := range []*Manager{a, g} {
		if _, err := s.AddManager(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	if a.PersonID != g.PersonID || g.Name != "Ada" {
		t.Fatalf("one address should be one person: %+v %+v", a, g)
	}

	a.Email, a.Name = "ada@new.test", "Ada L."
	if err := s.UpdateManagerIdentity(ctx, a); err != nil {
		t.Fatal(err)
	}

	still, _ := s.GetManager(ctx, g.ID)
	if still.Email != "ada@acme.test" {
		t.Fatalf("the Globex contact lost its address: %+v", still)
	}

	moved, _ := s.GetManager(ctx, a.ID)
	if moved.Email != "ada@new.test" || moved.Name != "Ada L." || moved.PersonID == g.PersonID {
		t.Fatalf("the Acme contact should now be the new address' person: %+v", moved)
	}

	// The same person cannot manage an organization twice.
	dup := &Manager{ID: g.ID, Email: "ada@new.test"}
	_, _ = s.AddManager(ctx, &Manager{OrgID: globex.ID, Email: "ada@new.test"})

	if err := s.UpdateManagerIdentity(ctx, dup); err != ErrDuplicate {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}
