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

// TestRestrictedPoleStaysTheCoordinations: a manager of a restricted pole
// neither lifts the restriction nor brings anyone in, by any way.
func TestRestrictedPoleStaysTheCoordinations(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	mustOK(t, e.st.AddPoleManager(ctx, f.secret.ID, f.lead.ID))
	lead := e.loginAs(f.leadEmail)

	// Lifting it is ignored; the page does not even offer it.
	code, body := e.get(lead, f.secret.URL()+"?tab=settings")
	if code != http.StatusOK || !strings.Contains(body, `value="restricted"`) || strings.Contains(body, `value="invitation"`) {
		t.Error("a restricted pole's manager is offered another mode")
	}

	e.post(lead, f.secret.ManageURL(), url.Values{"name": {f.secret.Name}, "access_mode": {store.PoleInvitation}})

	if p, _ := e.st.GetPole(ctx, f.secret.ID); p.AccessMode != store.PoleRestricted {
		t.Fatalf("a manager lifted the restriction: %s", p.AccessMode)
	}

	// Nor brings anyone in: by address, by invitation on a slot, nor sees
	// the people outside it.
	if code, _ := e.post(lead, f.secret.ManageURL()+"/members", url.Values{"emails": {"x@example.org"}}); code != http.StatusForbidden {
		t.Errorf("members added: %d", code)
	}

	sl := &store.Slot{PoleID: f.secret.ID, StartsAt: f.morning.StartsAt, EndsAt: f.morning.EndsAt, Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, sl))

	sl, err := e.st.GetSlot(ctx, sl.ID, 0)
	mustOK(t, err)

	if code, _ := e.post(lead, sl.ManageURL()+"/volunteers", url.Values{"email": {"new@example.org"}}); code != http.StatusForbidden {
		t.Errorf("invited on a slot: %d", code)
	}

	if _, err := e.st.PersonByEmail(ctx, "new@example.org"); err == nil {
		t.Error("an account was made by a refused invitation")
	}

	if code, _ := e.get(lead, f.secret.ManageURL()+"/people?q=ex"); code != http.StatusForbidden {
		t.Errorf("people searched: %d", code)
	}

	// The coordination does.
	e.post(e.adminClient(), f.secret.ManageURL(), url.Values{"name": {f.secret.Name}, "access_mode": {store.PoleInvitation}})

	if p, _ := e.st.GetPole(ctx, f.secret.ID); p.AccessMode != store.PoleInvitation {
		t.Errorf("the coordination did not lift it: %s", p.AccessMode)
	}
}

// TestPoleNoticesForItsPeople: a private pole's notices are for its people,
// not for a volunteer who happens on its page.
func TestPoleNoticesForItsPeople(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	mustOK(t, e.st.CreateMessage(ctx, &store.Message{EventID: f.event.ID, PoleID: f.accueil.ID, Body: "Code de la porte : 4242"}, f.lead.ID))

	outsider := e.volunteer("out@example.org", "Out", "/volunteers/")

	_, body := e.get(outsider, f.accueil.URL())
	if strings.Contains(body, "4242") {
		t.Error("a private pole's notice shown to an outsider")
	}

	in, err := e.st.EnsureVolunteer(ctx, "in@example.org", "In", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.accueil.ID, in.ID))

	_, body = e.get(e.loginAs("in@example.org"), f.accueil.URL())
	mustContain(t, body, "4242")
}

// TestMCPKeepsAccountsPrivate: the tools that name people answer alike for
// an address with an account and one without, to whom may not act.
func TestMCPKeepsAccountsPrivate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	_, err := e.st.UpsertAdmin(ctx, "boss@conf.test", "Boss")
	mustOK(t, err)

	other, err := e.st.EnsureVolunteer(ctx, "lead2@example.org", "Lead 2", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.AddPoleManager(ctx, f.secret.ID, other.ID))

	token, _, err := e.st.CreateAPIToken(ctx, other.ID, nil, "Claude", true, time.Hour)
	mustOK(t, err)

	s := e.mcpClient(token)

	answers := func(tool string, args func(email string) map[string]any) {
		known, _ := call(t, s, tool, args("boss@conf.test"))
		unknown, _ := call(t, s, tool, args("ghost@example.org"))

		if known != unknown {
			t.Errorf("%s tells accounts apart: %q / %q", tool, known, unknown)
		}
	}

	slot := func(email string) map[string]any {
		return map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "slot": f.morning.UID, "email": email, "emails": []string{email}}
	}

	answers("sign_up_people", slot)
	answers("take_off_slot", slot)
	answers("remove_member", func(email string) map[string]any {
		return map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "email": email}
	})

	// On their own restricted pole too, where they may not bring anyone in.
	sl := &store.Slot{PoleID: f.secret.ID, StartsAt: f.morning.StartsAt, EndsAt: f.morning.EndsAt, Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, sl))

	answers("sign_up_people", func(email string) map[string]any {
		return map[string]any{"edition": f.event.Slug, "pole": f.secret.Slug, "slot": sl.UID, "emails": []string{email}}
	})

	if _, err := e.st.PersonByEmail(ctx, "ghost@example.org"); err == nil {
		t.Error("an account was made by a refused sign-up")
	}
}
