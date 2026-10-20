package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
)

// NewToken returns a random URL-safe token and its hash. Only the hash is
// stored, so a database leak does not leak usable tokens.
func NewToken() (token, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)

	return token, HashToken(token)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// CreateMagicToken stores a single-use login token for an email.
func (s *Store) CreateMagicToken(ctx context.Context, email string, ttl time.Duration) (string, error) {
	token, hash := NewToken()
	_, err := s.db.ExecContext(ctx, `INSERT INTO magic_tokens (token_hash, email, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hash, NormalizeEmail(email), now().Add(ttl), now())

	return token, err
}

// CountRecentMagicTokens is used to rate-limit login emails.
func (s *Store) CountRecentMagicTokens(ctx context.Context, email string, since time.Duration) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM magic_tokens WHERE email = ? AND created_at >= ?`,
		NormalizeEmail(email), now().Add(-since)).Scan(&n)

	return n, err
}

// PeekMagicToken checks a token without consuming it.
func (s *Store) PeekMagicToken(ctx context.Context, token string) (email string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT email FROM magic_tokens WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		HashToken(token), now()).Scan(&email)

	return email, notFound(err)
}

// ConsumeMagicToken marks a valid token as used and returns its email.
func (s *Store) ConsumeMagicToken(ctx context.Context, token string) (email string, err error) {
	err = s.db.QueryRowContext(ctx, `UPDATE magic_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ? RETURNING email`,
		now(), HashToken(token), now()).Scan(&email)

	return email, notFound(err)
}

// Session is a login. Kind is SessionPerson (an e-mail link; ManagerID is
// the partner organization being looked at, if any) or SessionAdmin (the
// staff login; AdminID is set). Both belong to a person.
type Session struct {
	Kind             string
	PersonID         int64
	ManagerID        *int64
	AdminID          *int64
	ImpersonateOrgID *int64
	ExpiresAt        time.Time
}

const (
	SessionPerson = "person"
	SessionAdmin  = "admin"
)

func (s *Store) CreateSession(ctx context.Context, sess *Session, ttl time.Duration) (string, error) {
	token, hash := NewToken()
	sess.ExpiresAt = now().Add(ttl)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, kind, person_id, manager_id, admin_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hash, sess.Kind, sess.PersonID, nullInt(sess.ManagerID), nullInt(sess.AdminID), sess.ExpiresAt, now())

	return token, err
}

func (s *Store) GetSession(ctx context.Context, token, kind string) (*Session, error) {
	var (
		sess                        Session
		manager, admin, impersonate sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `SELECT kind, person_id, manager_id, admin_id, impersonate_org_id, expires_at FROM sessions
		WHERE token_hash = ? AND kind = ? AND expires_at > ?`, HashToken(token), kind, now()).
		Scan(&sess.Kind, &sess.PersonID, &manager, &admin, &impersonate, &sess.ExpiresAt)
	if err != nil {
		return nil, notFound(err)
	}

	sess.ManagerID, sess.AdminID, sess.ImpersonateOrgID = int64Ptr(manager), int64Ptr(admin), int64Ptr(impersonate)

	return &sess, nil
}

// ExtendSession pushes the expiry of a session to ttl from now.
func (s *Store) ExtendSession(ctx context.Context, token string, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, now().Add(ttl), HashToken(token))

	return err
}

// SwitchSessionManager points a person session at another organization the
// person manages.
func (s *Store) SwitchSessionManager(ctx context.Context, token string, managerID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET manager_id = ? WHERE token_hash = ?`, managerID, HashToken(token))

	return err
}

func (s *Store) SetImpersonation(ctx context.Context, token string, orgID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET impersonate_org_id = ? WHERE token_hash = ?`, nullInt(orgID), HashToken(token))

	return err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, HashToken(token))

	return err
}

// DetachManagerSessions takes an organization away from the sessions looking
// at it through this manager, used when the manager is removed or moved to
// another address. The people stay logged in, without that organization.
func (s *Store) DetachManagerSessions(ctx context.Context, managerID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET manager_id = NULL WHERE manager_id = ?`, managerID)

	return err
}

// Cleanup removes expired tokens and sessions.
func (s *Store) Cleanup(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now()); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM magic_tokens WHERE expires_at <= ?`, now().Add(-24*time.Hour)); err != nil {
		return err
	}

	// An expired API token stays listed a month, for its owner to see why
	// their assistant lost access.
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE expires_at <= ?`, now().Add(-30*24*time.Hour))

	return err
}

// Admin is a person who logs in as staff. The identity fields come from the
// person (see people.go).
type Admin struct {
	ID          int64
	UID         string
	PersonID    int64
	Email       string
	Name        string
	AvatarFile  string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

const adminCols = `a.id, a.uid, a.person_id, p.email, p.name, p.avatar_file, a.created_at, p.last_login_at`

const adminFrom = ` FROM admins a JOIN people p ON p.id = a.person_id`

func scanAdmin(row interface{ Scan(...any) error }) (*Admin, error) {
	var (
		a         Admin
		lastLogin sql.NullTime
	)

	if err := row.Scan(&a.ID, &a.UID, &a.PersonID, &a.Email, &a.Name, &a.AvatarFile, &a.CreatedAt, &lastLogin); err != nil {
		return nil, notFound(err)
	}

	a.LastLoginAt = timePtr(lastLogin)

	return &a, nil
}

// Initials returns up to two letters used when the admin has no avatar.
func (a *Admin) Initials() string { return initials(a.Label()) }

// initials returns up to two capital letters of a name, "?" when it has none.
func initials(label string) string {
	var out []rune

	for _, w := range strings.Fields(label) {
		for _, r := range w {
			if unicode.IsLetter(r) {
				out = append(out, unicode.ToUpper(r))

				break
			}
		}

		if len(out) == 2 {
			break
		}
	}

	if len(out) == 0 {
		return "?"
	}

	return string(out)
}

// UpsertAdmin creates or updates the admin matching the email and records the
// login time. The name from the identity provider only fills an empty name,
// so a name set on the staff profile is kept.
func (s *Store) UpsertAdmin(ctx context.Context, email, name string) (*Admin, error) {
	var id int64

	err := s.tx(ctx, func(tx *sql.Tx) error {
		personID, err := ensurePerson(ctx, tx, email, name, "")
		if err != nil {
			return err
		}

		if err := touchPersonLogin(ctx, tx, personID); err != nil {
			return err
		}

		return tx.QueryRowContext(ctx, `INSERT INTO admins (uid, person_id, created_at) VALUES (?, ?, ?)
			ON CONFLICT (person_id) DO UPDATE SET person_id = excluded.person_id
			RETURNING id`, NewUUID(), personID, now()).Scan(&id)
	})
	if err != nil {
		return nil, err
	}

	return s.GetAdmin(ctx, id)
}

func (s *Store) ListAdmins(ctx context.Context) ([]*Admin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+adminCols+adminFrom+` ORDER BY p.name COLLATE NOCASE, p.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Admin

	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	return out, rows.Err()
}

// UpdateAdminProfile saves the name and avatar of an admin. A new picture
// is an upload, a removed one an opt-out: either way the Gravatar fallback
// leaves it alone.
func (s *Store) UpdateAdminProfile(ctx context.Context, a *Admin) error {
	_, err := s.db.ExecContext(ctx, `UPDATE people SET name = ?1, avatar_file = ?2,
		avatar_source = CASE WHEN ?2 = avatar_file THEN avatar_source WHEN ?2 = '' THEN 'none' ELSE 'upload' END
		WHERE id = (SELECT person_id FROM admins WHERE id = ?3)`,
		a.Name, a.AvatarFile, a.ID)

	return err
}

// AdminByAvatarFile finds the admin using an avatar file.
func (s *Store) AdminByAvatarFile(ctx context.Context, file string) (*Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminCols+adminFrom+` WHERE p.avatar_file = ? AND p.avatar_file != ''`, file))
}

// Label is the name to display for an admin.
func (a *Admin) Label() string {
	if a.Name != "" {
		return a.Name
	}

	return a.Email
}

// GetAdminByUID finds an admin by its public identifier.
func (s *Store) GetAdminByUID(ctx context.Context, uid string) (*Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminCols+adminFrom+` WHERE a.uid = ?`, uid))
}

func (s *Store) GetAdmin(ctx context.Context, id int64) (*Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminCols+adminFrom+` WHERE a.id = ?`, id))
}

// NewUUID returns a random (version 4) UUID, used as the public identifier
// of rows without a slug.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	h := hex.EncodeToString(b[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
