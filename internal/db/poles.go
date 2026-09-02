package db

import (
	"database/sql"
	"strings"
	"time"
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

// Pole is a team inside an edition.
type Pole struct {
	ID           int64
	EditionID    int64
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

// IsOpen tells whether anyone can join by themselves.
func (p Pole) IsOpen() bool { return p.AccessMode == ModePublic }

// IsPrivate tells whether joining requires the invitation link.
func (p Pole) IsPrivate() bool { return p.AccessMode == ModePrivate }

// ManagersCanAdd tells whether pole managers may add members (admins always can).
func (p Pole) ManagersCanAdd() bool { return p.AccessMode != ModeRestricted }

// Color returns a stable palette index (0-7) for the pole.
func (p Pole) Color() int { return int(p.ID % 8) }

var defaultIcons = []string{"🙌", "🎪", "🍻", "🎤", "📦", "🧭", "🎟️", "🛠️"}

// DisplayIcon returns the configured icon or a default one.
func (p Pole) DisplayIcon() string {
	if p.Icon != "" {
		return p.Icon
	}
	if p.IsDefault {
		return "⭐"
	}
	return defaultIcons[p.Color()]
}

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

const poleCols = `id, edition_id, name, description, access_mode, COALESCE(access_key, ''), icon, default_tasks, image, is_default, freeze_at, position, created_at`

func scanPole(row interface{ Scan(...any) error }) (Pole, error) {
	var p Pole
	var freeze sql.NullString
	var created string
	if err := row.Scan(&p.ID, &p.EditionID, &p.Name, &p.Description, &p.AccessMode, &p.AccessKey, &p.Icon, &p.DefaultTasks, &p.Image, &p.IsDefault, &freeze, &p.Position, &created); err != nil {
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

// ListPoles returns the poles of an edition, default pole first.
func (d *DB) ListPoles(editionID int64) ([]Pole, error) {
	return d.queryPoles(`SELECT `+poleCols+` FROM poles WHERE edition_id = ? ORDER BY position, name COLLATE NOCASE`, editionID)
}

// GetPole returns one pole.
func (d *DB) GetPole(id int64) (Pole, error) {
	return scanPole(d.QueryRow(`SELECT `+poleCols+` FROM poles WHERE id = ?`, id))
}

// DefaultPole returns the edition's default (organizer) pole.
func (d *DB) DefaultPole(editionID int64) (Pole, error) {
	return scanPole(d.QueryRow(`SELECT `+poleCols+` FROM poles WHERE edition_id = ? AND is_default = 1`, editionID))
}

// CreatePole adds a pole to an edition.
func (d *DB) CreatePole(p Pole) (int64, error) {
	if p.AccessMode == "" {
		p.AccessMode = ModePublic
	}
	if p.AccessMode == ModePrivate && p.AccessKey == "" {
		p.AccessKey = NewAccessKey()
	}
	res, err := d.Exec(`INSERT INTO poles (edition_id, name, description, access_mode, access_key, icon, default_tasks, image, is_default, freeze_at, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		p.EditionID, p.Name, p.Description, p.AccessMode, OptString(p.AccessKey), p.Icon, p.DefaultTasks, p.Image, OptTime(p.FreezeAt), p.Position, Now())
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
	_, err := d.Exec(`UPDATE poles SET name = ?, description = ?, access_mode = ?, access_key = ?, icon = ?, default_tasks = ?, image = ?, freeze_at = ?, position = ? WHERE id = ?`,
		p.Name, p.Description, p.AccessMode, OptString(p.AccessKey), p.Icon, p.DefaultTasks, p.Image, OptTime(p.FreezeAt), p.Position, p.ID)
	return err
}

// SetPoleImage stores the uploaded image file name of a pole ("" to clear).
func (d *DB) SetPoleImage(id int64, image string) error {
	_, err := d.Exec(`UPDATE poles SET image = ? WHERE id = ?`, image, id)
	return err
}

// CopyPole duplicates a pole's settings, locations and managers into the
// target edition (slots and members are not copied). A private pole gets a
// fresh invitation key. It returns the new pole id.
func (d *DB) CopyPole(poleID, targetEditionID int64) (int64, error) {
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
		// The target edition already has a default pole: update it in place.
		if err := tx.QueryRow(`SELECT id FROM poles WHERE edition_id = ? AND is_default = 1`, targetEditionID).Scan(&newID); err != nil {
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
		tx.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM poles WHERE edition_id = ?`, targetEditionID).Scan(&pos)
		name := src.Name
		if targetEditionID == src.EditionID {
			name += " (copie)"
		}
		res, err := tx.Exec(`INSERT INTO poles (edition_id, name, description, access_mode, access_key, icon, default_tasks, image, is_default, freeze_at, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			targetEditionID, name, src.Description, src.AccessMode, key, src.Icon, src.DefaultTasks, src.Image, pos, Now())
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
	// Managers must also belong to the target edition's default pole.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at)
		SELECT dp.id, pm.user_id, ? FROM pole_managers pm, poles dp WHERE pm.pole_id = ? AND dp.edition_id = ? AND dp.is_default = 1`, Now(), poleID, targetEditionID); err != nil {
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

// ManagedPoleIDs returns the ids of the poles the user manages in an edition.
func (d *DB) ManagedPoleIDs(editionID, userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT pm.pole_id FROM pole_managers pm JOIN poles p ON p.id = pm.pole_id WHERE p.edition_id = ? AND pm.user_id = ?`, editionID, userID)
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

// JoinPole adds a user to a pole. Joining any pole also joins the edition's
// default pole so organizer-wide slots are visible to everyone.
func (d *DB) JoinPole(poleID, userID int64) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at) VALUES (?, ?, ?)`, poleID, userID, Now())
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT OR IGNORE INTO pole_members (pole_id, user_id, joined_at)
		SELECT dp.id, ?, ? FROM poles p JOIN poles dp ON dp.edition_id = p.edition_id AND dp.is_default = 1 WHERE p.id = ?`,
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

// MemberPoleIDs returns the ids of the poles the user joined in an edition.
func (d *DB) MemberPoleIDs(editionID, userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT pm.pole_id FROM pole_members pm JOIN poles p ON p.id = pm.pole_id WHERE p.edition_id = ? AND pm.user_id = ?`, editionID, userID)
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
		var created string
		var last sql.NullString
		if err := rows.Scan(&m.User.ID, &m.User.Email, &m.User.Name, &m.User.Phone, &m.User.IsAdmin, &created, &last, &m.IsManager, &m.SlotCount); err != nil {
			return nil, err
		}
		m.User.CreatedAt = ParseTime(created)
		m.User.LastLoginAt = NullTime(last)
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

// ListPoleCoverage computes coverage for every pole of an edition.
func (d *DB) ListPoleCoverage(editionID int64) ([]PoleCoverage, error) {
	poles, err := d.ListPoles(editionID)
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
