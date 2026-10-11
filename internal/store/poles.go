package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Pole access modes.
const (
	PolePublic     = "public"     // anyone signed in can join
	PolePrivate    = "private"    // join through an invitation link carrying a key
	PoleInvitation = "invitation" // pole managers and coordinators add members
	PoleRestricted = "restricted" // only coordinators and staff add members
)

// PoleModes lists the access modes in display order.
var PoleModes = []string{PolePublic, PolePrivate, PoleInvitation, PoleRestricted}

// ValidPoleMode tells whether m is one of the access modes.
func ValidPoleMode(m string) bool {
	for _, x := range PoleModes {
		if m == x {
			return true
		}
	}

	return false
}

// DefaultPoleName is the name given to the default pole of a new edition.
const DefaultPoleName = "Général"

// reservedPoleSlugs are path segments the volunteer pages of an edition use
// next to the pole slugs.
var reservedPoleSlugs = map[string]bool{
	"manage": true, "planning": true, "volunteers": true, "edition": true, "messages": true,
	"calendar": true, "slots": true, "tasks": true, "join": true, "leave": true, "new": true, "me": true,
}

// Pole is a team of an edition.
type Pole struct {
	ID          int64
	UID         string
	EventID     int64
	EventSlug   string
	Slug        string
	Name        string
	Description string
	AccessMode  string
	// AccessKey is the secret of the invitation link of a private pole.
	AccessKey string
	// Icon is an emoji shown next to the name, empty for the initials.
	Icon      string
	ImageFile string
	// DefaultTasks holds one task per line, pre-filled on new slots.
	DefaultTasks string
	IsDefault    bool
	// FreezeAt overrides the edition's volunteer freeze date.
	FreezeAt  *time.Time
	Position  int
	CreatedAt time.Time
}

// URL is the volunteer page of the pole.
func (p *Pole) URL() string { return "/volunteers/" + p.EventSlug + "/" + p.Slug }

// ManageURL is the base of the actions that run the pole; its page is URL.
func (p *Pole) ManageURL() string { return p.URL() + "/manage" }

// Initials stand for the pole when it has no icon.
func (p *Pole) Initials() string { return initials(p.Name) }

// IsOpen tells whether anyone can join by themselves.
func (p *Pole) IsOpen() bool { return p.AccessMode == PolePublic }

// IsPrivate tells whether joining requires the invitation link.
func (p *Pole) IsPrivate() bool { return p.AccessMode == PolePrivate }

// ManagersCanAdd tells whether pole managers may add members (coordinators
// and staff always can).
func (p *Pole) ManagersCanAdd() bool { return p.AccessMode != PoleRestricted }

// Color is a stable palette index (0-7) for the pole.
func (p *Pole) Color() int { return int(p.ID % 8) }

// DefaultTaskList splits DefaultTasks into non-empty labels.
func (p *Pole) DefaultTaskList() []string {
	var out []string

	for l := range strings.SplitSeq(p.DefaultTasks, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}

	return out
}

// newAccessKey returns a short random key for the invitation link of a
// private pole.
func newAccessKey() string {
	var b [6]byte
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}

// poleSlug derives a pole slug from a name: Slugify, kept short and clear of
// the reserved path segments.
func poleSlug(name string) string {
	slug := Slugify(name)
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}

	if reservedPoleSlugs[slug] {
		slug += "-pole"
	}

	return slug
}

// uniquePoleSlug returns a free pole slug of an edition derived from name,
// ignoring the pole exceptID when renaming. It runs on q so it sees the rows
// of an open transaction.
func uniquePoleSlug(ctx context.Context, q querier, eventID int64, name string, exceptID int64) (string, error) {
	base := poleSlug(name)
	slug := base

	for i := 2; ; i++ {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM poles WHERE event_id = ? AND slug = ? AND id != ?`, eventID, slug, exceptID).Scan(&n); err != nil {
			return "", err
		}

		if n == 0 {
			return slug, nil
		}

		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

const poleCols = `p.id, p.uid, p.event_id, e.slug, p.slug, p.name, p.description, p.access_mode, p.access_key, p.icon,
	p.image_file, p.default_tasks, p.is_default, p.freeze_at, p.position, p.created_at`

const poleFrom = ` FROM poles p JOIN events e ON e.id = p.event_id`

func scanPole(row interface{ Scan(...any) error }) (*Pole, error) { return scanPoleExtra(row) }

// scanPoleExtra reads the poleCols columns, plus any extra column the query
// selected after them.
func scanPoleExtra(row interface{ Scan(...any) error }, extra ...any) (*Pole, error) {
	var (
		p      Pole
		freeze sql.NullTime
	)

	dest := []any{&p.ID, &p.UID, &p.EventID, &p.EventSlug, &p.Slug, &p.Name, &p.Description, &p.AccessMode, &p.AccessKey, &p.Icon,
		&p.ImageFile, &p.DefaultTasks, &p.IsDefault, &freeze, &p.Position, &p.CreatedAt}

	err := row.Scan(append(dest, extra...)...)
	if err != nil {
		return nil, notFound(err)
	}

	p.FreezeAt = timePtr(freeze)

	return &p, nil
}

func (s *Store) queryPoles(ctx context.Context, where string, args ...any) ([]*Pole, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+poleCols+poleFrom+` WHERE `+where+` ORDER BY p.position, p.name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Pole

	for rows.Next() {
		p, err := scanPole(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// ListPoles returns the poles of an edition, the default pole first.
func (s *Store) ListPoles(ctx context.Context, eventID int64) ([]*Pole, error) {
	return s.queryPoles(ctx, `p.event_id = ?`, eventID)
}

func (s *Store) GetPole(ctx context.Context, id int64) (*Pole, error) {
	return scanPole(s.db.QueryRowContext(ctx, `SELECT `+poleCols+poleFrom+` WHERE p.id = ?`, id))
}

// GetPoleBySlug finds a pole of an edition by its slug.
func (s *Store) GetPoleBySlug(ctx context.Context, eventID int64, slug string) (*Pole, error) {
	return scanPole(s.db.QueryRowContext(ctx, `SELECT `+poleCols+poleFrom+` WHERE p.event_id = ? AND p.slug = ?`, eventID, slug))
}

// DefaultPole returns the default pole of an edition.
func (s *Store) DefaultPole(ctx context.Context, eventID int64) (*Pole, error) {
	return scanPole(s.db.QueryRowContext(ctx, `SELECT `+poleCols+poleFrom+` WHERE p.event_id = ? AND p.is_default = 1`, eventID))
}

// createDefaultPole gives a new edition its default pole.
func createDefaultPole(ctx context.Context, q querier, eventID int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO poles (uid, event_id, slug, name, is_default, position, created_at) VALUES (?, ?, ?, ?, 1, -1, ?)`,
		NewUUID(), eventID, poleSlug(DefaultPoleName), DefaultPoleName, now())

	return err
}

// CreatePole adds a pole to an edition. Its slug is derived from p.Slug, or
// from the name when p.Slug is empty, and made unique within the edition.
func (s *Store) CreatePole(ctx context.Context, p *Pole) error {
	if !ValidPoleMode(p.AccessMode) {
		p.AccessMode = PolePublic
	}

	if p.AccessMode == PolePrivate && p.AccessKey == "" {
		p.AccessKey = newAccessKey()
	}

	base := p.Slug
	if base == "" {
		base = p.Name
	}

	slug, err := uniquePoleSlug(ctx, s.db, p.EventID, base, 0)
	if err != nil {
		return err
	}

	p.UID, p.Slug, p.CreatedAt, p.IsDefault = NewUUID(), slug, now(), false

	res, err := s.db.ExecContext(ctx, `INSERT INTO poles (uid, event_id, slug, name, description, access_mode, access_key, icon,
		image_file, default_tasks, is_default, freeze_at, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		p.UID, p.EventID, p.Slug, p.Name, p.Description, p.AccessMode, p.AccessKey, p.Icon,
		p.ImageFile, p.DefaultTasks, nullTime(p.FreezeAt), p.Position, p.CreatedAt)
	if err != nil {
		return err
	}

	p.ID, err = res.LastInsertId()

	return err
}

// UpdatePole saves the editable fields of a pole. The image has its own
// setter, SetPoleImage.
func (s *Store) UpdatePole(ctx context.Context, p *Pole) error {
	if !ValidPoleMode(p.AccessMode) {
		return ErrBadInput
	}

	if p.AccessMode == PolePrivate && p.AccessKey == "" {
		p.AccessKey = newAccessKey()
	}

	base := p.Slug
	if base == "" {
		base = p.Name
	}

	slug, err := uniquePoleSlug(ctx, s.db, p.EventID, base, p.ID)
	if err != nil {
		return err
	}

	p.Slug = slug

	_, err = s.db.ExecContext(ctx, `UPDATE poles SET slug = ?, name = ?, description = ?, access_mode = ?, access_key = ?, icon = ?,
		default_tasks = ?, freeze_at = ?, position = ? WHERE id = ?`,
		p.Slug, p.Name, p.Description, p.AccessMode, p.AccessKey, p.Icon, p.DefaultTasks, nullTime(p.FreezeAt), p.Position, p.ID)

	return err
}

// SetPoleImage sets or clears the image of a pole. The caller owns the file
// in the files store and checks FileInUse before removing the old one.
func (s *Store) SetPoleImage(ctx context.Context, poleID int64, file string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE poles SET image_file = ? WHERE id = ?`, file, poleID)

	return err
}

// RegenerateAccessKey replaces the invitation key of a pole, which voids the
// links already handed out.
func (s *Store) RegenerateAccessKey(ctx context.Context, poleID int64) (string, error) {
	key := newAccessKey()
	_, err := s.db.ExecContext(ctx, `UPDATE poles SET access_key = ? WHERE id = ?`, key, poleID)

	return key, err
}

// DeletePole removes a pole with its slots. The default pole stays.
func (s *Store) DeletePole(ctx context.Context, poleID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM poles WHERE id = ? AND is_default = 0`, poleID)

	return err
}

// CopyPole copies the settings, locations and managers of a pole into an
// edition, which may be its own (slots and members stay behind). Copying the
// default pole updates the target's default pole in place. A private pole
// gets a fresh invitation key. It returns the new pole.
func (s *Store) CopyPole(ctx context.Context, poleID, targetEventID int64) (*Pole, error) {
	var newID int64

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error

		newID, err = copyPole(ctx, tx, poleID, targetEventID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return s.GetPole(ctx, newID)
}

func copyPole(ctx context.Context, tx *sql.Tx, poleID, targetEventID int64) (int64, error) {
	src, err := scanPole(tx.QueryRowContext(ctx, `SELECT `+poleCols+poleFrom+` WHERE p.id = ?`, poleID))
	if err != nil {
		return 0, err
	}

	var newID int64

	if src.IsDefault {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM poles WHERE event_id = ? AND is_default = 1`, targetEventID).Scan(&newID); err != nil {
			return 0, notFound(err)
		}

		if _, err := tx.ExecContext(ctx, `UPDATE poles SET description = ?, icon = ?, default_tasks = ?, image_file = ? WHERE id = ?`,
			src.Description, src.Icon, src.DefaultTasks, src.ImageFile, newID); err != nil {
			return 0, err
		}
	} else {
		key := ""
		if src.AccessMode == PolePrivate {
			key = newAccessKey()
		}

		name := src.Name
		if targetEventID == src.EventID {
			name += " (copie)"
		}

		slug, err := uniquePoleSlug(ctx, tx, targetEventID, name, 0)
		if err != nil {
			return 0, err
		}

		res, err := tx.ExecContext(ctx, `INSERT INTO poles (uid, event_id, slug, name, description, access_mode, access_key, icon,
			image_file, default_tasks, is_default, position, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, (SELECT COALESCE(MAX(position), 0) + 1 FROM poles WHERE event_id = ?), ?)`,
			NewUUID(), targetEventID, slug, name, src.Description, src.AccessMode, key, src.Icon,
			src.ImageFile, src.DefaultTasks, targetEventID, now())
		if err != nil {
			return 0, err
		}

		if newID, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT name, position FROM locations WHERE pole_id = ? ORDER BY position, id`, poleID)
	if err != nil {
		return 0, err
	}

	var locations []Location

	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.Name, &l.Position); err != nil {
			rows.Close()

			return 0, err
		}

		locations = append(locations, l)
	}

	rows.Close()

	for _, l := range locations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO locations (uid, pole_id, name, position) VALUES (?, ?, ?, ?)`,
			NewUUID(), newID, l.Name, l.Position); err != nil {
			return 0, err
		}
	}

	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pole_managers (pole_id, person_id) SELECT ?, person_id FROM pole_managers WHERE pole_id = ?`,
		newID, poleID); err != nil {
		return 0, err
	}

	// Managers are members of their pole and of the edition's default pole.
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO pole_members (pole_id, person_id, joined_at)
		SELECT dp.id, pm.person_id, ? FROM pole_managers pm, poles dp
		WHERE pm.pole_id = ? AND (dp.id = ? OR (dp.event_id = ? AND dp.is_default = 1))`,
		now(), poleID, newID, targetEventID)

	return newID, err
}

// --- pole managers --------------------------------------------------------

// IsPoleManager tells whether the person manages the pole.
func (s *Store) IsPoleManager(ctx context.Context, poleID, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM pole_managers WHERE pole_id = ? AND person_id = ?`, poleID, personID)
}

// ListPoleManagers returns the managers of a pole.
func (s *Store) ListPoleManagers(ctx context.Context, poleID int64) ([]*Person, error) {
	return s.queryPeople(ctx, `SELECT `+peCols+` FROM people pe JOIN pole_managers pm ON pm.person_id = pe.id
		WHERE pm.pole_id = ? ORDER BY pe.name COLLATE NOCASE, pe.email`, poleID)
}

// AddPoleManager makes a person manager, and so member, of a pole.
func (s *Store) AddPoleManager(ctx context.Context, poleID, personID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pole_managers (pole_id, person_id) VALUES (?, ?)`, poleID, personID); err != nil {
			return err
		}

		return joinPole(ctx, tx, poleID, personID)
	})
}

// RemovePoleManager takes the management of a pole away; the person stays a
// member.
func (s *Store) RemovePoleManager(ctx context.Context, poleID, personID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pole_managers WHERE pole_id = ? AND person_id = ?`, poleID, personID)

	return err
}

// ListManagedPoles returns the poles of an edition the person manages.
func (s *Store) ListManagedPoles(ctx context.Context, eventID, personID int64) ([]*Pole, error) {
	return s.queryPoles(ctx, `p.event_id = ? AND p.id IN (SELECT pole_id FROM pole_managers WHERE person_id = ?)`, eventID, personID)
}

// ManagedPoleIDs returns the set of poles of an edition the person manages.
func (s *Store) ManagedPoleIDs(ctx context.Context, eventID, personID int64) (map[int64]bool, error) {
	return s.idSet(ctx, `SELECT pm.pole_id FROM pole_managers pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND pm.person_id = ?`, eventID, personID)
}

// ManagesAnyPole tells whether the person manages a pole of any edition:
// sharing a phone number only concerns them.
func (s *Store) ManagesAnyPole(ctx context.Context, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM pole_managers WHERE person_id = ?`, personID)
}

// --- pole members ---------------------------------------------------------

// IsPoleMember tells whether the person joined the pole.
func (s *Store) IsPoleMember(ctx context.Context, poleID, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM pole_members WHERE pole_id = ? AND person_id = ?`, poleID, personID)
}

// JoinPole adds a person to a pole and to the default pole of its edition,
// whose slots concern every volunteer.
func (s *Store) JoinPole(ctx context.Context, poleID, personID int64) error {
	return joinPole(ctx, s.db, poleID, personID)
}

func joinPole(ctx context.Context, q querier, poleID, personID int64) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO pole_members (pole_id, person_id, joined_at)
		SELECT dp.id, ?, ? FROM poles p JOIN poles dp ON dp.id = p.id OR (dp.event_id = p.event_id AND dp.is_default = 1)
		WHERE p.id = ?`, personID, now(), poleID)

	return err
}

// LeavePole removes a person from a pole and from its slots.
func (s *Store) LeavePole(ctx context.Context, poleID, personID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM signups WHERE person_id = ? AND slot_id IN (SELECT id FROM slots WHERE pole_id = ?)`,
			personID, poleID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, `DELETE FROM pole_members WHERE pole_id = ? AND person_id = ?`, poleID, personID)

		return err
	})
}

// MemberPoleIDs returns the set of poles of an edition the person joined.
func (s *Store) MemberPoleIDs(ctx context.Context, eventID, personID int64) (map[int64]bool, error) {
	return s.idSet(ctx, `SELECT pm.pole_id FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND pm.person_id = ?`, eventID, personID)
}

// PreviousPoleSlugs returns the last edition before eventID the person took
// part in, and the slugs of the poles they were in there (the default pole
// aside). A copied pole keeps its slug, which is what lets the caller match
// them to this edition's poles. ErrNotFound when there is none.
func (s *Store) PreviousPoleSlugs(ctx context.Context, eventID, personID int64) (*Event, []string, error) {
	prev, err := scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events e
		WHERE e.id != ? AND (e.year, e.id) < (SELECT year, id FROM events WHERE id = ?)
		  AND EXISTS (SELECT 1 FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		              WHERE p.event_id = e.id AND pm.person_id = ? AND p.is_default = 0)
		ORDER BY e.year DESC, e.id DESC LIMIT 1`, eventID, eventID, personID))
	if err != nil {
		return nil, nil, err
	}

	slugs, err := s.stringList(ctx, `SELECT p.slug FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND pm.person_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, prev.ID, personID)

	return prev, slugs, err
}

// PoleMember is a member of a pole with their sign-up count in it.
type PoleMember struct {
	Person    *Person
	IsManager bool
	SlotCount int
}

// ListPoleMembers returns the members of a pole.
func (s *Store) ListPoleMembers(ctx context.Context, poleID int64) ([]*PoleMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+peCols+`,
			EXISTS (SELECT 1 FROM pole_managers pg WHERE pg.pole_id = pm.pole_id AND pg.person_id = pe.id),
			(SELECT COUNT(*) FROM signups sg JOIN slots sl ON sl.id = sg.slot_id WHERE sl.pole_id = pm.pole_id AND sg.person_id = pe.id)
		FROM people pe JOIN pole_members pm ON pm.person_id = pe.id
		WHERE pm.pole_id = ?
		ORDER BY pe.name COLLATE NOCASE, pe.email COLLATE NOCASE`, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*PoleMember

	for rows.Next() {
		var m PoleMember

		if m.Person, err = scanPerson(rows, &m.IsManager, &m.SlotCount); err != nil {
			return nil, err
		}

		out = append(out, &m)
	}

	return out, rows.Err()
}

// SearchPeopleNotInPole finds people matching q (name or e-mail) who have
// not joined the pole.
func (s *Store) SearchPeopleNotInPole(ctx context.Context, poleID int64, q string, limit int) ([]*Person, error) {
	pattern := "%" + escapeLike(q) + "%"

	return s.queryPeople(ctx, `SELECT `+personCols+` FROM people
		WHERE (name LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\')
		  AND id NOT IN (SELECT person_id FROM pole_members WHERE pole_id = ?)
		ORDER BY name COLLATE NOCASE, email COLLATE NOCASE LIMIT ?`, pattern, pattern, poleID, limit)
}

// PersonSlotsInPole lists the slots of a pole the person is signed up on. It
// is read before a removal so the notification can say what is cancelled.
func (s *Store) PersonSlotsInPole(ctx context.Context, poleID, personID int64) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN signups me ON me.slot_id = s.id AND me.person_id = ?
		WHERE s.pole_id = ? ORDER BY s.starts_at`, personID, poleID)
}

// --- locations ------------------------------------------------------------

// Location is a place of a pole (a room, a desk…).
type Location struct {
	ID       int64
	UID      string
	PoleID   int64
	Name     string
	Position int
}

// ListLocations returns the locations of a pole.
func (s *Store) ListLocations(ctx context.Context, poleID int64) ([]*Location, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, uid, pole_id, name, position FROM locations WHERE pole_id = ? ORDER BY position, name COLLATE NOCASE`, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Location

	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.ID, &l.UID, &l.PoleID, &l.Name, &l.Position); err != nil {
			return nil, err
		}

		out = append(out, &l)
	}

	return out, rows.Err()
}

// GetLocationByUID finds a location of a pole by its public identifier.
func (s *Store) GetLocationByUID(ctx context.Context, poleID int64, uid string) (*Location, error) {
	var l Location

	err := s.db.QueryRowContext(ctx, `SELECT id, uid, pole_id, name, position FROM locations WHERE pole_id = ? AND uid = ?`, poleID, uid).
		Scan(&l.ID, &l.UID, &l.PoleID, &l.Name, &l.Position)
	if err != nil {
		return nil, notFound(err)
	}

	return &l, nil
}

// CreateLocation adds a location at the end of a pole's list.
func (s *Store) CreateLocation(ctx context.Context, poleID int64, name string) (*Location, error) {
	l := &Location{UID: NewUUID(), PoleID: poleID, Name: name}

	err := s.db.QueryRowContext(ctx, `INSERT INTO locations (uid, pole_id, name, position)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM locations WHERE pole_id = ?))
		RETURNING id, position`, l.UID, poleID, name, poleID).Scan(&l.ID, &l.Position)
	if err != nil {
		return nil, err
	}

	return l, nil
}

func (s *Store) RenameLocation(ctx context.Context, id int64, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE locations SET name = ? WHERE id = ?`, name, id)

	return err
}

// DeleteLocation removes a location; its slots stay, without a location.
func (s *Store) DeleteLocation(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM locations WHERE id = ?`, id)

	return err
}

// --- coverage -------------------------------------------------------------

// PoleCoverage sums up how well the slots of a pole are staffed.
type PoleCoverage struct {
	Pole     *Pole
	Members  int
	Slots    int
	Capacity int
	Filled   int
	// Short counts the slots still missing at least one volunteer.
	Short    int
	Managers []*Person
}

// Percent is the share of places filled, 100 when there is none to fill.
func (c *PoleCoverage) Percent() int {
	if c.Capacity == 0 {
		return 100
	}

	return c.Filled * 100 / c.Capacity
}

// ListPoleCoverage returns the coverage of every pole of an edition.
func (s *Store) ListPoleCoverage(ctx context.Context, eventID int64) ([]*PoleCoverage, error) {
	poles, err := s.ListPoles(ctx, eventID)
	if err != nil {
		return nil, err
	}

	out := make([]*PoleCoverage, 0, len(poles))

	for _, p := range poles {
		c := &PoleCoverage{Pole: p}

		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pole_members WHERE pole_id = ?`, p.ID).Scan(&c.Members); err != nil {
			return nil, err
		}

		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(capacity), 0), COALESCE(SUM(n), 0),
				COALESCE(SUM(n < capacity), 0)
			FROM (SELECT sl.capacity, (SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = sl.id) AS n FROM slots sl WHERE sl.pole_id = ?)`, p.ID).
			Scan(&c.Slots, &c.Capacity, &c.Filled, &c.Short)
		if err != nil {
			return nil, err
		}

		if c.Managers, err = s.ListPoleManagers(ctx, p.ID); err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// --- small query helpers --------------------------------------------------

// exists tells whether a query returns a row.
func (s *Store) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var one int

	err := s.db.QueryRowContext(ctx, query+` LIMIT 1`, args...).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}

	return err == nil, err
}

// idSet collects the ids a query returns.
func (s *Store) idSet(ctx context.Context, query string, args ...any) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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

// stringList collects the strings a query returns.
func (s *Store) stringList(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, rows.Err()
}

// escapeLike escapes the wildcards of a LIKE pattern (with ESCAPE '\').
func escapeLike(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `%`, `\%`)

	return strings.ReplaceAll(v, `_`, `\_`)
}
