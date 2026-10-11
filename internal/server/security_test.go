package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"manage/internal/store"
)

func TestValidEmail(t *testing.T) {
	for email, want := range map[string]bool{
		"alice@acme.test":                  true,
		"alice.martin+conf@acme.fr":        true,
		"a@b.co\r\nbcc:c@d.co":             false,
		"a@b.co\nx":                        false,
		"a@b.co\x00":                       false,
		"Alice <alice@acme.test>":          false,
		"alice@acme":                       false,
		"@acme.test":                       false,
		"alice acme@acme.test":             false,
		strings.Repeat("a", 250) + "@b.fr": false,
	} {
		if got := validEmail(email); got != want {
			t.Errorf("validEmail(%q) = %v, want %v", email, got, want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, https := range []bool{false, true} {
		rec := httptest.NewRecorder()
		securityHeaders(ok, https).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		h := rec.Header()
		for _, want := range []string{"form-action 'self'", "object-src 'none'", "frame-ancestors 'none'", "script-src 'self'"} {
			if !strings.Contains(h.Get("Content-Security-Policy"), want) {
				t.Errorf("CSP lacks %q", want)
			}
		}

		if !strings.Contains(h.Get("Permissions-Policy"), "camera=()") || h.Get("Cross-Origin-Opener-Policy") != "same-origin" {
			t.Errorf("missing Permissions-Policy or COOP: %v", h)
		}

		if got := h.Get("Strict-Transport-Security") != ""; got != https {
			t.Errorf("HSTS sent=%v over https=%v", got, https)
		}
	}
}

// TestLanguageLinkDoesNotChangeEmailLanguage: a GET (which a third-party
// site can trigger) only changes the display language.
func TestLanguageLinkDoesNotChangeEmailLanguage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	_, body := e.get(alice, "/lang/en?next=/partners/")
	mustContain(t, body, "Dashboard")

	m, _ := e.st.GetManager(ctx, f.alice.ID)
	if m.Lang != "fr" {
		t.Errorf("GET /lang changed the email language to %q", m.Lang)
	}

	// The profile form (POST) does change it.
	e.post(alice, "/partners/profile", url.Values{"name": {"Alice"}, "lang": {"en"}})

	if m, _ = e.st.GetManager(ctx, f.alice.ID); m.Lang != "en" {
		t.Errorf("profile did not change the email language: %q", m.Lang)
	}
}

func TestPublicLinksRel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f := e.fixture(nil)
	f.acme.Website = "https://acme.test"
	mustOK(t, e.st.UpdateOrganization(ctx, f.acme))
	mustOK(t, e.st.SaveJobOffer(ctx, &store.JobOffer{OrgID: f.acme.ID, Title: "Dev", URL: "https://acme.test/jobs"}))

	_, body := e.get(e.client(), "/jobs")
	if n := strings.Count(body, `rel="noopener noreferrer nofollow ugc"`); n != 2 {
		t.Errorf("public partner links with hardened rel: %d, want 2", n)
	}
}

func TestLogoHistoryIsBounded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	for i := 0; i < maxLogoVersions+3; i++ {
		e.upload(alice, "/partners/logo", map[string]string{"variant": "default"}, "logo", "logo.png", pngBytes())
	}

	// A dark variant is pruned separately and never touched here.
	e.upload(alice, "/partners/logo", map[string]string{"variant": "dark"}, "logo", "dark.png", pngBytes())

	org, _ := e.st.GetOrganizationBySlug(ctx, "acme")
	logos, _ := e.st.ListLogos(ctx, org.ID)

	count := map[string]int{}
	for _, l := range logos {
		count[l.Variant]++
	}

	if count["default"] != maxLogoVersions || count["dark"] != 1 {
		t.Errorf("logo versions kept: %v", count)
	}

	if logos[0].File != org.LogoDarkFile && logos[0].File != org.LogoFile {
		t.Error("the current logo must be kept")
	}

	// Pruned files are gone from disk; kept ones are still there.
	entries, _ := os.ReadDir(e.app.files.Dir())
	if len(entries) != maxLogoVersions+1 {
		t.Errorf("files on disk: %d, want %d", len(entries), maxLogoVersions+1)
	}
}
