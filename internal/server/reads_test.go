package server

import (
	"context"
	"errors"
	"testing"

	"manage/internal/access"
	"manage/internal/store"
)

// TestReadServices reads the volunteer side as each actor may, without
// HTTP.
func TestReadServices(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	app := e.app

	actor := func(p *store.Person) *access.Actor {
		a, err := access.Load(ctx, e.st, p, false)
		mustOK(t, err)

		return a
	}

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))

	staff, err := access.Load(ctx, e.st, nil, true)
	mustOK(t, err)

	lead, volunteer := actor(f.lead), actor(nina)

	// The poles listed: a restricted pole for its people only.
	listed := func(a *access.Actor) map[string]bool {
		coverage, _, err := app.poleCoverage(ctx, a, f.event)
		mustOK(t, err)

		out := map[string]bool{}
		for _, c := range coverage {
			out[c.Pole.Slug] = true
		}

		return out
	}

	if got := listed(volunteer); got[f.secret.Slug] || !got[f.buvette.Slug] || !got[f.accueil.Slug] {
		t.Errorf("listed for a volunteer: %v", got)
	}

	if got := listed(staff); !got[f.secret.Slug] {
		t.Errorf("listed for the staff: %v", got)
	}

	// A draft edition is not read by a volunteer.
	if _, _, err := app.poleCoverage(ctx, volunteer, f.draft); !errors.Is(err, errNotFound) {
		t.Errorf("a draft read: %v", err)
	}

	// The planning: hers, with her slot as hers.
	days, _, err := app.planning(ctx, volunteer, f.event, planQuery{View: "mine"})
	mustOK(t, err)

	if len(days) != 1 || len(days[0].Poles) != 1 || days[0].Poles[0].Slots[0].Slot.ID != f.morning.ID {
		t.Fatalf("her planning: %+v", days)
	}

	// Not a pole she may not see.
	if _, _, err := app.planning(ctx, volunteer, f.event, planQuery{Pole: f.secret}); !errors.Is(err, errNotFound) {
		t.Errorf("a restricted pole planned: %v", err)
	}

	// The late tasks are read on the poles one runs.
	if _, _, err := app.planning(ctx, volunteer, f.event, planQuery{View: "late", Pole: f.buvette}); !errors.Is(err, errNotFound) {
		t.Errorf("late tasks of a pole not run: %v", err)
	}

	if _, _, err := app.planning(ctx, lead, f.event, planQuery{View: "late", Pole: f.buvette}); err != nil {
		t.Errorf("late tasks of the lead's pole: %v", err)
	}

	// The members of a pole, for whoever runs it.
	v, err := app.poleViewOf(ctx, lead, f.event, f.buvette)
	mustOK(t, err)

	members, err := app.poleMembers(ctx, lead, v)
	mustOK(t, err)

	if len(members) < 2 {
		t.Errorf("members: %d", len(members))
	}

	if _, err := app.poleMembers(ctx, volunteer, v); !errors.Is(err, errForbidden) {
		t.Errorf("members read by a volunteer: %v", err)
	}

	// A volunteer's page and the logistics, for the coordination.
	if _, err := app.volunteerFileOf(ctx, lead, f.event, nina); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer's page read by a lead: %v", err)
	}

	file, err := app.volunteerFileOf(ctx, staff, f.event, nina)
	mustOK(t, err)

	if len(file.Slots) != 1 {
		t.Errorf("her slots: %d", len(file.Slots))
	}

	outsider, err := e.st.EnsureVolunteer(ctx, "out@example.org", "Out", "", "fr")
	mustOK(t, err)

	if _, err := app.volunteerFileOf(ctx, staff, f.draft, outsider); !errors.Is(err, errNotFound) {
		t.Errorf("the page of someone not in the edition: %v", err)
	}

	if _, err := app.logisticsOf(ctx, lead, f.event); !errors.Is(err, errForbidden) {
		t.Errorf("logistics read by a lead: %v", err)
	}

	if _, err := app.logisticsOf(ctx, staff, f.event); err != nil {
		t.Errorf("logistics for the staff: %v", err)
	}
}
