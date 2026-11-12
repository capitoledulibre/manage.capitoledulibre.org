package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"manage/internal/config"
	"manage/internal/gravatar"
	"manage/internal/store"
)

func TestVolunteerReminders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	mustOK(t, e.st.SetSharePhone(ctx, f.lead.ID, true))

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.post(nina, f.tomorrow.URL()+"/signup", nil)

	// A slot of a draft edition, the same day, is not recalled.
	draftPole := &store.Pole{EventID: f.draft.ID, Name: "Prépa"}
	mustOK(t, e.st.CreatePole(ctx, draftPole))

	draftSlot := &store.Slot{PoleID: draftPole.ID, StartsAt: f.tomorrow.StartsAt, EndsAt: f.tomorrow.EndsAt, Capacity: 1}
	mustOK(t, e.st.CreateSlot(ctx, draftSlot))

	omar, _ := e.st.EnsureVolunteer(ctx, "omar@example.org", "Omar", "", "fr")
	mustOK(t, e.st.JoinPole(ctx, draftPole.ID, omar.ID))
	_, err := e.st.Signup(ctx, draftSlot.ID, omar.ID)
	mustOK(t, err)

	n, err := e.app.sendVolunteerReminders(ctx, f.tomorrow.StartsAt)
	if err != nil || n != 1 {
		t.Fatalf("reminders: %d %v", n, err)
	}

	mails := e.mailer.mailTo("nina@example.org")
	body := mails[len(mails)-1]
	mustContain(t, body, "Vos créneaux de", "Voici vos créneaux de bénévolat", "Buvette", f.tomorrow.URL(),
		"Contact : Lou Lead (responsable du pôle), lead@conf.test, 06 11 22 33 44", "/"+f.event.Slug+"/planning?view=mine")

	if e.mailer.count("omar@example.org") != 0 {
		t.Fatal("a draft edition's slot was recalled")
	}

	// Once a day.
	if n, _ := e.app.sendVolunteerReminders(ctx, f.tomorrow.StartsAt); n != 0 {
		t.Fatalf("recalled twice: %d", n)
	}
}

func TestProfilePhotoAndGravatar(t *testing.T) {
	var hits atomic.Int32

	known := gravatar.Hash("nina@example.org")
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if !strings.HasSuffix(r.URL.Path, "/"+known) {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes())
	}))
	t.Cleanup(gs.Close)

	e := newEnv(t, func(c *config.Config) { c.Gravatar, c.GravatarURL = true, gs.URL })
	ctx := context.Background()
	f := e.volFixture()

	// Nobody is looked up without asking: browsing tells gravatar.com
	// nothing.
	nina := e.volunteer("nina@example.org", "Nina", f.buvette.URL())
	e.post(nina, f.buvette.URL()+"/join", nil)
	e.app.gravatar.Wait()

	if n := hits.Load(); n != 0 {
		t.Fatalf("gravatar.com asked %d times without consent", n)
	}

	// Asked from the profile, the picture comes.
	_, body := e.post(nina, "/me/gravatar", nil)
	mustContain(t, body, "Avatar Gravatar récupéré.")

	p, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	if p.AvatarSource != store.AvatarGravatar || p.AvatarFile == "" {
		t.Fatalf("gravatar not fetched: %+v", p)
	}

	// Once: the next pages do not ask again.
	before := hits.Load()
	e.get(nina, f.buvette.URL())
	e.app.gravatar.Wait()

	if hits.Load() != before {
		t.Fatal("gravatar asked again within the TTL")
	}

	// Volunteers see each other's pictures; a visitor does not.
	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	e.app.gravatar.Wait()

	if code, _ := e.get(omar, "/avatars/"+p.AvatarFile); code != 200 {
		t.Fatalf("a volunteer sees another's picture: %d", code)
	}

	if code, _ := e.get(e.client(), "/avatars/"+p.AvatarFile); code != http.StatusNotFound {
		t.Fatalf("a visitor sees a picture: %d", code)
	}

	// An uploaded photo wins, and the Gravatar file goes.
	old := p.AvatarFile

	_, body = e.upload(nina, "/me/photo", nil, "photo", "me.png", pngBytes())
	mustContain(t, body, "Photo de profil enregistrée.", "Retirer ma photo")

	if strings.Contains(body, "Chercher mon avatar Gravatar") {
		t.Error("the Gravatar lookup is offered over an uploaded photo")
	}

	p, _ = e.st.PersonByEmail(ctx, "nina@example.org")
	if p.AvatarSource != store.AvatarUpload || p.AvatarFile == old {
		t.Fatalf("upload: %+v", p)
	}

	if code, _ := e.get(nina, "/avatars/"+old); code != http.StatusNotFound {
		t.Fatalf("replaced gravatar file still served: %d", code)
	}

	// Removing it is an opt-out: no Gravatar comes back.
	_, body = e.post(nina, "/me/photo/delete", nil)
	mustContain(t, body, "vos initiales sont affichées")
	e.app.gravatar.Wait()

	p, _ = e.st.PersonByEmail(ctx, "nina@example.org")
	if p.AvatarFile != "" || p.AvatarSource != store.AvatarOptOut {
		t.Fatalf("opt-out: %+v", p)
	}

	// Until asked for.
	_, body = e.post(nina, "/me/gravatar", nil)
	mustContain(t, body, "Avatar Gravatar récupéré.")

	_, body = e.post(omar, "/me/gravatar", nil)
	mustContain(t, body, "Aucun avatar n&#39;est associé à omar@example.org")

	// The picture shows in the lists and the menu.
	p, _ = e.st.PersonByEmail(ctx, "nina@example.org")
	_, body = e.get(nina, "/me")
	mustContain(t, body, `src="/avatars/`+p.AvatarFile+`"`)

	e.post(nina, f.morning.URL()+"/signup", url.Values{})
	_, body = e.get(omar, f.morning.URL())
	mustContain(t, body, `src="/avatars/`+p.AvatarFile+`"`)
}

// TestStaffAvatarIsKept checks that a staff picture is never replaced by
// the Gravatar fallback.
func TestStaffAvatarIsKept(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	a, err := e.st.UpsertAdmin(ctx, "staff@conf.test", "Sam")
	mustOK(t, err)

	a.AvatarFile = "sam.png"
	mustOK(t, e.st.UpdateAdminProfile(ctx, a))

	p, _ := e.st.GetPerson(ctx, a.PersonID)
	if p.AvatarSource != store.AvatarUpload || p.WantsGravatar() {
		t.Fatalf("staff avatar: %+v", p)
	}

	a.AvatarFile = ""
	mustOK(t, e.st.UpdateAdminProfile(ctx, a))

	if p, _ = e.st.GetPerson(ctx, a.PersonID); p.AvatarSource != store.AvatarOptOut {
		t.Fatalf("removed staff avatar: %+v", p)
	}
}

func TestVolunteerContact(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.ContactEmail, c.VolunteerContactEmail = "partenaires@conf.test", "benevoles@conf.test"
	})
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")

	// Volunteers see their own contact; partners keep theirs.
	_, body := e.get(nina, "/"+f.event.Slug)
	mustContain(t, body, "mailto:benevoles@conf.test")

	if strings.Contains(body, "partenaires@conf.test") {
		t.Error("the partner contact is shown to volunteers")
	}

	_, body = e.get(e.client(), "/")
	mustContain(t, body, "mailto:partenaires@conf.test")

	// Their e-mails answer to it.
	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")
	e.post(omar, f.buvette.URL()+"/join", nil)
	e.post(nina, f.morning.URL()+"/signup", nil)
	e.post(omar, f.morning.URL()+"/waitlist", nil)
	e.post(nina, f.morning.URL()+"/unsignup", nil)

	waitFor(t, func() bool {
		e.mailer.mu.Lock()
		defer e.mailer.mu.Unlock()

		for _, m := range e.mailer.msgs {
			if m.To[0] == "omar@example.org" && m.ReplyTo == "benevoles@conf.test" && strings.Contains(m.Body, "Une question ? benevoles@conf.test") {
				return true
			}
		}

		return false
	})
}
