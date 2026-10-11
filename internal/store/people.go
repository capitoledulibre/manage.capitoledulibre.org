package store

import (
	"context"
	"database/sql"
	"time"
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

// Person is one e-mail address and the identity attached to it.
type Person struct {
	ID          int64
	UID         string
	Email       string
	Name        string
	Lang        string
	AvatarFile  string
	CreatedAt   time.Time
	LastLoginAt *time.Time

	// The volunteer side of a person.
	Phone string
	// SharePhone lets the volunteers of the poles this person manages see
	// their phone number.
	SharePhone bool
	// AvatarSource says where AvatarFile came from, see AvatarUpload.
	AvatarSource    string
	AvatarCheckedAt *time.Time
}

// Where a profile picture came from. Recorded so that an uploaded photo is
// never overwritten by the Gravatar fallback, and so that a person who
// removed their picture is not handed one back on the next page view.
const (
	AvatarUnknown  = ""         // nothing yet: the Gravatar fallback may run
	AvatarUpload   = "upload"   // uploaded by the person
	AvatarGravatar = "gravatar" // fetched from gravatar.com, cached on disk
	AvatarOptOut   = "none"     // the person wants no picture
)

// WantsGravatar tells whether gravatar.com may be looked up for this person:
// there is no uploaded photo and they have not opted out.
func (p *Person) WantsGravatar() bool {
	return p.AvatarSource == AvatarUnknown || p.AvatarSource == AvatarGravatar
}

// Label is the name to display for a person.
func (p *Person) Label() string {
	if p.Name != "" {
		return p.Name
	}

	return p.Email
}

// Initials are shown in place of an avatar.
func (p *Person) Initials() string { return initials(p.Label()) }

const personCols = `id, uid, email, name, lang, avatar_file, created_at, last_login_at,
	phone, share_phone, avatar_source, avatar_checked_at`

// scanPerson reads the personCols columns, plus any extra column the query
// selected after them, so that joined queries share this one column list.
func scanPerson(row interface{ Scan(...any) error }, extra ...any) (*Person, error) {
	var (
		p                  Person
		lastLogin, checked sql.NullTime
	)

	dest := []any{&p.ID, &p.UID, &p.Email, &p.Name, &p.Lang, &p.AvatarFile, &p.CreatedAt, &lastLogin,
		&p.Phone, &p.SharePhone, &p.AvatarSource, &checked}

	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, notFound(err)
	}

	p.LastLoginAt, p.AvatarCheckedAt = timePtr(lastLogin), timePtr(checked)

	return &p, nil
}

// queryPeople runs a query selecting personCols, or peCols when the query
// joins other tables.
func (s *Store) queryPeople(ctx context.Context, query string, args ...any) ([]*Person, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Person

	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// peCols is personCols for a query where people is aliased pe (p is taken
// by poles and partnerships).
var peCols = prefixCols("pe", personCols)

func (s *Store) GetPerson(ctx context.Context, id int64) (*Person, error) {
	return scanPerson(s.db.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE id = ?`, id))
}

// GetPersonByUID finds a person by their public identifier.
func (s *Store) GetPersonByUID(ctx context.Context, uid string) (*Person, error) {
	return scanPerson(s.db.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE uid = ?`, uid))
}

// PersonByEmail finds the person behind an address.
func (s *Store) PersonByEmail(ctx context.Context, email string) (*Person, error) {
	return scanPerson(s.db.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE email = ?`, NormalizeEmail(email)))
}

// TouchPersonLogin records a login of a person.
func (s *Store) TouchPersonLogin(ctx context.Context, id int64) error {
	return touchPersonLogin(ctx, s.db, id)
}
