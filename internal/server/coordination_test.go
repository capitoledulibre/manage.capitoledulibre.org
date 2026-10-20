package server

import (
	"context"
	"errors"
	"testing"

	"manage/internal/access"
	"manage/internal/store"
)

// TestCoordinationService drives the coordination of an edition without
// HTTP.
func TestCoordinationService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	app := e.app

	actor := func(p *store.Person, staff bool) *access.Actor {
		a, err := access.Load(ctx, e.st, p, staff)
		mustOK(t, err)

		return a
	}

	staffP, err := e.st.EnsureVolunteer(ctx, "staff@conf.test", "Sam Staff", "", "fr")
	mustOK(t, err)

	staff := actor(staffP, true)
	lead := actor(f.lead, false)

	// Naming coordinators is the staff's call.
	if _, err := app.appointCoordinator(ctx, lead, f.event, "coco@example.org", "fr"); !errors.Is(err, errForbidden) {
		t.Errorf("a lead names a coordinator: %v", err)
	}

	cocoP, err := app.appointCoordinator(ctx, staff, f.event, "coco@example.org", "fr")
	mustOK(t, err)

	coco := actor(cocoP, false)

	// The coordination of one edition is not that of another.
	if _, err := app.createPole(ctx, coco, f.draft, newPole{Name: "Régie"}, "fr"); !errors.Is(err, errForbidden) {
		t.Errorf("a pole created in an edition not coordinated: %v", err)
	}

	if _, err := app.createPole(ctx, coco, f.event, newPole{Name: " "}, "fr"); err == nil {
		t.Error("a pole without a name")
	}

	regie, err := app.createPole(ctx, coco, f.event, newPole{Name: "Régie", Lead: "rene@example.org"}, "fr")
	mustOK(t, err)

	rene, err := e.st.PersonByEmail(ctx, "rene@example.org")
	mustOK(t, err)

	if ok, _ := e.st.IsPoleManager(ctx, regie.ID, rene.ID); !ok {
		t.Error("the first manager is not named")
	}

	// Registering someone on their behalf, in the default pole.
	nina, pole, err := app.registerVolunteer(ctx, coco, f.event, registration{Email: "nina@example.org", Name: "Nina"}, nil, "fr")
	mustOK(t, err)

	if !pole.IsDefault || nina.Name != "Nina" {
		t.Errorf("registered: %+v in %s", nina, pole.Name)
	}

	// Then in a pole, and corrected.
	mustOK(t, app.putInPole(ctx, coco, f.event, f.secret, []*store.Person{nina}))

	if ok, _ := e.st.IsPoleMember(ctx, f.secret.ID, nina.ID); !ok {
		t.Error("not put in the restricted pole")
	}

	mustOK(t, app.correctVolunteer(ctx, coco, f.event, nina, "Nina Simone", "0600000000"))

	if err := app.correctVolunteer(ctx, lead, f.event, nina, "Nope", ""); !errors.Is(err, errForbidden) {
		t.Errorf("a lead corrects a volunteer: %v", err)
	}

	// A notice to the edition, e-mailed to its volunteers.
	sent, err := app.postNotice(ctx, coco, noticeScope{Event: f.event}, "Rendez-vous à 8 h", true)
	mustOK(t, err)

	if sent == 0 {
		t.Error("nobody e-mailed")
	}

	e.mailed("nina@example.org", "Rendez-vous à 8 h")

	if _, err := app.postNotice(ctx, coco, noticeScope{Event: f.event}, "  ", false); err == nil {
		t.Error("an empty notice posted")
	}

	// A lead writes to their pole, not to the edition nor to another pole.
	if _, err := app.postNotice(ctx, lead, noticeScope{Event: f.event}, "Hello", false); !errors.Is(err, errForbidden) {
		t.Errorf("a lead writes to the edition: %v", err)
	}

	if _, err := app.postNotice(ctx, lead, noticeScope{Event: f.event, Pole: f.accueil}, "Hello", false); !errors.Is(err, errForbidden) {
		t.Errorf("a lead writes to another pole: %v", err)
	}

	if _, err := app.postNotice(ctx, lead, noticeScope{Event: f.event, Pole: f.buvette, Slot: f.morning}, "Café", false); err != nil {
		t.Errorf("a lead writes to a slot of theirs: %v", err)
	}

	// The default pole is not deleted; another is.
	def, err := e.st.DefaultPole(ctx, f.event.ID)
	mustOK(t, err)

	if rf, ok := refused(app.deletePole(ctx, coco, f.event, def)); !ok || rf.Key != "vol.err.default_pole_delete" {
		t.Error("the default pole deleted")
	}

	mustOK(t, app.deletePole(ctx, coco, f.event, regie))

	// Copying into an edition not coordinated is refused.
	if _, err := app.copyPole(ctx, coco, f.buvette, f.draft); !errors.Is(err, errForbidden) {
		t.Errorf("a pole copied into another edition: %v", err)
	}

	// The status moves, and the history says so.
	mustOK(t, app.saveVolunteerSettings(ctx, coco, f.event, volunteerSettings{Status: store.VolunteersFrozen}))

	if got, _ := e.st.GetEvent(ctx, f.event.ID); got.VolunteerStatus != store.VolunteersFrozen {
		t.Errorf("status: %s", got.VolunteerStatus)
	}

	// The staff takes the coordinator off.
	mustOK(t, app.dismissCoordinator(ctx, staff, f.event, cocoP))

	if ok, _ := e.st.IsEventCoordinator(ctx, f.event.ID, cocoP.ID); ok {
		t.Error("still a coordinator")
	}
}
