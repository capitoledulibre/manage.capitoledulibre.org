package server

import (
	"context"
	"errors"
	"testing"

	"manage/internal/access"
	"manage/internal/store"
)

// TestSignupService drives the sign-ups without HTTP, as an API would: the
// service checks the rights itself, whoever calls it.
func TestSignupService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	app := e.app

	actor := func(p *store.Person) *access.Actor {
		a, err := access.Load(ctx, e.st, p, false)
		mustOK(t, err)

		return a
	}

	view := func(a *access.Actor, p *store.Pole) *poleView {
		v, err := app.poleViewOf(ctx, a, f.event, p)
		mustOK(t, err)

		return v
	}

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)

	omar, err := e.st.EnsureVolunteer(ctx, "omar@example.org", "Omar", "", "fr")
	mustOK(t, err)

	lead := actor(f.lead)

	// A volunteer signs up on a public pole, which they join on the way.
	out, err := app.signUp(ctx, actor(nina), view(actor(nina), f.buvette), f.morning, false)
	mustOK(t, err)

	if !out.Joined || out.Slot.Filled != 1 {
		t.Fatalf("sign-up: %+v", out)
	}

	// The slot is full now: a refusal, with its reason.
	_, err = app.signUp(ctx, actor(omar), view(actor(omar), f.buvette), f.morning, false)
	if rf, ok := refused(err); !ok || rf.Key != "vol.err.full" {
		t.Errorf("full slot: %v", err)
	}

	// An overlapping slot asks first, and does nothing.
	out, err = app.signUp(ctx, actor(nina), view(actor(nina), f.buvette), f.overlap, false)
	mustOK(t, err)

	if len(out.Overlaps) != 1 {
		t.Fatalf("overlap: %+v", out)
	}

	if on, _ := e.st.IsSignedUp(ctx, f.overlap.ID, nina.ID); on {
		t.Error("an overlap signed up without confirmation")
	}

	// A restricted pole is not seen, so not signed up on.
	if _, err := app.poleViewOf(ctx, actor(nina), f.event, f.secret); !errors.Is(err, errNotFound) {
		t.Errorf("restricted pole seen: %v", err)
	}

	// Signing others up takes running the pole, whatever the caller.
	v := view(lead, f.buvette)
	if _, err := app.signUpOthers(ctx, actor(nina), v, f.morning, []*store.Person{omar}); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer signs others up: %v", err)
	}

	// The lead overbooks, and the newcomer joins the pole.
	done, err := app.signUpOthers(ctx, lead, v, f.morning, []*store.Person{omar})
	mustOK(t, err)

	if len(done) != 1 || done[0].Person.ID != omar.ID {
		t.Fatalf("placed: %+v", done)
	}

	if sl, _ := e.st.GetSlot(ctx, f.morning.ID, 0); sl.Filled != 2 {
		t.Errorf("overbooked slot: %d", sl.Filled)
	}

	if member, _ := e.st.IsPoleMember(ctx, f.buvette.ID, omar.ID); !member {
		t.Error("the newcomer did not join the pole")
	}

	e.mailed("omar@example.org")

	// A lead cannot bring people into a restricted pole; nothing is done.
	secret := &poleView{Event: f.event, Pole: f.secret, Manage: true}
	mustOK(t, e.st.AddPoleManager(ctx, f.secret.ID, f.lead.ID))

	secretSlot := &store.Slot{PoleID: f.secret.ID, StartsAt: f.morning.StartsAt, EndsAt: f.morning.EndsAt, Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, secretSlot))

	if _, err := app.signUpOthers(ctx, actor(f.lead), secret, secretSlot, []*store.Person{nina}); !errors.Is(err, errForbidden) {
		t.Errorf("a lead brings someone into a restricted pole: %v", err)
	}

	// Taking someone off: the lead may, a volunteer may not.
	if err := app.takeOff(ctx, actor(nina), v, f.morning, omar); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer takes someone off: %v", err)
	}

	mustOK(t, app.takeOff(ctx, lead, v, f.morning, omar))
	e.mailed("omar@example.org", "annulée")

	// The history names who did what.
	history, _, err := e.st.ListPoleLog(ctx, f.buvette.ID, store.LogPage{})
	mustOK(t, err)

	byLead := 0
	for _, h := range history {
		if h.ActorID == f.lead.ID {
			byLead++
		}
	}

	if byLead != 2 {
		t.Errorf("entries by the lead: %d", byLead)
	}
}
