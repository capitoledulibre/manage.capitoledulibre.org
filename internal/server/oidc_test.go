package server

import (
	"context"
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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"partners/internal/config"
	"partners/internal/files"
	"partners/internal/store"
)

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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})

	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		claims := f.claims
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
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
	ctx := context.Background()
	dir := t.TempDir()

	st, err := store.Open(ctx, filepath.Join(dir, "db.sqlite"))
	mustOK(t, err)
	t.Cleanup(func() { st.Close() })

	fstore, err := files.New(filepath.Join(dir, "files"))
	mustOK(t, err)

	cfg := &config.Config{
		OIDCIssuer: idp.srv.URL, OIDCClientID: "partners", OIDCClientSecret: "s3cret",
		AdminEmails: []string{"staff@conf.test"}, AdminGroup: "partners-admins",
		BaseURL: "http://127.0.0.1", ReminderInterval: time.Hour,
	}

	app, err := New(ctx, cfg, st, fstore, &captureMailer{})
	mustOK(t, err)

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	e := &env{t: t, srv: srv, st: st, app: app}

	// start begins the flow and returns the client, state and nonce sent to
	// the provider.
	start := func() (*http.Client, string, string) {
		t.Helper()

		c := e.client()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

		resp, err := c.Get(srv.URL + "/admin/login")
		mustOK(t, err)
		resp.Body.Close()

		loc, err := url.Parse(resp.Header.Get("Location"))
		mustOK(t, err)

		if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") || loc.Query().Get("code_challenge_method") != "S256" {
			t.Fatalf("unexpected authorize redirect: %s", loc)
		}

		return c, loc.Query().Get("state"), loc.Query().Get("nonce")
	}

	claims := func(nonce, email string, extra map[string]any) map[string]any {
		c := map[string]any{
			"iss": idp.srv.URL, "aud": "partners", "sub": "u1", "nonce": nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"email": email, "email_verified": true, "name": "Staff",
		}
		for k, v := range extra {
			c[k] = v
		}

		return c
	}

	callback := func(c *http.Client, state string) int {
		t.Helper()

		resp, err := c.Get(srv.URL + "/admin/callback?code=abc&state=" + url.QueryEscape(state))
		mustOK(t, err)
		resp.Body.Close()

		return resp.StatusCode
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
		{name: "allowed group", email: "other@conf.test", extra: map[string]any{"groups": []string{"partners-admins"}}, status: http.StatusSeeOther},
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

			if got := callback(c, state); got != tc.status {
				t.Fatalf("status %d, want %d", got, tc.status)
			}

			if tc.status == http.StatusSeeOther {
				c.CheckRedirect = nil

				_, body := e.get(c, "/admin/profile")
				mustContain(t, body, "Mon profil", tc.email, "Staff")
			}
		})
	}

	t.Run("callback without flow cookie", func(t *testing.T) {
		if got := callback(e.client(), "whatever"); got != http.StatusBadRequest {
			t.Errorf("status %d", got)
		}
	})

	t.Run("dev login disabled when OIDC is configured", func(t *testing.T) {
		cfg.Dev = true
		defer func() { cfg.Dev = false }()

		if code, _ := e.post(e.client(), "/admin/login", url.Values{"email": {"staff@conf.test"}}); code != 404 {
			t.Errorf("dev login with OIDC: %d", code)
		}
	})
}
