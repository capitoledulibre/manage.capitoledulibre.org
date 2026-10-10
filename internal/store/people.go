package store

import (
	"context"
	"database/sql"
)

// A person is one e-mail address, whatever they come for: partner contact
// (managers), staff (admins) and, later, volunteer. The address, the name,
// the language, the avatar and the last login belong to the person, so they
// are the same wherever that person appears.

// querier is what both *sql.DB and *sql.Tx offer, so the person helpers can
// run inside a caller's transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ensurePerson returns the id of the person with this address, creating it
// when needed. A name only fills an empty one, so a name the person chose
// is never overwritten by an invitation; the language is only used on
// creation.
func ensurePerson(ctx context.Context, q querier, email, name, lang string) (int64, error) {
	if lang == "" {
		lang = "fr"
	}

	var id int64

	err := q.QueryRowContext(ctx, `INSERT INTO people (uid, email, name, lang, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (email) DO UPDATE SET name = CASE WHEN people.name = '' THEN excluded.name ELSE people.name END
		RETURNING id`, NewUUID(), NormalizeEmail(email), name, lang, now()).Scan(&id)

	return id, err
}

// touchPersonLogin records a login of a person.
func touchPersonLogin(ctx context.Context, q querier, personID int64) error {
	_, err := q.ExecContext(ctx, `UPDATE people SET last_login_at = ? WHERE id = ?`, now(), personID)

	return err
}
