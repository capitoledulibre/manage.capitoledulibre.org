package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/config"
)

// noRedirect stops at a redirect, to read where it goes.
func noRedirect(c *http.Client) *http.Client {
	out := *c
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &out
}

// jsonOf reads a JSON answer: jsonOf(t)(http.Get(…)).
func jsonOf(t *testing.T) func(*http.Response, error) (int, map[string]any) {
	return func(resp *http.Response, err error) (int, map[string]any) {
		t.Helper()

		return getJSON(t, resp, err)
	}
}

func getJSON(t *testing.T, resp *http.Response, err error) (int, map[string]any) {
	t.Helper()
	mustOK(t, err)

	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("not JSON (%d): %v", resp.StatusCode, err)
	}

	return resp.StatusCode, out
}

// TestOAuthFlow goes through the authorization as an assistant does: it
// finds the server, registers, sends its user to agree, trades the code
// for tokens, reads, and renews them.
func TestOAuthFlow(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()
	base := e.srv.URL
	callback := "https://claude.ai/api/mcp/auth_callback"

	// Without a token, /mcp tells where to look.
	resp, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{}`))
	mustOK(t, err)
	resp.Body.Close()

	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `resource_metadata="`) {
		t.Errorf("WWW-Authenticate: %q", resp.Header.Get("WWW-Authenticate"))
	}

	_, meta := jsonOf(t)(http.Get(base + "/.well-known/oauth-protected-resource"))
	if meta["resource"] == nil || len(meta["authorization_servers"].([]any)) != 1 {
		t.Fatalf("resource metadata: %v", meta)
	}

	_, as := jsonOf(t)(http.Get(base + "/.well-known/oauth-authorization-server"))
	if as["registration_endpoint"] == nil || as["code_challenge_methods_supported"].([]any)[0] != "S256" {
		t.Fatalf("server metadata: %v", as)
	}

	// Registration: an unsafe address is refused.
	for _, unsafe := range []string{"http://evil.example/cb", "https://a.example;sandbox/cb", "https://*/cb", "https://a.example%20x/cb",
		"https://user@a.example/cb", "javascript://a.example/x"} {
		code, body := jsonOf(t)(http.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+unsafe+`"]}`)))
		if code != http.StatusBadRequest || body["error"] != "invalid_redirect_uri" {
			t.Errorf("unsafe redirect %s: %d %v", unsafe, code, body)
		}
	}

	code, client := jsonOf(t)(http.Post(base+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name":"Claude","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"client_secret_basic"}`)))
	if code != http.StatusCreated || client["token_endpoint_auth_method"] != "none" {
		t.Fatalf("register: %d %v", code, client)
	}

	clientID := client["client_id"].(string)

	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	authorize := base + "/oauth/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "state": {"xyz"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "scope": {"read write"},
	}.Encode()

	// Someone not signed in is asked to; a volunteer who runs nothing is
	// told there is nothing to read.
	_, page := e.get(e.client(), strings.TrimPrefix(authorize, base))
	mustContain(t, page, "Connectez-vous d&#39;abord", "/volunteers/login?next=", "/admin/login?next=")

	_, page = e.get(e.loginAs("nina@example.org"), strings.TrimPrefix(authorize, base))
	mustContain(t, page, "un assistant n&#39;aurait rien à lire")

	// An address the client did not register is not sent anything.
	bad := strings.Replace(authorize, url.QueryEscape(callback), url.QueryEscape("https://evil.example/cb"), 1)
	if code, page := e.get(e.loginAs(f.leadEmail), strings.TrimPrefix(bad, base)); code != http.StatusBadRequest || !strings.Contains(page, "pas valide") {
		t.Errorf("unregistered redirect: %d", code)
	}

	// The lead agrees, with changes.
	lead := e.loginAs(f.leadEmail)

	_, page = e.get(lead, strings.TrimPrefix(authorize, base))
	mustContain(t, page, "« Claude » demande à lire", "Lou Lead", `name="writes" value="yes"`, "claude.ai")

	// Agreeing sends the browser to the assistant: the page's policy lets
	// its form lead there, and nowhere else.
	resp, err = lead.Get(authorize)
	mustOK(t, err)
	resp.Body.Close()

	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https://claude.ai;") {
		t.Errorf("consent policy: %s", csp)
	}

	form := url.Values{}
	for _, k := range []string{"response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method", "scope"} {
		u, _ := url.Parse(authorize)
		form.Set(k, u.Query().Get(k))
	}

	deny := url.Values{}
	for k, v := range form {
		deny[k] = v
	}

	deny.Set("decision", "deny")

	resp, err = noRedirect(lead).PostForm(base+"/oauth/authorize", deny)
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, callback) || !strings.Contains(loc, "error=access_denied") || !strings.Contains(loc, "state=xyz") {
		t.Errorf("denied: %s", loc)
	}

	form.Set("decision", "allow")
	form.Set("writes", "yes")

	resp, err = noRedirect(lead).PostForm(base+"/oauth/authorize", form)
	mustOK(t, err)
	resp.Body.Close()

	back, err := url.Parse(resp.Header.Get("Location"))
	mustOK(t, err)

	if back.Query().Get("state") != "xyz" || back.Query().Get("iss") != e.app.cfg.BaseURL || back.Query().Get("code") == "" {
		t.Fatalf("sent back: %s", back)
	}

	exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {callback},
		"code": {back.Query().Get("code")}, "code_verifier": {"wrong" + verifier}}

	// A wrong verifier spends the code for nothing.
	if code, body := jsonOf(t)(http.PostForm(base+"/oauth/token", exchange)); code != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("wrong verifier: %d %v", code, body)
	}

	// Again, from the start, with the right one.
	resp, err = noRedirect(lead).PostForm(base+"/oauth/authorize", form)
	mustOK(t, err)
	resp.Body.Close()

	back, _ = url.Parse(resp.Header.Get("Location"))
	exchange.Set("code", back.Query().Get("code"))
	exchange.Set("code_verifier", verifier)

	code, tokens := jsonOf(t)(http.PostForm(base+"/oauth/token", exchange))
	if code != http.StatusOK || tokens["scope"] != "read write" || tokens["token_type"] != "Bearer" {
		t.Fatalf("token: %d %v", code, tokens)
	}

	// A code is used once: used again, as by someone who stole it, it
	// revokes what it gave.
	if code, _ := jsonOf(t)(http.PostForm(base+"/oauth/token", exchange)); code != http.StatusBadRequest {
		t.Errorf("a code used twice: %d", code)
	}

	resp, err = (&http.Client{Transport: bearer{tokens["access_token"].(string)}}).Post(base+"/mcp", "application/json", strings.NewReader(`{}`))
	mustOK(t, err)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the grant of a reused code: %d", resp.StatusCode)
	}

	// Once more, for good.
	resp, err = noRedirect(lead).PostForm(base+"/oauth/authorize", form)
	mustOK(t, err)
	resp.Body.Close()

	back, _ = url.Parse(resp.Header.Get("Location"))
	exchange.Set("code", back.Query().Get("code"))

	if code, tokens = jsonOf(t)(http.PostForm(base+"/oauth/token", exchange)); code != http.StatusOK {
		t.Fatalf("token: %d %v", code, tokens)
	}

	// The token reads, and changes, as the lead.
	s := e.mcpClient(tokens["access_token"].(string))

	if out, ok := call(t, s, "list_editions", nil); !ok || !strings.Contains(out, "manager of") {
		t.Errorf("list_editions: %s", out)
	}

	if out, ok := call(t, s, "post_notice", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "message": "Hello"}); !ok {
		t.Errorf("post_notice: %s", out)
	}

	// The grant shows on the lead's profile, renewed until its end.
	_, page = e.get(lead, "/me")
	mustContain(t, page, "<strong>Claude</strong>", "renouvelé jusqu&#39;au")

	// Renewing gives new tokens; the former ones stop working.
	code, renewed := jsonOf(t)(http.PostForm(base+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID},
		"refresh_token": {tokens["refresh_token"].(string)}}))
	if code != http.StatusOK || renewed["access_token"] == tokens["access_token"] {
		t.Fatalf("refresh: %d %v", code, renewed)
	}

	resp, err = (&http.Client{Transport: bearer{tokens["access_token"].(string)}}).Post(base+"/mcp", "application/json", strings.NewReader(`{}`))
	mustOK(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the former access token: %d", resp.StatusCode)
	}

	if out, ok := call(t, e.mcpClient(renewed["access_token"].(string)), "list_editions", nil); !ok {
		t.Errorf("the renewed token: %s", out)
	}
}

// TestStaffLoginComesBack: the staff login returns to the page that asked
// for it, a local one only.
func TestStaffLoginComesBack(t *testing.T) {
	e := newEnv(t)

	for next, want := range map[string]string{"/oauth/authorize?client_id=x": "/oauth/authorize?client_id=x", "https://evil.example/": "/admin/"} {
		c := noRedirect(e.client())

		resp, err := c.Get(e.srv.URL + "/admin/login?next=" + url.QueryEscape(next))
		mustOK(t, err)
		resp.Body.Close()

		resp, err = c.PostForm(e.srv.URL+"/admin/login", url.Values{"email": {"staff@conf.test"}})
		mustOK(t, err)
		resp.Body.Close()

		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("next %s: back to %q, want %q", next, got, want)
		}
	}
}

// TestStaffTokenFollowsTheList: a staff token stops working as soon as its
// owner is taken off the staff addresses.
func TestStaffTokenFollowsTheList(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AdminEmails = []string{"sam@conf.test"} })

	admin, err := e.st.UpsertAdmin(context.Background(), "sam@conf.test", "Sam")
	mustOK(t, err)

	token, _, err := e.st.CreateAPIToken(context.Background(), admin.PersonID, &admin.ID, "Claude", false, time.Hour)
	mustOK(t, err)

	if out, ok := call(t, e.mcpClient(token), "list_editions", nil); !ok {
		t.Fatalf("staff token: %s", out)
	}

	e.app.cfg.AdminEmails = []string{"other@conf.test"}

	resp, err := (&http.Client{Transport: bearer{token}}).Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	mustOK(t, err)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a former staff member's token: %d", resp.StatusCode)
	}
}
