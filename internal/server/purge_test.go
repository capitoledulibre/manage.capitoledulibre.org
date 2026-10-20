package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"manage/internal/config"
	"manage/internal/store"
)

func TestPurgeEdition(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()
	edit := "/admin/editions/" + f.event.Slug
	purge := "/admin/editions/" + f.event.Slug + "/purge"

	// A partner with an attendee, and its change log.
	org, ps, _ := e.addOrg("Acme", "alice@acme.test", f.event, store.StatusConfirmed, nil)
	mustOK(t, e.st.AddAttendee(ctx, &store.Attendee{PartnershipID: ps.ID, FirstName: "Bob", LastName: "Martin", Email: "bob@acme.test", Dietary: "sans gluten"}))
	mustOK(t, e.st.LogActivity(ctx, &store.Activity{OrgID: org.ID, PartnershipID: &ps.ID, Actor: "alice@acme.test", Action: "attendee_added", Details: "Bob Martin"}))

	// A volunteer of this edition only, signed up, in its history.
	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "0612345678", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))
	mustOK(t, e.st.Log(ctx, &store.LogEntry{EventID: f.event.ID, PoleID: f.buvette.ID, SubjectID: nina.ID, SubjectName: "Nina", Action: store.LogSignup, PoleName: "Buvette"}))

	// Not before the edition is over.
	_, body := e.post(admin, purge, nil)
	mustContain(t, body, "pas terminée")

	if got, _ := e.st.ListAttendees(ctx, ps.ID); len(got) != 1 {
		t.Fatal("purged before the end")
	}

	// Once it is over.
	start, end := time.Now().AddDate(0, 0, -3), time.Now().AddDate(0, 0, -2)
	f.event.StartsOn, f.event.EndsOn = &start, &end
	mustOK(t, e.st.SaveEvent(ctx, f.event))

	_, body = e.get(admin, edit)
	mustContain(t, body, "Purger les données de l'édition")

	_, body = e.post(admin, purge, nil)
	mustContain(t, body, "Édition purgée", "1 participants, 1 inscriptions", "Purgées le")

	if got, _ := e.st.ListAttendees(ctx, ps.ID); len(got) != 0 {
		t.Error("attendees kept")
	}

	if acts, _ := e.st.ListActivity(ctx, org.ID, 50); len(acts) != 0 {
		t.Errorf("partnership log kept: %d", len(acts))
	}

	if sl, _ := e.st.GetSlot(ctx, f.morning.ID, 0); sl.Filled != 0 {
		t.Error("sign-ups kept")
	}

	entries, _, _ := e.st.ListEventLog(ctx, f.event.ID, store.LogPage{})
	for _, en := range entries {
		if en.SubjectName != "" || en.ActorName != "" || en.SubjectID != 0 {
			t.Errorf("a name stays in the history: %+v", en)
		}
	}

	// The slots and poles stay, for the next edition.
	if _, err := e.st.GetPole(ctx, f.buvette.ID); err != nil {
		t.Error("the pole went")
	}

	// Nina joined after the end of the edition (here, at the test's start):
	// she stays. The partner contact and the staff always do.
	for _, addr := range []string{"nina@example.org", "alice@acme.test", "staff@conf.test"} {
		if _, err := e.st.PersonByEmail(ctx, addr); err != nil {
			t.Errorf("%s deleted", addr)
		}
	}

	// People with no part left who have not been seen since the end go,
	// with their address in the partner log.
	mustOK(t, e.st.LogActivity(ctx, &store.Activity{OrgID: org.ID, Actor: "nina@example.org", Action: "x", Details: "invited nina@example.org"}))

	res, err := e.st.PurgeEvent(ctx, f.event.ID, time.Now().Add(time.Hour))
	mustOK(t, err)

	if res.People == 0 {
		t.Fatal("nobody deleted")
	}

	if _, err := e.st.PersonByEmail(ctx, "nina@example.org"); err == nil {
		t.Error("a person with no part left stays")
	}

	for _, addr := range []string{"alice@acme.test", "staff@conf.test"} {
		if _, err := e.st.PersonByEmail(ctx, addr); err != nil {
			t.Errorf("%s deleted", addr)
		}
	}

	acts, _ := e.st.ListActivity(ctx, org.ID, 50)
	for _, a := range acts {
		if a.Actor == "nina@example.org" || strings.Contains(a.Details, "nina@example.org") {
			t.Errorf("the address stays in the partner log: %+v", a)
		}
	}
}

func TestPrivacyNotice(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.ContactEmail, c.VolunteerContactEmail = "partners@conf.test", "volunteers@conf.test"
	})
	c := e.client()

	// Linked from every page, for anyone.
	_, body := e.get(c, "/")
	mustContain(t, body, `href="/privacy"`)

	_, body = e.get(c, "/privacy")
	mustContain(t, body, "Données personnelles", "volunteers@conf.test", "partners@conf.test", "CNIL")

	if strings.Contains(body, "gravatar.com") {
		t.Error("Gravatar described while it is off")
	}

	e.get(c, "/lang/en")

	_, body = e.get(c, "/privacy")
	mustContain(t, body, "Personal data", "Your rights")
}
