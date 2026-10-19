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

func TestPlanning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	planning := "/volunteers/" + f.event.Slug + "/planning"

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina Bénévole", "0612345678", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))

	if _, err := e.st.Signup(ctx, f.morning.ID, nina.ID); err != nil {
		t.Fatal(err)
	}

	// A slot in a restricted pole and one in an invitation-only pole Omar
	// is not in.
	start := f.morning.StartsAt
	coffre := &store.Slot{PoleID: f.secret.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 1, Title: "Coffre"}
	badges := &store.Slot{PoleID: f.accueil.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 1, Title: "Badges"}

	for _, sl := range []*store.Slot{coffre, badges} {
		mustOK(t, e.st.CreateSlot(ctx, sl))
	}

	omarC := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	lead := e.loginAs(f.leadEmail)
	admin := e.adminClient()

	// A volunteer sees the open poles and their own, with who is signed up
	// but no phone number, and signs up only themselves.
	_, body := e.get(omarC, planning+"?day=all")
	mustContain(t, body, "Buvette", "Café", "Nina Bénévole", "/signup", "Tout", "Mes créneaux", "À pourvoir")

	for _, hidden := range []string{"06 12 34 56 78", "Coffre", "Badges", "Modifier le créneau", "signups.csv"} {
		if strings.Contains(body, hidden) {
			t.Errorf("a volunteer sees %q", hidden)
		}
	}

	// Signing up from the planning puts them in the list of the card. (A
	// sign-up that joins the pole reloads the whole page instead.)
	omar, _ := e.st.PersonByEmail(ctx, "omar@example.org")
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, omar.ID))

	_, body = e.hx(omarC, f.overlap.URL()+"/signup", url.Values{"back": {planning + "?day=all"}})
	mustContain(t, body, "Omar", `aria-label="Inscrit·es"`)

	// Views: mine, to fill.
	_, body = e.get(omarC, planning+"?view=mine&day=all")
	mustContain(t, body, `aria-current="page">Mes créneaux`, `href="`+f.overlap.URL()+`"`, "/calendar/")

	if strings.Contains(body, `href="`+f.morning.URL()+`"`) {
		t.Error("my slots list a slot of someone else")
	}

	_, body = e.get(omarC, planning+"?view=open&day=all")
	if strings.Contains(body, `href="`+f.morning.URL()+`"`) {
		t.Error("a full slot is to fill")
	}

	// A day: the others go; without a day, today during the edition.
	_, body = e.get(omarC, planning+"?day="+dayKey(f.tomorrow.StartsAt))
	if strings.Contains(body, "Café") {
		t.Error("the day filter kept another day")
	}

	// The lead sees the phone numbers of their pole and the links to manage
	// it; the restricted pole stays hidden.
	_, body = e.get(lead, planning+"?pole="+f.buvette.Slug+"&day=all")
	mustContain(t, body, "06 12 34 56 78", "Modifier le créneau", `href="`+f.buvette.ManageURL()+`/signups.csv"`)

	if strings.Contains(body, "Coffre") {
		t.Error("a lead sees a restricted pole they are not in")
	}

	// The coordination sees every pole.
	_, body = e.get(admin, planning+"?day=all")
	mustContain(t, body, "Coffre", "Badges", "06 12 34 56 78", `href="/volunteers/`+f.event.Slug+`/edition/signups.csv"`)

	// On paper: a day per page, boxes to tick, a line per free place.
	mustContain(t, body, "hidden print:block", "break-before-page", "roster-box", "roster-blank", "Document interne")

	// The CSV: a line per volunteer and slot, and the empty slots; for the
	// leads of the pole and the coordination only.
	csvURL := f.buvette.ManageURL() + "/signups.csv"

	if code, _ := e.get(omarC, csvURL); code != http.StatusForbidden {
		t.Errorf("a volunteer downloads the CSV: %d", code)
	}

	code, body := e.get(lead, csvURL)
	if code != http.StatusOK {
		t.Fatalf("csv: %d", code)
	}

	mustContain(t, body, "day,start,end,pole,place,title,capacity,name,email,phone",
		dayKey(f.morning.StartsAt)+","+clock(f.morning.StartsAt)+","+clock(f.morning.EndsAt)+",Buvette,,Café,1,Nina Bénévole,nina@example.org,0612345678",
		",Buvette,,,3,,,")

	_, body = e.get(admin, "/volunteers/"+f.event.Slug+"/edition/signups.csv")
	mustContain(t, body, "Nina Bénévole,nina@example.org", "Coffre")
}
