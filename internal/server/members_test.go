package server

import (
	"context"
	"errors"
	"testing"

	"manage/internal/access"
	"manage/internal/store"
)

// TestMemberService drives the people of a pole without HTTP.
func TestMemberService(t *testing.T) {
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

	// A private pole takes its key: a wrong one is refused, naming the pole.
	err = app.joinPole(ctx, actor(nina), view(actor(nina), f.accueil), "wrong")
	if rf, ok := refused(err); !ok || rf.Key != "vol.err.bad_invite" || rf.Args[0] != "Accueil" {
		t.Fatalf("wrong key: %v", err)
	}

	mustOK(t, app.joinPole(ctx, actor(nina), view(actor(nina), f.accueil), f.accueil.AccessKey))

	if member, _ := e.st.IsPoleMember(ctx, f.accueil.ID, nina.ID); !member {
		t.Fatal("not joined with the key")
	}

	// The default pole is everyone's: nobody leaves it.
	def, err := e.st.DefaultPole(ctx, f.event.ID)
	mustOK(t, err)

	err = app.leavePole(ctx, actor(nina), view(actor(nina), def))
	if rf, ok := refused(err); !ok || rf.Key != "vol.err.default_pole" {
		t.Errorf("left the default pole: %v", err)
	}

	mustOK(t, app.leavePole(ctx, actor(nina), view(actor(nina), f.accueil)))

	// Adding people takes running the pole; a restricted one, the
	// coordination.
	lead := actor(f.lead)
	buvette := view(lead, f.buvette)

	if _, err := app.addMembers(ctx, actor(nina), buvette, []newMember{{Email: "x@example.org"}}, "fr"); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer adds members: %v", err)
	}

	added, err := app.addMembers(ctx, lead, buvette, []newMember{{Email: "omar@example.org", Name: "Omar"}}, "fr")
	mustOK(t, err)

	if len(added) != 1 || added[0].Name != "Omar" {
		t.Fatalf("added: %+v", added)
	}

	mustOK(t, e.st.AddPoleManager(ctx, f.secret.ID, f.lead.ID))

	lead = actor(f.lead)
	if _, err := app.addMembers(ctx, lead, view(lead, f.secret), []newMember{{Email: "x@example.org"}}, "fr"); !errors.Is(err, errForbidden) {
		t.Errorf("a lead adds members to a restricted pole: %v", err)
	}

	// Taking someone out: whoever runs the pole, and they are told.
	if err := app.removeMember(ctx, actor(nina), buvette, added[0]); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer takes someone out: %v", err)
	}

	mustOK(t, app.removeMember(ctx, lead, buvette, added[0]))
	e.mailed("omar@example.org")

	// Naming managers is the coordination's call.
	if _, err := app.appointManager(ctx, lead, buvette, "nina@example.org", "fr"); !errors.Is(err, errForbidden) {
		t.Errorf("a lead names a manager: %v", err)
	}

	staff, err := access.Load(ctx, e.st, nil, true)
	mustOK(t, err)

	if _, err := app.appointManager(ctx, staff, buvette, "not an address", "fr"); err == nil {
		t.Error("a manager named by a wrong address")
	}

	p, err := app.appointManager(ctx, staff, buvette, "nina@example.org", "fr")
	mustOK(t, err)

	if ok, _ := e.st.IsPoleManager(ctx, f.buvette.ID, p.ID); !ok {
		t.Error("not named")
	}

	mustOK(t, app.dismissManager(ctx, staff, buvette, p))

	if ok, _ := e.st.IsPoleManager(ctx, f.buvette.ID, p.ID); ok {
		t.Error("still a manager")
	}
}
