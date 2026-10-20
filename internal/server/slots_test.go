package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// TestSlotService drives the slots of a pole without HTTP.
func TestSlotService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	app := e.app

	actor := func(p *store.Person) *access.Actor {
		a, err := access.Load(ctx, e.st, p, false)
		mustOK(t, err)

		return a
	}

	lead := actor(f.lead)

	v, err := app.poleViewOf(ctx, lead, f.event, f.buvette)
	mustOK(t, err)

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)

	day := f.morning.StartsAt.Add(24 * time.Hour)
	at := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, paris)
	draft := slotDraft{Title: "Bar", Capacity: 2, StartsAt: at, EndsAt: at.Add(time.Hour)}

	// Running the pole is required, whoever calls.
	if _, _, err := app.createSlots(ctx, actor(nina), v, draft, slotSeries{}, false); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer creates slots: %v", err)
	}

	// A wrong draft is refused, and comes back as typed.
	bad := draft
	bad.Capacity = 0

	_, typed, err := app.createSlots(ctx, lead, v, bad, slotSeries{}, false)
	if rf, ok := refused(err); !ok || rf.Key != "vol.err.slot_capacity" || typed.Title != "Bar" {
		t.Errorf("bad capacity: %v %+v", err, typed)
	}

	// A series: four hours of one-hour slots, each with its tasks.
	n, _, err := app.createSlots(ctx, lead, v, draft, slotSeries{Until: at.Add(4 * time.Hour), Tasks: []string{"Caisse", " "}}, false)
	mustOK(t, err)

	if n != 4 {
		t.Fatalf("series: %d", n)
	}

	// Outside the edition's dates, a confirmation first.
	ev := *f.event
	first, last := f.morning.StartsAt, f.morning.StartsAt.Add(48*time.Hour)
	ev.StartsOn, ev.EndsOn = &first, &last
	outside := &poleView{Event: &ev, Pole: f.buvette, Manage: true}
	far := slotDraft{Capacity: 1, StartsAt: at.Add(30 * 24 * time.Hour), EndsAt: at.Add(30*24*time.Hour + time.Hour)}

	if _, _, err := app.createSlots(ctx, lead, outside, far, slotSeries{}, false); err == nil || err.Error() != outsideKey {
		t.Errorf("outside the edition: %v", err)
	}

	if n, _, err := app.createSlots(ctx, lead, outside, far, slotSeries{}, true); err != nil || n != 1 {
		t.Errorf("outside, confirmed: %d %v", n, err)
	}

	// Moving a slot tells who is on it.
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.overlap.ID, nina.ID))

	moved := slotDraft{Capacity: 3, StartsAt: f.overlap.StartsAt.Add(time.Hour), EndsAt: f.overlap.EndsAt.Add(time.Hour)}

	told, after, err := app.updateSlot(ctx, lead, v, f.overlap, moved, false)
	mustOK(t, err)

	if told != 1 || !after.StartsAt.Equal(moved.StartsAt) {
		t.Errorf("moved: told %d, %v", told, after.StartsAt)
	}

	e.mailed("nina@example.org")

	// Copying a day twice makes no doubles.
	to := day.Add(24 * time.Hour)

	created, skipped, err := app.copyDay(ctx, lead, v, day, to)
	mustOK(t, err)

	again, skippedAgain, err := app.copyDay(ctx, lead, v, day, to)
	mustOK(t, err)

	if created != 4 || skipped != 0 || again != 0 || skippedAgain != 4 {
		t.Errorf("day copy: %d %d, then %d %d", created, skipped, again, skippedAgain)
	}

	if _, _, err := app.copyDay(ctx, lead, v, day, day); err == nil {
		t.Error("a day copied onto itself")
	}

	// The generator's slots: a new place, and no doubles.
	plan := []plannedSlot{
		{Place: "Comptoir", StartsAt: at, EndsAt: at.Add(time.Hour), Capacity: 1},
		{Place: "Comptoir", StartsAt: at, EndsAt: at.Add(time.Hour), Capacity: 1},
	}

	n, err = app.createPlanned(ctx, lead, v, []string{"Comptoir"}, plan, nil)
	mustOK(t, err)

	if n != 1 {
		t.Errorf("planned: %d", n)
	}

	// Another pole's slot is out of reach, and a bad one stops all.
	other := &store.Slot{PoleID: f.accueil.ID, StartsAt: at, EndsAt: at.Add(time.Hour), Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, other))

	if _, err := app.deleteSlots(ctx, lead, v, []*store.Slot{f.morning, other}); !errors.Is(err, errForbidden) {
		t.Errorf("another pole's slot deleted: %v", err)
	}

	if _, err := e.st.GetSlot(ctx, f.morning.ID, 0); err != nil {
		t.Error("a refused deletion deleted a slot")
	}

	// Deleting tells who was on it.
	done, err := app.deleteSlots(ctx, lead, v, []*store.Slot{f.overlap})
	mustOK(t, err)

	if done.Slots != 1 || done.Signups != 1 || done.Told != 1 {
		t.Errorf("deleted: %+v", done)
	}

	e.mailed("nina@example.org", "supprimé")
}
