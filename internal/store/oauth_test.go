package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOAuth(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	p, err := s.EnsureVolunteer(ctx, "lou@example.org", "Lou", "", "fr")
	if err != nil {
		t.Fatal(err)
	}

	client, err := s.RegisterOAuthClient(ctx, "Claude", []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:3000/cb"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetOAuthClient(ctx, client.ClientID)
	if err != nil || got.Name != "Claude" || len(got.RedirectURIs) != 2 {
		t.Fatalf("client: %v %+v", err, got)
	}

	// A code is used once.
	grant := &OAuthGrant{ClientID: client.ID, PersonID: p.ID, RedirectURI: client.RedirectURIs[0], Challenge: "abc", Writes: true}

	code, err := s.CreateOAuthCode(ctx, grant, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	g, err := s.ConsumeOAuthCode(ctx, code)
	if err != nil || g.PersonID != p.ID || !g.Writes || g.Challenge != "abc" {
		t.Fatalf("code: %v %+v", err, g)
	}

	if _, err := s.ConsumeOAuthCode(ctx, code); !errors.Is(err, ErrCodeReused) {
		t.Errorf("a code used twice: %v", err)
	}

	old, err := s.CreateOAuthCode(ctx, grant, -time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ConsumeOAuthCode(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired code: %v", err)
	}

	// Tokens: the access token works, and shows as a grant to the client.
	access, refresh, err := s.IssueOAuthTokens(ctx, client, g, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	tok, err := s.APITokenFor(ctx, access)
	if err != nil || !tok.Writes || tok.Client != "Claude" || tok.RenewsUntil == nil || !tok.Until().Equal(*tok.RenewsUntil) {
		t.Fatalf("access token: %v %+v", err, tok)
	}

	// Another client cannot use the refresh token.
	other, err := s.RegisterOAuthClient(ctx, "Other", []string{"https://other.example/cb"})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := s.RefreshOAuthTokens(ctx, other, refresh, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("a refresh by another client: %v", err)
	}

	// Refreshing renews both; the former ones stop working.
	access2, refresh2, writes, err := s.RefreshOAuthTokens(ctx, client, refresh, time.Hour)
	if err != nil || !writes {
		t.Fatalf("refresh: %v", err)
	}

	if _, err := s.APITokenFor(ctx, access); !errors.Is(err, ErrNotFound) {
		t.Errorf("the former access token works: %v", err)
	}

	if _, _, _, err := s.RefreshOAuthTokens(ctx, client, refresh, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("the former refresh token works: %v", err)
	}

	if _, err := s.APITokenFor(ctx, access2); err != nil {
		t.Errorf("the new access token: %v", err)
	}

	// Refreshing renews the tokens, never the grant's end.
	renewed, err := s.APITokenFor(ctx, access2)
	if err != nil || !renewed.RenewsUntil.Equal(*tok.RenewsUntil) {
		t.Errorf("the grant's end moved: %v → %v", tok.RenewsUntil, renewed.RenewsUntil)
	}

	// Nor beyond it: an access token never outlives its grant.
	short, err := s.CreateOAuthCode(ctx, grant, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	sg, err := s.ConsumeOAuthCode(ctx, short)
	if err != nil {
		t.Fatal(err)
	}

	shortAccess, _, err := s.IssueOAuthTokens(ctx, client, sg, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if st, err := s.APITokenFor(ctx, shortAccess); err != nil || st.ExpiresAt.After(*st.RenewsUntil) {
		t.Errorf("an access token outlives its grant: %v %+v", err, st)
	}

	// A code used again revokes the grant it was traded for.
	if _, err := s.ConsumeOAuthCode(ctx, short); !errors.Is(err, ErrCodeReused) {
		t.Errorf("reuse: %v", err)
	}

	if _, err := s.APITokenFor(ctx, shortAccess); !errors.Is(err, ErrNotFound) {
		t.Errorf("the grant of a reused code works: %v", err)
	}

	// Revoking the grant ends both.
	list, err := s.ListAPITokens(ctx, p.ID)
	if err != nil || len(list) != 1 || list[0].Client != "Claude" {
		t.Fatalf("grants: %v %d", err, len(list))
	}

	if err := s.DeleteAPIToken(ctx, p.ID, list[0].UID); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := s.RefreshOAuthTokens(ctx, client, refresh2, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked grant renews: %v", err)
	}
}

// TestOAuthClientPurge: a client granted nothing for a month goes; one
// with a grant stays.
func TestOAuthClientPurge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	p, err := s.EnsureVolunteer(ctx, "lou@example.org", "Lou", "", "fr")
	if err != nil {
		t.Fatal(err)
	}

	idle, err := s.RegisterOAuthClient(ctx, "Idle", []string{"https://idle.example/cb"})
	if err != nil {
		t.Fatal(err)
	}

	used, err := s.RegisterOAuthClient(ctx, "Used", []string{"https://used.example/cb"})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.IssueOAuthTokens(ctx, used, &OAuthGrant{ClientID: used.ID, PersonID: p.ID}, time.Hour, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	recent, err := s.RegisterOAuthClient(ctx, "Recent", []string{"https://recent.example/cb"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE oauth_clients SET created_at = ? WHERE id IN (?, ?)`, now().Add(-40*24*time.Hour), idle.ID, used.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}

	for c, want := range map[*OAuthClient]bool{idle: false, used: true, recent: true} {
		if _, err := s.GetOAuthClient(ctx, c.ClientID); (err == nil) != want {
			t.Errorf("%s kept: %v, want %v", c.Name, err == nil, want)
		}
	}
}
