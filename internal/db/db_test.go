package db

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSignupFlow(t *testing.T) {
	d := openTest(t)
	edID, err := d.CreateEdition("Test", "2026-11-14", "2026-11-15", nil, "Organisation")
	if err != nil {
		t.Fatal(err)
	}
	poleID, err := d.CreatePole(Pole{EditionID: edID, Name: "Buvette", AccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	slotID, err := d.CreateSlot(Slot{PoleID: poleID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := d.GetOrCreateUser("alice@example.org")
	bob, _ := d.GetOrCreateUser("bob@example.org")

	// Joining a pole also joins the edition's default pole.
	if err := d.JoinPole(poleID, alice.ID); err != nil {
		t.Fatal(err)
	}
	def, _ := d.DefaultPole(edID)
	if !d.IsPoleMember(def.ID, alice.ID) {
		t.Fatal("alice should be a member of the default pole")
	}

	// Capacity is enforced.
	if ok, err := d.Signup(slotID, alice.ID); err != nil || !ok {
		t.Fatalf("alice signup: ok=%v err=%v", ok, err)
	}
	if ok, _ := d.Signup(slotID, alice.ID); !ok {
		t.Fatal("signing up twice should be idempotent")
	}
	if ok, _ := d.Signup(slotID, bob.ID); ok {
		t.Fatal("bob should be refused, slot is full")
	}
	slot, _ := d.GetSlot(slotID, alice.ID)
	if slot.Filled != 1 || !slot.Mine || !slot.IsFull() {
		t.Fatalf("unexpected slot state: %+v", slot)
	}

	// Overlap detection.
	overlaps, _ := d.OverlappingSlots(alice.ID, poleID, 0, start.Add(time.Hour), start.Add(3*time.Hour))
	if len(overlaps) != 1 {
		t.Fatalf("expected 1 overlap, got %d", len(overlaps))
	}
	overlaps, _ = d.OverlappingSlots(alice.ID, poleID, 0, start.Add(2*time.Hour), start.Add(3*time.Hour))
	if len(overlaps) != 0 {
		t.Fatalf("adjacent slots must not overlap, got %d", len(overlaps))
	}

	// Leaving the pole drops the signups.
	if err := d.LeavePole(poleID, alice.ID); err != nil {
		t.Fatal(err)
	}
	slot, _ = d.GetSlot(slotID, alice.ID)
	if slot.Filled != 0 {
		t.Fatal("leaving the pole should remove signups")
	}
}

func TestSignupsLocked(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name   string
		ed     Edition
		pole   *time.Time
		locked bool
	}{
		{"open, no freeze", Edition{Status: StatusOpen}, nil, false},
		{"open, edition freeze passed", Edition{Status: StatusOpen, FreezeAt: &past}, nil, true},
		{"open, edition freeze ahead", Edition{Status: StatusOpen, FreezeAt: &future}, nil, false},
		{"pole freeze overrides edition", Edition{Status: StatusOpen, FreezeAt: &future}, &past, true},
		{"pole freeze ahead overrides passed edition freeze", Edition{Status: StatusOpen, FreezeAt: &past}, &future, false},
		{"frozen status", Edition{Status: StatusFrozen}, nil, true},
		{"draft status", Edition{Status: StatusDraft}, nil, true},
	}
	for _, c := range cases {
		if got := c.ed.SignupsLocked(c.pole, now); got != c.locked {
			t.Errorf("%s: got %v, want %v", c.name, got, c.locked)
		}
	}
}

func TestMagicLinkSingleUse(t *testing.T) {
	d := openTest(t)
	u, _ := d.GetOrCreateUser("x@example.org")
	tok, err := d.CreateMagicLink(u.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := d.ConsumeMagicLink(tok); err != nil || id != u.ID {
		t.Fatalf("first use: id=%d err=%v", id, err)
	}
	if _, err := d.ConsumeMagicLink(tok); err == nil {
		t.Fatal("second use should fail")
	}
}
