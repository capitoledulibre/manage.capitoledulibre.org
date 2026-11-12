package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestRoleReferents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	lead := e.loginAs(f.leadEmail)
	e.volunteer("nina@example.org", "Nina Ref", "/volunteers/")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	paul := e.volunteer("paul@example.org", "Paul", "/volunteers/")
	paulP, _ := e.st.PersonByEmail(ctx, "paul@example.org")
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, paulP.ID))

	bar, err := e.st.CreateLocation(ctx, f.buvette.ID, "Bar")
	mustOK(t, err)
	_, err = e.st.CreateLocation(ctx, f.buvette.ID, "Cuisine")
	mustOK(t, err)

	// On the half hour, clear of the fixture's slots.
	start := time.Now().Add(96 * time.Hour).Truncate(time.Hour).Add(30 * time.Minute)
	sl := &store.Slot{PoleID: f.buvette.ID, LocationID: bar.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 2}
	mustOK(t, e.st.CreateSlot(ctx, sl))

	page := f.buvette.URL()
	slotPage := page + "/slots/" + sl.UID
	referents := f.buvette.ManageURL() + "/locations/" + bar.UID + "/referents"

	noReferents := func(why string) {
		t.Helper()

		if refs, _ := e.st.RoleReferents(ctx, f.buvette.ID); len(refs) != 0 {
			t.Fatalf("%s: %+v", why, refs)
		}
	}

	// Only a member of the pole can be one.
	_, body := e.post(lead, referents, url.Values{"person": {ninaP.UID}})
	mustContain(t, body, "doit être membre du pôle")
	noReferents("a non-member made a referent")

	// And only whoever runs the pole names one.
	e.post(paul, referents, url.Values{"person": {paulP.UID}})
	noReferents("a volunteer named a referent")

	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, ninaP.ID))

	_, body = e.post(lead, referents, url.Values{"person": {ninaP.UID}})
	mustContain(t, body, "Nina Ref")

	// On the slot's page, the role's referent comes first among the
	// contacts, then the leads.
	_, body = e.get(paul, slotPage)
	mustContain(t, body, "référent·e du poste", "responsable du pôle")

	if strings.Index(body, "Nina Ref") > strings.Index(body, "responsable du pôle") {
		t.Error("the referent does not come first")
	}

	// By role, the group names its referent.
	_, body = e.get(paul, page+"?layout=role")
	mustContain(t, body, "Référent·e :", `<strong class="font-medium text-gray-800">Nina Ref</strong>`)

	// The eve's reminder names the referent first, with why.
	_, err = e.st.Signup(ctx, sl.ID, paulP.ID)
	mustOK(t, err)

	n, err := e.app.sendVolunteerReminders(ctx, sl.StartsAt)
	if err != nil || n != 1 {
		t.Fatalf("reminders: %d %v", n, err)
	}

	mails := e.mailer.mailTo("paul@example.org")
	mail := mails[len(mails)-1]
	mustContain(t, mail, "Nina Ref (référent·e du poste), nina@example.org", "Lou Lead (responsable du pôle)")

	if strings.Index(mail, "Nina Ref") > strings.Index(mail, "Lou Lead") {
		t.Error("the reminder does not name the referent first")
	}

	// On the day, the home page's slot of the moment names them too.
	now := time.Now()
	current := &store.Slot{PoleID: f.buvette.ID, LocationID: bar.ID, StartsAt: now.Add(-10 * time.Minute), EndsAt: now.Add(50 * time.Minute), Capacity: 2}
	mustOK(t, e.st.CreateSlot(ctx, current))
	mustOK(t, e.st.ForceSignup(ctx, current.ID, paulP.ID))

	_, body = e.get(paul, "/"+f.event.Slug+"/")
	mustContain(t, body, "En ce moment", "Nina Ref", "référent·e du poste", "responsable du pôle")

	// Who leaves the pole stops being one.
	mustOK(t, e.st.LeavePole(ctx, f.buvette.ID, ninaP.ID))

	_, body = e.get(paul, slotPage)
	if strings.Contains(body, "référent·e du poste") {
		t.Error("a former member is still a referent")
	}

	// Taken off by the lead.
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, ninaP.ID))
	e.post(lead, referents+"/"+ninaP.UID+"/delete", url.Values{})
	noReferents("the referent is still there")
}
