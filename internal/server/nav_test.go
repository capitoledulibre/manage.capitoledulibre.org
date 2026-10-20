package server

import (
	"context"
	"strings"
	"testing"

	"manage/internal/store"
)

// TestSiteNav: one header for everyone, its sections following what the
// person is, and an edition switch that keeps the section.
func TestSiteNav(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	slug := f.event.Slug

	old := &store.Event{Slug: "ec-2025", Name: "EC 2025", Year: 2025}
	mustOK(t, e.st.SaveEvent(ctx, old))

	// A partner contact: their space and its tabs, no volunteering nor
	// administration.
	_, body := e.get(e.loginAs("alice@acme.test"), "/"+slug+"/partner/logo")
	mustContain(t, body, `aria-current="page" href="/`+slug+`/partner/">Partenaires</a>`, `href="/`+slug+`/partner/attendees"`, ">Logo</a>")

	for _, no := range []string{">Bénévoles</a>", ">Planning</a>", `href="/admin/editions"`} {
		if strings.Contains(body, no) {
			t.Errorf("partner contact sees %s", no)
		}
	}

	// A volunteer: the volunteers and the planning only, once the edition
	// opens to them.
	f.event.VolunteerStatus = store.VolunteersOpen
	mustOK(t, e.st.SaveVolunteerSettings(ctx, f.event))

	_, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)

	_, body = e.get(e.loginAs("nina@example.org"), "/"+slug+"/planning")
	mustContain(t, body, `href="/`+slug+`/">Bénévoles</a>`, `aria-current="page" href="/`+slug+`/planning">Planning</a>`)

	for _, no := range []string{">Partenaires</a>", `href="/admin/editions"`} {
		if strings.Contains(body, no) {
			t.Errorf("volunteer sees %s", no)
		}
	}

	// The staff: every section, the administration, and the switch to the
	// other edition in the same section.
	_, body = e.get(e.adminClient(), "/"+slug+"/partners/")
	mustContain(t, body, ">Partenaires</a>", ">Bénévoles</a>", ">Planning</a>", `href="/admin/editions"`,
		`aria-label="Édition : `+f.event.Name+`"`, `href="/ec-2025/partners/"`)

	// The switch of a page within a section leads to the section's root.
	if got := switchURL("/"+slug+"/partners/acme/documents", f.event, old); got != "/ec-2025/partners/" {
		t.Errorf("switchURL = %q", got)
	}

	if got := switchURL("/"+slug+"/poles/buvette", f.event, old); got != "/ec-2025/" {
		t.Errorf("switchURL = %q", got)
	}
}
