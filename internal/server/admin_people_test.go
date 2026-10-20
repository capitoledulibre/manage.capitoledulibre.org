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
	_, body = e.get(admin, "/"+f.event.Slug+"/partners/")
	mustContain(t, body, `href="/`+f.event.Slug+`/volunteers"`, ">Bénévoles</a>", `href="/admin/volunteers"`, `href="/admin/people"`)

	_, body = e.get(admin, "/"+f.event.Slug+"/volunteers")
	mustContain(t, body, `href="/`+f.event.Slug+`/partners/"`, `href="/admin/" class="menu-item"`)

	// The menu entry opens the volunteer side of the current edition.
	_, body = e.get(admin, "/admin/volunteers")
	mustContain(t, body, "Édition · "+f.event.Name)

	// The editions list gives the volunteer status.
	_, body = e.get(admin, "/admin/editions")
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

func TestStaffSetsPersonPhoto(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	admin := e.adminClient()

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))

	// From her volunteer page, which it comes back to.
	fiche := "/" + f.event.Slug + "/volunteers/" + nina.UID

	_, body := e.get(admin, fiche)
	mustContain(t, body, `action="/admin/people/`+nina.UID+`/photo"`, `name="back" value="`+fiche+`"`)

	_, body = e.upload(admin, "/admin/people/"+nina.UID+"/photo", map[string]string{"back": fiche}, "photo", "nina.png", pngBytes())
	mustContain(t, body, "Photo de Nina enregistrée.")

	got, _ := e.st.GetPerson(ctx, nina.ID)
	if got.AvatarFile == "" || got.AvatarSource != store.AvatarUpload {
		t.Fatalf("avatar: %q %q", got.AvatarFile, got.AvatarSource)
	}

	if code, _ := e.get(admin, "/avatars/"+got.AvatarFile); code != http.StatusOK {
		t.Errorf("avatar served: %d", code)
	}

	// Not a picture: refused, the old one kept.
	e.upload(admin, "/admin/people/"+nina.UID+"/photo", nil, "photo", "nina.pdf", []byte("%PDF-1.4"))

	if again, _ := e.st.GetPerson(ctx, nina.ID); again.AvatarFile != got.AvatarFile {
		t.Error("a refused file replaced the picture")
	}

	// A coordinator who is not staff cannot.
	e.post(admin, "/"+f.event.Slug+"/volunteers/coordinators", url.Values{"email": {"coco@example.org"}})

	e.upload(e.loginAs("coco@example.org"), "/admin/people/"+nina.UID+"/photo", nil, "photo", "x.png", pngBytes())

	if again, _ := e.st.GetPerson(ctx, nina.ID); again.AvatarFile != got.AvatarFile {
		t.Error("a coordinator changed the picture")
	}

	// Taken off, as she would herself.
	e.post(admin, "/admin/people/"+nina.UID+"/photo/delete", url.Values{"back": {fiche}})

	if gone, _ := e.st.GetPerson(ctx, nina.ID); gone.AvatarFile != "" || gone.AvatarSource != store.AvatarOptOut {
		t.Errorf("after removal: %q %q", gone.AvatarFile, gone.AvatarSource)
	}
}
