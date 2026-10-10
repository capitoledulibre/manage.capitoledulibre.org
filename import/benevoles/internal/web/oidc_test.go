package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"benevoles/internal/config"
)

// TestStaffLogin covers the development staff login and what an
// administrator session is: rights that come with that session only.
func TestStaffLogin(t *testing.T) {
	e := newEnvWith(t, func(c *config.Config) { c.BaseURL = "http://localhost:8080" })

	want(t, e.client().get("/login", 200), `href="/admin/login"`)
	want(t, e.client().get("/admin/login", 200), "Se connecter comme staff")

	// A volunteer opening the administration is sent to the staff login.
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.get("/admin", 303)

	staff := e.client()
	staff.post("/admin/login/dev", 303, url.Values{"email": {"vol@example.test"}, "next": {"/admin/users"}})
	want(t, staff.get("/admin/users", 200), "Comptes")
	base, _ := url.Parse(e.ts.URL)
	for _, ck := range staff.http.Jar.Cookies(base) {
		if ck.Name != sessionCookie {
			continue
		}
		sess, err := e.db.GetSession(ck.Value)
		if err != nil || !sess.Admin {
			t.Fatalf("expected an administrator session: %+v %v", sess, err)
		}
		if left := time.Until(sess.ExpiresAt); left > adminSessionTTL || left < adminSessionTTL-time.Minute {
			t.Fatalf("administrator session lasts %v", left)
		}
	}

	// The same address logged in by e-mail link stays a volunteer.
	vol.get("/admin", 303)
	e.login("vol@example.test", "").get("/admin", 303)

	// ADMIN_EMAILS restricts the development login too.
	e.srv.Cfg.AdminEmails = []string{"boss@example.test"}
	e.client().post("/admin/login/dev", 403, url.Values{"email": {"vol@example.test"}})
	e.client().post("/admin/login/dev", 303, url.Values{"email": {"boss@example.test"}})
}

// TestStaffLoginNeedsLoopback: DEV_MODE forgotten on a public server does not
// open the administration.
func TestStaffLoginNeedsLoopback(t *testing.T) {
	e := newEnv(t) // DevMode on, BaseURL http://example.test
	anon := e.client()
	anon.get("/admin/login", 404)
	anon.post("/admin/login/dev", 404, url.Values{"email": {"x@example.test"}})
	wantNot(t, anon.get("/login", 200), `href="/admin/login"`)
}

// fakeIdP is a minimal OpenID Connect provider issuing RS256 ID tokens with
// the claims configured by the test.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	claims map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		claims := f.claims
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(t, claims),
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := enc(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeIdP) setClaims(c map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = c
}

func TestOIDCLogin(t *testing.T) {
	idp := newFakeIdP(t)
	e := newEnvWith(t, func(c *config.Config) {
		c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret = idp.srv.URL, "benevoles", "s3cret"
		c.AdminEmails, c.AdminGroup = []string{"staff@conf.test"}, "benevoles-admins"
	})

	// start begins the flow and returns the client, state and nonce sent to
	// the provider.
	start := func() (*client, string, string) {
		t.Helper()
		c := e.client()
		req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/admin/login?next=/admin/users", nil)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") || loc.Query().Get("code_challenge_method") != "S256" {
			t.Fatalf("unexpected authorize redirect: %s", loc)
		}
		return c, loc.Query().Get("state"), loc.Query().Get("nonce")
	}
	claims := func(nonce, email string, extra map[string]any) map[string]any {
		c := map[string]any{
			"iss": idp.srv.URL, "aud": "benevoles", "sub": "u1", "nonce": nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"email": email, "email_verified": true, "name": "Sam Staff",
		}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	callback := func(c *client, state string) *http.Response {
		t.Helper()
		resp, err := c.http.Get(e.ts.URL + "/admin/callback?code=abc&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	cases := []struct {
		name   string
		email  string
		extra  map[string]any
		nonce  func(string) string
		state  func(string) string
		status int
	}{
		{name: "allowed email", email: "staff@conf.test", status: http.StatusSeeOther},
		{name: "allowed group", email: "other@conf.test", extra: map[string]any{"groups": []string{"benevoles-admins"}}, status: http.StatusSeeOther},
		{name: "not allowed", email: "random@example.org", status: http.StatusForbidden},
		{name: "unverified email", email: "staff@conf.test", extra: map[string]any{"email_verified": false}, status: http.StatusForbidden},
		{name: "wrong nonce", email: "staff@conf.test", nonce: func(string) string { return "forged" }, status: http.StatusBadRequest},
		{name: "wrong state", email: "staff@conf.test", state: func(string) string { return "forged" }, status: http.StatusBadRequest},
		{name: "other audience", email: "staff@conf.test", extra: map[string]any{"aud": "another-app"}, status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, state, nonce := start()
			if tc.nonce != nil {
				nonce = tc.nonce(nonce)
			}
			if tc.state != nil {
				state = tc.state(state)
			}
			idp.setClaims(claims(nonce, tc.email, tc.extra))
			resp := callback(c, state)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.status == http.StatusSeeOther {
				if loc := resp.Header.Get("Location"); loc != "/admin/users" {
					t.Fatalf("redirected to %q, want the page asked for", loc)
				}
				want(t, c.get("/admin/users", 200), tc.email)
				// The name comes from the identity provider.
				want(t, c.get("/profile", 200), `value="Sam Staff"`)
			}
		})
	}

	t.Run("callback without flow cookie", func(t *testing.T) {
		if got := callback(e.client(), "whatever").StatusCode; got != http.StatusBadRequest {
			t.Errorf("status %d", got)
		}
	})
	t.Run("no development login next to OIDC", func(t *testing.T) {
		e.client().post("/admin/login/dev", 404, url.Values{"email": {"staff@conf.test"}})
	})
}
