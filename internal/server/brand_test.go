package server

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"partners/internal/config"
	"partners/internal/store"
	"partners/web"
)

func TestSiteIdentity(t *testing.T) {
	dir := t.TempDir()
	logo := filepath.Join(dir, "logo.svg")
	mustOK(t, os.WriteFile(logo, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>`), 0o644))

	e := newEnv(t, func(c *config.Config) {
		c.SiteName = "PyConFR 100%"
		c.SiteURL = "https://www.pycon.fr"
		c.ContactEmail = "sponsors@pycon.fr"
		c.SiteLogo = logo
	})

	// Pages show the configured identity, and the logo replaces the
	// initials.
	_, body := e.get(e.client(), "/")
	mustContain(t, body, "<title>Connexion · PyConFR 100%</title>", "Espace partenaires · PyConFR 100%",
		`href="https://www.pycon.fr">www.pycon.fr</a>`, "mailto:sponsors@pycon.fr", `src="/brand/logo?v=`, `rel="icon"`)

	if strings.Contains(body, "Example") {
		t.Error("the default event name leaks into pages")
	}

	resp, err := e.client().Get(e.srv.URL + e.app.site.LogoURL)
	mustOK(t, err)
	resp.Body.Close()

	if resp.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("logo headers: %v", resp.Header)
	}

	// Emails use the name too, and a "%" in it is not a format verb.
	ctx := context.Background()
	org := &store.Organization{Name: "Acme"}
	mustOK(t, e.st.CreateOrganization(ctx, org))

	_, err = e.st.AddManager(ctx, &store.Manager{OrgID: org.ID, Email: "a@acme.test"})
	mustOK(t, err)

	e.post(e.client(), "/login", url.Values{"email": {"a@acme.test"}})

	msg := e.mailer.msgs[len(e.mailer.msgs)-1]
	if msg.Subject != "Votre lien de connexion — PyConFR 100%" || !strings.Contains(msg.Body, "L'équipe PyConFR 100%") ||
		strings.Contains(msg.Body, "%!") {
		t.Errorf("email: %q\n%s", msg.Subject, msg.Body)
	}
}

func TestSiteDefaultsAndInitials(t *testing.T) {
	e := newEnv(t)

	_, body := e.get(e.client(), "/")
	mustContain(t, body, "Example Conf", ">EC<")

	if code, _ := e.get(e.client(), "/brand/logo"); code != 404 {
		t.Errorf("logo without configuration: %d", code)
	}

	for name, want := range map[string]string{"Example Conf": "EC", "PyConFR": "P", "Journées du Logiciel Libre": "JdL", "   ": "?"} {
		if got := siteInitials(name); got != want {
			t.Errorf("siteInitials(%q) = %q, want %q", name, got, want)
		}
	}

	// An invalid logo file is reported at startup.
	bad := filepath.Join(t.TempDir(), "logo.png")
	mustOK(t, os.WriteFile(bad, []byte("not an image"), 0o644))

	if _, err := loadBrandLogo(bad); err == nil {
		t.Error("invalid logo accepted")
	}

	if _, err := loadBrandLogo("/does/not/exist.svg"); err == nil {
		t.Error("missing logo accepted")
	}
}

func TestLanguageFlags(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(nil)

	alice := e.loginAs("alice@acme.test")
	e.post(alice, "/p/profile", url.Values{"name": {"Alice"}, "lang": {"en"}})

	// The profile picker has the manager's language checked, and the header
	// offers the other language with its flag.
	_, body := e.get(alice, "/p/managers")
	mustContain(t, body, `name="lang" value="en" class="sr-only" checked`, `aria-label="English"`, `aria-label="Français"`, `hreflang="fr"`)

	// The staff sees each manager's email language as a flag.
	_, body = e.get(e.adminClient(), "/admin/organizations/"+f.acme.Slug)
	mustContain(t, body, `title="Emails en anglais"`)
}

// TestNoNativeDialogs forbids window.confirm, alert and prompt: confirmations
// use the HTML dialog of the layout.
func TestNoNativeDialogs(t *testing.T) {
	native := regexp.MustCompile(`\b(?:confirm|alert|prompt)\(`)

	err := fs.WalkDir(web.Static, "static/js", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := fs.ReadFile(web.Static, path)
		if err != nil {
			return err
		}

		// Comments may mention the functions being replaced.
		code := regexp.MustCompile(`(?m)//.*$`).ReplaceAll(data, nil)

		if m := native.Find(code); m != nil {
			t.Errorf("%s uses a native browser dialog: %s", path, m)
		}

		return nil
	})
	mustOK(t, err)

	e := newEnv(t)

	_, body := e.get(e.client(), "/")
	mustContain(t, body, `<dialog id="confirm-dialog"`, `data-confirm-message`, `value="ok"`, ">Confirmer<", ">Annuler<")
}

// TestUserMenu: logged-in users get a drop-down with profile, theme and
// logout; other pages keep the theme switch in the header.
func TestUserMenu(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(nil)

	count := func(body, s string) int { return strings.Count(body, s) }

	// Staff: avatar menu with the admin profile and logout.
	admin := e.adminClient()
	_, body := e.get(admin, "/admin/organizations")
	mustContain(t, body, `<details class="relative" data-menu>`, `href="/admin/profile" class="menu-item`, `action="/admin/logout"`, "Mon profil")

	if count(body, `class="theme-switch"`) != 1 {
		t.Error("the theme switch must only be in the menu")
	}

	// Partner manager: initials, profile on the managers page.
	alice := e.loginAs("alice@acme.test")
	_, body = e.get(alice, "/p/")
	mustContain(t, body, "data-menu", `href="/p/managers#profile"`, `action="/logout"`, "alice@acme.test")

	// Public pages and the staff view of a partner: no user menu.
	_, body = e.get(e.client(), "/")
	if strings.Contains(body, "data-menu") || count(body, `class="theme-switch"`) != 1 {
		t.Error("public page: theme switch expected in the header, no user menu")
	}

	e.post(admin, "/admin/organizations/"+f.acme.Slug+"/view-as", nil)
	_, body = e.get(admin, "/p/")
	if strings.Contains(body, "data-menu") {
		t.Error("the staff view of a partner must not show the partner's user menu")
	}
}
