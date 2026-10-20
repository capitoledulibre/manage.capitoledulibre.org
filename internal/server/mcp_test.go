package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"manage/internal/store"
)

// bearer adds a token to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(r)
}

// mcpClient connects to the server's MCP endpoint with a token.
func (e *env) mcpClient(token string) *mcp.ClientSession {
	e.t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)

	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: e.srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}

	e.t.Cleanup(func() { _ = session.Close() })

	return session
}

// call calls a tool, and returns its structured result as JSON, or its
// error text.
func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()

	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}

	if res.IsError {
		return res.Content[0].(*mcp.TextContent).Text, false
	}

	out, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	return string(out), true
}

func TestMCP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	ed := map[string]any{"edition": f.event.Slug}

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "0600000000", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))
	f.event.TshirtSizes = "S\nM"
	mustOK(t, e.st.SaveVolunteerSettings(ctx, f.event))
	mustOK(t, e.st.SaveVolunteerDetails(ctx, &store.VolunteerDetails{EventID: f.event.ID, PersonID: nina.ID, Tshirt: "M", Dietary: "sans gluten",
		AskedSizes: []string{"S", "M"}}))

	// Without a token, or with a wrong one, nothing; the site's cookies do
	// not count.
	for _, c := range []*http.Client{http.DefaultClient, e.loginAs(f.leadEmail), {Transport: bearer{"mgt_nope"}}} {
		resp, err := c.Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		mustOK(t, err)
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("without a valid token: %d", resp.StatusCode)
		}
	}

	// A volunteer runs nothing: no token for them.
	if code, _ := e.post(e.loginAs("nina@example.org"), "/me/tokens", nil); code != http.StatusForbidden {
		t.Errorf("a volunteer made a token: %d", code)
	}

	// The lead makes one from their profile: shown once.
	lead := e.loginAs(f.leadEmail)

	_, body := e.get(lead, "/me")
	mustContain(t, body, "Accès pour un assistant IA (MCP)", e.srv.URL+"/mcp")

	_, body = e.post(lead, "/me/tokens", map[string][]string{"name": {"Claude"}})
	mustContain(t, body, "Copiez-le maintenant")

	token := regexp.MustCompile(`value="(mgt_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)
	if token == nil {
		t.Fatal("no token shown")
	}

	_, body = e.get(lead, "/me")
	if strings.Contains(body, token[1]) {
		t.Error("the token is shown again")
	}

	session := e.mcpClient(token[1])

	tools, err := session.ListTools(ctx, nil)
	mustOK(t, err)

	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only", tool.Name)
		}
	}

	out, ok := call(t, session, "list_editions", nil)
	if !ok || !strings.Contains(out, `"manager of `+f.buvette.Slug+`"`) || strings.Contains(out, f.draft.Slug) {
		t.Errorf("list_editions: %s", out)
	}

	// The lead's pole: its people with their numbers.
	out, ok = call(t, session, "planning", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug})
	if !ok || !strings.Contains(out, `"name":"Nina"`) || !strings.Contains(out, "0600000000") {
		t.Errorf("planning: %s", out)
	}

	out, ok = call(t, session, "pole_members", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug})
	if !ok || !strings.Contains(out, "nina@example.org") {
		t.Errorf("pole_members: %s", out)
	}

	// Not a restricted pole, nor what is the coordination's.
	if out, ok := call(t, session, "pole_members", map[string]any{"edition": f.event.Slug, "pole": f.secret.Slug}); ok {
		t.Errorf("pole_members of a restricted pole: %s", out)
	}

	if out, ok := call(t, session, "volunteer", map[string]any{"edition": f.event.Slug, "email": "nina@example.org"}); ok || !strings.Contains(out, "not allowed") {
		t.Errorf("volunteer for a lead: %s", out)
	}

	if out, ok := call(t, session, "logistics", ed); ok || !strings.Contains(out, "not allowed") {
		t.Errorf("logistics for a lead: %s", out)
	}

	if out, ok := call(t, session, "planning", map[string]any{"edition": f.event.Slug, "view": "bogus"}); ok {
		t.Errorf("a bad view: %s", out)
	}

	// The staff: a volunteer's page, without what they eat.
	admin, err := e.st.UpsertAdmin(ctx, "sam@conf.test", "Sam")
	mustOK(t, err)

	staffToken, _, err := e.st.CreateAPIToken(ctx, admin.PersonID, &admin.ID, "Staff", time.Hour)
	mustOK(t, err)

	staff := e.mcpClient(staffToken)

	out, ok = call(t, staff, "volunteer", map[string]any{"edition": f.event.Slug, "email": "nina@example.org"})
	if !ok || !strings.Contains(out, `"has_dietary_needs":true`) || !strings.Contains(out, `"tshirt":"M"`) || strings.Contains(out, "gluten") || !strings.Contains(out, "0600000000") {
		t.Errorf("volunteer: %s", out)
	}

	out, ok = call(t, staff, "logistics", ed)
	if !ok || !strings.Contains(out, `"dietary_needs":1`) || strings.Contains(out, "gluten") {
		t.Errorf("logistics: %s", out)
	}

	out, ok = call(t, staff, "pole_coverage", ed)
	if !ok || !strings.Contains(out, f.secret.Slug) {
		t.Errorf("pole_coverage: %s", out)
	}

	// Revoked, the lead's token stops working.
	tokens, err := e.st.ListAPITokens(ctx, f.lead.ID)
	mustOK(t, err)
	e.post(lead, "/me/tokens/"+tokens[0].UID+"/delete", nil)

	resp, err := (&http.Client{Transport: bearer{token[1]}}).Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	mustOK(t, err)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked token: %d", resp.StatusCode)
	}
}
