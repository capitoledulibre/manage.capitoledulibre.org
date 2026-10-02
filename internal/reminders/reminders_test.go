package reminders

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"benevoles/internal/db"
)

// TestBodyNamesContact: the day-before recap says whom to reach, with the
// phone only when the manager chose to share it.
func TestBodyNamesContact(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Paris")
	evID, _ := d.CreateEvent("Test", "2026-11-21", "2026-11-22", nil, "Général")
	poleID, _ := d.CreatePole(db.Pole{EventID: evID, Name: "Buvette", AccessMode: db.ModePublic})
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, loc)
	slotID, _ := d.CreateSlot(db.Slot{PoleID: poleID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 2})
	mgr, _ := d.GetOrCreateUser("resp@example.test")
	d.UpdateProfile(mgr.ID, "Rémi Responsable", "0611223344")
	d.AddPoleManager(poleID, mgr.ID)
	vol, _ := d.GetOrCreateUser("vol@example.test")
	d.JoinPole(poleID, vol.ID)
	d.Signup(slotID, vol.ID)

	s := &Sender{DB: d, Loc: loc, BaseURL: "http://example.test", OrgName: "EC"}
	day := time.Date(2026, 11, 21, 0, 0, 0, 0, loc)
	body := func() string {
		slots, err := d.ListUserSlotsBetween(vol.ID, day, day.Add(24*time.Hour))
		if err != nil || len(slots) != 1 {
			t.Fatalf("slots: %v %v", slots, err)
		}
		return s.body(vol, day, slots)
	}
	b := body()
	if !strings.Contains(b, "Contact : Rémi Responsable, resp@example.test") {
		t.Fatalf("no contact in the recap:\n%s", b)
	}
	if strings.Contains(b, "0611223344") {
		t.Fatal("the phone is shown without the manager's consent")
	}
	d.SetSharePhone(mgr.ID, true)
	if b := body(); !strings.Contains(b, "resp@example.test, 0611223344") {
		t.Fatalf("shared phone missing:\n%s", b)
	}
}
