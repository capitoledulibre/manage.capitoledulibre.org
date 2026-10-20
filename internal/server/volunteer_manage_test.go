package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

// mailed waits for a message to addr containing every want.
func (e *env) mailed(addr string, want ...string) {
	e.t.Helper()

	waitFor(e.t, func() bool {
	next:
		for _, m := range e.mailer.mailTo(addr) {
			for _, w := range want {
				if !strings.Contains(m, w) {
					continue next
				}
			}

			return true
		}

		return false
	})
}

func TestPoleManagement(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	manage := f.buvette.ManageURL()

	lead := e.loginAs(f.leadEmail)
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")

	// Only the people who run the pole get in: a volunteer has the slots of
	// the pole's page, and no tab.

	for _, tab := range []string{"", "?tab=members", "?tab=settings"} {
		_, body := e.get(nina, f.buvette.URL()+tab)
		if strings.Contains(body, "Sections du pôle") || strings.Contains(body, "Réglages") {
			t.Fatalf("a volunteer sees the management tabs on %q", tab)
		}
	}

	if code, _ := e.post(nina, manage+"/slots", url.Values{}); code != http.StatusForbidden {
		t.Fatalf("a volunteer creates a slot: %d", code)
	}

	// The management is tabs of the pole's page.
	for _, tab := range []string{"", "?tab=members", "?tab=messages", "?tab=settings", "?tab=history"} {
		code, body := e.get(lead, f.buvette.URL()+tab)
		if code != 200 || !strings.Contains(body, `aria-current="page">`) || !strings.Contains(body, "Générer des créneaux") {
			t.Fatalf("tab %q: %d", tab, code)
		}
	}

	_, body := e.get(lead, f.buvette.URL()+"?tab=settings")
	mustContain(t, body, `aria-current="page">Réglages`)

	// The lead finds their pole from the menu; its slots show the links to
	// edit them, its tabs the management.
	_, body = e.get(lead, f.buvette.URL())
	mustContain(t, body, `href="`+f.buvette.URL()+`"`, "Gérer : Buvette", "Modifier le créneau", "?tab=members")

	// Places, then a series of slots on each of them.
	e.post(lead, manage+"/locations", url.Values{"names": {"Bar\nTerrasse\n"}})

	locations, _ := e.st.ListLocations(ctx, f.buvette.ID)
	if len(locations) != 2 {
		t.Fatalf("locations: %d", len(locations))
	}

	_, body = e.post(lead, manage+"/slots", url.Values{
		"starts_at": {"2026-11-14T09:00"}, "ends_at": {"2026-11-14T11:00"}, "repeat_until": {"2026-11-14T15:00"},
		"all_locations": {"1"}, "capacity": {"2"}, "title": {"Service"}, "tasks": {"Nettoyer\nRanger"},
	})
	mustContain(t, body, "6 créneaux créés.")

	slots, _ := e.st.ListSlots(ctx, f.buvette.ID, 0)

	var series []*store.Slot

	for _, sl := range slots {
		if sl.Title == "Service" {
			series = append(series, sl)
		}
	}

	if len(series) != 6 || series[0].TaskCount != 2 || series[0].StartsAt.In(paris).Hour() != 9 {
		t.Fatalf("series: %d slots, first %+v", len(series), series[0])
	}

	// A bad slot is refused, the form kept.
	_, body = e.post(lead, manage+"/slots", url.Values{"starts_at": {"2026-11-14T09:00"}, "ends_at": {"2026-11-14T08:00"}, "capacity": {"1"}, "title": {"Oups"}})
	mustContain(t, body, "la fin après le début", `value="Oups"`)

	// Nina signs up; moving the slot tells her.
	e.post(nina, f.morning.URL()+"/signup", nil)

	start := f.morning.StartsAt.Add(time.Hour).In(paris).Format("2006-01-02T15:04")
	end := f.morning.EndsAt.Add(time.Hour).In(paris).Format("2006-01-02T15:04")

	_, body = e.post(lead, f.morning.ManageURL(), url.Values{"starts_at": {start}, "ends_at": {end}, "capacity": {"1"}, "title": {"Café"}})
	mustContain(t, body, "Le bénévole inscrit a été prévenu du changement par e-mail.")
	e.mailed("nina@example.org", "Créneau modifié", "Café")

	// The slot is full: Omar waits; one more place tells him.
	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	e.post(omar, f.buvette.URL()+"/join", nil)
	e.post(omar, f.morning.URL()+"/waitlist", nil)

	_, body = e.get(lead, f.buvette.URL())
	mustContain(t, body, "1 en attente")

	e.post(lead, f.morning.ManageURL(), url.Values{"starts_at": {start}, "ends_at": {end}, "capacity": {"2"}, "title": {"Café"}})
	e.mailed("omar@example.org", "Une place s'est libérée")

	// The edit page offers the members not on the slot, by UID.
	omarP, _ := e.st.PersonByEmail(ctx, "omar@example.org")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")

	_, body = e.get(lead, f.morning.ManageURL())
	mustContain(t, body, `<option value="`+omarP.UID+`">Omar`, "Allumer la machine")

	if strings.Contains(body, `<option value="`+ninaP.UID+`"`) {
		t.Error("a volunteer on the slot is offered again")
	}

	_, body = e.post(lead, f.morning.ManageURL()+"/volunteers", url.Values{"person": {omarP.UID}})
	mustContain(t, body, "Omar a été inscrit·e sur le créneau", "prévenu·e par e-mail")
	e.mailed("omar@example.org", "Vous êtes inscrit·e", "Un responsable vous a inscrit·e")

	// Signing Nina up where she already is at that time warns the lead.
	_, body = e.post(lead, f.overlap.ManageURL()+"/volunteers", url.Values{"person": {ninaP.UID}})
	mustContain(t, body, "déjà sur un autre créneau au même moment", "Buvette (")
	e.post(lead, f.overlap.ManageURL()+"/volunteers/"+ninaP.UID+"/delete", nil)

	// Taking Nina off the slot tells her.
	_, body = e.post(lead, f.morning.ManageURL()+"/volunteers/"+ninaP.UID+"/delete", nil)
	mustContain(t, body, "Nina a été retiré·e du créneau")
	e.mailed("nina@example.org", "Inscription annulée")

	// Tasks.
	e.post(lead, f.morning.ManageURL()+"/tasks", url.Values{"labels": {"Rendre les clés"}})

	tasks, _ := e.st.ListTasks(ctx, f.morning.ID)
	if len(tasks) != 2 {
		t.Fatalf("tasks: %d", len(tasks))
	}

	e.post(lead, manage+"/tasks/"+tasks[1].UID+"/delete", nil)

	if tasks, _ = e.st.ListTasks(ctx, f.morning.ID); len(tasks) != 1 {
		t.Fatalf("task not deleted: %d", len(tasks))
	}

	// Messages, e-mailed to the members once confirmed.
	_, body = e.get(lead, f.buvette.URL()+"?tab=messages")
	mustContain(t, body, `data-confirm-if="[name=notify]:checked"`, "Prévenir aussi par e-mail les bénévoles du pôle (")

	_, body = e.post(lead, manage+"/messages", url.Values{"body": {"Rendez-vous au bar à 8 h 45."}, "notify": {"1"}})
	mustContain(t, body, "Message publié et envoyé à")
	e.mailed("omar@example.org", "Rendez-vous au bar à 8 h 45.", "Message")

	msgs, _ := e.st.ListPoleMessages(ctx, f.buvette.ID)
	e.post(lead, manage+"/messages/"+msgs[0].UID+"/delete", nil)

	if msgs, _ = e.st.ListPoleMessages(ctx, f.buvette.ID); len(msgs) != 0 {
		t.Fatal("message not deleted")
	}

	// Deleting a slot tells the people on it.
	_, body = e.post(lead, f.morning.ManageURL()+"/delete", nil)
	mustContain(t, body, "Le bénévole inscrit a été prévenu par e-mail.")
	e.mailed("omar@example.org", "Créneau annulé")

	// Settings: a lead cannot make the pole restricted, the coordination can.
	e.post(lead, manage, url.Values{"name": {"Buvette"}, "slug": {"bar"}, "access_mode": {store.PoleRestricted}, "description": {"Le bar"}})

	p, _ := e.st.GetPole(ctx, f.buvette.ID)
	if p.Slug != "bar" || p.AccessMode != store.PolePublic || p.Description != "Le bar" {
		t.Fatalf("settings: %+v", p)
	}

	manage = p.ManageURL()

	admin := e.adminClient()
	e.post(admin, manage, url.Values{"name": {"Buvette"}, "slug": {"bar"}, "access_mode": {store.PoleRestricted}})

	if p, _ = e.st.GetPole(ctx, f.buvette.ID); p.AccessMode != store.PoleRestricted {
		t.Fatal("the coordination makes a pole restricted")
	}

	// A restricted pole: only the coordination adds members.
	if code, _ := e.post(lead, manage+"/members", url.Values{"emails": {"zoe@example.org"}}); code != http.StatusForbidden {
		t.Fatalf("a lead adds to a restricted pole: %d", code)
	}

	e.post(admin, manage, url.Values{"name": {"Buvette"}, "slug": {"bar"}, "access_mode": {store.PolePublic}})

	// Members: by address, and through the htmx search.
	_, body = e.post(lead, manage+"/members", url.Values{"emails": {"Zoé Martin <zoe@example.org>\nnot-an-address"}})
	mustContain(t, body, "1 bénévole ajouté·e au pôle.", "Zoé Martin")

	paul, _ := e.st.EnsureVolunteer(ctx, "paul@example.org", "Paul Dupont", "", "fr")

	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+manage+"/people?q=dupo", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := lead.Do(req)
	mustOK(t, err)

	found, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	mustContain(t, string(found), `id="member-search-results"`, "Paul Dupont", "&lt;paul@example.org&gt;")

	_, body = e.hx(lead, manage+"/members", url.Values{"emails": {"paul@example.org"}, "q": {"dupo"}})
	mustContain(t, body, `id="members-table" hx-swap-oob="true"`, "Paul Dupont", "1 personne ajoutée au pôle.")

	if ok, _ := e.st.IsPoleMember(ctx, f.buvette.ID, paul.ID); !ok {
		t.Fatal("Paul was not added")
	}

	// Removing a member tells them.
	_, body = e.post(lead, manage+"/members/"+paul.UID+"/delete", nil)
	mustContain(t, body, "Paul Dupont a été retiré·e du pôle")
	e.mailed("paul@example.org", "Retrait du pôle")

	// Leads are the coordination's call.
	if code, _ := e.post(lead, manage+"/managers", url.Values{"email": {"zoe@example.org"}}); code != http.StatusForbidden {
		t.Fatalf("a lead appoints a lead: %d", code)
	}

	_, body = e.post(admin, manage+"/managers", url.Values{"email": {"zoe@example.org"}})
	mustContain(t, body, "Zoé Martin est responsable du pôle.")

	zoe, _ := e.st.PersonByEmail(ctx, "zoe@example.org")
	e.post(admin, manage+"/managers/"+zoe.UID+"/delete", nil)

	if ok, _ := e.st.IsPoleManager(ctx, f.buvette.ID, zoe.ID); ok {
		t.Fatal("lead not removed")
	}

	// The pole picture is served to the volunteers.
	_, body = e.upload(lead, manage+"/image", nil, "image", "pole.png", pngBytes())
	mustContain(t, body, "Image du pôle enregistrée.")

	p, _ = e.st.GetPole(ctx, f.buvette.ID)
	if code, _ := e.get(nina, "/volunteer-images/"+p.ImageFile); code != 200 || p.ImageFile == "" {
		t.Fatalf("pole picture: %d %q", code, p.ImageFile)
	}

	e.post(lead, manage+"/image/delete", nil)

	if code, _ := e.get(nina, "/volunteer-images/"+p.ImageFile); code != http.StatusNotFound {
		t.Fatalf("removed picture still served: %d", code)
	}

	// No integer id on the management pages.
	numeric := regexp.MustCompile(`(?:href|action|hx-post|hx-get)="[^"]*/[0-9]+(?:[/"?#])|<option value="[0-9]+"`)

	slots, _ = e.st.ListSlots(ctx, f.buvette.ID, 0)

	page := p.URL()

	for _, path := range []string{page, page + "?tab=members", page + "?tab=messages", page + "?tab=settings", manage + "/slots/new", slots[0].ManageURL()} {
		code, body := e.get(lead, path)
		if code != 200 {
			t.Errorf("%s: %d", path, code)
		}

		for _, m := range numeric.FindAllString(body, -1) {
			t.Errorf("%s exposes an integer id: %s", path, m)
		}
	}
}

func TestSignUpFromMembers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	lead := e.loginAs(f.leadEmail)

	omar, err := e.st.EnsureVolunteer(ctx, "omar@example.org", "Omar", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, omar.ID))

	past := time.Now().Add(-5 * time.Hour)
	over := &store.Slot{PoleID: f.buvette.ID, StartsAt: past, EndsAt: past.Add(2 * time.Hour), Capacity: 2}
	mustOK(t, e.st.CreateSlot(ctx, over))

	// The members list offers the slots still to come, not the past one.
	_, body := e.get(lead, f.buvette.URL()+"?tab=members")
	mustContain(t, body, "Inscrire sur…", `/members/`+omar.UID+`/signup`, `<option value="`+f.morning.UID+`">`)

	if strings.Contains(body, `<option value="`+over.UID+`">`) {
		t.Error("a slot over is offered")
	}

	_, body = e.post(lead, f.buvette.ManageURL()+"/members/"+omar.UID+"/signup", url.Values{"slot": {f.morning.UID}})
	mustContain(t, body, "Omar a été inscrit·e sur le créneau")
	e.mailed("omar@example.org", "Vous êtes inscrit·e")

	if on, _ := e.st.IsSignedUp(ctx, f.morning.ID, omar.ID); !on {
		t.Error("not signed up")
	}

	// Only members, and only slots of the pole.
	stranger, _ := e.st.EnsureVolunteer(ctx, "x@example.org", "X", "", "fr")
	if code, _ := e.post(lead, f.buvette.ManageURL()+"/members/"+stranger.UID+"/signup", url.Values{"slot": {f.morning.UID}}); code != http.StatusNotFound {
		t.Errorf("non-member: %d", code)
	}
}

func TestBulkSlotDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	lead := e.loginAs(f.leadEmail)
	bulk := f.buvette.ManageURL() + "/slots/bulk"

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.overlap.ID, nina.ID))

	// The pole links to its slots as a timetable, a box per slot.
	_, body := e.get(lead, f.buvette.URL())
	mustContain(t, body, `href="`+bulk+`"`)

	_, body = e.get(lead, bulk)
	mustContain(t, body, "Créneaux en lot", `name="slot" value="`+f.morning.UID+`"`, `data-filled="1"`, "Tout le pôle")

	// The deletion always asks first: the boxes live outside its form, so
	// no data-confirm-if could see them.
	if i := strings.Index(body, `id="bulk-delete"`); i < 0 || strings.Contains(body[i:i+600], "data-confirm-if") || !strings.Contains(body[i:i+600], `data-confirm="Supprimer`) {
		t.Error("the bulk deletion does not always confirm")
	}

	if code, _ := e.get(e.loginAs("nina@example.org"), bulk); code != http.StatusForbidden {
		t.Errorf("a volunteer opens the bulk page: %d", code)
	}

	// Two slots at once: Nina gets one e-mail listing both.
	before := e.mailer.count("nina@example.org")

	_, body = e.post(lead, bulk+"/delete", url.Values{"slot": {f.morning.UID, f.overlap.UID}})
	mustContain(t, body, "2 créneaux supprimés. Personnes prévenues par e-mail : 1.")

	slots, _ := e.st.ListSlots(ctx, f.buvette.ID, 0)
	if len(slots) != 1 || slots[0].ID != f.tomorrow.ID {
		t.Fatalf("left: %d slots", len(slots))
	}

	waitFor(t, func() bool { return e.mailer.count("nina@example.org") == before+1 })
	e.mailed("nina@example.org", "Créneaux annulés", "Ces 2 créneaux")

	// Another pole's slot is out of reach.
	other := &store.Slot{PoleID: f.accueil.ID, StartsAt: f.morning.StartsAt, EndsAt: f.morning.EndsAt, Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, other))

	if code, _ := e.post(lead, bulk+"/delete", url.Values{"slot": {f.tomorrow.UID, other.UID}}); code != http.StatusNotFound {
		t.Errorf("another pole's slot: %d", code)
	}

	if left, _ := e.st.ListSlots(ctx, f.buvette.ID, 0); len(left) != 1 {
		t.Error("a refused selection deleted some of it")
	}
}
