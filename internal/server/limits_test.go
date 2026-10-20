package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"manage/internal/config"
	"manage/internal/store"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")}

	cases := []struct {
		name, remote, xff, want string
		proxies                 []netip.Prefix
	}{
		{"direct, no header", "203.0.113.9:4000", "", "203.0.113.9", trusted},
		{"forged by a direct client", "203.0.113.9:4000", "1.2.3.4", "203.0.113.9", trusted},
		{"through the proxy", "127.0.0.1:4000", "198.51.100.5", "198.51.100.5", trusted},
		{"client prepends a fake", "127.0.0.1:4000", "1.2.3.4, 198.51.100.5", "198.51.100.5", trusted},
		{"two proxies", "127.0.0.1:4000", "198.51.100.5, 10.0.0.2", "198.51.100.5", trusted},
		{"IPv6 peer", "[::1]:4000", "198.51.100.5", "::1", trusted},
		{"no trusted proxy", "127.0.0.1:4000", "198.51.100.5", "127.0.0.1", nil},
	}

	for _, c := range cases {
		s := &Server{cfg: &config.Config{TrustedProxies: c.proxies}}

		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote

		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}

		if got := s.clientIP(r); got != c.want {
			t.Errorf("%s: clientIP = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestLoginRateLimitByIP: one client cannot make the server send e-mails to
// many addresses.
func TestLoginRateLimitByIP(t *testing.T) {
	e := newEnv(t)
	c := e.client()

	for i := range loginsPerIP {
		if code, _ := e.post(c, "/login", url.Values{"email": {"x" + string(rune('a'+i%26)) + "@example.org"}}); code != 200 {
			t.Fatalf("request %d: %d", i, code)
		}
	}

	code, body := e.post(c, "/login", url.Values{"email": {"last@example.org"}})
	if code != http.StatusTooManyRequests {
		t.Fatalf("request over the limit: %d", code)
	}

	mustContain(t, body, "Trop de demandes de connexion")
}

// TestPersonSessionSlides: a person who keeps coming back keeps their
// session.
func TestPersonSessionSlides(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")

	base, _ := url.Parse(e.srv.URL)

	var token string

	for _, ck := range nina.Jar.Cookies(base) {
		if ck.Name == personCookie {
			token = ck.Value
		}
	}

	// Age the session to ten days from its end.
	mustOK(t, e.st.ExtendSession(ctx, token, 10*24*time.Hour))

	if code, _ := e.get(nina, "/"+f.event.Slug); code != 200 {
		t.Fatalf("page: %d", code)
	}

	sess, err := e.st.GetSession(ctx, token, store.SessionPerson)
	mustOK(t, err)

	if time.Until(sess.ExpiresAt) < personSessionTTL-time.Hour {
		t.Fatalf("session not renewed: ends in %s", time.Until(sess.ExpiresAt))
	}
}
