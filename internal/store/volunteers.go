package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// SignupDeadline is the moment sign-ups close, or nil when they stay open
// until the volunteer status changes. poleFreeze, when set, overrides the
// edition's date.
func (e *Event) SignupDeadline(poleFreeze *time.Time) *time.Time {
	if poleFreeze != nil {
		return poleFreeze
	}

	return e.VolunteerFreezeAt
}

// SignupsLocked tells whether volunteers can no longer change their
// sign-ups in a pole whose freeze date is poleFreeze (nil for none).
func (e *Event) SignupsLocked(poleFreeze *time.Time, now time.Time) bool {
	if e.VolunteerStatus != VolunteersOpen {
		return true
	}

	freeze := e.SignupDeadline(poleFreeze)

	return freeze != nil && !now.Before(*freeze)
}

// CurrentVolunteerEvent returns the edition volunteers should see: the most
// recent open one, else the most recent frozen one. ErrNotFound when none.
func (s *Store) CurrentVolunteerEvent(ctx context.Context) (*Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events
		WHERE volunteer_status IN ('open', 'frozen')
		ORDER BY volunteer_status = 'open' DESC, year DESC, id DESC LIMIT 1`))
}

// --- coordinators ---------------------------------------------------------

// IsEventCoordinator tells whether the person coordinates the volunteers of
// an edition.
func (s *Store) IsEventCoordinator(ctx context.Context, eventID, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM event_coordinators WHERE event_id = ? AND person_id = ?`, eventID, personID)
}

// ListEventCoordinators returns the coordinators of an edition.
func (s *Store) ListEventCoordinators(ctx context.Context, eventID int64) ([]*Person, error) {
	return s.queryPeople(ctx, `SELECT `+peCols+` FROM people pe JOIN event_coordinators ec ON ec.person_id = pe.id
		WHERE ec.event_id = ? ORDER BY pe.name COLLATE NOCASE, pe.email`, eventID)
}

// AddEventCoordinator makes a person coordinator of an edition. Coordinators
// are volunteers too, so they join its default pole.
func (s *Store) AddEventCoordinator(ctx context.Context, eventID, personID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return addEventCoordinator(ctx, tx, eventID, personID) })
}

func addEventCoordinator(ctx context.Context, q querier, eventID, personID int64) error {
	if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO event_coordinators (event_id, person_id) VALUES (?, ?)`, eventID, personID); err != nil {
		return err
	}

	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO pole_members (pole_id, person_id, joined_at)
		SELECT id, ?, ? FROM poles WHERE event_id = ? AND is_default = 1`, personID, now(), eventID)

	return err
}

// RemoveEventCoordinator takes a person off the coordination; they stay a
// volunteer.
func (s *Store) RemoveEventCoordinator(ctx context.Context, eventID, personID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM event_coordinators WHERE event_id = ? AND person_id = ?`, eventID, personID)

	return err
}

// IsPoleCoordinator tells whether the person coordinates the edition of a
// pole.
func (s *Store) IsPoleCoordinator(ctx context.Context, poleID, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM event_coordinators ec JOIN poles p ON p.event_id = ec.event_id
		WHERE p.id = ? AND ec.person_id = ?`, poleID, personID)
}

// --- an edition's volunteers ----------------------------------------------

// VolunteerStats sums up the volunteer side of an edition.
type VolunteerStats struct {
	Volunteers int
	Slots      int
	Capacity   int
	Filled     int
}

// GetVolunteerStats counts the volunteers, slots and places of an edition.
func (s *Store) GetVolunteerStats(ctx context.Context, eventID int64) (*VolunteerStats, error) {
	var st VolunteerStats

	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT pm.person_id) FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ?`, eventID).Scan(&st.Volunteers)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(s.capacity), 0),
			(SELECT COUNT(*) FROM signups sg JOIN slots s2 ON s2.id = sg.slot_id JOIN poles p2 ON p2.id = s2.pole_id WHERE p2.event_id = ?)
		FROM slots s JOIN poles p ON p.id = s.pole_id WHERE p.event_id = ?`, eventID, eventID).
		Scan(&st.Slots, &st.Capacity, &st.Filled)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

// EventVolunteer is a volunteer of an edition with the poles they joined,
// and what they signed up for: slots, and the time they add up to.
type EventVolunteer struct {
	Person    *Person
	Poles     []string
	SlotCount int
	Minutes   int
}

// Hours renders the time signed up for, "6 h" or "4 h 30".
func (v *EventVolunteer) Hours() string {
	h, m := v.Minutes/60, v.Minutes%60
	if m == 0 {
		return strconv.Itoa(h) + "\u00a0h"
	}

	return strconv.Itoa(h) + "\u00a0h\u00a0" + strconv.Itoa(m)
}

// ListEventVolunteers returns everyone who joined a pole of an edition.
func (s *Store) ListEventVolunteers(ctx context.Context, eventID int64) ([]*EventVolunteer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+peCols+`, GROUP_CONCAT(p.name, char(31)),
			(SELECT COUNT(*) FROM signups sg JOIN slots s ON s.id = sg.slot_id JOIN poles p2 ON p2.id = s.pole_id
			 WHERE p2.event_id = ? AND sg.person_id = pe.id),
			(SELECT COALESCE(SUM(CAST(ROUND((julianday(s.ends_at) - julianday(s.starts_at)) * 1440) AS INTEGER)), 0)
			 FROM signups sg JOIN slots s ON s.id = sg.slot_id JOIN poles p2 ON p2.id = s.pole_id
			 WHERE p2.event_id = ? AND sg.person_id = pe.id)
		FROM people pe
		JOIN pole_members pm ON pm.person_id = pe.id
		JOIN poles p ON p.id = pm.pole_id AND p.event_id = ?
		GROUP BY pe.id
		ORDER BY pe.name COLLATE NOCASE, pe.email COLLATE NOCASE`, eventID, eventID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*EventVolunteer

	for rows.Next() {
		var (
			v     EventVolunteer
			poles sql.NullString
		)

		if v.Person, err = scanPerson(rows, &poles, &v.SlotCount, &v.Minutes); err != nil {
			return nil, err
		}

		if poles.Valid {
			v.Poles = strings.Split(poles.String, "\x1f")
		}

		out = append(out, &v)
	}

	return out, rows.Err()
}

// PersonPoles maps the volunteers of an edition to the names of the poles
// they joined, the default pole aside.
func (s *Store) PersonPoles(ctx context.Context, eventID int64) (map[int64][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pm.person_id, p.name FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE p.event_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]string{}

	for rows.Next() {
		var (
			id   int64
			name string
		)

		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}

		out[id] = append(out[id], name)
	}

	return out, rows.Err()
}

// CopyVolunteerSide prepares an edition from another one: every pole is
// copied (settings, locations, managers; no slots, no members), the
// coordinators carry over, and the logo too when the target has none.
func (s *Store) CopyVolunteerSide(ctx context.Context, srcEventID, dstEventID int64) error {
	if srcEventID == dstEventID {
		return ErrBadInput
	}

	return s.tx(ctx, func(tx *sql.Tx) error {
		var poleIDs []int64

		rows, err := tx.QueryContext(ctx, `SELECT id FROM poles WHERE event_id = ? ORDER BY is_default DESC, position, name COLLATE NOCASE`, srcEventID)
		if err != nil {
			return err
		}

		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()

				return err
			}

			poleIDs = append(poleIDs, id)
		}

		rows.Close()

		for _, id := range poleIDs {
			if _, err := copyPole(ctx, tx, id, dstEventID); err != nil {
				return err
			}
		}

		// The team running an edition usually runs the next one.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO event_coordinators (event_id, person_id)
			SELECT ?, person_id FROM event_coordinators WHERE event_id = ?`, dstEventID, srcEventID); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pole_members (pole_id, person_id, joined_at)
			SELECT dp.id, ec.person_id, ? FROM event_coordinators ec JOIN poles dp ON dp.event_id = ec.event_id AND dp.is_default = 1
			WHERE ec.event_id = ?`, now(), dstEventID); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `UPDATE events SET logo_file = (SELECT logo_file FROM events WHERE id = ?)
			WHERE id = ? AND logo_file = ''`, srcEventID, dstEventID)

		return err
	})
}

// --- a person as a volunteer ----------------------------------------------

// IsVolunteer tells whether the person joined a pole or coordinates an
// edition, whichever the edition.
func (s *Store) IsVolunteer(ctx context.Context, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM pole_members WHERE person_id = ?1
		UNION ALL SELECT 1 FROM event_coordinators WHERE person_id = ?1`, personID)
}

// ListCoordinatedEvents returns the editions the person coordinates, the
// most recent first.
func (s *Store) ListCoordinatedEvents(ctx context.Context, personID int64) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM events
		WHERE id IN (SELECT event_id FROM event_coordinators WHERE person_id = ?) ORDER BY year DESC, id DESC`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Event

	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, e)
	}

	return out, rows.Err()
}

// EnsureVolunteer returns the person with this address, creating them when
// needed, and fills their name and phone when still empty: what the person
// entered is never overwritten.
func (s *Store) EnsureVolunteer(ctx context.Context, email, name, phone, lang string) (*Person, error) {
	var id int64

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error

		if id, err = ensurePerson(ctx, tx, email, name, lang); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `UPDATE people SET phone = ? WHERE id = ? AND phone = ''`, phone, id)

		return err
	})
	if err != nil {
		return nil, err
	}

	return s.GetPerson(ctx, id)
}

// UpdateVolunteerProfile saves the name and phone of a person.
func (s *Store) UpdateVolunteerProfile(ctx context.Context, personID int64, name, phone string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE people SET name = ?, phone = ? WHERE id = ?`, name, phone, personID)

	return err
}

// SetPersonLang sets the language of a person's e-mails.
func (s *Store) SetPersonLang(ctx context.Context, personID int64, lang string) error {
	if lang != "fr" && lang != "en" {
		return ErrBadInput
	}

	_, err := s.db.ExecContext(ctx, `UPDATE people SET lang = ? WHERE id = ?`, lang, personID)

	return err
}

// SetSharePhone records whether a pole manager shows their phone to the
// volunteers of their poles.
func (s *Store) SetSharePhone(ctx context.Context, personID int64, share bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE people SET share_phone = ? WHERE id = ?`, share, personID)

	return err
}

// SetPersonAvatar records a profile picture and where it came from, and
// returns the file it replaces. The caller removes that file from the files
// store once FileInUse says nothing else uses it.
func (s *Store) SetPersonAvatar(ctx context.Context, personID int64, file, source string) (string, error) {
	var old string

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT avatar_file FROM people WHERE id = ?`, personID).Scan(&old); err != nil {
			return notFound(err)
		}

		_, err := tx.ExecContext(ctx, `UPDATE people SET avatar_file = ?, avatar_source = ?, avatar_checked_at = NULL WHERE id = ?`,
			file, source, personID)

		return err
	})

	return old, err
}

// MarkGravatarChecked records that gravatar.com was just asked about the
// person, hit or miss, so the lookup does not run on every page view.
func (s *Store) MarkGravatarChecked(ctx context.Context, personID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE people SET avatar_checked_at = ? WHERE id = ?`, now(), personID)

	return err
}

// FileInUse tells whether a stored image is still referenced by a pole, an
// edition or a person. Copying a pole or an edition makes two rows share a
// file, so a removal checks before deleting it.
func (s *Store) FileInUse(ctx context.Context, file string) (bool, error) {
	if file == "" {
		return false, nil
	}

	return s.exists(ctx, `SELECT 1 FROM poles WHERE image_file = ?1
		UNION ALL SELECT 1 FROM events WHERE logo_file = ?1
		UNION ALL SELECT 1 FROM people WHERE avatar_file = ?1`, file)
}

// VolunteerImageInUse tells whether a stored file is the image of a pole or
// the logo of an edition.
func (s *Store) VolunteerImageInUse(ctx context.Context, file string) (bool, error) {
	if file == "" {
		return false, nil
	}

	return s.exists(ctx, `SELECT 1 FROM poles WHERE image_file = ?1 UNION ALL SELECT 1 FROM events WHERE logo_file = ?1`, file)
}

// Roles sums up what a person is on the site, for the staff's list.
type Roles struct {
	// Organizations are the partners the person is a contact of.
	Organizations []string
	Staff         bool
	// Poles are the poles of the edition looked at the person joined, the
	// default pole aside; Coordinator says they coordinate it.
	Poles       []string
	Volunteer   bool
	Coordinator bool
}

// PersonRoles reads the roles of a person, the volunteer ones in an
// edition (0 for none).
func (s *Store) PersonRoles(ctx context.Context, personID, eventID int64) (*Roles, error) {
	var (
		r   Roles
		err error
	)

	if r.Organizations, err = s.stringList(ctx, `SELECT o.name FROM managers m JOIN organizations o ON o.id = m.organization_id
		WHERE m.person_id = ? ORDER BY o.name COLLATE NOCASE`, personID); err != nil {
		return nil, err
	}

	if r.Staff, err = s.exists(ctx, `SELECT 1 FROM admins WHERE person_id = ?`, personID); err != nil {
		return nil, err
	}

	if r.Volunteer, err = s.exists(ctx, `SELECT 1 FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE pm.person_id = ? AND p.event_id = ?`, personID, eventID); err != nil {
		return nil, err
	}

	if r.Poles, err = s.stringList(ctx, `SELECT p.name FROM pole_members pm JOIN poles p ON p.id = pm.pole_id
		WHERE pm.person_id = ? AND p.event_id = ? AND p.is_default = 0 ORDER BY p.position, p.name`, personID, eventID); err != nil {
		return nil, err
	}

	if r.Coordinator, err = s.IsEventCoordinator(ctx, eventID, personID); err != nil {
		return nil, err
	}

	return &r, nil
}

// DeletePerson removes a person and everything attached to them: partner
// contacts, sessions, volunteer sign-ups. Staff members are not deleted
// here: their access comes from the staff login. It returns the slots the
// person was signed up on, for the waiting lists.
func (s *Store) DeletePerson(ctx context.Context, personID int64) ([]int64, error) {
	staff, err := s.exists(ctx, `SELECT 1 FROM admins WHERE person_id = ?`, personID)
	if err != nil {
		return nil, err
	}

	if staff {
		return nil, ErrBadInput
	}

	var freed []int64

	err = s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT slot_id FROM signups WHERE person_id = ?`, personID)
		if err != nil {
			return err
		}

		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()

				return err
			}

			freed = append(freed, id)
		}

		rows.Close()

		if err := forgetInLog(ctx, tx, personID); err != nil {
			return err
		}

		// The partner change log names people by address.
		var email string
		if err := tx.QueryRowContext(ctx, `SELECT email FROM people WHERE id = ?`, personID).Scan(&email); err != nil {
			return notFound(err)
		}

		if err := forgetInActivity(ctx, tx, email); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, personID)

		return err
	})

	return freed, err
}

// SearchPeople returns one page of the people matching q on the name or the
// address; an empty q matches everyone.
func (s *Store) SearchPeople(ctx context.Context, q string, limit, offset int) ([]*Person, error) {
	pattern := "%" + escapeLike(q) + "%"

	return s.queryPeople(ctx, `SELECT `+personCols+` FROM people
		WHERE name LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\'
		ORDER BY name COLLATE NOCASE, email COLLATE NOCASE LIMIT ? OFFSET ?`, pattern, pattern, limit, offset)
}

// CountPeople counts what SearchPeople matches, paging aside.
func (s *Store) CountPeople(ctx context.Context, q string) (int, error) {
	pattern := "%" + escapeLike(q) + "%"

	var n int

	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE name LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\'`,
		pattern, pattern).Scan(&n)

	return n, err
}

// LeaveVolunteering removes the volunteer side of a person: sign-ups,
// waiting lists, poles, leads and coordination, phone and calendar feed.
// The person goes too, unless they are a partner contact or staff, who
// still need it. It returns the slots the person was signed up on, so the
// waiting lists can be told, and whether the person was deleted.
func (s *Store) LeaveVolunteering(ctx context.Context, personID int64) (freed []int64, deleted bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT slot_id FROM signups WHERE person_id = ?`, personID)
		if err != nil {
			return err
		}

		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()

				return err
			}

			freed = append(freed, id)
		}

		rows.Close()

		var other bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM managers WHERE person_id = ?1)
			OR EXISTS (SELECT 1 FROM admins WHERE person_id = ?1)`, personID).Scan(&other); err != nil {
			return err
		}

		if !other {
			deleted = true

			if err := forgetInLog(ctx, tx, personID); err != nil {
				return err
			}

			_, err := tx.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, personID)

			return err
		}

		for _, q := range []string{
			`DELETE FROM signups WHERE person_id = ?`,
			`DELETE FROM waitlist WHERE person_id = ?`,
			`DELETE FROM pole_members WHERE person_id = ?`,
			`DELETE FROM pole_managers WHERE person_id = ?`,
			`DELETE FROM event_coordinators WHERE person_id = ?`,
			`DELETE FROM volunteer_reminders WHERE person_id = ?`,
			`DELETE FROM volunteer_details WHERE person_id = ?`,
			`UPDATE people SET phone = '', share_phone = 0, calendar_token = NULL WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, personID); err != nil {
				return err
			}
		}

		return nil
	})

	return freed, deleted, err
}

// --- calendar subscription ------------------------------------------------

// CalendarToken returns the secret of the person's calendar feed, creating
// it on first use. It lets an agenda application poll the planning without
// a session, so it has the entropy of a login token.
func (s *Store) CalendarToken(ctx context.Context, personID int64) (string, error) {
	var tok sql.NullString

	if err := s.db.QueryRowContext(ctx, `SELECT calendar_token FROM people WHERE id = ?`, personID).Scan(&tok); err != nil {
		return "", notFound(err)
	}

	if tok.Valid && tok.String != "" {
		return tok.String, nil
	}

	return s.ResetCalendarToken(ctx, personID)
}

// ResetCalendarToken issues a new secret, which cuts off every calendar
// subscribed with the previous one.
func (s *Store) ResetCalendarToken(ctx context.Context, personID int64) (string, error) {
	tok, _ := NewToken()

	_, err := s.db.ExecContext(ctx, `UPDATE people SET calendar_token = ? WHERE id = ?`, tok, personID)

	return tok, err
}

// PersonByCalendarToken finds the owner of a calendar secret.
func (s *Store) PersonByCalendarToken(ctx context.Context, tok string) (*Person, error) {
	if tok == "" {
		return nil, ErrNotFound
	}

	return scanPerson(s.db.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE calendar_token = ?`, tok))
}
