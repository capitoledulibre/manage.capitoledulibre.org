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

	if _, err := s.ConsumeOAuthCode(ctx, code); !errors.Is(err, ErrNotFound) {
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

	if _, _, _, err := s.RefreshOAuthTokens(ctx, other, refresh, time.Hour, 24*time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("a refresh by another client: %v", err)
	}

	// Refreshing renews both; the former ones stop working.
	access2, refresh2, writes, err := s.RefreshOAuthTokens(ctx, client, refresh, time.Hour, 24*time.Hour)
	if err != nil || !writes {
		t.Fatalf("refresh: %v", err)
	}

	if _, err := s.APITokenFor(ctx, access); !errors.Is(err, ErrNotFound) {
		t.Errorf("the former access token works: %v", err)
	}

	if _, _, _, err := s.RefreshOAuthTokens(ctx, client, refresh, time.Hour, 24*time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("the former refresh token works: %v", err)
	}

	if _, err := s.APITokenFor(ctx, access2); err != nil {
		t.Errorf("the new access token: %v", err)
	}

	// Revoking the grant ends both.
	list, err := s.ListAPITokens(ctx, p.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("grants: %v %d", err, len(list))
	}

	if err := s.DeleteAPIToken(ctx, p.ID, list[0].UID); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := s.RefreshOAuthTokens(ctx, client, refresh2, time.Hour, 24*time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked grant renews: %v", err)
	}
}
