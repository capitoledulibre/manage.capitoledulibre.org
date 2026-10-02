package db

import (
	"database/sql"
	"strings"
	"time"
	"unicode"
)

// Pole access modes.
const (
	ModePublic     = "public"     // anyone registered can join
	ModePrivate    = "private"    // join through an invitation link carrying a key
	ModeInvitation = "invitation" // managers and admins add members
	ModeRestricted = "restricted" // only admins add members
)

// Modes lists the access modes in display order.
var Modes = []string{ModePublic, ModePrivate, ModeInvitation, ModeRestricted}

// Pole is a team inside an event.
type Pole struct {
	ID           int64
	EventID      int64
	EventSlug    string
	Slug         string
	Name         string
	Description  string
	AccessMode   string
	AccessKey    string // used by ModePrivate
	Icon         string // emoji shown next to the name, may be empty
	DefaultTasks string // one task per line, pre-filled on new slots
	Image        string // uploaded image file name, may be empty
	IsDefault    bool
	FreezeAt     *time.Time
	Position     int
	CreatedAt    time.Time
}

// URL is the public page of the pole.
func (p Pole) URL() string { return "/" + p.EventSlug + "/" + p.Slug }

// ManageURL is the management page of the pole.
func (p Pole) ManageURL() string { return p.URL() + "/manage" }

// Initials returns one or two uppercase letters derived from the name, used
// as the default icon.
func (p Pole) Initials() string {
	words := strings.Fields(p.Name)
	var out []rune
	for _, w := range words {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 && len(words) > 0 {
		out = []rune(words[0])[:1]
	}
	return string(out)
}

// IsOpen tells whether anyone can join by themselves.
func (p Pole) IsOpen() bool { return p.AccessMode == ModePublic }

// IsPrivate tells whether joining requires the invitation link.
func (p Pole) IsPrivate() bool { return p.AccessMode == ModePrivate }

// ManagersCanAdd tells whether pole managers may add members (admins always can).
func (p Pole) ManagersCanAdd() bool { return p.AccessMode != ModeRestricted }

// Color returns a stable palette index (0-7) for the pole.
func (p Pole) Color() int { return int(p.ID % 8) }

// DisplayIcon returns the configured emoji icon, or "" when initials should be shown.
func (p Pole) DisplayIcon() string { return p.Icon }

// DefaultTaskList splits DefaultTasks into non-empty labels.
func (p Pole) DefaultTaskList() []string {
	var out []string
	for _, l := range strings.Split(p.DefaultTasks, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// NewAccessKey generates a short random key for private poles.
func NewAccessKey() string { return randomToken()[:12] }

const poleCols = `id, event_id, (SELECT slug FROM events WHERE events.id = poles.event_id), slug, name, description, access_mode, COALESCE(access_key, ''), icon, default_tasks, image, is_default, freeze_at, position, created_at`

func scanPole(row interface{ Scan(...any) error }) (Pole, error) {
	var p Pole
	var freeze sql.NullString
	var created string
	if err := row.Scan(&p.ID, &p.EventID, &p.EventSlug, &p.Slug, &p.Name, &p.Description, &p.AccessMode, &p.AccessKey, &p.Icon, &p.DefaultTasks, &p.Image, &p.IsDefault, &freeze, &p.Position, &created); err != nil {
		return p, err
	}
	p.FreezeAt = NullTime(freeze)
	p.CreatedAt = ParseTime(created)
	return p, nil
}

func (d *DB) queryPoles(q string, args ...any) ([]Pole, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pole
	for rows.Next() {
		p, err := scanPole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPoles returns the poles of an event, default pole first.
func (d *DB) ListPoles(eventID int64) ([]Pole, error) {
	return d.queryPoles(`SELECT `+poleCols+` FROM poles WHERE event_id = ? ORDER BY position, name COLLATE NOCASE`, eventID)
}

// GetPoleBySlug returns a pole by event and pole slugs.
func (d *DB) GetPoleBySlug(eventSlug, slug string) (Pole, error) {
	return scanPole(d.QueryRow(`SELECT `+poleCols+` FROM poles WHERE slug = ? AND event_id = (SELECT id FROM events WHERE slug = ?)`, slug, eventSlug))
}

// GetPole returns one pole.
func (d *DB) GetPole(id int64) (Pole, error) {
	return scanPole(d.QueryRow(`SELECT `+poleCols+` FROM poles WHERE id = ?`, id))
}

// DefaultPole returns the event's default (organizer) pole.
func (d *DB) DefaultPole(eventID int64) (Pole, error) {
	return scanPole(d.QueryRow(`SELECT `+poleCols+` FROM poles WHERE event_id = ? AND is_default = 1`, eventID))
}

// CreatePole adds a pole to an event.
func (d *DB) CreatePole(p Pole) (int64, error) {
	if p.AccessMode == "" {
		p.AccessMode = ModePublic
	}
	if p.AccessMode == ModePrivate && p.AccessKey == "" {
		p.AccessKey = NewAccessKey()
	}
	base := p.Slug
	if !ValidSlug(base) {
		base = p.Name
	}
	p.Slug = d.UniquePoleSlug(p.EventID, base, 0)
	res, err := d.Exec(`INSERT INTO poles (event_id, slug, name, description, access_mode, access_key, icon, default_tasks, image, is_default, freeze_at, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		p.EventID, p.Slug, p.Name, p.Description, p.AccessMode, OptString(p.AccessKey), p.Icon, p.DefaultTasks, p.Image, OptTime(p.FreezeAt), p.Position, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePole updates the editable fields of a pole.
func (d *DB) UpdatePole(p Pole) error {
	if p.AccessMode == ModePrivate && p.AccessKey == "" {
		p.AccessKey = NewAccessKey()
	}
	base := p.Slug
	if !ValidSlug(base) {
		base = p.Name
	}
	p.Slug = d.UniquePoleSlug(p.EventID, base, p.ID)
	_, err := d.Exec(`UPDATE poles SET slug = ?, name = ?, description = ?, access_mode = ?, access_key = ?, icon = ?, default_tasks = ?, image = ?, freeze_at = ?, position = ? WHERE id = ?`,
		p.Slug, p.Name, p.Description, p.AccessMode, OptString(p.AccessKey), p.Icon, p.DefaultTasks, p.Image, OptTime(p.FreezeAt), p.Position, p.ID)
	return err
}

// SetPoleImage stores the uploaded image file name of a pole ("" to clear).
func (d *DB) SetPoleImage(id int64, image string) error {
	_, err := d.Exec(`UPDATE poles SET image = ? WHERE id = ?`, image, id)
	return err
}

// CopyPole duplicates a pole's settings, locations and managers into the
// target event (slots and members are not copied). A private pole gets a
// fresh invitation key. It returns the new pole id.
func (d *DB) CopyPole(poleID, targetEventID int64) (int64, error) {
	src, err := d.GetPole(poleID)
	if err != nil {
		return 0, err
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var newID int64
	if src.IsDefault {
		// The target event already has a default pole: update it in place.
		if err := tx.QueryRow(`SELECT id FROM poles WHERE event_id = ? AND is_default = 1`, targetEventID).Scan(&newID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE poles SET description = ?, icon = ?, default_tasks = ?, image = ? WHERE id = ?`,
			src.Description, src.Icon, src.DefaultTasks, src.Image, newID); err != nil {
			return 0, err
		}
	} else {
		key := any(nil)
		if src.AccessMode == ModePrivate {
			key = NewAccessKey()
		}
		var pos int
		tx.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM poles WHERE event_id = ?`, targetEventID).Scan(&pos)
		name := src.Name
		if targetEventID == src.EventID {
			name += " (copie)"
		}
		res, err := tx.Exec(`INSERT INTO poles (event_id, slug, name, description, access_mode, access_key, icon, default_tasks, image, is_default, freeze_at, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			targetEventID, uniquePoleSlug(tx, targetEventID, name, 0), name, src.Description, src.AccessMode, key, src.Icon, src.DefaultTasks, src.Image, pos, Now())
		if err != nil {
			return 0, err
		}
		newID, _ = res.LastInsertId()
	}
	if _, err := tx.Exec(`INSERT INTO locations (pole_id, name, position) SELECT ?, name, position FROM locations WHERE pole_id = ?`, newID, poleID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO pole_managers (pole_id, user_id) SELECT ?, user_id FROM pole_managers WHERE pole_id = ?`, newID, poleID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at) SELECT ?, user_id, ? FROM pole_managers WHERE pole_id = ?`, newID, Now(), poleID); err != nil {
		return 0, err
	}
	// Managers must also belong to the target event's default pole.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at)
		SELECT dp.id, pm.user_id, ? FROM pole_managers pm, poles dp WHERE pm.pole_id = ? AND dp.event_id = ? AND dp.is_default = 1`, Now(), poleID, targetEventID); err != nil {
		return 0, err
	}
	return newID, tx.Commit()
}

// RegenerateAccessKey replaces the invitation key of a pole and returns it.
func (d *DB) RegenerateAccessKey(id int64) (string, error) {
	key := NewAccessKey()
	_, err := d.Exec(`UPDATE poles SET access_key = ? WHERE id = ?`, key, id)
	return key, err
}

// DeletePole removes a pole and its slots.
func (d *DB) DeletePole(id int64) error {
	_, err := d.Exec(`DELETE FROM poles WHERE id = ? AND is_default = 0`, id)
	return err
}

// --- managers -----------------------------------------------------------

// IsPoleManager tells whether the user manages the pole.
func (d *DB) IsPoleManager(poleID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM pole_managers WHERE pole_id = ? AND user_id = ?`, poleID, userID).Scan(&n)
	return n > 0
}

// ListPoleManagers returns the managers of a pole.
func (d *DB) ListPoleManagers(poleID int64) ([]User, error) {
	return d.queryUsers(`SELECT `+userCols+` FROM users u WHERE u.id IN (SELECT user_id FROM pole_managers WHERE pole_id = ?) ORDER BY u.name COLLATE NOCASE, u.email`, poleID)
}

// AddPoleManager makes a user manager (and member) of a pole.
func (d *DB) AddPoleManager(poleID, userID int64) error {
	if _, err := d.Exec(`INSERT OR IGNORE INTO pole_managers (pole_id, user_id) VALUES (?, ?)`, poleID, userID); err != nil {
		return err
	}
	return d.JoinPole(poleID, userID)
}

// RemovePoleManager revokes management rights.
func (d *DB) RemovePoleManager(poleID, userID int64) error {
	_, err := d.Exec(`DELETE FROM pole_managers WHERE pole_id = ? AND user_id = ?`, poleID, userID)
	return err
}

// ListManagedPoles returns the poles the user manages in an event.
func (d *DB) ListManagedPoles(eventID, userID int64) ([]Pole, error) {
	return d.queryPoles(`SELECT `+poleCols+` FROM poles WHERE event_id = ? AND id IN (SELECT pole_id FROM pole_managers WHERE user_id = ?) ORDER BY position, name COLLATE NOCASE`, eventID, userID)
}

// ManagedPoleIDs returns the ids of the poles the user manages in an event.
func (d *DB) ManagedPoleIDs(eventID, userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT pm.pole_id FROM pole_managers pm JOIN poles p ON p.id = pm.pole_id WHERE p.event_id = ? AND pm.user_id = ?`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// --- members ------------------------------------------------------------

// IsPoleMember tells whether the user joined the pole.
func (d *DB) IsPoleMember(poleID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM pole_members WHERE pole_id = ? AND user_id = ?`, poleID, userID).Scan(&n)
	return n > 0
}

// JoinPole adds a user to a pole. Joining any pole also joins the event's
// default pole so organizer-wide slots are visible to everyone.
func (d *DB) JoinPole(poleID, userID int64) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at) VALUES (?, ?, ?)`, poleID, userID, Now())
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at)
		SELECT dp.id, ?, ? FROM poles p JOIN poles dp ON dp.event_id = p.event_id AND dp.is_default = 1 WHERE p.id = ?`,
		userID, Now(), poleID)
	return err
}

// LeavePole removes a user from a pole and from all of its slots.
func (d *DB) LeavePole(poleID, userID int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM signups WHERE user_id = ? AND slot_id IN (SELECT id FROM slots WHERE pole_id = ?)`, userID, poleID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM pole_members WHERE pole_id = ? AND user_id = ?`, poleID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// MemberPoleIDs returns the ids of the poles the user joined in an event.
func (d *DB) MemberPoleIDs(eventID, userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT pm.pole_id FROM pole_members pm JOIN poles p ON p.id = pm.pole_id WHERE p.event_id = ? AND pm.user_id = ?`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// PreviousPoleSlugs returns the event the user last took part in before
// eventID, and the slugs of the poles they were in there (the default pole
// aside). A duplicated event keeps its pole slugs, which is what lets the
// caller match them to this year's poles. ErrNotFound when there is none.
func (d *DB) PreviousPoleSlugs(eventID, userID int64) (Event, []string, error) {
	prev, err := scanEvent(d.QueryRow(`SELECT `+eventCols+` FROM events e
		WHERE e.id != ? AND e.starts_on < (SELECT starts_on FROM events WHERE id = ?)
		  AND EXISTS (SELECT 1 FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		              WHERE p.event_id = e.id AND pm.user_id = ? AND p.is_default = 0)
		ORDER BY e.starts_on DESC LIMIT 1`, eventID, eventID, userID))
	if err != nil {
		return prev, nil, err
	}
	rows, err := d.Query(`SELECT p.slug FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND pm.user_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, prev.ID, userID)
	if err != nil {
		return prev, nil, err
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return prev, nil, err
		}
		slugs = append(slugs, slug)
	}
	return prev, slugs, rows.Err()
}

// PoleMember is a member with their slot count in the pole.
type PoleMember struct {
	User      User
	IsManager bool
	SlotCount int
}

// ListPoleMembers returns members of a pole with their signup counts.
func (d *DB) ListPoleMembers(poleID int64) ([]PoleMember, error) {
	rows, err := d.Query(`SELECT `+userCols+`,
			EXISTS(SELECT 1 FROM pole_managers pg WHERE pg.pole_id = ? AND pg.user_id = u.id),
			(SELECT COUNT(*) FROM signups sg JOIN slots s ON s.id = sg.slot_id WHERE s.pole_id = ? AND sg.user_id = u.id)
		FROM users u JOIN pole_members pm ON pm.user_id = u.id
		WHERE pm.pole_id = ?
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`, poleID, poleID, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PoleMember
	for rows.Next() {
		var m PoleMember
		u, err := scanUser(rows, &m.IsManager, &m.SlotCount)
		if err != nil {
			return nil, err
		}
		m.User = u
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) queryUsers(q string, args ...any) ([]User, error) {
	rows, err := d.Query(q, args...)
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

// --- locations ----------------------------------------------------------

// Location subdivides a pole calendar (a room, a desk...).
type Location struct {
	ID       int64
	PoleID   int64
	Name     string
	Position int
}

// ListLocations returns the locations of a pole.
func (d *DB) ListLocations(poleID int64) ([]Location, error) {
	rows, err := d.Query(`SELECT id, pole_id, name, position FROM locations WHERE pole_id = ? ORDER BY position, name COLLATE NOCASE`, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Location
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.ID, &l.PoleID, &l.Name, &l.Position); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLocation returns one location.
func (d *DB) GetLocation(id int64) (Location, error) {
	var l Location
	err := d.QueryRow(`SELECT id, pole_id, name, position FROM locations WHERE id = ?`, id).Scan(&l.ID, &l.PoleID, &l.Name, &l.Position)
	return l, err
}

// CreateLocation adds a location to a pole.
func (d *DB) CreateLocation(poleID int64, name string) (int64, error) {
	res, err := d.Exec(`INSERT INTO locations (pole_id, name, position) VALUES (?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM locations WHERE pole_id = ?))`, poleID, name, poleID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RenameLocation renames a location.
func (d *DB) RenameLocation(id int64, name string) error {
	_, err := d.Exec(`UPDATE locations SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteLocation deletes a location; its slots become pole-wide.
func (d *DB) DeleteLocation(id int64) error {
	_, err := d.Exec(`DELETE FROM locations WHERE id = ?`, id)
	return err
}

// --- coverage -----------------------------------------------------------

// PoleCoverage summarizes how full a pole is.
type PoleCoverage struct {
	Pole      Pole
	Members   int
	Slots     int
	Capacity  int
	Filled    int
	ShortSlot int // slots with at least one missing volunteer
	Managers  []User
}

// Percent returns the fill ratio as an integer percentage.
func (c PoleCoverage) Percent() int {
	if c.Capacity == 0 {
		return 100
	}
	return c.Filled * 100 / c.Capacity
}

// ListPoleCoverage computes coverage for every pole of an event.
func (d *DB) ListPoleCoverage(eventID int64) ([]PoleCoverage, error) {
	poles, err := d.ListPoles(eventID)
	if err != nil {
		return nil, err
	}
	var out []PoleCoverage
	for _, p := range poles {
		c := PoleCoverage{Pole: p}
		if err := d.QueryRow(`SELECT COUNT(*) FROM pole_members WHERE pole_id = ?`, p.ID).Scan(&c.Members); err != nil {
			return nil, err
		}
		err := d.QueryRow(`SELECT COUNT(*), COALESCE(SUM(capacity), 0), COALESCE(SUM(n), 0), COALESCE(SUM(CASE WHEN n < capacity THEN 1 ELSE 0 END), 0)
			FROM (SELECT s.capacity, (SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = s.id) AS n FROM slots s WHERE s.pole_id = ?)`, p.ID).
			Scan(&c.Slots, &c.Capacity, &c.Filled, &c.ShortSlot)
		if err != nil {
			return nil, err
		}
		c.Managers, _ = d.ListPoleManagers(p.ID)
		out = append(out, c)
	}
	return out, nil
}

// UserSlotIDsInPole lists the slots of a pole the user is signed up on. It is
// read before a removal so the notification can state what is being cancelled.
func (d *DB) UserSlotIDsInPole(poleID, userID int64) ([]int64, error) {
	rows, err := d.Query(`SELECT s.id FROM signups sg JOIN slots s ON s.id = sg.slot_id
		WHERE s.pole_id = ? AND sg.user_id = ? ORDER BY s.starts_at`, poleID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
