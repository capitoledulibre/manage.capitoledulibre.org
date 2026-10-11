package config

import (
	"net/netip"
	"testing"
)

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

	// Outside dev mode, e-mails (login links) must not end up in the log,
	// nor travel in clear, and the site is served over HTTPS.
	oidc := Config{OIDCIssuer: "https://id.example.org", OIDCClientID: "manage", OIDCClientSecret: "secret",
		BaseURL: "https://manage.example.org", SMTPTLS: "starttls"}
	if err := oidc.ValidateServe(); err == nil {
		t.Error("serve without SMTP outside dev mode must fail")
	}

	oidc.SMTPHost, oidc.MailFrom = "smtp.example.org", "manage@example.org"
	if err := oidc.ValidateServe(); err != nil {
		t.Error(err)
	}

	plain := oidc
	plain.BaseURL = "http://manage.example.org"

	if err := plain.ValidateServe(); err == nil {
		t.Error("serve over plain HTTP outside dev mode must fail")
	}

	clear := oidc
	clear.SMTPTLS = "none"

	if err := clear.ValidateServe(); err == nil {
		t.Error("SMTP in clear to a remote server must fail")
	}

	clear.SMTPHost = "127.0.0.1"
	if err := clear.ValidateServe(); err != nil {
		t.Errorf("SMTP in clear to a local relay: %v", err)
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

	// Nothing about a given event is built in.
	if c.SiteName != DefaultSiteName || c.SiteURL != "" || c.ContactEmail != "" || c.MailFrom != "" {
		t.Errorf("defaults: %q %q %q %q", c.SiteName, c.SiteURL, c.ContactEmail, c.MailFrom)
	}

	// Sending e-mails takes a sender.
	c.Dev, c.SMTPHost = true, "smtp.example.org"
	if err := c.ValidateServe(); err == nil {
		t.Error("SMTP without a sender is accepted")
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

func TestParseProxies(t *testing.T) {
	ps, err := ParseProxies("private, 203.0.113.7, 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}

	contains := func(addr string) bool {
		a := netip.MustParseAddr(addr)
		for _, p := range ps {
			if p.Contains(a) {
				return true
			}
		}

		return false
	}

	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "172.17.0.1": true, "192.168.1.10": true,
		"203.0.113.7": true, "2001:db8::1": true,
		"203.0.113.8": false, "8.8.8.8": false,
	} {
		if got := contains(addr); got != want {
			t.Errorf("%s trusted = %v, want %v", addr, got, want)
		}
	}

	if ps, _ := ParseProxies("none"); len(ps) != 0 {
		t.Errorf("none trusts nobody, got %v", ps)
	}

	if _, err := ParseProxies("not-an-ip"); err == nil {
		t.Error("an invalid entry is refused")
	}
}
