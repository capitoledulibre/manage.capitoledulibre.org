package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestSignUpPeopleFloodsNobody: one call cannot e-mail someone again and
// again, nor take a crowd.
func TestSignUpPeopleFloodsNobody(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	token, _, err := e.st.CreateAPIToken(ctx, f.lead.ID, nil, "Claude", true, time.Hour)
	mustOK(t, err)

	s := e.mcpClient(token)
	slot := map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "slot": f.overlap.UID}

	same := make([]string, 200)
	for i := range same {
		same[i] = "victim@example.org"
	}

	slot["emails"] = same

	out, ok := call(t, s, "sign_up_people", slot)
	if !ok || !strings.Contains(out, "1 person signed up") {
		t.Fatalf("the same address 200 times: %s", out)
	}

	// Signed up again: nothing more, no e-mail more.
	slot["emails"] = []string{"victim@example.org"}
	call(t, s, "sign_up_people", slot)

	waitFor(t, func() bool { return len(e.mailer.mailTo("victim@example.org")) >= 1 })
	time.Sleep(100 * time.Millisecond)

	if n := len(e.mailer.mailTo("victim@example.org")); n != 1 {
		t.Errorf("e-mails to the victim: %d", n)
	}

	// A crowd at once is refused.
	crowd := make([]string, maxAtOnce+1)
	for i := range crowd {
		crowd[i] = fmt.Sprintf("p%d@example.org", i)
	}

	slot["emails"] = crowd

	if out, ok := call(t, s, "sign_up_people", slot); ok {
		t.Errorf("a crowd signed up: %s", out)
	}

	if out, ok := call(t, s, "add_members", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "emails": crowd}); ok {
		t.Errorf("a crowd added: %s", out)
	}
}

// TestNothingPrivateCached: what a session shows is not cached, public
// pages and files are.
func TestNothingPrivateCached(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()

	for _, c := range []struct {
		client *http.Client
		path   string
		want   string
	}{
		{e.adminClient(), "/admin/editions", "private, no-store"},
		{e.loginAs(f.leadEmail), f.buvette.URL(), "private, no-store"},
		{e.client(), "/", ""},
		{e.loginAs(f.leadEmail), "/icon.svg", "public, max-age=86400"},
	} {
		resp, err := c.client.Get(e.srv.URL + c.path)
		mustOK(t, err)
		resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != c.want {
			t.Errorf("%s: Cache-Control %q, want %q", c.path, got, c.want)
		}
	}
}

// TestSignupsCSVDefused: an address starting like a formula is quoted in
// the sign-ups export, as every other field.
func TestSignupsCSVDefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	p, err := e.st.EnsureVolunteer(ctx, "=1+1@example.org", "Eve", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, p.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, p.ID))

	_, body := e.get(e.loginAs(f.leadEmail), f.buvette.ManageURL()+"/signups.csv")
	mustContain(t, body, "'=1+1@example.org")
}

// TestConsentNamesAnApp: an app's address, without a host, is shown whole.
func TestConsentNamesAnApp(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()

	client, err := e.st.RegisterOAuthClient(context.Background(), "App", []string{"com.example.app:/callback"})
	mustOK(t, err)

	_, page := e.get(e.loginAs(f.leadEmail), "/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {client.ClientID},
		"redirect_uri": {"com.example.app:/callback"}, "code_challenge": {strings.Repeat("c", 43)}, "code_challenge_method": {"S256"}}.Encode())
	mustContain(t, page, "Vous reviendrez ensuite sur com.example.app:/callback.")
}
