package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// OAuthClient is an application an assistant registered to be granted
// access (OAuth 2.1 dynamic client registration): a public client, which
// proves itself with PKCE, and the addresses it may be sent back to.
type OAuthClient struct {
	ID           int64
	ClientID     string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

func scanOAuthClient(row interface{ Scan(...any) error }) (*OAuthClient, error) {
	var (
		c    OAuthClient
		uris string
	)

	if err := row.Scan(&c.ID, &c.ClientID, &c.Name, &uris, &c.CreatedAt); err != nil {
		return nil, notFound(err)
	}

	c.RedirectURIs = strings.Split(uris, "\n")

	return &c, nil
}

const oauthClientCols = `id, client_id, name, redirect_uris, created_at`

// RegisterOAuthClient records a client.
func (s *Store) RegisterOAuthClient(ctx context.Context, name string, redirectURIs []string) (*OAuthClient, error) {
	token, _ := NewToken()

	return scanOAuthClient(s.db.QueryRowContext(ctx, `INSERT INTO oauth_clients (client_id, name, redirect_uris, created_at)
		VALUES (?, ?, ?, ?) RETURNING `+oauthClientCols, "mgc_"+token, name, strings.Join(redirectURIs, "\n"), now()))
}

// GetOAuthClient returns a client by its client_id.
func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	return scanOAuthClient(s.db.QueryRowContext(ctx, `SELECT `+oauthClientCols+` FROM oauth_clients WHERE client_id = ?`, clientID))
}

// OAuthGrant is what a person grants a client: their rights (a staff
// member's with AdminID), and whether it may change things.
type OAuthGrant struct {
	ClientID    int64
	PersonID    int64
	AdminID     *int64
	RedirectURI string
	Challenge   string
	Writes      bool
}

// CreateOAuthCode records a grant behind a one-time code, valid for ttl.
func (s *Store) CreateOAuthCode(ctx context.Context, g *OAuthGrant, ttl time.Duration) (string, error) {
	code, hash := NewToken()

	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_codes (code_hash, client_id, person_id, admin_id, redirect_uri, code_challenge, writes, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, hash, g.ClientID, g.PersonID, nullInt(g.AdminID), g.RedirectURI, g.Challenge, g.Writes, now().Add(ttl))

	return code, err
}

// ConsumeOAuthCode returns the grant behind a code, once: a code is
// deleted as it is read, valid or not.
func (s *Store) ConsumeOAuthCode(ctx context.Context, code string) (*OAuthGrant, error) {
	var (
		g       OAuthGrant
		admin   sql.NullInt64
		expires time.Time
	)

	err := s.db.QueryRowContext(ctx, `DELETE FROM oauth_codes WHERE code_hash = ?
		RETURNING client_id, person_id, admin_id, redirect_uri, code_challenge, writes, expires_at`, HashToken(code)).
		Scan(&g.ClientID, &g.PersonID, &admin, &g.RedirectURI, &g.Challenge, &g.Writes, &expires)
	if err != nil {
		return nil, notFound(err)
	}

	if !expires.After(now()) {
		return nil, ErrNotFound
	}

	if admin.Valid {
		g.AdminID = &admin.Int64
	}

	return &g, nil
}

// IssueOAuthTokens turns a grant into an access token, valid for access,
// and a refresh token, valid for refresh. Both are shown once.
func (s *Store) IssueOAuthTokens(ctx context.Context, client *OAuthClient, g *OAuthGrant, access, refresh time.Duration) (string, string, error) {
	accessToken, accessHash := NewToken()
	refreshToken, refreshHash := NewToken()

	_, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (uid, token_hash, person_id, admin_id, name, writes, expires_at, created_at,
			client_id, refresh_hash, refresh_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, NewUUID(), accessHash, g.PersonID, nullInt(g.AdminID), client.Name, g.Writes,
		now().Add(access), now(), client.ID, refreshHash, now().Add(refresh))
	if err != nil {
		return "", "", err
	}

	return "mgt_" + accessToken, "mgr_" + refreshToken, nil
}

// RefreshOAuthTokens renews a grant from its refresh token: a new access
// token and a new refresh token, the former ones no longer working. It
// returns whether the grant may change things too.
func (s *Store) RefreshOAuthTokens(ctx context.Context, client *OAuthClient, refreshToken string, access, refresh time.Duration) (string, string, bool, error) {
	raw, ok := strings.CutPrefix(refreshToken, "mgr_")
	if !ok {
		return "", "", false, ErrNotFound
	}

	accessToken, accessHash := NewToken()
	newRefresh, refreshHash := NewToken()

	var writes bool

	err := s.db.QueryRowContext(ctx, `UPDATE api_tokens SET token_hash = ?, expires_at = ?, refresh_hash = ?, refresh_expires_at = ?
		WHERE refresh_hash = ? AND client_id = ? AND refresh_expires_at > ? RETURNING writes`,
		accessHash, now().Add(access), refreshHash, now().Add(refresh), HashToken(raw), client.ID, now()).Scan(&writes)
	if err != nil {
		return "", "", false, notFound(err)
	}

	return "mgt_" + accessToken, "mgr_" + newRefresh, writes, nil
}
