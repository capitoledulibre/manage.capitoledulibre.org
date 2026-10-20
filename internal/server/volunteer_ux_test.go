package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestVolunteerUX(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	home := "/volunteers/" + f.event.Slug

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")

	// The language of the e-mails is chosen on the profile.
	_, body := e.get(nina, "/volunteers/profile")
	mustContain(t, body, "Langue des e-mails", `name="lang" value="en"`)

	e.post(nina, "/volunteers/profile", url.Values{"name": {"Nina"}, "lang": {"en"}})

	if p, _ := e.st.GetPerson(ctx, ninaP.ID); p.Lang != "en" {
		t.Fatalf("e-mail language: %q", p.Lang)
	}

	e.post(nina, "/volunteers/profile", url.Values{"name": {"Nina"}, "lang": {"fr"}})

	// The default pole stays out of sight while it holds nothing.
	def, _ := e.st.DefaultPole(ctx, f.event.ID)

	_, body = e.get(nina, home)
	if strings.Contains(body, `href="`+def.URL()+`"`) {
		t.Error("an empty default pole is shown")
	}

	mustContain(t, body, `aria-label="Navigation bénévole"`)

	start := time.Now().Add(72 * time.Hour)
	mustOK(t, e.st.CreateSlot(ctx, &store.Slot{PoleID: def.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 1}))

	_, body = e.get(nina, home)
	mustContain(t, body, `href="`+def.URL()+`"`)

	// On the day, the slot under way comes first, with its tasks and lead.
	now := &store.Slot{PoleID: f.buvette.ID, StartsAt: time.Now().Add(-30 * time.Minute), EndsAt: time.Now().Add(30 * time.Minute), Capacity: 2, Title: "Service"}
	mustOK(t, e.st.CreateSlot(ctx, now))
	_, err := e.st.CreateTask(ctx, now.ID, "Rincer les tasses")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, ninaP.ID))
	mustOK(t, e.st.ForceSignup(ctx, now.ID, ninaP.ID))

	_, body = e.get(nina, home)
	mustContain(t, body, "En ce moment", "Buvette – Service", "Rincer les tasses", "Lou Lead")

	// Open slots: by pole, and the urgent ones flagged.
	mustOK(t, e.st.JoinPole(ctx, f.accueil.ID, ninaP.ID))

	acc := time.Now().Add(96 * time.Hour)
	mustOK(t, e.st.CreateSlot(ctx, &store.Slot{PoleID: f.accueil.ID, StartsAt: acc, EndsAt: acc.Add(time.Hour), Capacity: 1, Title: "Badges"}))

	_, body = e.get(nina, home+"/planning?view=open&day=all")
	mustContain(t, body, "Tous les pôles", "?pole=accueil", "dans moins de 48")

	_, body = e.get(nina, home+"/planning?view=open&day=all&pole=accueil")
	mustContain(t, body, ">Accueil</a>", "Badges")

	if strings.Contains(body, "Buvette") && strings.Contains(body, `href="`+f.tomorrow.URL()+`"`) {
		t.Error("the pole filter keeps other poles")
	}
}

func TestLeadSlotTools(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	d1 := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	f.event.StartsOn, f.event.EndsOn = &d1, &d2
	mustOK(t, e.st.SaveEvent(ctx, f.event))

	lead := e.loginAs(f.leadEmail)
	manage := f.buvette.ManageURL()

	// A slot outside the dates of the edition asks for a confirmation.
	form := url.Values{"starts_at": {"2026-11-16T09:00"}, "ends_at": {"2026-11-16T11:00"}, "capacity": {"2"}, "title": {"Rangement"}}

	_, body := e.post(lead, manage+"/slots", form)
	mustContain(t, body, "en dehors des dates de l&#39;édition", `name="outside_ok"`, `value="Rangement"`)

	form.Set("outside_ok", "yes")

	_, body = e.post(lead, manage+"/slots", form)
	mustContain(t, body, "Créneau créé.")

	// Within the dates, no question.
	_, body = e.post(lead, manage+"/slots", url.Values{"starts_at": {"2026-11-14T09:00"}, "ends_at": {"2026-11-14T11:00"}, "capacity": {"2"}, "title": {"Ouverture"}, "tasks": {"Ouvrir"}})
	mustContain(t, body, "Créneau créé.")

	// A day is copied to another, tasks included, nobody signed up.
	_, body = e.post(lead, manage+"/days/copy", url.Values{"from": {"2026-11-14"}, "to": {"2026-11-15"}})
	mustContain(t, body, "créneau copié vers le dimanche 15 novembre")

	slots, _ := e.st.ListSlots(ctx, f.buvette.ID, 0)

	var copied *store.Slot

	for _, sl := range slots {
		if sl.Title == "Ouverture" && dayKey(sl.StartsAt) == "2026-11-15" {
			copied = sl
		}
	}

	if copied == nil || copied.TaskCount != 1 || copied.Filled != 0 || clock(copied.StartsAt) != "09:00" {
		t.Fatalf("copied day: %+v", copied)
	}

	// Copying it again makes no doubles.
	_, body = e.post(lead, manage+"/days/copy", url.Values{"from": {"2026-11-14"}, "to": {"2026-11-15"}})
	mustContain(t, body, "existent déjà le dimanche 15 novembre")

	if again, _ := e.st.ListSlots(ctx, f.buvette.ID, 0); len(again) != len(slots) {
		t.Fatalf("copying twice made doubles: %d then %d slots", len(slots), len(again))
	}

	_, body = e.post(lead, manage+"/days/copy", url.Values{"from": {"2026-11-14"}, "to": {"2026-11-14"}})
	mustContain(t, body, "Choisissez une autre date")

	// Moving a slot out of the dates asks too, keeping what was typed.
	_, body = e.post(lead, copied.ManageURL(), url.Values{"starts_at": {"2026-11-20T09:00"}, "ends_at": {"2026-11-20T11:00"}, "capacity": {"3"}, "title": {"Ouverture"}})
	mustContain(t, body, "en dehors des dates", `value="2026-11-20T09:00"`, `value="3"`)

	if sl, _ := e.st.GetSlot(ctx, copied.ID, 0); dayKey(sl.StartsAt) != "2026-11-15" {
		t.Fatal("the slot moved without confirmation")
	}
}

func TestDayOfEventNavigation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	planning := "/volunteers/" + f.event.Slug + "/planning"

	p, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))

	nina := e.loginAs("nina@example.org")

	// The home tab comes first; with no slot, the planning shows them all.
	_, body := e.get(nina, planning)
	mustContain(t, body, ">Accueil", `aria-current="page">Tout`)

	// With a slot, the planning opens on hers.
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, p.ID))

	_, body = e.get(nina, planning)
	mustContain(t, body, `aria-current="page">Mes créneaux`)

	// "Everything" still leads to everything: the links carry their view.
	mustContain(t, body, `href="`+planning+`?view=all"`)

	_, body = e.get(nina, planning+"?view=all")
	mustContain(t, body, `aria-current="page">Tout`)

	// The grid layout: a timetable per pole, the hours down, each slot a
	// link with how full it is; the filters keep it.
	_, body = e.get(nina, planning+"?day=all&view=all&layout=grid")
	mustContain(t, body, `aria-current="true">Grille`, ">Les horaires</th>", `href="`+f.morning.URL()+`"`, ">1/1</span>", ">moi</span>",
		"layout=grid&amp;view=open")

	// Her calendar: one tap from the home page.
	_, body = e.get(nina, "/volunteers/"+f.event.Slug)
	mustContain(t, body, "Ajouter mes créneaux à mon agenda")

	_, body = e.get(nina, planning+"?view=mine&calendar=1")
	mustContain(t, body, `id="calendar" class="card mt-8" open`, `href="webcal://`)

	// The lead still sees everyone's.
	_, body = e.get(e.loginAs(f.leadEmail), planning)
	mustContain(t, body, `aria-current="page">Tout`)

	// Once sign-ups close, her slot says whom to tell.
	f.event.VolunteerStatus = store.VolunteersFrozen
	mustOK(t, e.st.SaveVolunteerSettings(ctx, f.event))

	_, body = e.get(nina, planning)
	mustContain(t, body, "Un empêchement", f.morning.URL()+"#contacts")
}

func TestWaitlistJoinsOpenPoleAndExpiredSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// Nina fills the one place of the morning slot.
	p, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, p.ID))

	// Omar, not in the public pole yet, may wait for it: that joins him.
	omar := e.loginAs("omar@example.org")
	omarP, _ := e.st.PersonByEmail(ctx, "omar@example.org")

	_, body := e.get(omar, f.buvette.URL())
	mustContain(t, body, "Me prévenir si une place se libère")

	e.post(omar, f.morning.URL()+"/waitlist", url.Values{"back": {f.buvette.URL()}})

	if ok, _ := e.st.IsPoleMember(ctx, f.buvette.ID, omarP.ID); !ok {
		t.Error("waiting for a slot of a public pole does not join it")
	}

	if ok, _ := e.st.OnWaitlist(ctx, f.morning.ID, omarP.ID); !ok {
		t.Error("not on the waiting list")
	}

	// A form sent once logged out comes back to its page, not its action.
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+f.overlap.URL()+"/signup", nil)
	req.Header.Set("Referer", e.srv.URL+f.buvette.URL()+"?day=all")

	c := e.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.Do(req)
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/volunteers/login?next="+url.QueryEscape(f.buvette.URL()+"?day=all") {
		t.Errorf("login after an expired session: %q", loc)
	}
}
