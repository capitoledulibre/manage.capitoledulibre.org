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

	// Only the people who run the pole get in.
	if code, _ := e.get(nina, manage); code != http.StatusForbidden {
		t.Fatalf("a volunteer opens the management: %d", code)
	}

	if code, _ := e.post(nina, manage+"/slots", url.Values{}); code != http.StatusForbidden {
		t.Fatalf("a volunteer creates a slot: %d", code)
	}

	for _, tab := range []string{"", "?tab=members", "?tab=messages", "?tab=settings"} {
		if code, body := e.get(lead, manage+tab); code != 200 || !strings.Contains(body, "Gestion de Buvette") {
			t.Fatalf("tab %q: %d", tab, code)
		}
	}

	// The lead finds the management from the menu and the pole page.
	_, body := e.get(lead, f.buvette.URL())
	mustContain(t, body, `href="`+manage+`"`, "Gérer : Buvette", "Gérer ce pôle")

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

	_, body = e.get(lead, manage)
	mustContain(t, body, `title="En liste d&#39;attente">1</span>`)

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
	mustContain(t, body, "Omar a été inscrit·e sur le créneau")

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

	// Messages, e-mailed to the members.
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

	for _, path := range []string{manage, manage + "?tab=members", manage + "?tab=messages", manage + "?tab=settings", manage + "/slots/new", slots[0].ManageURL()} {
		code, body := e.get(lead, path)
		if code != 200 {
			t.Errorf("%s: %d", path, code)
		}

		for _, m := range numeric.FindAllString(body, -1) {
			t.Errorf("%s exposes an integer id: %s", path, m)
		}
	}
}
