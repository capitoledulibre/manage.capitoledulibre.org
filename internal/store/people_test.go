package store

import (
	"context"
	"path/filepath"
	"testing"
)

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
