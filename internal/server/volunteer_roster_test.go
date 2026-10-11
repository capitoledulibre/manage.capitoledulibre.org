package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRoster(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina Bénévole", "0612345678", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))

	if _, err := e.st.Signup(ctx, f.morning.ID, nina.ID); err != nil {
		t.Fatal(err)
	}

	roster := f.buvette.ManageURL() + "/roster"
	csvURL := f.buvette.ManageURL() + "/signups.csv"
	global := "/volunteers/" + f.event.Slug + "/coordination/roster"

	// Phone numbers on paper: for the leads of the pole and the coordination
	// only.
	ninaC := e.loginAs("nina@example.org")
	lead := e.loginAs(f.leadEmail)

	for _, path := range []string{roster, csvURL} {
		if code, _ := e.get(ninaC, path); code != http.StatusForbidden {
			t.Errorf("a volunteer opens %s: %d", path, code)
		}
	}

	if code, _ := e.get(lead, global); code != http.StatusForbidden {
		t.Errorf("a lead opens the edition's roster: %d", code)
	}

	_, body := e.get(lead, f.buvette.ManageURL())
	mustContain(t, body, `href="`+roster+`"`, `href="`+csvURL+`"`)

	// The pole's roster: the volunteer with their phone, the checklist, the
	// free places, a page per day.
	_, body = e.get(lead, roster)
	mustContain(t, body, "Feuille de route", "Nina Bénévole", "06 12 34 56 78", "Café", "Allumer la machine",
		"3 places libres", "roster-box", "print:break-before-page", "data-print", "Document interne")

	// One day only.
	_, body = e.get(lead, roster+"?day="+dayKey(f.tomorrow.StartsAt))
	if strings.Contains(body, "Nina Bénévole") {
		t.Error("the day filter kept another day")
	}

	// The CSV: a line per volunteer and slot, and the empty slots.
	code, body := e.get(lead, csvURL)
	if code != http.StatusOK {
		t.Fatalf("csv: %d", code)
	}

	mustContain(t, body, "day,start,end,pole,place,title,capacity,name,email,phone",
		dayKey(f.morning.StartsAt)+","+clock(f.morning.StartsAt)+","+clock(f.morning.EndsAt)+",Buvette,,Café,1,Nina Bénévole,nina@example.org,0612345678",
		",Buvette,,,3,,,")

	// The edition's roster, every pole or one.
	admin := e.adminClient()

	_, body = e.get(admin, global)
	mustContain(t, body, "Tous les pôles", "Nina Bénévole", `href="?pole=`+f.buvette.Slug+`"`)

	_, body = e.get(admin, global+"?pole="+f.buvette.Slug)
	mustContain(t, body, "Feuille de route <span class=\"font-normal text-gray-500\">· Buvette</span>")

	_, body = e.get(admin, "/volunteers/"+f.event.Slug+"/coordination/signups.csv")
	mustContain(t, body, "Nina Bénévole,nina@example.org")
}
