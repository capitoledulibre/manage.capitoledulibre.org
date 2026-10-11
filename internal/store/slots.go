package store

import (
	"context"
	"database/sql"
	"time"
)

// Slot is a shift of a pole, at one of its locations or none, needing a
// number of volunteers.
type Slot struct {
	ID           int64
	UID          string
	PoleID       int64
	PoleName     string
	PoleSlug     string
	EventID      int64
	EventSlug    string
	LocationID   int64 // 0 for no location
	LocationName string
	Title        string
	StartsAt     time.Time
	EndsAt       time.Time
	Capacity     int
	CreatedAt    time.Time

	// Filled counts the sign-ups; Mine tells whether the person the slots
	// were read for is one of them.
	Filled    int
	Mine      bool
	TaskCount int
	TaskDone  int
}

// URL is the volunteer page of the slot.
func (s *Slot) URL() string {
	return "/volunteers/" + s.EventSlug + "/" + s.PoleSlug + "/slots/" + s.UID
}

// ManageURL is the edit page of the slot.
func (s *Slot) ManageURL() string {
	return "/volunteers/" + s.EventSlug + "/" + s.PoleSlug + "/manage/slots/" + s.UID
}

// PoleColor is the palette index of the slot's pole, as Pole.Color.
func (s *Slot) PoleColor() int { return int(s.PoleID % 8) }

// Missing is the number of volunteers still needed.
func (s *Slot) Missing() int { return max(s.Capacity-s.Filled, 0) }

// IsFull tells whether no more volunteers are needed.
func (s *Slot) IsFull() bool { return s.Filled >= s.Capacity }

// Day is the calendar day of the slot in loc, to group slots by day.
func (s *Slot) Day(loc *time.Location) string { return s.StartsAt.In(loc).Format(time.DateOnly) }

// slotSelect reads a slot with its pole and edition. Its one placeholder is
// the person the Mine flag is computed for (0 for nobody).
const slotSelect = `SELECT s.id, s.uid, s.pole_id, p.name, p.slug, p.event_id, e.slug, COALESCE(s.location_id, 0), COALESCE(l.name, ''),
	s.title, s.starts_at, s.ends_at, s.capacity, s.created_at,
	(SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = s.id),
	EXISTS (SELECT 1 FROM signups sg WHERE sg.slot_id = s.id AND sg.person_id = ?),
	(SELECT COUNT(*) FROM slot_tasks t WHERE t.slot_id = s.id),
	(SELECT COUNT(*) FROM slot_tasks t WHERE t.slot_id = s.id AND t.done_at IS NOT NULL)
	FROM slots s JOIN poles p ON p.id = s.pole_id JOIN events e ON e.id = p.event_id
	LEFT JOIN locations l ON l.id = s.location_id `

func scanSlot(row interface{ Scan(...any) error }) (*Slot, error) {
	var s Slot

	err := row.Scan(&s.ID, &s.UID, &s.PoleID, &s.PoleName, &s.PoleSlug, &s.EventID, &s.EventSlug, &s.LocationID, &s.LocationName,
		&s.Title, &s.StartsAt, &s.EndsAt, &s.Capacity, &s.CreatedAt,
		&s.Filled, &s.Mine, &s.TaskCount, &s.TaskDone)
	if err != nil {
		return nil, notFound(err)
	}

	return &s, nil
}

// querySlots reads the slots selected by tail (joins, WHERE and ORDER BY),
// with Mine computed for personID.
func (s *Store) querySlots(ctx context.Context, personID int64, tail string, args ...any) ([]*Slot, error) {
	rows, err := s.db.QueryContext(ctx, slotSelect+tail, append([]any{personID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Slot

	for rows.Next() {
		sl, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, sl)
	}

	return out, rows.Err()
}

// ListSlots returns the slots of a pole in chronological order, with Mine
// computed for personID (0 for nobody).
func (s *Store) ListSlots(ctx context.Context, poleID, personID int64) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `WHERE s.pole_id = ? ORDER BY s.starts_at, l.position, s.id`, poleID)
}

func (s *Store) GetSlot(ctx context.Context, id, personID int64) (*Slot, error) {
	return scanSlot(s.db.QueryRowContext(ctx, slotSelect+`WHERE s.id = ?`, personID, id))
}

// GetSlotByUID finds a slot of an edition by its public identifier.
func (s *Store) GetSlotByUID(ctx context.Context, eventID int64, uid string, personID int64) (*Slot, error) {
	return scanSlot(s.db.QueryRowContext(ctx, slotSelect+`WHERE p.event_id = ? AND s.uid = ?`, personID, eventID, uid))
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}

	return id
}

// CreateSlot adds a slot to a pole.
func (s *Store) CreateSlot(ctx context.Context, sl *Slot) error {
	if sl.Capacity < 1 || !sl.EndsAt.After(sl.StartsAt) {
		return ErrBadInput
	}

	sl.UID, sl.CreatedAt = NewUUID(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO slots (uid, pole_id, location_id, title, starts_at, ends_at, capacity, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sl.UID, sl.PoleID, nullID(sl.LocationID), sl.Title, sl.StartsAt.UTC(), sl.EndsAt.UTC(), sl.Capacity, sl.CreatedAt)
	if err != nil {
		return err
	}

	sl.ID, err = res.LastInsertId()

	return err
}

// UpdateSlot saves the editable fields of a slot.
func (s *Store) UpdateSlot(ctx context.Context, sl *Slot) error {
	if sl.Capacity < 1 || !sl.EndsAt.After(sl.StartsAt) {
		return ErrBadInput
	}

	_, err := s.db.ExecContext(ctx, `UPDATE slots SET location_id = ?, title = ?, starts_at = ?, ends_at = ?, capacity = ? WHERE id = ?`,
		nullID(sl.LocationID), sl.Title, sl.StartsAt.UTC(), sl.EndsAt.UTC(), sl.Capacity, sl.ID)

	return err
}

// DeleteSlot removes a slot with its sign-ups.
func (s *Store) DeleteSlot(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM slots WHERE id = ?`, id)

	return err
}

// --- sign-ups -------------------------------------------------------------

// Signup puts the person on a slot. It returns false when the slot is full;
// signing up twice is not an error.
func (s *Store) Signup(ctx context.Context, slotID, personID int64) (bool, error) {
	ok := false

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var capacity, filled int
		var mine bool

		err := tx.QueryRowContext(ctx, `SELECT capacity, (SELECT COUNT(*) FROM signups WHERE slot_id = slots.id),
			EXISTS (SELECT 1 FROM signups WHERE slot_id = slots.id AND person_id = ?) FROM slots WHERE id = ?`, personID, slotID).
			Scan(&capacity, &filled, &mine)
		if err != nil {
			return notFound(err)
		}

		if mine {
			ok = true

			return nil
		}

		if filled >= capacity {
			return nil
		}

		ok = true

		return addSignup(ctx, tx, slotID, personID)
	})

	return ok, err
}

// ForceSignup puts the person on a slot even when it is full (a manager's
// decision).
func (s *Store) ForceSignup(ctx context.Context, slotID, personID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return addSignup(ctx, tx, slotID, personID) })
}

func addSignup(ctx context.Context, tx *sql.Tx, slotID, personID int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO signups (slot_id, person_id, created_at) VALUES (?, ?, ?)`,
		slotID, personID, now()); err != nil {
		return err
	}

	// Having the place makes waiting for it pointless.
	_, err := tx.ExecContext(ctx, `DELETE FROM waitlist WHERE slot_id = ? AND person_id = ?`, slotID, personID)

	return err
}

// Unsignup takes the person off a slot.
func (s *Store) Unsignup(ctx context.Context, slotID, personID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM signups WHERE slot_id = ? AND person_id = ?`, slotID, personID)

	return err
}

// ListSlotVolunteers returns the people signed up on a slot, first come
// first.
func (s *Store) ListSlotVolunteers(ctx context.Context, slotID int64) ([]*Person, error) {
	return s.queryPeople(ctx, `SELECT `+peCols+` FROM people pe JOIN signups sg ON sg.person_id = pe.id
		WHERE sg.slot_id = ? ORDER BY sg.created_at, pe.id`, slotID)
}

// ListPersonSlots returns the slots of an edition the person is signed up
// on, in chronological order.
func (s *Store) ListPersonSlots(ctx context.Context, eventID, personID int64) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN signups me ON me.slot_id = s.id AND me.person_id = ?
		WHERE p.event_id = ? ORDER BY s.starts_at`, personID, eventID)
}

// ListEventSlots returns every slot of an edition, for the global planning.
func (s *Store) ListEventSlots(ctx context.Context, eventID int64) ([]*Slot, error) {
	return s.querySlots(ctx, 0, `WHERE p.event_id = ? ORDER BY s.starts_at, p.position, l.position, s.id`, eventID)
}

// ListOpenSlots returns the slots of an edition starting after from that
// still need volunteers and do not have the person on them. Which poles the
// person may sign up in is for the caller to decide.
func (s *Store) ListOpenSlots(ctx context.Context, eventID, personID int64, from time.Time) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `WHERE p.event_id = ? AND s.starts_at > ?
		  AND (SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = s.id) < s.capacity
		  AND NOT EXISTS (SELECT 1 FROM signups sg WHERE sg.slot_id = s.id AND sg.person_id = ?)
		ORDER BY s.starts_at, p.position, l.position, s.id`, eventID, from.UTC(), personID)
}

// OverlappingSlots returns the slots of the person, in the edition of the
// pole, that overlap [start, end), slot excludeID aside.
func (s *Store) OverlappingSlots(ctx context.Context, personID, poleID, excludeID int64, start, end time.Time) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN signups me ON me.slot_id = s.id AND me.person_id = ?
		WHERE p.event_id = (SELECT event_id FROM poles WHERE id = ?)
		  AND s.id != ? AND s.starts_at < ? AND s.ends_at > ?
		ORDER BY s.starts_at`, personID, poleID, excludeID, end.UTC(), start.UTC())
}

// PeopleWithSlotsBetween returns the people with a slot starting in
// [from, to), any edition.
func (s *Store) PeopleWithSlotsBetween(ctx context.Context, from, to time.Time) ([]int64, error) {
	ids, err := s.idSet(ctx, `SELECT DISTINCT sg.person_id FROM signups sg JOIN slots s ON s.id = sg.slot_id
		WHERE s.starts_at >= ? AND s.starts_at < ?`, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}

	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}

	return out, nil
}

// ListPersonSlotsBetween returns the slots of the person starting in
// [from, to), any edition.
func (s *Store) ListPersonSlotsBetween(ctx context.Context, personID int64, from, to time.Time) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN signups me ON me.slot_id = s.id AND me.person_id = ?
		WHERE s.starts_at >= ? AND s.starts_at < ? ORDER BY s.starts_at`, personID, from.UTC(), to.UTC())
}

// VolunteerReminderSent tells whether the person already got the reminder
// for the slots of day (YYYY-MM-DD).
func (s *Store) VolunteerReminderSent(ctx context.Context, personID int64, day string) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM volunteer_reminders WHERE person_id = ? AND day = ?`, personID, day)
}

// MarkVolunteerReminderSent records that the reminder for day went out.
func (s *Store) MarkVolunteerReminderSent(ctx context.Context, personID int64, day string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO volunteer_reminders (person_id, day, sent_at) VALUES (?, ?, ?)`,
		personID, day, now())

	return err
}

// --- tasks ----------------------------------------------------------------

// SlotTask is an item of the checklist of a slot.
type SlotTask struct {
	ID       int64
	UID      string
	SlotID   int64
	Label    string
	Position int
	// DoneBy is the name of whoever ticked it, empty when not done.
	DoneBy string
	DoneAt *time.Time
}

// Done tells whether the task is ticked.
func (t *SlotTask) Done() bool { return t.DoneAt != nil }

const taskSelect = `SELECT t.id, t.uid, t.slot_id, t.label, t.position, COALESCE(NULLIF(pe.name, ''), pe.email, ''), t.done_at
	FROM slot_tasks t LEFT JOIN people pe ON pe.id = t.done_by `

func scanTask(row interface{ Scan(...any) error }) (*SlotTask, error) {
	var (
		t    SlotTask
		done sql.NullTime
	)

	if err := row.Scan(&t.ID, &t.UID, &t.SlotID, &t.Label, &t.Position, &t.DoneBy, &done); err != nil {
		return nil, notFound(err)
	}

	t.DoneAt = timePtr(done)

	return &t, nil
}

// ListTasks returns the checklist of a slot.
func (s *Store) ListTasks(ctx context.Context, slotID int64) ([]*SlotTask, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+`WHERE t.slot_id = ? ORDER BY t.position, t.id`, slotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SlotTask

	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, t)
	}

	return out, rows.Err()
}

// GetTaskByUID finds a task by its public identifier.
func (s *Store) GetTaskByUID(ctx context.Context, uid string) (*SlotTask, error) {
	return scanTask(s.db.QueryRowContext(ctx, taskSelect+`WHERE t.uid = ?`, uid))
}

// CreateTask appends a task to the checklist of a slot.
func (s *Store) CreateTask(ctx context.Context, slotID int64, label string) (*SlotTask, error) {
	t := &SlotTask{UID: NewUUID(), SlotID: slotID, Label: label}

	err := s.db.QueryRowContext(ctx, `INSERT INTO slot_tasks (uid, slot_id, label, position)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM slot_tasks WHERE slot_id = ?))
		RETURNING id, position`, t.UID, slotID, label, slotID).Scan(&t.ID, &t.Position)
	if err != nil {
		return nil, err
	}

	return t, nil
}

func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM slot_tasks WHERE id = ?`, id)

	return err
}

// ToggleTask ticks a task for the person, or unticks it.
func (s *Store) ToggleTask(ctx context.Context, id, personID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE slot_tasks SET
		done_by = CASE WHEN done_at IS NULL THEN ? ELSE NULL END,
		done_at = CASE WHEN done_at IS NULL THEN ? ELSE NULL END
		WHERE id = ?`, personID, now(), id)

	return err
}

// CopyTasks copies the labels of the checklist of a slot onto another.
func (s *Store) CopyTasks(ctx context.Context, fromSlotID, toSlotID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT label, position FROM slot_tasks WHERE slot_id = ? ORDER BY position, id`, fromSlotID)
		if err != nil {
			return err
		}

		var tasks []SlotTask

		for rows.Next() {
			var t SlotTask
			if err := rows.Scan(&t.Label, &t.Position); err != nil {
				rows.Close()

				return err
			}

			tasks = append(tasks, t)
		}

		rows.Close()

		for _, t := range tasks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO slot_tasks (uid, slot_id, label, position) VALUES (?, ?, ?, ?)`,
				NewUUID(), toSlotID, t.Label, t.Position); err != nil {
				return err
			}
		}

		return nil
	})
}

// --- waiting list ---------------------------------------------------------

// JoinWaitlist records that the person wants the slot if a place frees up.
func (s *Store) JoinWaitlist(ctx context.Context, slotID, personID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO waitlist (slot_id, person_id, created_at) VALUES (?, ?, ?)`,
		slotID, personID, now())

	return err
}

func (s *Store) LeaveWaitlist(ctx context.Context, slotID, personID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM waitlist WHERE slot_id = ? AND person_id = ?`, slotID, personID)

	return err
}

// OnWaitlist tells whether the person waits for the slot.
func (s *Store) OnWaitlist(ctx context.Context, slotID, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM waitlist WHERE slot_id = ? AND person_id = ?`, slotID, personID)
}

// ListWaitlist returns the people waiting for a slot, first come first.
func (s *Store) ListWaitlist(ctx context.Context, slotID int64) ([]*Person, error) {
	return s.queryPeople(ctx, `SELECT `+peCols+` FROM people pe JOIN waitlist w ON w.person_id = pe.id
		WHERE w.slot_id = ? ORDER BY w.created_at, pe.id`, slotID)
}

// ListPersonWaitlist returns the slots of an edition the person waits for.
func (s *Store) ListPersonWaitlist(ctx context.Context, eventID, personID int64) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN waitlist w ON w.slot_id = s.id AND w.person_id = ?
		WHERE p.event_id = ? ORDER BY s.starts_at`, personID, eventID)
}

// WaitlistCounts maps the slots of a pole to the number of people waiting.
func (s *Store) WaitlistCounts(ctx context.Context, poleID int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.slot_id, COUNT(*) FROM waitlist w
		JOIN slots s ON s.id = w.slot_id WHERE s.pole_id = ? GROUP BY w.slot_id`, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]int{}

	for rows.Next() {
		var (
			id int64
			n  int
		)

		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}

		out[id] = n
	}

	return out, rows.Err()
}

// PersonWaitlistInPole returns the set of slots of a pole the person waits
// for, so a page renders its cards without a query per card.
func (s *Store) PersonWaitlistInPole(ctx context.Context, poleID, personID int64) (map[int64]bool, error) {
	return s.idSet(ctx, `SELECT w.slot_id FROM waitlist w JOIN slots s ON s.id = w.slot_id
		WHERE s.pole_id = ? AND w.person_id = ?`, poleID, personID)
}

// --- roster -----------------------------------------------------------------

// ListEventSignups returns the people signed up on the slots of an edition,
// or of one of its poles when poleID is not 0, by slot, first come first.
// It reads them in one query, for the printed rosters.
func (s *Store) ListEventSignups(ctx context.Context, eventID, poleID int64) (map[int64][]*Person, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+peCols+`, sg.slot_id FROM signups sg
		JOIN people pe ON pe.id = sg.person_id JOIN slots s ON s.id = sg.slot_id JOIN poles p ON p.id = s.pole_id
		WHERE p.event_id = ? AND (? = 0 OR s.pole_id = ?) ORDER BY sg.created_at, pe.id`, eventID, poleID, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]*Person{}

	for rows.Next() {
		var slotID int64

		p, err := scanPerson(rows, &slotID)
		if err != nil {
			return nil, err
		}

		out[slotID] = append(out[slotID], p)
	}

	return out, rows.Err()
}

// ListEventTasks returns the checklists of the slots of an edition, or of
// one of its poles when poleID is not 0, by slot.
func (s *Store) ListEventTasks(ctx context.Context, eventID, poleID int64) (map[int64][]*SlotTask, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+`JOIN slots s ON s.id = t.slot_id JOIN poles p ON p.id = s.pole_id
		WHERE p.event_id = ? AND (? = 0 OR s.pole_id = ?) ORDER BY t.position, t.id`, eventID, poleID, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]*SlotTask{}

	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}

		out[t.SlotID] = append(out[t.SlotID], t)
	}

	return out, rows.Err()
}
