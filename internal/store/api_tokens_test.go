package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAPITokens(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	p, err := s.EnsureVolunteer(ctx, "lou@example.org", "Lou", "", "fr")
	if err != nil {
		t.Fatal(err)
	}

	admin, err := s.UpsertAdmin(ctx, "sam@example.org", "Sam")
	if err != nil {
		t.Fatal(err)
	}

	token, made, err := s.CreateAPIToken(ctx, p.ID, nil, "Claude", time.Hour)
	if err != nil || made.Staff {
		t.Fatalf("create: %v %+v", err, made)
	}

	// The token works, and its use is recorded; a wrong one does not.
	got, err := s.APITokenFor(ctx, token)
	if err != nil || got.PersonID != p.ID || got.LastUsedAt == nil {
		t.Fatalf("valid token: %v %+v", err, got)
	}

	for _, bad := range []string{"", "mgt_", token + "x", token[4:]} {
		if _, err := s.APITokenFor(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}

	// An expired token stops working.
	old, _, err := s.CreateAPIToken(ctx, p.ID, nil, "Old", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.APITokenFor(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired token accepted: %v", err)
	}

	// A staff token goes with the staff access.
	staffToken, made, err := s.CreateAPIToken(ctx, admin.PersonID, &admin.ID, "Staff", time.Hour)
	if err != nil || !made.Staff {
		t.Fatalf("staff token: %v %+v", err, made)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM admins WHERE id = ?`, admin.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.APITokenFor(ctx, staffToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("a staff token outlives the staff access: %v", err)
	}

	// Revoking: one's own only.
	list, err := s.ListAPITokens(ctx, p.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	if err := s.DeleteAPIToken(ctx, admin.PersonID, list[0].UID); !errors.Is(err, ErrNotFound) {
		t.Errorf("someone else's token revoked: %v", err)
	}

	if err := s.DeleteAPIToken(ctx, p.ID, got.UID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.APITokenFor(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked token works: %v", err)
	}
}
