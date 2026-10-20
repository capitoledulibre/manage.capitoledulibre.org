package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

// Findings of the security audit of 2026-10-11, kept fixed.

func TestSafeRedirect(t *testing.T) {
	for _, next := range []string{
		"", "evil.com", "//evil.com", "/\\evil.com", "/./\\evil.com", "/a/../\\evil.com",
		"https://evil.com", "/\t/evil.com", "/x\r\nLocation: //evil.com",
	} {
		if got := safeRedirect(next, "/home"); got != "/home" {
			t.Errorf("%q kept as %q", next, got)
		}
	}

	for _, next := range []string{"/volunteers/", "/ec-2026/partner/", "/a/b#c", "/./x"} {
		if got := safeRedirect(next, "/home"); got != next {
			t.Errorf("%q refused", next)
		}
	}

	// End to end: http.Redirect cleans "/./\host" into "/\host", which a
	// browser follows to another site.
	e := newEnv(t)
	c := e.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.Get(e.srv.URL + "/lang/fr?next=/./%5Cevil.com")
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	if loc := resp.Header.Get("Location"); strings.Contains(loc, "evil") {
		t.Errorf("redirected to %q", loc)
	}
}

func TestSecretsOutOfLogs(t *testing.T) {
	for path, want := range map[string]string{
		"/auth/s3cr3t":         "/auth/…",
		"/calendar/s3cr3t.ics": "/calendar/…",
		"/ec-2026/me":          "/ec-2026/me",
	} {
		if got := logPath(httptest.NewRequest(http.MethodGet, path, nil)); got != want {
			t.Errorf("%s logged as %s", path, got)
		}
	}
}

func TestDevAdminLoginFromTheMachineOnly(t *testing.T) {
	e := newEnv(t)

	for addr, want := range map[string]bool{"127.0.0.1:5000": true, "[::1]:5000": true, "203.0.113.5:5000": false, "192.168.1.20:5000": false} {
		r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
		r.RemoteAddr = addr

		if got := e.app.devAdminLogin(r); got != want {
			t.Errorf("dev login from %s: %v", addr, got)
		}
	}
}

func TestLeadSearchFindsVolunteersOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// A partner contact, a staff member, a volunteer of another pole and
	// one in no pole yet.
	e.addOrg("Acme", "alice@acme.test", f.event, store.StatusConfirmed, nil)

	_, err := e.st.UpsertAdmin(ctx, "staff@conf.test", "")
	mustOK(t, err)

	_, err = e.st.EnsureVolunteer(ctx, "newbie@example.org", "Newbie", "", "fr")
	mustOK(t, err)

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.accueil.ID, nina.ID))

	lead := e.loginAs(f.leadEmail)
	search := f.buvette.ManageURL() + "/people?q="

	if _, body := e.get(lead, search+"acme"); strings.Contains(body, "alice@acme.test") {
		t.Error("a lead finds a partner contact")
	}

	if _, body := e.get(lead, search+"staff"); strings.Contains(body, "staff@conf.test") {
		t.Error("a lead finds a staff member")
	}

	_, body := e.get(lead, search+"nina")
	mustContain(t, body, "nina@example.org")

	_, body = e.get(lead, search+"newbie")
	mustContain(t, body, "newbie@example.org")
}

func TestAttendeesCSVIsSafe(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)

	mustOK(t, e.st.AddAttendee(ctx, &store.Attendee{PartnershipID: f.acmeP.ID, FirstName: "=HYPERLINK(\"https://evil\")", LastName: "+1", Dietary: "@SUM(A1)"}))

	_, body := e.get(e.adminClient(), "/"+f.event.Slug+"/partners/attendees.csv")
	mustContain(t, body, `'=HYPERLINK`, "'+1", "'@SUM")
}

func TestSlotPageOfAClosedPole(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// A slot of the private pole, with someone on it and a message.
	start := f.morning.StartsAt
	badges := &store.Slot{PoleID: f.accueil.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 2, Title: "Badges"}
	mustOK(t, e.st.CreateSlot(ctx, badges))

	paul, err := e.st.EnsureVolunteer(ctx, "paul@example.org", "Paul Insider", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.accueil.ID, paul.ID))
	mustOK(t, e.st.ForceSignup(ctx, badges.ID, paul.ID))
	mustOK(t, e.st.CreateMessage(ctx, &store.Message{EventID: f.event.ID, PoleID: f.accueil.ID, SlotID: badges.ID, Body: "Code du local : 1234"}, paul.ID))

	badges, _ = e.st.GetSlot(ctx, badges.ID, 0)

	// A volunteer outside the pole sees the slot, not who is on it.
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	_, body := e.get(nina, badges.URL())

	for _, hidden := range []string{"Paul Insider", "Code du local"} {
		if strings.Contains(body, hidden) {
			t.Errorf("a non-member sees %q", hidden)
		}
	}

	_, body = e.get(e.loginAs("paul@example.org"), badges.URL())
	mustContain(t, body, "Paul Insider", "Code du local")
}

func TestUnnamedVolunteerKeepsTheirAddress(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// Added by address by the lead, never logged in: no name yet.
	quiet, err := e.st.EnsureVolunteer(ctx, "quiet@example.org", "", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, quiet.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.overlap.ID, quiet.ID))

	planning := "/" + f.event.Slug + "/planning?day=all"

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	for _, path := range []string{planning, f.overlap.URL(), f.buvette.URL()} {
		if _, body := e.get(nina, path); strings.Contains(body, "quiet@example.org") {
			t.Errorf("%s shows the address of an unnamed volunteer", path)
		}
	}

	_, body := e.get(e.loginAs(f.leadEmail), planning)
	mustContain(t, body, "quiet@example.org")
}

func TestLeadAddsAndRemovesOnlyTheirPeople(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// The lead of a restricted pole may sign up its members, not bring in
	// new people: only the coordination does.
	mustOK(t, e.st.AddPoleManager(ctx, f.secret.ID, f.lead.ID))

	start := f.morning.StartsAt
	coffre := &store.Slot{PoleID: f.secret.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 3, Title: "Coffre"}
	mustOK(t, e.st.CreateSlot(ctx, coffre))
	coffre, _ = e.st.GetSlot(ctx, coffre.ID, 0)

	lead := e.loginAs(f.leadEmail)

	if code, _ := e.post(lead, coffre.ManageURL()+"/volunteers", url.Values{"email": {"stranger@example.org"}}); code != http.StatusForbidden {
		t.Errorf("a restricted pole's lead brings in a new person: %d", code)
	}

	member, err := e.st.EnsureVolunteer(ctx, "member@example.org", "Member", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.secret.ID, member.ID))

	if code, _ := e.post(lead, coffre.ManageURL()+"/volunteers", url.Values{"person": {member.UID}}); code != http.StatusOK {
		t.Errorf("signing up a member: %d", code)
	}

	// Removing someone who is not there: no e-mail, no name revealed.
	outsider, err := e.st.EnsureVolunteer(ctx, "outsider@example.org", "Outsider", "", "fr")
	mustOK(t, err)

	for _, path := range []string{
		f.buvette.ManageURL() + "/members/" + outsider.UID + "/delete",
		f.morning.ManageURL() + "/volunteers/" + outsider.UID + "/delete",
	} {
		if code, _ := e.post(lead, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d", path, code)
		}
	}

	if n := e.mailer.count("outsider@example.org"); n != 0 {
		t.Errorf("the outsider got %d e-mails", n)
	}
}

func TestRateKey(t *testing.T) {
	for ip, want := range map[string]string{
		"203.0.113.5":          "203.0.113.5",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"::ffff:203.0.113.5":   "::ffff:203.0.113.5",
		"not an address":       "not an address",
	} {
		if got := rateKey(ip); got != want {
			t.Errorf("rateKey(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestGeneratorStopsEarly(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()

	// A thousand ranges times a thousand places: a million slots, refused
	// at once rather than counted one by one.
	var ranges, places strings.Builder

	for i := range 1000 {
		ranges.WriteString("09:00-10:00\n")
		places.WriteString("Salle " + strconv.Itoa(i) + "\n")
	}

	start := time.Now()
	_, body := e.post(e.loginAs(f.leadEmail), f.buvette.ManageURL()+"/generate", url.Values{
		"from": {"2026-11-14"}, "to": {"2026-11-15"}, "mode": {"list"}, "list": {ranges.String()},
		"new_locations": {places.String()}, "capacity": {"1"}, "action": {"preview"},
	})

	mustContain(t, body, "plus de 2 000 créneaux")

	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("preview took %s", d)
	}
}

func TestRequestBodiesAreBounded(t *testing.T) {
	e := newEnv(t)

	// A form field after a large file part: the server stops reading.
	body := strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"f\"; filename=\"a\"\r\n\r\n" +
		strings.Repeat("a", maxUploadBody+1) + "\r\n--x\r\nContent-Disposition: form-data; name=\"email\"\r\n\r\na@b.fr\r\n--x--\r\n")

	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/login", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")

	resp, err := e.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// The address after the file was never reached: the form is refused as
	// if it had none.
	mustContain(t, string(page), "Adresse e-mail invalide.")
}

func TestCalendarFeedIsSealed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// A title with a bare CR cannot add properties to the feed.
	f.morning.Title = "Café\rBEGIN:VALARM"
	mustOK(t, e.st.UpdateSlot(ctx, f.morning))

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))

	token, err := e.st.CalendarToken(ctx, nina.ID)
	mustOK(t, err)

	_, body := e.get(e.client(), "/calendar/"+token+".ics")
	if strings.Contains(body, "\rBEGIN:VALARM") {
		t.Error("a CR in a title reaches the feed")
	}

	// A corrected address cuts off the calendar of the old one.
	nina.Email = "nina.dupont@example.org"
	mustOK(t, e.st.UpdatePerson(ctx, nina))

	if _, err := e.st.PersonByCalendarToken(ctx, token); err == nil {
		t.Error("the calendar token survives a change of address")
	}

	// Unchanged address, token kept.
	again, err := e.st.CalendarToken(ctx, nina.ID)
	mustOK(t, err)

	nina.Name = "Nina Dupont"
	mustOK(t, e.st.UpdatePerson(ctx, nina))

	if _, err := e.st.PersonByCalendarToken(ctx, again); err != nil {
		t.Error("the calendar token goes with a change of name")
	}
}
