package config

import "testing"

func TestDevAdminLogin(t *testing.T) {
	cases := []struct {
		base string
		dev  bool
		oidc bool
		want bool
	}{
		{"http://localhost:8080", true, false, true},
		{"http://127.0.0.1:18089", true, false, true},
		{"http://[::1]:8080", true, false, true},
		{"https://partners.example.org", true, false, false},
		{"http://192.168.1.10:8080", true, false, false},
		{"http://localhost:8080", false, false, false},
		{"http://localhost:8080", true, true, false},
	}

	for _, c := range cases {
		cfg := &Config{BaseURL: c.base, Dev: c.dev}
		if c.oidc {
			cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret = "https://sso", "id", "secret"
		}

		if got := cfg.DevAdminLogin(); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestValidateServe(t *testing.T) {
	if err := (&Config{}).ValidateServe(); err == nil {
		t.Error("serve without OIDC nor dev mode must fail")
	}

	if err := (&Config{Dev: true}).ValidateServe(); err != nil {
		t.Error(err)
	}
}

func TestSiteDefaults(t *testing.T) {
	for _, k := range []string{"MANAGE_SITE_NAME", "MANAGE_SITE_URL", "MANAGE_CONTACT_EMAIL", "MANAGE_MAIL_FROM"} {
		t.Setenv(k, "")
	}

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if c.SiteName != DefaultSiteName || c.SiteURL != "https://conf.example.org" || c.MailFrom != `"Example Conf" <partners@example.org>` {
		t.Errorf("defaults: %q %q %q", c.SiteName, c.SiteURL, c.MailFrom)
	}

	t.Setenv("MANAGE_SITE_NAME", "PyConFR")
	t.Setenv("MANAGE_CONTACT_EMAIL", "sponsors@pycon.fr")
	t.Setenv("MANAGE_SITE_URL", "https://www.pycon.fr/")

	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}

	if c.MailFrom != `"PyConFR" <sponsors@pycon.fr>` || c.SiteURL != "https://www.pycon.fr" {
		t.Errorf("derived: %q %q", c.MailFrom, c.SiteURL)
	}
}

func TestExportTokenLength(t *testing.T) {
	c := &Config{Dev: true, ExportToken: "short"}
	if err := c.ValidateServe(); err == nil {
		t.Error("a short export token must be refused")
	}

	c.ExportToken = "0123456789abcdef0123456789abcdef"
	if err := c.ValidateServe(); err != nil {
		t.Errorf("32-character token refused: %v", err)
	}

	c.ExportToken = ""
	if err := c.ValidateServe(); err != nil {
		t.Errorf("no token (export disabled) refused: %v", err)
	}
}
