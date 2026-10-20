package store

import (
	"context"
	"database/sql"
	"time"
)

// APIToken is a personal token for the API (MCP). Staff tells that it
// carries the staff's rights.
type APIToken struct {
	ID         int64
	UID        string
	PersonID   int64
	Staff      bool
	Name       string
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

const apiTokenCols = `id, uid, person_id, admin_id IS NOT NULL, name, expires_at, last_used_at, created_at`

func scanAPIToken(row interface{ Scan(...any) error }) (*APIToken, error) {
	var (
		t    APIToken
		used sql.NullTime
	)

	if err := row.Scan(&t.ID, &t.UID, &t.PersonID, &t.Staff, &t.Name, &t.ExpiresAt, &used, &t.CreatedAt); err != nil {
		return nil, notFound(err)
	}

	t.LastUsedAt = timePtr(used)

	return &t, nil
}

// CreateAPIToken makes a token for a person, valid for ttl; with an
// adminID, it carries that staff member's rights. It returns the token,
// shown once: only its hash is kept.
func (s *Store) CreateAPIToken(ctx context.Context, personID int64, adminID *int64, name string, ttl time.Duration) (string, *APIToken, error) {
	token, hash := NewToken()

	var admin any
	if adminID != nil {
		admin = *adminID
	}

	row := s.db.QueryRowContext(ctx, `INSERT INTO api_tokens (uid, token_hash, person_id, admin_id, name, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING `+apiTokenCols, NewUUID(), hash, personID, admin, name, now().Add(ttl), now())

	t, err := scanAPIToken(row)
	if err != nil {
		return "", nil, err
	}

	return "mgt_" + token, t, nil
}

// APITokenFor returns the valid token of a bearer, and records its use.
// ErrNotFound for an unknown, expired or revoked one.
func (s *Store) APITokenFor(ctx context.Context, token string) (*APIToken, error) {
	if len(token) < 5 || token[:4] != "mgt_" {
		return nil, ErrNotFound
	}

	t, err := scanAPIToken(s.db.QueryRowContext(ctx, `UPDATE api_tokens SET last_used_at = ?
		WHERE token_hash = ? AND expires_at > ? RETURNING `+apiTokenCols, now(), HashToken(token[4:]), now()))
	if err != nil {
		return nil, err
	}

	return t, nil
}

// ListAPITokens returns a person's tokens, the most recent first.
func (s *Store) ListAPITokens(ctx context.Context, personID int64) ([]*APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE person_id = ? ORDER BY created_at DESC, id DESC`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*APIToken

	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, t)
	}

	return out, rows.Err()
}

// DeleteAPIToken revokes a token of a person.
func (s *Store) DeleteAPIToken(ctx context.Context, personID int64, uid string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE person_id = ? AND uid = ?`, personID, uid)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}
