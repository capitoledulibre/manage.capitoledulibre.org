package server

import (
	"context"
	"net/url"
	"testing"

	"manage/internal/store"
)

// TestPersonLogin covers the single login: a session belongs to a person,
// whatever they come for, and a partner organization is only what that
// person is looking at.
func TestPersonLogin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)

	t.Run("an unknown address gets no link, and is not told so", func(t *testing.T) {
		_, body := e.post(e.client(), "/login", url.Values{"email": {"nobody@example.test"}})
		mustContain(t, body, "adresse connue")

		if n := e.mailer.count("nobody@example.test"); n != 0 {
			t.Fatalf("%d e-mails sent to an unknown address", n)
		}
	})

	t.Run("a known person without an organization reaches their home page", func(t *testing.T) {
		// A staff member is a known person; through an e-mail link they get
		// a person session, with no partner organization.
		_, err := e.st.UpsertAdmin(ctx, "sam@conf.test", "Sam Staff")
		mustOK(t, err)

		_, body := e.post(e.client(), "/login", url.Values{"email": {"sam@conf.test"}})
		mustContain(t, body, "adresse connue")

		c := e.client()
		_, body = e.post(c, e.mailer.lastLink(t, "sam@conf.test"), nil)
		mustContain(t, body, "Bonjour Sam Staff", "aucun espace ne vous est ouvert")

		if code, _ := e.get(c, "/partners/organization"); code != 200 {
			t.Fatalf("partner pages should send back home, got %d", code)
		}
	})

	t.Run("one person, two organizations, one session", func(t *testing.T) {
		globex := &store.Organization{Name: "Globex"}
		mustOK(t, e.st.CreateOrganization(ctx, globex))

		g := &store.Manager{OrgID: globex.ID, Email: f.alice.Email}
		_, err := e.st.AddManager(ctx, g)
		mustOK(t, err)

		if g.PersonID != f.alice.PersonID {
			t.Fatal("the same address should be the same person")
		}

		c := e.loginAs(f.alice.Email)
		_, body := e.get(c, "/partners/choose")
		mustContain(t, body, "Acme", "Globex")

		_, body = e.post(c, "/partners/choose", url.Values{"manager": {g.UID}})
		mustContain(t, body, "Globex")

		// Removed from Globex, Alice keeps her login and picks again.
		mustOK(t, e.st.DetachManagerSessions(ctx, g.ID))
		mustOK(t, e.st.DeleteManager(ctx, globex.ID, g.ID))

		_, body = e.get(c, "/")
		mustContain(t, body, "Acme")

		// Someone else's organization cannot be picked.
		if code, _ := e.post(c, "/partners/choose", url.Values{"manager": {f.bob.UID}}); code != 403 {
			t.Fatalf("picking another person's organization: %d", code)
		}
	})
}
