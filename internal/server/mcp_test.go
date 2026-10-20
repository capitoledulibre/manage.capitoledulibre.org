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

	staffToken, _, err := e.st.CreateAPIToken(ctx, admin.PersonID, &admin.ID, "Staff", true, time.Hour)
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

func TestMCPWrites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))

	// A read-only token sees no tool that changes things.
	readToken, _, err := e.st.CreateAPIToken(ctx, f.lead.ID, nil, "Read", false, time.Hour)
	mustOK(t, err)

	tools, err := e.mcpClient(readToken).ListTools(ctx, nil)
	mustOK(t, err)

	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("a read-only token offers %s", tool.Name)
		}
	}

	// The lead's token, allowed to change things, made from their profile.
	lead := e.loginAs(f.leadEmail)
	_, body := e.post(lead, "/me/tokens", map[string][]string{"name": {"Claude"}, "writes": {"yes"}})
	mustContain(t, body, "lecture et modifications")

	token := regexp.MustCompile(`value="(mgt_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)[1]
	s := e.mcpClient(token)

	tools, err = s.ListTools(ctx, nil)
	mustOK(t, err)

	destructive := map[string]bool{}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			destructive[tool.Name] = tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		}
	}

	if len(destructive) != 9 || !destructive["delete_slot"] || destructive["sign_up_people"] {
		t.Errorf("tools that change things: %v", destructive)
	}

	slot := map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "slot": f.morning.UID}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range slot {
			out[k] = v
		}

		for k, v := range extra {
			out[k] = v
		}

		return out
	}

	// Two on a slot of one: done, told, and the overbooking said.
	out, ok := call(t, s, "sign_up_people", with(map[string]any{"emails": []string{"nina@example.org", "newbie@example.org"}}))
	if !ok || !strings.Contains(out, "2 people signed up") || !strings.Contains(out, "overbooked") {
		t.Fatalf("sign_up_people: %s", out)
	}

	e.mailed("nina@example.org")
	e.mailed("newbie@example.org")

	out, ok = call(t, s, "take_off_slot", with(map[string]any{"email": "newbie@example.org"}))
	if !ok {
		t.Errorf("take_off_slot: %s", out)
	}

	// Slots: created, moved, deleted; a place must exist.
	day := f.morning.StartsAt.Add(24 * time.Hour).In(paris).Format(time.DateOnly)
	create := map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "starts_at": day + "T14:00", "ends_at": day + "T16:00", "capacity": 2}

	if out, ok := call(t, s, "create_slot", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "starts_at": day + "T14:00",
		"ends_at": day + "T16:00", "place": "Nowhere"}); ok || !strings.Contains(out, "no place named") {
		t.Errorf("create_slot at an unknown place: %s", out)
	}

	out, ok = call(t, s, "create_slot", create)
	if !ok || !strings.Contains(out, `"capacity":2`) {
		t.Fatalf("create_slot: %s", out)
	}

	id := regexp.MustCompile(`"id":"([^"]+)"`).FindStringSubmatch(out)[1]
	made := map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "slot": id}

	out, ok = call(t, s, "update_slot", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "slot": id, "capacity": 5})
	if !ok || !strings.Contains(out, `"capacity":5`) || !strings.Contains(out, day+"T14:00") {
		t.Errorf("update_slot: %s", out)
	}

	if out, ok := call(t, s, "delete_slot", made); !ok {
		t.Errorf("delete_slot: %s", out)
	}

	// A notice to the pole; not to the edition, which is the coordination's.
	if out, ok := call(t, s, "post_notice", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "message": "Merci !", "send_email": true}); !ok ||
		!strings.Contains(out, "e-mailed") {
		t.Errorf("post_notice to the pole: %s", out)
	}

	if out, ok := call(t, s, "post_notice", map[string]any{"edition": f.event.Slug, "message": "Hello"}); ok || !strings.Contains(out, "not allowed") {
		t.Errorf("post_notice to the edition by a lead: %s", out)
	}

	if out, ok := call(t, s, "post_notice", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "message": " "}); ok ||
		!strings.Contains(out, "Write the message") {
		t.Errorf("an empty notice: %s", out)
	}

	// Not another pole, nor the coordination's reminders.
	if out, ok := call(t, s, "add_members", map[string]any{"edition": f.event.Slug, "pole": f.accueil.Slug, "emails": []string{"x@example.org"}}); ok {
		t.Errorf("add_members to another pole: %s", out)
	}

	if out, ok := call(t, s, "remind", map[string]any{"edition": f.event.Slug, "audience": "idle"}); ok || !strings.Contains(out, "not allowed") {
		t.Errorf("remind by a lead: %s", out)
	}

	// Members: added and taken out.
	if out, ok := call(t, s, "add_members", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "emails": []string{"omar@example.org"}}); !ok {
		t.Errorf("add_members: %s", out)
	}

	if out, ok := call(t, s, "remove_member", map[string]any{"edition": f.event.Slug, "pole": f.buvette.Slug, "email": "omar@example.org"}); !ok {
		t.Errorf("remove_member: %s", out)
	}

	// The history names the lead.
	history, _, err := e.st.ListPoleLog(ctx, f.buvette.ID, store.LogPage{})
	mustOK(t, err)

	if len(history) == 0 || history[0].ActorID != f.lead.ID {
		t.Errorf("history: %+v", history)
	}
}
