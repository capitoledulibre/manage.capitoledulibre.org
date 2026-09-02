package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// User is a volunteer account, kept across editions.
type User struct {
	ID          int64
	Email       string
	Name        string
	Phone       string
	IsAdmin     bool
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// DisplayName returns the name, falling back to the e-mail.
func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

const userCols = `u.id, u.email, u.name, u.phone, u.is_admin, u.created_at, u.last_login_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	var last sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Phone, &u.IsAdmin, &created, &last)
	if err != nil {
		return u, err
	}
	u.CreatedAt = ParseTime(created)
	u.LastLoginAt = NullTime(last)
	return u, nil
}

// GetUser returns a user by id.
func (d *DB) GetUser(id int64) (User, error) {
	return scanUser(d.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.id = ?`, id))
}

// GetUserByEmail returns a user by e-mail (case-insensitive).
func (d *DB) GetUserByEmail(email string) (User, error) {
	return scanUser(d.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.email = ?`, email))
}

// GetOrCreateUser returns the user with this e-mail, creating it if needed.
func (d *DB) GetOrCreateUser(email string) (User, error) {
	u, err := d.GetUserByEmail(email)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	res, err := d.Exec(`INSERT INTO users (email, created_at) VALUES (?, ?)`, email, Now())
	if err != nil {
		return u, err
	}
	id, _ := res.LastInsertId()
	return d.GetUser(id)
}

// UpdateProfile updates name and phone.
func (d *DB) UpdateProfile(id int64, name, phone string) error {
	_, err := d.Exec(`UPDATE users SET name = ?, phone = ? WHERE id = ?`, name, phone, id)
	return err
}

// SetAdmin grants or revokes the global administrator flag.
func (d *DB) SetAdmin(id int64, admin bool) error {
	_, err := d.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, admin, id)
	return err
}

// CountAdmins returns the number of administrators.
func (d *DB) CountAdmins() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n, err
}

// ListUsers returns all users ordered by name then e-mail.
func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.Query(`SELECT ` + userCols + ` FROM users u ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user and everything attached to it.
func (d *DB) DeleteUser(id int64) error {
	_, err := d.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// --- magic links --------------------------------------------------------

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// CreateMagicLink creates a one-time login token valid for ttl.
func (d *DB) CreateMagicLink(userID int64, ttl time.Duration) (string, error) {
	tok := randomToken()
	_, err := d.Exec(`INSERT INTO magic_links (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(tok), userID, FormatTime(time.Now().Add(ttl)))
	return tok, err
}

// ConsumeMagicLink validates and burns a token, returning the user id.
func (d *DB) ConsumeMagicLink(tok string) (int64, error) {
	var userID int64
	var expires string
	var used sql.NullString
	err := d.QueryRow(`SELECT user_id, expires_at, used_at FROM magic_links WHERE token_hash = ?`, hashToken(tok)).
		Scan(&userID, &expires, &used)
	if err != nil {
		return 0, err
	}
	if used.Valid || ParseTime(expires).Before(time.Now()) {
		return 0, sql.ErrNoRows
	}
	if _, err := d.Exec(`UPDATE magic_links SET used_at = ? WHERE token_hash = ?`, Now(), hashToken(tok)); err != nil {
		return 0, err
	}
	d.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, Now(), userID)
	// Housekeeping: drop expired links.
	d.Exec(`DELETE FROM magic_links WHERE expires_at < ?`, Now())
	return userID, nil
}

// --- sessions -----------------------------------------------------------

// CreateSession opens a browser session.
func (d *DB) CreateSession(userID int64, ttl time.Duration) (string, error) {
	id := randomToken()
	_, err := d.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, id, userID, FormatTime(time.Now().Add(ttl)))
	return id, err
}

// SessionUser returns the user attached to a valid session.
func (d *DB) SessionUser(id string) (User, error) {
	var userID int64
	var expires string
	if err := d.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE id = ?`, id).Scan(&userID, &expires); err != nil {
		return User{}, err
	}
	if ParseTime(expires).Before(time.Now()) {
		d.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return User{}, sql.ErrNoRows
	}
	return d.GetUser(userID)
}

// DeleteSession logs a session out.
func (d *DB) DeleteSession(id string) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// UserPoles maps user ids to the names of the poles they joined in an edition.
func (d *DB) UserPoles(editionID int64) (map[int64][]string, error) {
	rows, err := d.Query(`SELECT pm.user_id, p.name FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.edition_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var uid int64
		var name string
		if err := rows.Scan(&uid, &name); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], name)
	}
	return out, rows.Err()
}

// SearchUsersNotInPole finds users matching q (name or e-mail) who have not
// joined the pole yet. Matching is case-insensitive substring.
func (d *DB) SearchUsersNotInPole(poleID int64, q string, limit int) ([]User, error) {
	pattern := "%" + escapeLike(q) + "%"
	return d.queryUsers(`SELECT `+userCols+` FROM users u
		WHERE (u.name LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\')
		  AND u.id NOT IN (SELECT user_id FROM pole_members WHERE pole_id = ?)
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE LIMIT ?`, pattern, pattern, poleID, limit)
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
