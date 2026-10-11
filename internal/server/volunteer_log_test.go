package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manage/internal/store"
)

func TestVolunteerLog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	admin := e.adminClient()
	lead := e.loginAs(f.leadEmail)
	nina := e.volunteer("nina@example.org", "Nina Bénévole", "/volunteers/")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")

	// What a volunteer does, and what a lead does to them.
	e.post(nina, f.morning.URL()+"/signup", nil)
	e.post(nina, f.overlap.URL()+"/signup", url.Values{"force": {"1"}})
	e.post(lead, f.overlap.ManageURL()+"/volunteers/"+ninaP.UID+"/delete", nil)
	e.post(lead, f.morning.ManageURL(), url.Values{
		"starts_at": {f.morning.StartsAt.Add(30 * 60e9).In(paris).Format("2006-01-02T15:04")},
		"ends_at":   {f.morning.EndsAt.Add(30 * 60e9).In(paris).Format("2006-01-02T15:04")},
		"capacity":  {"1"}, "title": {"Café"},
	})
	e.post(lead, f.tomorrow.ManageURL()+"/delete", nil)

	_, body := e.get(lead, f.buvette.ManageURL()+"?tab=history")
	mustContain(t, body, "Historique",
		"Nina Bénévole a rejoint le pôle Buvette",
		"Nina Bénévole s&#39;est inscrit·e sur Buvette",
		"Lou Lead a retiré Nina Bénévole de Buvette",
		"Lou Lead a déplacé un créneau du pôle Buvette, désormais",
		"Lou Lead a supprimé un créneau du pôle Buvette")

	// The coordination sees the edition's, including its own actions.
	base := "/volunteers/" + f.event.Slug + "/coordination"
	e.post(admin, base+"/poles", url.Values{"name": {"Régie"}})
	e.post(admin, base+"/settings", url.Values{"status": {store.VolunteersFrozen}})

	_, body = e.get(admin, base+"?tab=history")
	mustContain(t, body, "a créé le pôle Régie", "a passé l&#39;édition en « Gelé »", "Nina Bénévole s&#39;est inscrit·e")

	// In English for an English reader.
	e.get(lead, "/lang/en")
	_, body = e.get(lead, f.buvette.ManageURL()+"?tab=history")
	mustContain(t, body, "Nina Bénévole signed up for Buvette", "Lou Lead took Nina Bénévole off Buvette")

	// A volunteer does not read it.
	if code, _ := e.get(nina, f.buvette.ManageURL()+"?tab=history"); code != http.StatusForbidden {
		t.Fatalf("a volunteer reads the history: %d", code)
	}

	// Someone who deletes their account leaves no name behind.
	e.post(nina, "/volunteers/profile/delete", url.Values{"confirm": {"yes"}})

	e.get(lead, "/lang/fr")
	_, body = e.get(lead, f.buvette.ManageURL()+"?tab=history")

	if strings.Contains(body, "Nina Bénévole") {
		t.Error("a deleted person is still named in the history")
	}

	mustContain(t, body, "une personne supprimée s&#39;est inscrit·e sur Buvette")
}

func TestAdminPersonPage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	admin := e.adminClient()
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.post(nina, f.morning.URL()+"/signup", nil)

	org := &store.Organization{Name: "Acme"}
	mustOK(t, e.st.CreateOrganization(ctx, org))
	_, err := e.st.AddManager(ctx, &store.Manager{OrgID: org.ID, Email: "nina@example.org"})
	mustOK(t, err)

	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	page := "/admin/people/" + ninaP.UID

	// From the list to the page.
	_, body := e.get(admin, "/admin/people")
	mustContain(t, body, `href="`+page+`?back=`)

	_, body = e.get(admin, page)
	mustContain(t, body, "Nina", "Partenaire · Acme", `href="/admin/organizations/`+org.Slug+`"`, "Buvette", f.morning.ManageURL(),
		"Nina s&#39;est inscrit·e sur Buvette", "Envoyer un lien de connexion")

	// Correcting the details; a new address logs the person out.
	_, body = e.post(admin, page, url.Values{"name": {"Nina Durand"}, "email": {"nina.durand@example.org"}, "phone": {"0601020304"}, "lang": {"en"}})
	mustContain(t, body, "Adresse modifiée")

	p, _ := e.st.GetPerson(ctx, ninaP.ID)
	if p.Name != "Nina Durand" || p.Email != "nina.durand@example.org" || p.Phone != "0601020304" || p.Lang != "en" {
		t.Fatalf("person: %+v", p)
	}

	// The old session is gone: back to the login form.
	if _, body := e.get(nina, "/volunteers/"+f.event.Slug); !strings.Contains(body, `action="/login"`) {
		t.Error("the old session still works after the address changed")
	}

	// An address already used is refused.
	e.volunteer("omar@example.org", "Omar", "/volunteers/")

	_, body = e.post(admin, page, url.Values{"name": {"Nina Durand"}, "email": {"omar@example.org"}, "lang": {"fr"}})
	mustContain(t, body, "omar@example.org est déjà l&#39;adresse d&#39;une autre personne.")

	// A login link valid a week.
	_, body = e.post(admin, page+"/send-link", nil)
	mustContain(t, body, "Lien de connexion envoyé à nina.durand@example.org.")
	e.mailed("nina.durand@example.org", "valid for 7 days", "/auth/")

	// The staff keep their address.
	staff, _ := e.st.PersonByEmail(ctx, "staff@conf.test")
	e.post(admin, "/admin/people/"+staff.UID, url.Values{"name": {"Sam"}, "email": {"other@conf.test"}, "lang": {"fr"}})

	if p, _ := e.st.GetPerson(ctx, staff.ID); p.Email != "staff@conf.test" || p.Name != "Sam" {
		t.Fatalf("staff person: %+v", p)
	}
}
