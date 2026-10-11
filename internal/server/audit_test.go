package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	for _, next := range []string{"/volunteers/", "/partners/?event=ec-2026", "/a/b#c", "/./x"} {
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
		"/auth/s3cr3t":           "/auth/…",
		"/calendar/s3cr3t.ics":   "/calendar/…",
		"/volunteers/ec-2026/me": "/volunteers/ec-2026/me",
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

	_, body := e.get(e.adminClient(), "/admin/events/"+f.event.Slug+"/attendees.csv")
	mustContain(t, body, `'=HYPERLINK`, "'+1", "'@SUM")
}
