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

func TestLogisticsAndReminders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()
	edition := "/volunteers/" + f.event.Slug + "/edition"
	home := "/volunteers/" + f.event.Slug

	// The coordination says what the edition provides, one per line.
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersOpen},
		"tshirt_sizes": {"S\nM\n\n L \nM"}, "meals": {"Samedi midi\nDimanche midi"}})

	ev, _ := e.st.GetEvent(ctx, f.event.ID)
	if ev.TshirtSizes != "S\nM\nL" || len(ev.MealList()) != 2 {
		t.Fatalf("settings: %q %q", ev.TshirtSizes, ev.Meals)
	}

	// Three volunteers: Nina and Omar on a slot, Paul on none (as the lead
	// of the fixture).
	volunteer := func(addr, name string, slot *store.Slot) *http.Client {
		p, err := e.st.EnsureVolunteer(ctx, addr, name, "", "fr")
		mustOK(t, err)
		mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))

		if slot != nil {
			mustOK(t, e.st.ForceSignup(ctx, slot.ID, p.ID))
		}

		return e.loginAs(addr)
	}

	nina := volunteer("nina@example.org", "Nina", f.morning)
	volunteer("omar@example.org", "Omar", f.overlap)
	volunteer("paul@example.org", "Paul", nil)

	// Nina answers from the home page of the edition.
	_, body := e.get(nina, home)
	mustContain(t, body, "Pour l&#39;organisation", "ring-2 ring-brand-600", `<option value="L"`, "Dimanche midi")

	if code, _ := e.post(nina, home+"/details", url.Values{"tshirt": {"XXXL"}}); code != http.StatusBadRequest {
		t.Errorf("a size not on offer: %d", code)
	}

	_, body = e.post(nina, home+"/details", url.Values{"tshirt": {"M"}, "meal": {"Samedi midi"}, "dietary": {"végétarienne"}})
	mustContain(t, body, "C&#39;est noté, merci.", `<option value="M" selected>`, "Merci, c&#39;est noté.")

	// The coordination counts, for the volunteers on a slot.
	_, body = e.get(admin, edition+"?tab=logistics")
	mustContain(t, body, "Logistique", "végétarienne", "Pas encore répondu (1)", "Omar", "2 bénévoles ne sont sur aucun créneau")

	_, body = e.get(admin, edition+"/logistics.csv")
	mustContain(t, body, "name,email,slots,minutes,tshirt,Samedi midi,Dimanche midi,dietary", "Nina,nina@example.org,1,120,M,1,,végétarienne", "Omar,omar@example.org,1,120,,,,")

	// Reminders: who has not answered, who is on no slot.
	_, body = e.post(admin, edition+"/remind", url.Values{"audience": {"details"}})
	mustContain(t, body, "E-mail de relance envoyé.")
	e.mailed("omar@example.org", "T-shirt et repas")

	_, body = e.get(admin, edition+"/volunteers?filter=idle")
	mustContain(t, body, "Sans créneau (2)", "Paul", "Écrire aux 2 personnes sans créneau")

	if strings.Contains(body, "nina@example.org") {
		t.Error("the idle filter keeps a volunteer on a slot")
	}

	e.post(admin, edition+"/remind", url.Values{"audience": {"idle"}})
	e.mailed("paul@example.org", "On a besoin de vous", "/planning?view=open")

	if n := e.mailer.count("nina@example.org"); n != 0 {
		t.Errorf("Nina got %d reminders", n)
	}

	// The time signed up for.
	_, body = e.get(admin, edition+"/volunteers")
	mustContain(t, body, "2 h")

	// The next edition offers her size again; meals are asked anew.
	next, _ := e.st.GetEvent(ctx, f.draft.ID)
	next.TshirtSizes = "S\nM"
	mustOK(t, e.st.SaveVolunteerSettings(ctx, next))

	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")

	d, err := e.st.GetVolunteerDetails(ctx, next.ID, ninaP.ID)
	mustOK(t, err)

	if d.Answered || d.Tshirt != "M" || len(d.Meals) != 0 {
		t.Errorf("prefill: %+v", d)
	}

	// The purge of the edition takes the answers with it.
	_, err = e.st.PurgeEvent(ctx, f.event.ID, time.Now().Add(-time.Hour))
	mustOK(t, err)

	if got, _ := e.st.ListVolunteerDetails(ctx, f.event.ID); len(got) != 0 {
		t.Error("answers kept after the purge")
	}
}

func TestLogisticsClosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()
	edition := "/volunteers/" + f.event.Slug + "/edition"
	home := "/volunteers/" + f.event.Slug

	p, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, p.ID))

	nina := e.loginAs("nina@example.org")

	// An edition that asks nothing takes no answer and no reminder.
	if code, _ := e.post(nina, home+"/details", url.Values{"tshirt": {""}}); code != http.StatusForbidden {
		t.Errorf("answer to an edition asking nothing: %d", code)
	}

	if code, _ := e.post(admin, edition+"/remind", url.Values{"audience": {"details"}}); code != http.StatusBadRequest {
		t.Errorf("reminder for an edition asking nothing: %d", code)
	}

	// Once frozen, the answers show but no longer change, and nobody is
	// reminded.
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersOpen}, "tshirt_sizes": {"S\nM"}})
	e.post(nina, home+"/details", url.Values{"tshirt": {"M"}})
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersFrozen}, "tshirt_sizes": {"S\nM\nL"}})

	if code, _ := e.post(nina, home+"/details", url.Values{"tshirt": {"S"}}); code != http.StatusBadRequest {
		t.Errorf("answer to a frozen edition: %d", code)
	}

	_, body := e.get(nina, home)
	mustContain(t, body, "Les inscriptions sont closes", "<fieldset class=\"space-y-4\" disabled>", `<option value="M" selected>`)

	if code, _ := e.post(admin, edition+"/remind", url.Values{"audience": {"details"}}); code != http.StatusBadRequest {
		t.Errorf("reminder for a frozen edition: %d", code)
	}

	if d, _ := e.st.GetVolunteerDetails(ctx, f.event.ID, p.ID); d.Tshirt != "M" {
		t.Errorf("answer changed after the freeze: %+v", d)
	}
}

func TestLogisticsChoicesChanged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()
	edition := "/volunteers/" + f.event.Slug + "/edition"
	home := "/volunteers/" + f.event.Slug

	p, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, p.ID))

	nina := e.loginAs("nina@example.org")

	// Nina answers when only sizes and one meal are asked.
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersOpen}, "tshirt_sizes": {"S\nM"}, "meals": {"Samedi soir"}})
	e.post(nina, home+"/details", url.Values{"tshirt": {"M"}, "meal": {"Samedi soir"}})

	_, body := e.get(nina, home)
	mustContain(t, body, "Merci, c&#39;est noté.")

	// A meal is renamed and another added: her answer is to be checked
	// again, and the meal gone still shows in the counts and the CSV.
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersOpen}, "tshirt_sizes": {"S\nM"},
		"meals": {"Samedi soir (buffet)\nDimanche midi"}})

	_, body = e.get(nina, home)
	mustContain(t, body, "Les choix proposés ont changé", "ring-2 ring-brand-600")

	_, body = e.get(admin, edition+"?tab=logistics")
	mustContain(t, body, "Réponse à revoir (1)", "plus proposé", "Relancer la personne")

	_, body = e.get(admin, edition+"/logistics.csv")
	mustContain(t, body, "tshirt,Samedi soir (buffet),Dimanche midi,Samedi soir,dietary,to_check", "Nina,nina@example.org,1,120,M,,,1,,1")

	e.post(admin, edition+"/remind", url.Values{"audience": {"details"}})
	e.mailed("nina@example.org", "T-shirt et repas")

	// Answering again settles it.
	e.post(nina, home+"/details", url.Values{"tshirt": {"M"}, "meal": {"Dimanche midi"}})

	_, body = e.get(admin, edition+"?tab=logistics")
	if strings.Contains(body, "Réponse à revoir") || strings.Contains(body, "plus proposé") {
		t.Error("an answer given again is still to check")
	}

	// A new size is to be checked by those who took none; a meal named as
	// a formula does not reach the CSV as one.
	e.post(nina, home+"/details", url.Values{"tshirt": {""}, "meal": {"Dimanche midi"}})
	e.post(admin, edition+"/settings", url.Values{"status": {store.VolunteersOpen}, "tshirt_sizes": {"S\nM\nXXL"},
		"meals": {"Dimanche midi\n=HYPERLINK(\"x\")"}})

	_, body = e.get(admin, edition+"?tab=logistics")
	mustContain(t, body, "Réponse à revoir (1)")

	_, body = e.get(admin, edition+"/logistics.csv")
	mustContain(t, body, `'=HYPERLINK(""x"")`)
}
