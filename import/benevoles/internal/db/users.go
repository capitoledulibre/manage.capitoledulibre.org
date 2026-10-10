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

// User is a volunteer account, kept across events.
type User struct {
	ID          int64
	Email       string
	Name        string
	Phone       string
	CreatedAt   time.Time
	LastLoginAt *time.Time

	// Avatar is a file name in the upload directory, "" when the account
	// shows its initials. AvatarSource says where it came from.
	Avatar          string
	AvatarSource    string
	AvatarCheckedAt *time.Time

	// SharePhone lets the volunteers of the poles this account manages see
	// its phone number.
	SharePhone bool

	// IsAdmin is not stored with the account: it is set for the length of a
	// session opened through the staff login (see Session.Admin).
	IsAdmin bool
}

// Where a profile picture came from. Recorded explicitly so that an uploaded
// photo is never overwritten by the Gravatar fallback, and so that a volunteer
// who removed their picture is not handed one back on the next page view.
const (
	AvatarNone     = ""         // nothing yet: the Gravatar fallback may run
	AvatarUpload   = "upload"   // uploaded by the volunteer
	AvatarGravatar = "gravatar" // fetched from gravatar.com, cached on disk
	AvatarOptOut   = "none"     // the volunteer wants no picture
)

// AvatarURL is the picture to display, or "" when the initials should be
// used instead. Pictures are always served from this application, including
// the ones fetched from gravatar.com.
func (u User) AvatarURL() string {
	if u.Avatar == "" {
		return ""
	}
	return "/uploads/" + u.Avatar
}

// WantsGravatar tells whether gravatar.com may be looked up for this account:
// there is no uploaded photo and the volunteer has not opted out.
func (u User) WantsGravatar() bool {
	return u.AvatarSource == AvatarNone || u.AvatarSource == AvatarGravatar
}

// DisplayName returns the name, falling back to the e-mail.
func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

const userCols = `u.id, u.email, u.name, u.phone, u.created_at, u.last_login_at,
	u.avatar, u.avatar_source, u.avatar_checked_at, u.share_phone`

// scanUser reads the userCols columns, plus any extra column the query
// selected after them, so that joined queries share this one column list.
func scanUser(row interface{ Scan(...any) error }, extra ...any) (User, error) {
	var u User
	var created string
	var last, checked sql.NullString
	dest := []any{&u.ID, &u.Email, &u.Name, &u.Phone, &created, &last,
		&u.Avatar, &u.AvatarSource, &checked, &u.SharePhone}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return u, err
	}
	u.CreatedAt = ParseTime(created)
	u.LastLoginAt = NullTime(last)
	u.AvatarCheckedAt = NullTime(checked)
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

// SetSharePhone records whether a manager shows their phone number to the
// volunteers of their poles.
func (d *DB) SetSharePhone(id int64, share bool) error {
	_, err := d.Exec(`UPDATE users SET share_phone = ? WHERE id = ?`, share, id)
	return err
}

// ManagesAnyPole tells whether the user manages at least one pole, in any
// event: the phone-sharing choice only concerns them.
func (d *DB) ManagesAnyPole(userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM pole_managers WHERE user_id = ?`, userID).Scan(&n)
	return n > 0
}

// SetAvatar records a profile picture and returns the file name it replaces,
// which the caller should drop from the upload directory.
func (d *DB) SetAvatar(id int64, name, source string) (string, error) {
	var old string
	if err := d.QueryRow(`SELECT avatar FROM users WHERE id = ?`, id).Scan(&old); err != nil {
		return "", err
	}
	_, err := d.Exec(`UPDATE users SET avatar = ?, avatar_source = ?, avatar_checked_at = NULL WHERE id = ?`, name, source, id)
	return old, err
}

// MarkGravatarChecked remembers that gravatar.com has just been asked about
// this account, hit or miss, so the lookup is not retried on every page view.
func (d *DB) MarkGravatarChecked(id int64) error {
	_, err := d.Exec(`UPDATE users SET avatar_checked_at = ? WHERE id = ?`, Now(), id)
	return err
}

// ImageInUse tells whether an uploaded file is still referenced somewhere.
// Duplicating an event or copying a pole makes two rows point at the same
// file, so a deletion has to check before unlinking it.
func (d *DB) ImageInUse(name string) bool {
	if name == "" {
		return false
	}
	var n int
	err := d.QueryRow(`SELECT
		(SELECT COUNT(*) FROM poles WHERE image = ?) +
		(SELECT COUNT(*) FROM events WHERE image = ?) +
		(SELECT COUNT(*) FROM users WHERE avatar = ?) +
		(SELECT COUNT(*) FROM settings WHERE key = 'org_logo' AND value = ?)`,
		name, name, name, name).Scan(&n)
	return err != nil || n > 0
}

// ListUsers returns all users ordered by name then e-mail. Callers rendering
// a page should prefer SearchUsers, which pages the result.
func (d *DB) ListUsers() ([]User, error) {
	return d.queryUsers(`SELECT ` + userCols + ` FROM users u ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`)
}

// SearchUsers returns one page of users matching q on the name or the e-mail.
// An empty q matches everyone.
func (d *DB) SearchUsers(q string, limit, offset int) ([]User, error) {
	if q == "" {
		return d.queryUsers(`SELECT `+userCols+` FROM users u
			ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE LIMIT ? OFFSET ?`, limit, offset)
	}
	pattern := "%" + escapeLike(q) + "%"
	return d.queryUsers(`SELECT `+userCols+` FROM users u
		WHERE u.name LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\'
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE LIMIT ? OFFSET ?`,
		pattern, pattern, limit, offset)
}

// CountUsers counts the users SearchUsers would return, ignoring paging.
func (d *DB) CountUsers(q string) (int, error) {
	var n int
	if q == "" {
		err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
		return n, err
	}
	pattern := "%" + escapeLike(q) + "%"
	err := d.QueryRow(`SELECT COUNT(*) FROM users u
		WHERE u.name LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\'`, pattern, pattern).Scan(&n)
	return n, err
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

// PeekMagicLink tells which user a token logs in, without using it up. Mail
// security gateways open the links of incoming messages to scan them; the
// page they reach must not spend the token the volunteer is about to click.
func (d *DB) PeekMagicLink(tok string) (int64, error) {
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
	return userID, nil
}

// ConsumeMagicLink validates and burns a token, returning the user id.
func (d *DB) ConsumeMagicLink(tok string) (int64, error) {
	userID, err := d.PeekMagicLink(tok)
	if err != nil {
		return 0, err
	}
	// Burn it only if nobody did in between: two clicks, one session.
	res, err := d.Exec(`UPDATE magic_links SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`, Now(), hashToken(tok))
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	d.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, Now(), userID)
	// Housekeeping: drop expired links.
	d.Exec(`DELETE FROM magic_links WHERE expires_at < ?`, Now())
	return userID, nil
}

// --- sessions -----------------------------------------------------------

// CreateSession opens a browser session.
// admin marks a session opened through the staff login, which carries the
// administrator rights.
func (d *DB) CreateSession(userID int64, ttl time.Duration, admin bool) (string, error) {
	id := randomToken()
	_, err := d.Exec(`INSERT INTO sessions (id, user_id, expires_at, admin) VALUES (?, ?, ?, ?)`, id, userID, FormatTime(time.Now().Add(ttl)), admin)
	return id, err
}

// Session is a valid browser session.
type Session struct {
	User      User // IsAdmin set from Admin
	ExpiresAt time.Time
	Admin     bool
}

// GetSession returns a valid session with its user.
func (d *DB) GetSession(id string) (Session, error) {
	var sess Session
	var userID int64
	var expires string
	if err := d.QueryRow(`SELECT user_id, expires_at, admin FROM sessions WHERE id = ?`, id).Scan(&userID, &expires, &sess.Admin); err != nil {
		return sess, err
	}
	sess.ExpiresAt = ParseTime(expires)
	if sess.ExpiresAt.Before(time.Now()) {
		d.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return Session{}, sql.ErrNoRows
	}
	u, err := d.GetUser(userID)
	if err != nil {
		return Session{}, err
	}
	u.IsAdmin = sess.Admin
	sess.User = u
	return sess, nil
}

// ExtendSession pushes the expiry of a session to ttl from now.
func (d *DB) ExtendSession(id string, ttl time.Duration) error {
	_, err := d.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, FormatTime(time.Now().Add(ttl)), id)
	return err
}

// DeleteSession logs a session out.
func (d *DB) DeleteSession(id string) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// UserPoles maps user ids to the names of the poles they joined in an event.
func (d *DB) UserPoles(eventID int64) (map[int64][]string, error) {
	rows, err := d.Query(`SELECT pm.user_id, p.name FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, eventID)
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

// EnsureVolunteer returns the user with this e-mail, creating it if needed,
// and fills in the name and phone when they are still empty. Existing values
// entered by the volunteer are never overwritten.
func (d *DB) EnsureVolunteer(email, name, phone string) (User, error) {
	u, err := d.GetOrCreateUser(email)
	if err != nil {
		return u, err
	}
	if u.Name == "" && name != "" {
		u.Name = name
	}
	if u.Phone == "" && phone != "" {
		u.Phone = phone
	}
	if err := d.UpdateProfile(u.ID, u.Name, u.Phone); err != nil {
		return u, err
	}
	return u, nil
}

// --- calendar subscription ----------------------------------------------

// CalendarToken returns the user's calendar secret, creating it on first use.
// The token is what lets an agenda application poll the planning without a
// session, so it is generated with the same entropy as a login token.
func (d *DB) CalendarToken(userID int64) (string, error) {
	var tok sql.NullString
	if err := d.QueryRow(`SELECT calendar_token FROM users WHERE id = ?`, userID).Scan(&tok); err != nil {
		return "", err
	}
	if tok.Valid && tok.String != "" {
		return tok.String, nil
	}
	return d.ResetCalendarToken(userID)
}

// ResetCalendarToken issues a new secret, which invalidates every calendar
// already subscribed with the previous one.
func (d *DB) ResetCalendarToken(userID int64) (string, error) {
	tok := randomToken()
	if _, err := d.Exec(`UPDATE users SET calendar_token = ? WHERE id = ?`, tok, userID); err != nil {
		return "", err
	}
	return tok, nil
}

// UserByCalendarToken finds the owner of a calendar secret.
func (d *DB) UserByCalendarToken(tok string) (User, error) {
	if tok == "" {
		return User{}, ErrNotFound
	}
	return scanUser(d.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.calendar_token = ?`, tok))
}
