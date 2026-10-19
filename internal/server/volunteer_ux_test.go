package server

import (
	"context"
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

	_, body = e.post(lead, manage+"/days/copy", url.Values{"from": {"2026-11-14"}, "to": {"2026-11-14"}})
	mustContain(t, body, "Choisissez une autre date")

	// Moving a slot out of the dates asks too, keeping what was typed.
	_, body = e.post(lead, copied.ManageURL(), url.Values{"starts_at": {"2026-11-20T09:00"}, "ends_at": {"2026-11-20T11:00"}, "capacity": {"3"}, "title": {"Ouverture"}})
	mustContain(t, body, "en dehors des dates", `value="2026-11-20T09:00"`, `value="3"`)

	if sl, _ := e.st.GetSlot(ctx, copied.ID, 0); dayKey(sl.StartsAt) != "2026-11-15" {
		t.Fatal("the slot moved without confirmation")
	}
}
