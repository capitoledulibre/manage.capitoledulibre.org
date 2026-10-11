package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manage/internal/store"
)

func TestAdminWelcomeAndTabs(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()

	// A fresh install opens on the steps.
	code, body := e.get(admin, "/admin/")
	if code != 200 {
		t.Fatalf("admin home: %d", code)
	}

	mustContain(t, body, "Créer la première édition", "Organiser les bénévoles")

	f := e.volFixture()
	mustOK(t, e.st.SaveEvent(context.Background(), &store.Event{ID: f.event.ID, Slug: f.event.Slug, Name: f.event.Name, Year: f.event.Year, IsCurrent: true}))

	// The edition has its two sides, linked both ways.
	_, body = e.get(admin, "/admin/events/"+f.event.Slug)
	mustContain(t, body, `href="/volunteers/`+f.event.Slug+`/coordination"`, ">Bénévoles</a>", `href="/admin/volunteers"`, `href="/admin/people"`)

	_, body = e.get(admin, "/volunteers/"+f.event.Slug+"/coordination")
	mustContain(t, body, `href="/admin/events/`+f.event.Slug+`"`, `href="/admin/" class="menu-item"`)

	// The menu entry opens the coordination of the current edition.
	_, body = e.get(admin, "/admin/volunteers")
	mustContain(t, body, "Coordination · "+f.event.Name)

	// The editions list gives the volunteer status.
	_, body = e.get(admin, "/admin/events")
	mustContain(t, body, "Inscriptions ouvertes", "Brouillon")
}

func TestAdminPeople(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	mustOK(t, e.st.SaveEvent(ctx, &store.Event{ID: f.event.ID, Slug: f.event.Slug, Name: f.event.Name, Year: f.event.Year, IsCurrent: true}))
	acme, _, _ := e.addOrg("Acme", "alice@acme.test", f.event, store.StatusConfirmed, nil)

	admin := e.adminClient()
	nina := e.volunteer("nina@example.org", "Nina Bénévole", "/volunteers/")
	e.post(nina, f.morning.URL()+"/signup", nil)

	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	e.post(omar, f.buvette.URL()+"/join", nil)
	e.post(omar, f.morning.URL()+"/waitlist", nil)

	// Not for partners or volunteers: they are sent to the staff login.
	noFollow := &http.Client{Jar: nina.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, req := range []func() (*http.Response, error){
		func() (*http.Response, error) { return noFollow.Get(e.srv.URL + "/admin/people") },
		func() (*http.Response, error) {
			return noFollow.PostForm(e.srv.URL+"/admin/people/"+f.lead.UID+"/delete", nil)
		},
	} {
		resp, err := req()
		mustOK(t, err)
		resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login" {
			t.Fatalf("a volunteer reaches the people list: %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	_, body := e.get(admin, "/admin/people")
	mustContain(t, body, "Nina Bénévole", "Bénévole · Buvette", "Partenaire · "+acme.Name, "Staff", "Lou Lead")

	_, body = e.get(admin, "/admin/people?q=nina")
	mustContain(t, body, "Nina Bénévole", "1 à 1 sur 1")

	if strings.Contains(body, "Lou Lead") {
		t.Error("the search keeps everyone")
	}

	// Deleting a volunteer frees their place for the waiting list.
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")

	_, body = e.post(admin, "/admin/people/"+ninaP.UID+"/delete", url.Values{"back": {"/admin/people?q=nina"}})
	mustContain(t, body, "Nina Bénévole a été supprimé·e.")

	if _, err := e.st.PersonByEmail(ctx, "nina@example.org"); err != store.ErrNotFound {
		t.Fatalf("person kept: %v", err)
	}

	e.mailed("omar@example.org", "Une place s'est libérée")

	// The staff are not deleted here.
	staff, _ := e.st.PersonByEmail(ctx, "staff@conf.test")

	_, body = e.post(admin, "/admin/people/"+staff.UID+"/delete", nil)
	mustContain(t, body, "Un membre du staff ne se supprime pas ici")
}
