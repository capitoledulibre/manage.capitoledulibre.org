package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

// fixture is a small data set built directly in the store.
type fixture struct {
	event    *store.Event
	level    *store.Level
	acme     *store.Organization
	acmeP    *store.Partnership
	alice    *store.Manager
	prospect *store.Organization
	prosP    *store.Partnership
	bob      *store.Manager
}

func (e *env) fixture(attendeesDeadline *time.Time) *fixture {
	e.t.Helper()

	ctx := context.Background()
	f := &fixture{}

	f.event = &store.Event{Slug: "ec-2026", Name: "EC 2026", Year: 2026, IsCurrent: true, AttendeesDeadline: attendeesDeadline}
	mustOK(e.t, e.st.SaveEvent(ctx, f.event))

	f.level = &store.Level{EventID: f.event.ID, Name: "Or", Position: 2, Price: 2000}
	mustOK(e.t, e.st.SaveLevel(ctx, f.level))

	f.acme, f.acmeP, f.alice = e.addOrg("Acme", "alice@acme.test", f.event, store.StatusConfirmed, &f.level.ID)
	f.prospect, f.prosP, f.bob = e.addOrg("Prospect Inc", "bob@prospect.test", f.event, store.StatusProspect, nil)

	return f
}

func (e *env) addOrg(name, email string, ev *store.Event, status string, levelID *int64) (*store.Organization, *store.Partnership, *store.Manager) {
	e.t.Helper()

	ctx := context.Background()

	o := &store.Organization{Name: name}
	mustOK(e.t, e.st.CreateOrganization(ctx, o))

	p := &store.Partnership{OrgID: o.ID, EventID: ev.ID, Status: status, LevelID: levelID}
	mustOK(e.t, e.st.CreatePartnership(ctx, p))

	m := &store.Manager{OrgID: o.ID, Email: email}
	_, err := e.st.AddManager(ctx, m)
	mustOK(e.t, err)

	return o, p, m
}

// loginAs logs a new client in as the manager email, without going through
// the mailbox.
func (e *env) loginAs(email string) *http.Client {
	e.t.Helper()

	token, err := e.st.CreateMagicToken(context.Background(), email, time.Minute)
	mustOK(e.t, err)

	c := e.client()
	if code, _ := e.post(c, "/auth/"+token, nil); code != 200 {
		e.t.Fatalf("login %s: %d", email, code)
	}

	return c
}

func (e *env) adminClient() *http.Client {
	e.t.Helper()

	c := e.client()
	if code, body := e.post(c, "/admin/login", url.Values{"email": {"staff@conf.test"}}); code != 200 || !strings.Contains(body, "Déconnexion") {
		e.t.Fatalf("admin login: %d", code)
	}

	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func TestAccessControl(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	past := time.Now().AddDate(0, 0, -3)
	f := e.fixture(&past)

	alice := e.loginAs("alice@acme.test")
	bob := e.loginAs("bob@prospect.test")
	admin := e.adminClient()

	t.Run("attendees locked after the deadline for partners only", func(t *testing.T) {
		if code, _ := e.post(alice, "/p/attendees", url.Values{"first_name": {"A"}, "last_name": {"B"}}); code != 403 {
			t.Errorf("partner add after deadline: %d", code)
		}

		_, body := e.get(alice, "/p/attendees")
		mustContain(t, body, "La date limite est passée")

		e.post(admin, partnershipURL(f.acme, f.event)+"/attendees", url.Values{"first_name": {"A"}, "last_name": {"B"}})

		if n, _ := e.st.CountAttendees(ctx, f.acmeP.ID); n != 1 {
			t.Errorf("staff add after deadline: %d attendees", n)
		}
	})

	t.Run("prospect partnership is hidden from the partner", func(t *testing.T) {
		f.prosP.QuoteFile = "0123456789abcdef0123456789abcdef.pdf"
		mustOK(t, e.st.UpdatePartnership(ctx, f.prosP))

		_, body := e.get(bob, "/p/")
		mustContain(t, body, "Aucun partenariat en cours")

		if code, _ := e.get(bob, "/p/documents/"+f.event.Slug+"/quote"); code != 404 {
			t.Errorf("prospect quote: %d", code)
		}
	})

	t.Run("cannot switch to another person's account", func(t *testing.T) {
		if code, _ := e.post(bob, "/p/choose", url.Values{"manager": {f.alice.UID}}); code != 403 {
			t.Errorf("choose foreign manager: %d", code)
		}
	})

	t.Run("admin area requires a staff session", func(t *testing.T) {
		anon := e.client()

		_, body := e.get(anon, "/admin/organizations")
		mustContain(t, body, "Connexion staff")

		e.post(anon, "/admin/organizations", url.Values{"name": {"Intruder"}})
		e.get(alice, "/admin/organizations/"+f.acme.Slug)

		orgs, _ := e.st.ListOrganizations(ctx)
		for _, o := range orgs {
			if o.Name == "Intruder" {
				t.Error("anonymous user created an organization")
			}
		}

		if code, _ := e.get(anon, "/api/partners.json?token=wrong"); code != 401 {
			t.Errorf("export with wrong token: %d", code)
		}
	})

	t.Run("removed manager loses access immediately", func(t *testing.T) {
		carol := &store.Manager{OrgID: f.acme.ID, Email: "carol@acme.test"}
		_, err := e.st.AddManager(ctx, carol)
		mustOK(t, err)

		carolClient := e.loginAs("carol@acme.test")
		e.post(alice, "/p/managers/"+carol.UID+"/delete", nil)

		_, body := e.get(carolClient, "/p/")
		mustContain(t, body, "Recevoir un lien")
	})

	t.Run("the last manager cannot be removed", func(t *testing.T) {
		_, body := e.post(bob, "/p/managers/"+f.bob.UID+"/delete", nil)
		mustContain(t, body, "dernier manager")
	})

	t.Run("prospect logos are not public", func(t *testing.T) {
		mustOK(t, e.st.AddLogo(ctx, &store.Logo{OrgID: f.prospect.ID, Variant: store.LogoDefault, File: "11111111111111111111111111111111.png", UploadedBy: "t"}))
		mustOK(t, e.st.AddLogo(ctx, &store.Logo{OrgID: f.acme.ID, Variant: store.LogoDefault, File: "22222222222222222222222222222222.png", UploadedBy: "t"}))

		// The files do not exist on disk: 404 from the file store once
		// access is granted, 404 from the handler otherwise. Check the
		// cache headers to tell both apart.
		check := func(c *http.Client, file, wantCache string) {
			t.Helper()

			resp, err := c.Get(e.srv.URL + "/logos/" + file)
			mustOK(t, err)
			resp.Body.Close()

			if got := resp.Header.Get("Cache-Control"); got != wantCache {
				t.Errorf("%s: Cache-Control %q, want %q", file, got, wantCache)
			}
		}

		check(e.client(), "11111111111111111111111111111111.png", "")
		check(bob, "11111111111111111111111111111111.png", "private, no-cache")
		check(admin, "11111111111111111111111111111111.png", "private, no-cache")
		check(e.client(), "22222222222222222222222222222222.png", "public, max-age=86400")
	})

	t.Run("stop impersonation", func(t *testing.T) {
		e.post(admin, "/admin/organizations/"+f.acme.Slug+"/view-as", nil)
		_, body := e.get(admin, "/p/")
		mustContain(t, body, "en tant que staff")

		e.post(admin, "/admin/stop-impersonate", nil)
		_, body = e.get(admin, "/p/")
		mustContain(t, body, "Recevoir un lien")
	})

	t.Run("dev login is refused on a public base URL", func(t *testing.T) {
		base := e.app.cfg.BaseURL
		e.app.cfg.BaseURL = "https://partners.example.org"

		defer func() { e.app.cfg.BaseURL = base }()

		if code, _ := e.post(e.client(), "/admin/login", url.Values{"email": {"staff@conf.test"}}); code != 404 {
			t.Errorf("dev login on public URL: %d", code)
		}

		if code, _ := e.get(e.client(), "/admin/login"); code != 404 {
			t.Errorf("dev login form on public URL: %d", code)
		}
	})
}

func TestEditionIsKeptInLinks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)

	old := &store.Event{Slug: "ec-2025", Name: "EC 2025", Year: 2025}
	mustOK(t, e.st.SaveEvent(ctx, old))
	mustOK(t, e.st.CreatePartnership(ctx, &store.Partnership{OrgID: f.acme.ID, EventID: old.ID, Status: store.StatusConfirmed}))

	alice := e.loginAs("alice@acme.test")

	_, body := e.get(alice, "/p/?event=ec-2025")
	mustContain(t, body, "Avancement · EC 2025", `href="/p/attendees?event=ec-2025"`, "event%3dec-2025")

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "Avancement · EC 2026")
}

func TestSocialPosts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()
	alice := e.loginAs("alice@acme.test")
	base := partnershipURL(f.acme, f.event) + "/posts"

	e.post(admin, base, url.Values{"network": {"linkedin"}, "planned_at": {"2026-10-12"}, "note": {"Annonce Or"}})

	_, body := e.get(alice, "/p/")
	mustContain(t, body, "Annonces sur les réseaux sociaux", "LinkedIn", "Prévu le 12/10/2026", "À venir")

	posts, _ := e.st.ListSocialPosts(ctx, f.acmeP.ID)
	if len(posts) != 1 {
		t.Fatalf("posts: %d", len(posts))
	}

	before := e.mailer.count("alice@acme.test")

	e.post(admin, base, url.Values{"post": {posts[0].UID}, "network": {"linkedin"}, "planned_at": {"2026-10-12"},
		"url": {"https://www.linkedin.com/posts/ec-123"}, "notify": {"on"}})

	waitFor(t, func() bool { return e.mailer.count("alice@acme.test") == before+1 })

	_, body = e.get(alice, "/p/")
	mustContain(t, body, `href="https://www.linkedin.com/posts/ec-123"`, "Voir le post")

	// Saving again does not notify twice.
	e.post(admin, base, url.Values{"post": {posts[0].UID}, "network": {"linkedin"}, "url": {"https://www.linkedin.com/posts/ec-123"}, "notify": {"on"}})
	time.Sleep(50 * time.Millisecond)

	if n := e.mailer.count("alice@acme.test"); n != before+1 {
		t.Errorf("notified %d times", n-before)
	}

	// Invalid network and cross-partnership edits are rejected.
	if code, _ := e.post(admin, base, url.Values{"network": {"myspace"}}); code != 400 {
		t.Errorf("invalid network: %d", code)
	}

	if code, _ := e.post(admin, partnershipURL(f.prospect, f.event)+"/posts", url.Values{"post": {posts[0].UID}, "network": {"x"}}); code != 404 {
		t.Errorf("edit through another partnership: %d", code)
	}

	// Prospects do not see anything, even if posts exist.
	mustOK(t, e.st.SaveSocialPost(ctx, &store.SocialPost{PartnershipID: f.prosP.ID, Network: "x"}))

	_, body = e.get(e.loginAs("bob@prospect.test"), "/p/")
	if strings.Contains(body, "Annonces sur les réseaux") {
		t.Error("prospect sees social posts")
	}
}

func TestReminderRetriedAfterMailFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	deadline := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := e.fixture(nil)
	f.event.LogoDeadline = &deadline
	mustOK(t, e.st.SaveEvent(ctx, f.event))

	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	e.mailer.setFail(true)
	mustOK(t, e.app.sendReminders(ctx, at))

	e.mailer.setFail(false)
	mustOK(t, e.app.sendReminders(ctx, at))
	mustOK(t, e.app.sendReminders(ctx, at))

	if n := e.mailer.count("alice@acme.test"); n != 1 {
		t.Errorf("expected exactly one reminder after the outage, got %d", n)
	}

	if n := e.mailer.count("bob@prospect.test"); n != 0 {
		t.Errorf("prospects must not be reminded: %d", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	for i := 0; i < 100; i++ {
		if cond() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition not met")
}

func TestPricingAndTax(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()
	alice := e.loginAs("alice@acme.test")

	// Level price, excluding VAT by default.
	_, body := e.get(alice, "/p/")
	mustContain(t, body, "2 000 € HT")

	// The level switches to "VAT not applicable".
	e.post(admin, "/admin/events/"+f.event.Slug+"/levels", url.Values{
		"level": {f.level.UID}, "name": {"Or"}, "position": {"2"}, "price": {"2000"}, "tax_mode": {"none"},
	})

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "2 000 € TVA non applicable")

	// A negotiated price replaces the level price everywhere.
	pid := partnershipURL(f.acme, f.event)
	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "price_override": {"1500"}})

	p, _ := e.st.GetPartnership(ctx, f.acmeP.ID)
	if p.PriceOverride == nil || *p.PriceOverride != 1500 {
		t.Fatalf("override not saved: %v", p.PriceOverride)
	}

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "1 500 € TVA non applicable")

	_, body = e.get(admin, "/admin/events/"+f.event.Slug)
	mustContain(t, body, "Montant confirmé", "1 500 €", "négocié")

	// Clearing the field goes back to the level price; bad input is refused.
	if code, _ := e.post(admin, pid, url.Values{"status": {"confirmed"}, "price_override": {"-3"}}); code != 400 {
		t.Errorf("negative price: %d", code)
	}

	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "price_override": {""}})

	p, _ = e.st.GetPartnership(ctx, f.acmeP.ID)
	if p.PriceOverride != nil {
		t.Error("override not cleared")
	}

	// Document steps show green checks once done.
	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID},
		"quote_sent_at": {"2026-09-10"}, "quote_signed_at": {"2026-09-15"}})

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "Signé · 15/09/2026", "Envoyée · en attente")
}

func TestAdminManagers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()

	// The first contact's name is stored when creating an organization.
	e.post(admin, "/admin/organizations", url.Values{"name": {"Nouvelle"}, "manager_name": {"Nina Durand"}, "manager_email": {"nina@nouvelle.test"}})

	ms, _ := e.st.ManagersByEmail(ctx, "nina@nouvelle.test")
	if len(ms) != 1 || ms[0].Name != "Nina Durand" {
		t.Fatalf("first contact: %+v", ms)
	}

	alice := e.loginAs("alice@acme.test")
	carol := &store.Manager{OrgID: f.acme.ID, Email: "carol@acme.test"}
	_, err := e.st.AddManager(ctx, carol)
	mustOK(t, err)

	path := "/admin/organizations/" + f.acme.Slug + "/managers/" + f.alice.UID

	// Renaming keeps the session.
	e.post(admin, path, url.Values{"name": {"Alice Martin"}, "email": {"alice@acme.test"}, "lang": {"en"}})

	m, _ := e.st.GetManager(ctx, f.alice.ID)
	if m.Name != "Alice Martin" || m.Lang != "en" {
		t.Errorf("not updated: %+v", m)
	}

	_, body := e.get(alice, "/p/")
	mustContain(t, body, "Dashboard")

	// A duplicate email within the organization is refused.
	_, body = e.post(admin, path, url.Values{"name": {"Alice"}, "email": {"carol@acme.test"}})
	mustContain(t, body, "déjà manager")

	// Changing the email logs the manager out.
	e.post(admin, path, url.Values{"name": {"Alice Martin"}, "email": {"alice.martin@acme.test"}})

	m, _ = e.st.GetManager(ctx, f.alice.ID)
	if m.Email != "alice.martin@acme.test" {
		t.Errorf("email not changed: %s", m.Email)
	}

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "Recevoir un lien")

	// A manager of another organization cannot be edited through this one.
	if code, _ := e.post(admin, "/admin/organizations/"+f.acme.Slug+"/managers/"+f.bob.UID, url.Values{"email": {"x@y.test"}}); code != 404 {
		t.Errorf("cross-organization edit: %d", code)
	}
}

func TestRenewOwnerAndPayment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()
	other := e.client()
	e.post(other, "/admin/login", url.Values{"email": {"other@conf.test"}})

	admins, _ := e.st.ListAdmins(ctx)
	var me, colleague *store.Admin

	for _, a := range admins {
		switch a.Email {
		case "staff@conf.test":
			me = a
		case "other@conf.test":
			colleague = a
		}
	}

	// Owner and "my partners" filter.
	pid := partnershipURL(f.acme, f.event)
	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "owner": {me.UID}})
	e.post(admin, partnershipURL(f.prospect, f.event), url.Values{"status": {"prospect"}, "owner": {colleague.UID}})

	_, body := e.get(admin, "/admin/events/"+f.event.Slug+"?mine=1")
	mustContain(t, body, "Acme", "staff@conf.test", "Tous les partenaires")

	if strings.Contains(body, "Prospect Inc") {
		t.Error("mine filter shows a colleague's partnership")
	}

	if code, _ := e.post(admin, pid, url.Values{"status": {"confirmed"}, "owner": {"not-a-uid"}}); code != 400 {
		t.Errorf("unknown owner: %d", code)
	}

	// Renewal for the next edition keeps level (by name), owner and managers.
	next := &store.Event{Slug: "ec-2027", Name: "EC 2027", Year: 2027}
	mustOK(t, e.st.SaveEvent(ctx, next))

	nextGold := &store.Level{EventID: next.ID, Name: "Or", Price: 2500}
	mustOK(t, e.st.SaveLevel(ctx, nextGold))

	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "owner": {me.UID}})
	addEdition := "/admin/organizations/" + f.acme.Slug + "/partnerships"
	_, body = e.post(admin, addEdition, url.Values{"event": {next.Slug}})
	mustContain(t, body, "Partenariat reconduit", "Pipeline EC 2027")

	parts, _ := e.st.ListEventPartnerships(ctx, next.ID)
	if len(parts) != 1 {
		t.Fatalf("renewed partnerships: %d", len(parts))
	}

	np := parts[0]
	if np.OrgID != f.acme.ID || np.Status != store.StatusContacted || np.LevelID == nil || *np.LevelID != nextGold.ID ||
		np.OwnerAdminID == nil || *np.OwnerAdminID != me.ID {
		t.Errorf("unexpected renewal: %+v", np)
	}

	_, body = e.post(admin, addEdition, url.Values{"event": {next.Slug}, "return": {pid}})
	mustContain(t, body, "déjà un partenariat")

	// Payment instructions appear on the partner dashboard.
	f.event.PaymentInfo = "IBAN : FR76 1234"
	f.event.PaymentMention = "TVA non applicable, art. 293 B du CGI"
	mustOK(t, e.st.SaveEvent(ctx, f.event))

	f.acmeP, _ = e.st.GetPartnership(ctx, f.acmeP.ID)
	f.acmeP.PONumber = "PO-42"
	mustOK(t, e.st.UpdatePartnership(ctx, f.acmeP))

	_, body = e.get(e.loginAs("alice@acme.test"), "/p/?event=ec-2026")
	mustContain(t, body, "Règlement", "IBAN : FR76 1234", "art. 293 B", "Bon de commande : PO-42")
}

func TestProfileAndTheme(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	e.post(alice, "/p/profile", url.Values{"name": {"Alice M."}, "lang": {"en"}})

	m, _ := e.st.GetManager(ctx, f.alice.ID)
	if m.Name != "Alice M." || m.Lang != "en" {
		t.Errorf("profile not saved: %+v", m)
	}

	_, body := e.get(alice, "/p/managers")
	mustContain(t, body, "My profile", "Alice M.")

	// Staff impersonating a partner cannot edit a manager's profile.
	admin := e.adminClient()
	e.post(admin, "/admin/organizations/"+f.acme.Slug+"/view-as", nil)

	if code, _ := e.post(admin, "/p/profile", url.Values{"name": {"Hacked"}}); code != 403 {
		t.Errorf("impersonated profile edit: %d", code)
	}

	// Theme: system by default, explicit choices rendered by the server.
	anon := e.client()

	_, body = e.get(anon, "/")
	mustContain(t, body, `data-theme="system"`, `class="h-full bg-gray-50"`)

	_, body = e.get(anon, "/theme/dark?next=/jobs")
	mustContain(t, body, `data-theme="dark"`, `class="h-full bg-gray-50 dark"`, "Nos partenaires recrutent")

	_, body = e.get(anon, "/theme/light?next=/")
	mustContain(t, body, `data-theme="light"`)

	if code, _ := e.get(anon, "/theme/purple"); code != 404 {
		t.Errorf("invalid theme: %d", code)
	}

	// Open redirects are refused.
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
		Get(e.srv.URL + "/theme/dark?next=//evil.example")
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("open redirect: %q", loc)
	}
}

func TestAdminProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	admin := e.adminClient()

	// Initials are shown until an avatar is uploaded.
	e.post(admin, "/admin/profile", url.Values{"name": {"Lionel Porcheron"}})
	_, body := e.get(admin, "/admin/profile")
	mustContain(t, body, "Lionel Porcheron", ">LP<")

	_, body = e.upload(admin, "/admin/profile/avatar", nil, "avatar", "me.png", pngBytes())
	mustContain(t, body, "Modifications enregistrées")

	admins, _ := e.st.ListAdmins(ctx)
	if len(admins) != 1 || admins[0].AvatarFile == "" {
		t.Fatalf("avatar not saved: %+v", admins)
	}

	avatar := admins[0].AvatarFile
	mustContain(t, body, "/avatars/"+avatar)

	if code, _ := e.get(admin, "/avatars/"+avatar); code != 200 {
		t.Errorf("staff avatar access: %d", code)
	}

	if code, _ := e.get(e.client(), "/avatars/"+avatar); code != 404 {
		t.Errorf("anonymous avatar access: %d", code)
	}

	_, body = e.upload(admin, "/admin/profile/avatar", nil, "avatar", "me.pdf", []byte("%PDF-1.4"))
	mustContain(t, body, "Format non accepté")

	// A new login keeps the name set on the profile.
	e.adminClient()

	a, _ := e.st.GetAdmin(ctx, admins[0].ID)
	if a.Name != "Lionel Porcheron" || a.AvatarFile != avatar {
		t.Errorf("profile lost on login: %+v", a)
	}

	e.post(admin, "/admin/profile/avatar/delete", nil)

	a, _ = e.st.GetAdmin(ctx, a.ID)
	if a.AvatarFile != "" {
		t.Error("avatar not removed")
	}
}

func TestStaticAssetsAreVersioned(t *testing.T) {
	e := newEnv(t)

	_, body := e.get(e.client(), "/")
	v := assetVersions["/static/css/app.css"]

	if v == "" || !strings.Contains(body, "/static/css/app.css?v="+v) {
		t.Fatalf("stylesheet URL not versioned (v=%q)", v)
	}

	check := func(path, want string) {
		t.Helper()

		resp, err := http.Get(e.srv.URL + path)
		mustOK(t, err)
		resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", path, got, want)
		}
	}

	check("/static/css/app.css?v="+v, "public, max-age=31536000, immutable")
	check("/static/css/app.css?v=stale", "no-cache")
	check("/static/css/app.css", "no-cache")
}

func TestMedalsBadgesAndContact(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()

	// The level color is saved; an invalid value falls back to the default.
	e.post(admin, "/admin/events/"+f.event.Slug+"/levels", url.Values{
		"level": {f.level.UID}, "name": {"Or"}, "position": {"2"}, "price": {"2000"}, "color": {"#123ABC"},
	})

	l, _ := e.st.GetLevel(ctx, f.level.ID)
	if l.Color != "#123abc" {
		t.Errorf("color: %q", l.Color)
	}

	e.post(admin, "/admin/events/"+f.event.Slug+"/levels", url.Values{
		"level": {f.level.UID}, "name": {"Or"}, "position": {"2"}, "price": {"2000"}, "color": {"red; background:url(x)"},
	})

	l, _ = e.st.GetLevel(ctx, f.level.ID)
	if l.Color != "" {
		t.Errorf("invalid color kept: %q", l.Color)
	}

	// A past confirmed edition shows as a badge on the staff and partner pages.
	old := &store.Event{Slug: "ec-2025", Name: "EC 2025", Year: 2025}
	mustOK(t, e.st.SaveEvent(ctx, old))

	platinum := &store.Level{EventID: old.ID, Name: "Platine", Position: 3, Price: 4000}
	mustOK(t, e.st.SaveLevel(ctx, platinum))
	mustOK(t, e.st.CreatePartnership(ctx, &store.Partnership{OrgID: f.acme.ID, EventID: old.ID, Status: store.StatusConfirmed, LevelID: &platinum.ID}))

	_, body := e.get(admin, "/admin/organizations/"+f.acme.Slug)
	mustContain(t, body, "Partenaire Platine 2025", "Partenaire Or 2026", "<svg")

	// The partner sees the medal of their level, their badges and their
	// staff contact, whose avatar they may load.
	me, _ := e.st.UpsertAdmin(ctx, "staff@conf.test", "")
	me.Name = "Lionel Porcheron"
	me.AvatarFile = "33333333333333333333333333333333.png"
	mustOK(t, e.st.UpdateAdminProfile(ctx, me))

	f.acmeP, _ = e.st.GetPartnership(ctx, f.acmeP.ID)
	f.acmeP.OwnerAdminID = &me.ID
	mustOK(t, e.st.UpdatePartnership(ctx, f.acmeP))

	alice := e.loginAs("alice@acme.test")

	_, body = e.get(alice, "/p/")
	mustContain(t, body, "Partenaire Platine 2025", "Votre contact", "Lionel Porcheron", "mailto:staff@conf.test", "/avatars/"+me.AvatarFile)

	// Access is granted (the file does not exist on disk, hence the file
	// store 404), unlike for another organization's manager.
	check := func(c *http.Client, wantCache string) {
		t.Helper()

		resp, err := c.Get(e.srv.URL + "/avatars/" + me.AvatarFile)
		mustOK(t, err)
		resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != wantCache {
			t.Errorf("Cache-Control %q, want %q", got, wantCache)
		}
	}

	check(alice, "private, max-age=3600")
	check(e.loginAs("bob@prospect.test"), "")
}

func TestMedalColors(t *testing.T) {
	if got := mixHex("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("mixHex: %s", got)
	}

	for name, want := range map[string]string{"Or": "#d4a42c", "Gold": "#d4a42c", "Platine": "#d3e4f0", "Argent": "#aab2bb", "Bronze": "#c07a3a", "Soutien": "#ea580c"} {
		if got := defaultMedalColor(name); got != want {
			t.Errorf("defaultMedalColor(%q) = %s, want %s", name, got, want)
		}
	}

	levels := []*store.Level{{ID: 3}, {ID: 2}, {ID: 1}}
	if a, b := rankedMedal(levels[0], levels).Size, rankedMedal(levels[2], levels).Size; a <= b {
		t.Errorf("higher level should get a larger medal: %d <= %d", a, b)
	}

	if m := newMedal(nil, 20); m.Base == "" || m.Light == "" || m.Dark == "" {
		t.Errorf("medal without level: %+v", m)
	}
}

func TestPartnerFileNavigation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()

	old := &store.Event{Slug: "ec-2025", Name: "EC 2025", Year: 2025}
	mustOK(t, e.st.SaveEvent(ctx, old))

	oldP := &store.Partnership{OrgID: f.acme.ID, EventID: old.ID, Status: store.StatusConfirmed}
	mustOK(t, e.st.CreatePartnership(ctx, oldP))

	// The partner list opens the file on the most recent edition.
	_, body := e.get(admin, "/admin/organizations")
	mustContain(t, body, `href="`+partnershipURL(f.acme, f.event)+`"`, "Dernière édition")

	// Every tab of the file shares the header: breadcrumb, one tab per
	// edition and the organization tab, with the active one marked.
	_, body = e.get(admin, partnershipURL(f.acme, old))
	mustContain(t, body, "Pipeline EC 2025", `href="`+partnershipURL(f.acme, f.event)+`"`, ">2026", ">2025",
		`href="/admin/organizations/`+f.acme.Slug+`"`, "Voir l'édition 2025 comme le partenaire", `name="event" value="ec-2025"`)

	_, body = e.get(admin, "/admin/organizations/"+f.acme.Slug)
	mustContain(t, body, ">Partenaires</a>", "Voir comme le partenaire", "Managers")

	// The active menu section follows the page.
	for path, want := range map[string]string{
		"/admin/events/" + f.event.Slug:             "pipeline",
		"/admin/events/" + f.event.Slug + "/levels": "editions",
		partnershipURL(f.acme, f.event):             "partners",
		"/admin/organizations":                      "partners",
		"/admin/profile":                            "profile",
	} {
		if got := adminNav(path); got != want {
			t.Errorf("adminNav(%s) = %q, want %q", path, got, want)
		}
	}

	// Viewing as the partner opens the edition of the tab, and coming back
	// returns to that tab.
	back := partnershipURL(f.acme, old)
	_, body = e.post(admin, "/admin/organizations/"+f.acme.Slug+"/view-as", url.Values{"event": {"ec-2025"}, "return": {back}})
	mustContain(t, body, "Vue partenaire de", "Avancement · EC 2025")

	_, body = e.post(admin, "/admin/stop-impersonate", nil)
	mustContain(t, body, "Pipeline EC 2025", "Voir l'édition 2025 comme le partenaire")

	// The return target cannot leave the admin area.
	e.post(admin, "/admin/organizations/"+f.acme.Slug+"/view-as", url.Values{"return": {"https://evil.example/"}})

	resp, err := (&http.Client{Jar: admin.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
		PostForm(e.srv.URL+"/admin/stop-impersonate", nil)
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/admin/organizations/"+f.acme.Slug {
		t.Errorf("unsafe return: %q", loc)
	}

	// Adding the first edition of a new partner starts as a prospect.
	fresh := &store.Organization{Name: "Nouveau"}
	mustOK(t, e.st.CreateOrganization(ctx, fresh))
	e.post(admin, "/admin/organizations/"+fresh.Slug+"/partnerships", url.Values{"event": {f.event.Slug}})

	parts, _ := e.st.ListOrgPartnerships(ctx, fresh.ID)
	if len(parts) != 1 || parts[0].Status != store.StatusProspect {
		t.Errorf("first edition: %+v", parts)
	}
}

func TestCloneLevelsFromAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()

	mustOK(t, e.st.SavePerk(ctx, &store.Perk{EventID: f.event.ID, NameFR: "Logo sur le site"}))

	// A new edition can start from the levels of the previous one.
	_, body := e.post(admin, "/admin/events/new", url.Values{"name": {"EC 2027"}, "year": {"2027"}, "copy_from": {f.event.Slug}})
	mustContain(t, body, "Niveaux et contreparties copiés", `value="Or"`, "Logo sur le site")

	next, err := e.st.GetEventBySlug(ctx, "ec-2027")
	mustOK(t, err)

	// Copying again from the levels page adds nothing.
	_, body = e.post(admin, "/admin/events/"+next.Slug+"/clone", url.Values{"source": {f.event.Slug}})
	mustContain(t, body, "Rien à copier")

	if code, _ := e.post(admin, "/admin/events/"+next.Slug+"/clone", url.Values{"source": {next.Slug}}); code != 400 {
		t.Errorf("clone into itself: %d", code)
	}

	// Without copy_from, a new edition starts empty.
	e.post(admin, "/admin/events/new", url.Values{"name": {"EC 2028"}, "year": {"2028"}})

	empty, _ := e.st.GetEventBySlug(ctx, "ec-2028")
	if levels, _ := e.st.ListLevels(ctx, empty.ID); len(levels) != 0 {
		t.Errorf("levels copied without asking: %d", len(levels))
	}
}

// TestNoNumericIDsInPages renders every page with data and checks that no
// link, form action or submitted identifier is an integer id.
func TestNoNumericIDsInPages(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	admin := e.adminClient()

	me, _ := e.st.UpsertAdmin(ctx, "staff@conf.test", "")
	perk := &store.Perk{EventID: f.event.ID, NameFR: "Logo"}
	mustOK(t, e.st.SavePerk(ctx, perk))
	mustOK(t, e.st.SetLevelPerkMatrix(ctx, f.event.ID, [][2]int64{{f.level.ID, perk.ID}}))

	f.acmeP.OwnerAdminID = &me.ID
	mustOK(t, e.st.UpdatePartnership(ctx, f.acmeP))
	mustOK(t, e.st.AddAttendee(ctx, &store.Attendee{PartnershipID: f.acmeP.ID, FirstName: "A", LastName: "B"}))
	mustOK(t, e.st.SaveSocialPost(ctx, &store.SocialPost{PartnershipID: f.acmeP.ID, Network: "x"}))
	mustOK(t, e.st.SaveJobOffer(ctx, &store.JobOffer{OrgID: f.acme.ID, Title: "Dev", URL: "https://acme.test/jobs"}))

	old := &store.Event{Slug: "ec-2025", Name: "EC 2025", Year: 2025}
	mustOK(t, e.st.SaveEvent(ctx, old))

	numeric := regexp.MustCompile(`(?:href|action)="[^"]*/[0-9]+(?:[/"?#])` +
		`|<option value="[0-9]+"` +
		`|type="(?:hidden|checkbox|radio)" name="[^"]*" value="[0-9]+"` +
		`|name="lp" value="[0-9]+[-:]`)

	check := func(c *http.Client, path string) {
		t.Helper()

		code, body := e.get(c, path)
		if code != 200 {
			t.Errorf("%s: status %d", path, code)

			return
		}

		for _, m := range numeric.FindAllString(body, -1) {
			t.Errorf("%s exposes an integer id: %s", path, m)
		}
	}

	for _, path := range []string{
		"/admin/events", "/admin/events/new", eventURL(f.event), eventURL(f.event) + "/edit", eventURL(f.event) + "/levels",
		"/admin/organizations", orgURL(f.acme), partnershipURL(f.acme, f.event), partnershipURL(f.prospect, f.event), "/admin/profile",
	} {
		check(admin, path)
	}

	alice := e.loginAs("alice@acme.test")

	for _, path := range []string{"/p/", "/p/organization", "/p/logo", "/p/attendees", "/p/jobs", "/p/managers", "/jobs"} {
		check(alice, path)
	}

	jobs, _ := e.st.ListJobOffers(ctx, f.acme.ID)
	check(alice, "/p/jobs/"+jobs[0].UID)
}
