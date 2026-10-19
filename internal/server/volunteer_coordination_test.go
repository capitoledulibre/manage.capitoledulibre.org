package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestCoordination(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	base := "/volunteers/" + f.event.Slug + "/edition"

	admin := e.adminClient()
	lead := e.loginAs(f.leadEmail)
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.post(nina, f.buvette.URL()+"/join", nil)

	// A lead or a volunteer is not the coordination.
	for _, c := range []*http.Client{lead, nina} {
		if code, _ := e.get(c, base); code != http.StatusForbidden {
			t.Fatalf("coordination open to a non-coordinator: %d", code)
		}
	}

	// The staff appoint a coordinator, who joins the default pole.
	_, body := e.post(admin, base+"/coordinators", url.Values{"email": {"coco@example.org"}})
	mustContain(t, body, "coco@example.org fait partie de la coordination.")

	coco := e.loginAs("coco@example.org")

	cocoP, _ := e.st.PersonByEmail(ctx, "coco@example.org")
	def, _ := e.st.DefaultPole(ctx, f.event.ID)

	if ok, _ := e.st.IsPoleMember(ctx, def.ID, cocoP.ID); !ok {
		t.Fatal("a coordinator joins the default pole")
	}

	// The poles short of volunteers link to their slots to fill.
	_, body = e.get(coco, base)
	mustContain(t, body, "/planning?view=open&amp;pole="+f.buvette.Slug+"&amp;day=all")

	for _, tab := range []string{"", "?tab=messages", "?tab=settings"} {
		if code, body := e.get(coco, base+tab); code != 200 || !strings.Contains(body, "Édition · Example Conf 2026") {
			t.Fatalf("tab %q: %d", tab, code)
		}
	}

	// The coordinators get the link in the menu; coordinators do not appoint
	// coordinators.
	_, body = e.get(coco, "/volunteers/"+f.event.Slug)
	mustContain(t, body, `href="`+base+`"`)

	if code, _ := e.post(coco, base+"/coordinators", url.Values{"email": {"x@example.org"}}); code != http.StatusForbidden {
		t.Fatalf("a coordinator appoints a coordinator: %d", code)
	}

	// A coordinator manages every pole.
	if code, _ := e.get(coco, f.buvette.URL()); code != 200 {
		t.Fatalf("coordinator on a pole: %d", code)
	}

	// New pole, with its lead.
	_, body = e.post(coco, base+"/poles", url.Values{"name": {"Régie"}, "access_mode": {store.PoleInvitation}, "lead": {"rene@example.org"}})
	mustContain(t, body, "Pôle « Régie » créé.")

	regie, err := e.st.GetPoleBySlug(ctx, f.event.ID, "regie")
	mustOK(t, err)

	rene, _ := e.st.PersonByEmail(ctx, "rene@example.org")
	if ok, _ := e.st.IsPoleManager(ctx, regie.ID, rene.ID); !ok || regie.AccessMode != store.PoleInvitation {
		t.Fatalf("new pole: %+v", regie)
	}

	// Copy, then delete it; the default pole stays.
	_, body = e.post(coco, base+"/poles/regie/copy", nil)
	mustContain(t, body, "Pôle « Régie » copié")

	if _, err := e.st.GetPoleBySlug(ctx, f.event.ID, "regie-copie"); err != nil {
		t.Fatal("pole not copied")
	}

	_, body = e.post(coco, base+"/poles/regie-copie/delete", nil)
	mustContain(t, body, "Pôle « Régie (copie) » supprimé.")

	_, body = e.post(coco, base+"/poles/"+def.Slug+"/delete", nil)
	mustContain(t, body, "Le pôle par défaut ne peut pas être supprimé.")

	// A pole is only copied into an edition the person coordinates.
	if code, _ := e.post(coco, base+"/poles/regie/copy", url.Values{"to": {f.draft.Slug}}); code != http.StatusForbidden {
		t.Fatalf("copy into an edition not coordinated: %d", code)
	}

	// No e-mail about an edition volunteers cannot see.
	if code, _ := e.post(e.adminClient(), "/volunteers/"+f.draft.Slug+"/edition/messages", url.Values{"body": {"Hello"}, "notify": {"1"}}); code != http.StatusBadRequest {
		t.Errorf("e-mail about a draft edition: %d", code)
	}

	// Edition-wide message, e-mailed to every volunteer.
	_, body = e.post(coco, base+"/messages", url.Values{"body": {"Briefing vendredi 18 h."}, "notify": {"1"}})
	mustContain(t, body, "Message publié et envoyé à")
	e.mailed("nina@example.org", "Briefing vendredi 18 h.", "Example Conf 2026")

	_, body = e.get(nina, "/volunteers/"+f.event.Slug)
	mustContain(t, body, "Briefing vendredi 18 h.")

	msgs, _ := e.st.ListEventMessages(ctx, f.event.ID)
	e.post(coco, base+"/messages/"+msgs[0].UID+"/delete", nil)

	if msgs, _ = e.st.ListEventMessages(ctx, f.event.ID); len(msgs) != 0 {
		t.Fatal("message not deleted")
	}

	// Settings: freeze the edition.
	e.post(coco, base+"/settings", url.Values{"status": {store.VolunteersFrozen}, "freeze_at": {"2026-11-13T00:00"}})

	ev, _ := e.st.GetEvent(ctx, f.event.ID)
	if ev.VolunteerStatus != store.VolunteersFrozen || ev.VolunteerFreezeAt == nil {
		t.Fatalf("settings: %+v", ev)
	}

	if code, _ := e.post(coco, base+"/settings", url.Values{"status": {"bogus"}}); code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", code)
	}

	// Planning and volunteers.
	// The planning of every pole, with the links to edit the slots.
	_, body = e.get(coco, "/volunteers/"+f.event.Slug+"/planning?day=all")
	mustContain(t, body, "Tous les pôles", "Buvette", `href="`+f.morning.ManageURL()+`"`, "Modifier le créneau")

	_, body = e.post(coco, base+"/volunteers", url.Values{"name": {"Paul"}, "email": {"paul@example.org"}, "phone": {"0600000000"}, "pole": {f.accueil.Slug}})
	mustContain(t, body, "Paul est enregistré·e, dans le pôle « Accueil ».", "paul@example.org", "06 00 00 00 00")

	_, body = e.post(coco, base+"/volunteers", url.Values{"name": {"Léon"}, "email": {"leon@example.org"}})
	mustContain(t, body, "dans le pôle « "+def.Name+" »")

	leon, _ := e.st.PersonByEmail(ctx, "leon@example.org")
	_, body = e.post(coco, base+"/volunteers/"+leon.UID+"/join", url.Values{"pole": {"regie"}})
	mustContain(t, body, "Léon a été ajouté·e au pôle « Régie ».")

	_, body = e.get(coco, base+"/volunteers.csv")
	mustContain(t, body, "name,email,phone,poles,slots", "Paul,paul@example.org,0600000000")

	// Another edition is prepared from this one, by the staff.
	if code, _ := e.post(coco, "/volunteers/"+f.draft.Slug+"/edition/copy", url.Values{"from": {f.event.Slug}}); code != http.StatusForbidden {
		t.Fatalf("copy by a non-coordinator of the target: %d", code)
	}

	_, body = e.post(admin, "/volunteers/"+f.draft.Slug+"/edition/copy", url.Values{"from": {f.event.Slug}})
	mustContain(t, body, "Pôles, coordination et logo repris de « Example Conf 2026 »")

	if _, err := e.st.GetPoleBySlug(ctx, f.draft.ID, "buvette"); err != nil {
		t.Fatal("poles not copied")
	}

	if ok, _ := e.st.IsEventCoordinator(ctx, f.draft.ID, cocoP.ID); !ok {
		t.Fatal("coordination not copied")
	}

	// The staff remove a coordinator.
	e.post(admin, base+"/coordinators/"+cocoP.UID+"/delete", nil)

	if code, _ := e.get(coco, base); code != http.StatusForbidden {
		t.Fatalf("removed coordinator still in: %d", code)
	}

	// The staff editions list links to the coordination.
	_, body = e.get(admin, "/admin/events")
	mustContain(t, body, `href="`+base+`"`)

	// No integer id on the coordination pages.
	numeric := regexp.MustCompile(`(?:href|action)="[^"]*/[0-9]+(?:[/"?#])|<option value="[0-9]+"`)

	for _, path := range []string{base, base + "?tab=messages", base + "?tab=settings", "/volunteers/" + f.event.Slug + "/planning", base + "/volunteers"} {
		code, body := e.get(admin, path)
		if code != 200 {
			t.Errorf("%s: %d", path, code)
		}

		for _, m := range numeric.FindAllString(body, -1) {
			t.Errorf("%s exposes an integer id: %s", path, m)
		}
	}
}

func TestLeaveVolunteering(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// A volunteer only: the account goes, and the waiting list hears of the
	// freed place.
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.post(nina, f.morning.URL()+"/signup", nil)

	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	e.post(omar, f.buvette.URL()+"/join", nil)
	e.post(omar, f.morning.URL()+"/waitlist", nil)

	_, body := e.get(nina, "/volunteers/profile")
	mustContain(t, body, "Supprimer mon compte")

	if code, _ := e.post(nina, "/volunteers/profile/delete", nil); code != http.StatusBadRequest {
		t.Fatalf("leaving without confirmation: %d", code)
	}

	_, body = e.post(nina, "/volunteers/profile/delete", url.Values{"confirm": {"yes"}})
	mustContain(t, body, "Votre compte a été supprimé.")

	if _, err := e.st.PersonByEmail(ctx, "nina@example.org"); err != store.ErrNotFound {
		t.Fatalf("person kept: %v", err)
	}

	e.mailed("omar@example.org", "Une place s'est libérée")

	// A partner contact who volunteers keeps the account.
	org := &store.Organization{Name: "Acme"}
	mustOK(t, e.st.CreateOrganization(ctx, org))
	_, err := e.st.AddManager(ctx, &store.Manager{OrgID: org.ID, Email: "omar@example.org", Name: "Omar"})
	mustOK(t, err)

	_, body = e.get(omar, "/volunteers/profile")
	mustContain(t, body, "Votre compte reste")

	e.post(omar, "/volunteers/profile/delete", url.Values{"confirm": {"yes"}})

	omarP, err := e.st.PersonByEmail(ctx, "omar@example.org")
	mustOK(t, err)

	if ok, _ := e.st.IsVolunteer(ctx, omarP.ID); ok {
		t.Fatal("still a volunteer")
	}
}

func TestVolunteerPageAndBulk(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()
	base := "/volunteers/" + f.event.Slug + "/edition"

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "0600000000", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))

	omar, err := e.st.EnsureVolunteer(ctx, "omar@example.org", "Omar", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, omar.ID))

	// The list links to each person, and filters by pole.
	_, body := e.get(admin, base+"/volunteers")
	mustContain(t, body, base+"/volunteers/"+nina.UID, `form="bulk" name="person" value="`+omar.UID+`"`)

	_, body = e.get(admin, base+"/volunteers?pole="+f.accueil.Slug)
	if strings.Contains(body, "nina@example.org") {
		t.Error("the pole filter keeps someone not in it")
	}

	// A person's page: slots, hours, history.
	_, body = e.get(admin, base+"/volunteers/"+nina.UID)
	mustContain(t, body, "Nina", "06 00 00 00 00", "Buvette", "2 h", f.morning.URL())

	if code, _ := e.get(admin, base+"/volunteers/"+e.mustPersonUID("nobody@example.org")); code != http.StatusNotFound {
		t.Errorf("someone not in the edition: %d", code)
	}

	// Both added to a pole at once.
	_, body = e.post(admin, base+"/volunteers/join", url.Values{"pole": {f.accueil.Slug}, "person": {nina.UID, omar.UID}})
	mustContain(t, body, "2 personnes ajoutées au pôle")

	for _, p := range []*store.Person{nina, omar} {
		if ok, _ := e.st.IsPoleMember(ctx, f.accueil.ID, p.ID); !ok {
			t.Errorf("%s not added", p.Email)
		}
	}

	_, body = e.get(admin, base+"/volunteers/"+nina.UID)
	mustContain(t, body, "Accueil")
}

func (e *env) mustPersonUID(email string) string {
	e.t.Helper()

	p, err := e.st.EnsureVolunteer(context.Background(), email, "", "", "fr")
	mustOK(e.t, err)

	return p.UID
}

func TestEditionShift(t *testing.T) {
	d := func(y int, m time.Month, day int) *time.Time {
		v := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)

		return &v
	}

	// From summer time to winter time, 10:00 in Paris stays 10:00.
	shift := editionShift(&store.Event{StartsOn: d(2025, 10, 25)}, &store.Event{StartsOn: d(2026, 11, 14)})
	in := time.Date(2025, 10, 26, 10, 0, 0, 0, paris)

	if got := shift(in).In(paris); got.Format("2006-01-02 15:04") != "2026-11-15 10:00" {
		t.Errorf("shift = %s", got)
	}

	if editionShift(&store.Event{}, &store.Event{StartsOn: d(2026, 11, 14)}) != nil {
		t.Error("an edition without dates gives a shift")
	}
}

func TestCopyEditionWithSlots(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()

	// Dates for both editions.
	for _, ev := range []*store.Event{f.event, f.draft} {
		start := time.Date(ev.Year, 11, 14, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)
		ev.StartsOn, ev.EndsOn = &start, &end
		mustOK(t, e.st.SaveEvent(ctx, ev))
	}

	// A coordinator of the next edition only cannot copy from this one.
	_, _ = e.post(admin, "/volunteers/"+f.draft.Slug+"/edition/coordinators", url.Values{"email": {"coco@example.org"}})
	coco := e.loginAs("coco@example.org")

	if code, _ := e.post(coco, "/volunteers/"+f.draft.Slug+"/edition/copy", url.Values{"from": {f.event.Slug}, "slots": {"1"}}); code != http.StatusForbidden {
		t.Errorf("copy from an edition not coordinated: %d", code)
	}

	// Coordinating both, they can, slots included.
	_, _ = e.post(admin, "/volunteers/"+f.event.Slug+"/edition/coordinators", url.Values{"email": {"coco@example.org"}})

	_, body := e.get(coco, "/volunteers/"+f.draft.Slug+"/edition?tab=settings")
	mustContain(t, body, `name="slots" value="1"`, f.event.Name)

	e.post(coco, "/volunteers/"+f.draft.Slug+"/edition/copy", url.Values{"from": {f.event.Slug}, "slots": {"1"}})

	buvette, err := e.st.GetPoleBySlug(ctx, f.draft.ID, f.buvette.Slug)
	mustOK(t, err)

	slots, _ := e.st.ListSlots(ctx, buvette.ID, 0)
	if len(slots) != 3 {
		t.Fatalf("copied slots: %d", len(slots))
	}

	if want := f.morning.StartsAt.AddDate(1, 0, 0); slots[1].StartsAt.Year() != want.Year() || slots[1].Filled != 0 {
		t.Errorf("copied slot: %+v", slots[1])
	}
}
