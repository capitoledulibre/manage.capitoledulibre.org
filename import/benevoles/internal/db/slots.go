package db

import (
	"database/sql"
	"time"
)

// Slot is a time range at a location needing a number of volunteers.
type Slot struct {
	ID           int64
	PoleID       int64
	LocationID   int64 // 0 = whole pole
	LocationName string
	Title        string
	StartsAt     time.Time
	EndsAt       time.Time
	Capacity     int
	Filled       int  // number of signups
	Mine         bool // current user is signed up (filled by ListSlots)
	TaskCount    int
	TaskDone     int
	CreatedAt    time.Time
}

// Missing returns how many volunteers are still needed.
func (s Slot) Missing() int {
	if m := s.Capacity - s.Filled; m > 0 {
		return m
	}
	return 0
}

// IsFull tells whether no more volunteers are needed.
func (s Slot) IsFull() bool { return s.Filled >= s.Capacity }

// Day returns the local calendar day key, used for grouping.
func (s Slot) Day(loc *time.Location) string { return s.StartsAt.In(loc).Format("2006-01-02") }

const slotCols = `s.id, s.pole_id, COALESCE(s.location_id, 0), COALESCE(l.name, ''), s.title, s.starts_at, s.ends_at, s.capacity,
	(SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = s.id),
	EXISTS(SELECT 1 FROM signups sg WHERE sg.slot_id = s.id AND sg.user_id = ?),
	(SELECT COUNT(*) FROM slot_tasks t WHERE t.slot_id = s.id),
	(SELECT COUNT(*) FROM slot_tasks t WHERE t.slot_id = s.id AND t.done_at IS NOT NULL), s.created_at`

func scanSlot(row interface{ Scan(...any) error }) (Slot, error) {
	var s Slot
	var starts, ends, created string
	if err := row.Scan(&s.ID, &s.PoleID, &s.LocationID, &s.LocationName, &s.Title, &starts, &ends, &s.Capacity, &s.Filled, &s.Mine, &s.TaskCount, &s.TaskDone, &created); err != nil {
		return s, err
	}
	s.StartsAt = ParseTime(starts)
	s.EndsAt = ParseTime(ends)
	s.CreatedAt = ParseTime(created)
	return s, nil
}

func (d *DB) querySlots(q string, args ...any) ([]Slot, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Slot
	for rows.Next() {
		s, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListSlots returns the slots of a pole in chronological order. userID is used
// to fill the Mine flag and may be 0.
func (d *DB) ListSlots(poleID, userID int64) ([]Slot, error) {
	return d.querySlots(`SELECT `+slotCols+` FROM slots s LEFT JOIN locations l ON l.id = s.location_id
		WHERE s.pole_id = ? ORDER BY s.starts_at, l.position, s.id`, userID, poleID)
}

// GetSlot returns one slot.
func (d *DB) GetSlot(id, userID int64) (Slot, error) {
	return scanSlot(d.QueryRow(`SELECT `+slotCols+` FROM slots s LEFT JOIN locations l ON l.id = s.location_id WHERE s.id = ?`, userID, id))
}

// CreateSlot inserts a slot.
func (d *DB) CreateSlot(s Slot) (int64, error) {
	res, err := d.Exec(`INSERT INTO slots (pole_id, location_id, title, starts_at, ends_at, capacity, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.PoleID, OptInt(s.LocationID), s.Title, FormatTime(s.StartsAt), FormatTime(s.EndsAt), s.Capacity, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSlot updates a slot's fields.
func (d *DB) UpdateSlot(s Slot) error {
	_, err := d.Exec(`UPDATE slots SET location_id = ?, title = ?, starts_at = ?, ends_at = ?, capacity = ? WHERE id = ?`,
		OptInt(s.LocationID), s.Title, FormatTime(s.StartsAt), FormatTime(s.EndsAt), s.Capacity, s.ID)
	return err
}

// DeleteSlot removes a slot and its signups.
func (d *DB) DeleteSlot(id int64) error {
	_, err := d.Exec(`DELETE FROM slots WHERE id = ?`, id)
	return err
}

// --- signups ------------------------------------------------------------

// Signup adds the user to a slot. Returns false when the slot is already full.
func (d *DB) Signup(slotID, userID int64) (bool, error) {
	tx, err := d.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var capacity, filled int
	if err := tx.QueryRow(`SELECT capacity, (SELECT COUNT(*) FROM signups WHERE slot_id = ?) FROM slots WHERE id = ?`, slotID, slotID).Scan(&capacity, &filled); err != nil {
		return false, err
	}
	var already int
	tx.QueryRow(`SELECT COUNT(*) FROM signups WHERE slot_id = ? AND user_id = ?`, slotID, userID).Scan(&already)
	if already > 0 {
		return true, tx.Commit()
	}
	if filled >= capacity {
		return false, nil
	}
	if _, err := tx.Exec(`INSERT INTO signups (slot_id, user_id, created_at) VALUES (?, ?, ?)`, slotID, userID, Now()); err != nil {
		return false, err
	}
	// Having the place makes waiting for it pointless.
	if _, err := tx.Exec(`DELETE FROM waitlist WHERE slot_id = ? AND user_id = ?`, slotID, userID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Unsignup removes the user from a slot.
func (d *DB) Unsignup(slotID, userID int64) error {
	_, err := d.Exec(`DELETE FROM signups WHERE slot_id = ? AND user_id = ?`, slotID, userID)
	return err
}

// ListSlotVolunteers returns the users signed up on a slot.
func (d *DB) ListSlotVolunteers(slotID int64) ([]User, error) {
	return d.queryUsers(`SELECT `+userCols+` FROM users u JOIN signups sg ON sg.user_id = u.id WHERE sg.slot_id = ? ORDER BY sg.created_at`, slotID)
}

// ListUserWaitlist returns the slots of an event the user waits a place on,
// in chronological order.
func (d *DB) ListUserWaitlist(eventID, userID int64) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		JOIN waitlist w ON w.slot_id = s.id AND w.user_id = ?
		WHERE p.event_id = ? ORDER BY s.starts_at`, userID, userID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

// MySlot is a slot enriched with its pole, for the personal planning views.
type MySlot struct {
	Slot
	PoleName string
	EventID  int64
}

// ListUserSlots returns every slot the user is signed up on within an event.
func (d *DB) ListUserSlots(eventID, userID int64) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		JOIN signups sg ON sg.slot_id = s.id AND sg.user_id = ?
		WHERE p.event_id = ? ORDER BY s.starts_at`, userID, userID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

func scanMySlots(rows *sql.Rows) ([]MySlot, error) {
	var out []MySlot
	for rows.Next() {
		var m MySlot
		var starts, ends, created string
		if err := rows.Scan(&m.ID, &m.PoleID, &m.LocationID, &m.LocationName, &m.Title, &starts, &ends, &m.Capacity, &m.Filled, &m.Mine, &m.TaskCount, &m.TaskDone, &created, &m.PoleName, &m.EventID); err != nil {
			return nil, err
		}
		m.StartsAt = ParseTime(starts)
		m.EndsAt = ParseTime(ends)
		m.CreatedAt = ParseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListEventSlots returns every slot of an event with its pole name, in
// chronological order, for the global planning view.
func (d *DB) ListEventSlots(eventID int64) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		WHERE p.event_id = ? ORDER BY s.starts_at, p.position, l.position, s.id`, 0, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

// ListOpenSlots returns the slots of an event that still need volunteers,
// start after from and do not already have the user on them, in
// chronological order. Which poles the user may sign up in is for the caller
// to decide.
func (d *DB) ListOpenSlots(eventID, userID int64, from time.Time) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		WHERE p.event_id = ? AND s.starts_at > ?
		  AND (SELECT COUNT(*) FROM signups sg WHERE sg.slot_id = s.id) < s.capacity
		  AND NOT EXISTS (SELECT 1 FROM signups sg WHERE sg.slot_id = s.id AND sg.user_id = ?)
		ORDER BY s.starts_at, p.position, l.position, s.id`, userID, eventID, FormatTime(from), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

// OverlappingSlots returns the user's slots (in the same event as poleID)
// that overlap the given range, excluding excludeSlot.
func (d *DB) OverlappingSlots(userID, poleID, excludeSlot int64, start, end time.Time) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		JOIN signups sg ON sg.slot_id = s.id AND sg.user_id = ?
		WHERE p.event_id = (SELECT event_id FROM poles WHERE id = ?)
		  AND s.id != ? AND s.starts_at < ? AND s.ends_at > ?
		ORDER BY s.starts_at`, userID, userID, poleID, excludeSlot, FormatTime(end), FormatTime(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

// UsersWithSlotsBetween returns the ids of users having a slot starting in [from, to).
func (d *DB) UsersWithSlotsBetween(from, to time.Time) ([]int64, error) {
	rows, err := d.Query(`SELECT DISTINCT sg.user_id FROM signups sg JOIN slots s ON s.id = sg.slot_id
		WHERE s.starts_at >= ? AND s.starts_at < ?`, FormatTime(from), FormatTime(to))
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

// ListUserSlotsBetween returns the user's slots starting in [from, to), any event.
func (d *DB) ListUserSlotsBetween(userID int64, from, to time.Time) ([]MySlot, error) {
	rows, err := d.Query(`SELECT `+slotCols+`, p.name, p.event_id
		FROM slots s JOIN poles p ON p.id = s.pole_id LEFT JOIN locations l ON l.id = s.location_id
		JOIN signups sg ON sg.slot_id = s.id AND sg.user_id = ?
		WHERE s.starts_at >= ? AND s.starts_at < ? ORDER BY s.starts_at`, userID, userID, FormatTime(from), FormatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMySlots(rows)
}

// ReminderSent records / checks that a reminder went out.
func (d *DB) ReminderSent(userID int64, day string) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM reminders_sent WHERE user_id = ? AND day = ?`, userID, day).Scan(&n)
	return n > 0
}

// MarkReminderSent records that a reminder went out.
func (d *DB) MarkReminderSent(userID int64, day string) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO reminders_sent (user_id, day, sent_at) VALUES (?, ?, ?)`, userID, day, Now())
	return err
}

// --- tasks --------------------------------------------------------------

// Task is one item of a slot's checklist.
type Task struct {
	ID       int64
	SlotID   int64
	Label    string
	Position int
	DoneBy   string // display name of who ticked it, empty if not done
	DoneAt   *time.Time
}

// Done tells whether the task has been ticked.
func (t Task) Done() bool { return t.DoneAt != nil }

// ListTasks returns the checklist of a slot.
func (d *DB) ListTasks(slotID int64) ([]Task, error) {
	rows, err := d.Query(`SELECT t.id, t.slot_id, t.label, t.position, COALESCE(NULLIF(u.name, ''), u.email, ''), t.done_at
		FROM slot_tasks t LEFT JOIN users u ON u.id = t.done_by WHERE t.slot_id = ? ORDER BY t.position, t.id`, slotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var done sql.NullString
		if err := rows.Scan(&t.ID, &t.SlotID, &t.Label, &t.Position, &t.DoneBy, &done); err != nil {
			return nil, err
		}
		t.DoneAt = NullTime(done)
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask returns one task.
func (d *DB) GetTask(id int64) (Task, error) {
	var t Task
	var done sql.NullString
	err := d.QueryRow(`SELECT t.id, t.slot_id, t.label, t.position, COALESCE(NULLIF(u.name, ''), u.email, ''), t.done_at
		FROM slot_tasks t LEFT JOIN users u ON u.id = t.done_by WHERE t.id = ?`, id).Scan(&t.ID, &t.SlotID, &t.Label, &t.Position, &t.DoneBy, &done)
	t.DoneAt = NullTime(done)
	return t, err
}

// CreateTask appends a task to a slot's checklist.
func (d *DB) CreateTask(slotID int64, label string) (int64, error) {
	res, err := d.Exec(`INSERT INTO slot_tasks (slot_id, label, position) VALUES (?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM slot_tasks WHERE slot_id = ?))`, slotID, label, slotID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteTask removes a task.
func (d *DB) DeleteTask(id int64) error {
	_, err := d.Exec(`DELETE FROM slot_tasks WHERE id = ?`, id)
	return err
}

// ToggleTask ticks or unticks a task.
func (d *DB) ToggleTask(id, userID int64) error {
	_, err := d.Exec(`UPDATE slot_tasks SET
		done_by = CASE WHEN done_at IS NULL THEN ? ELSE NULL END,
		done_at = CASE WHEN done_at IS NULL THEN ? ELSE NULL END
		WHERE id = ?`, userID, Now(), id)
	return err
}

// CopyTasks duplicates the checklist of one slot onto another (labels only).
func (d *DB) CopyTasks(fromSlot, toSlot int64) error {
	_, err := d.Exec(`INSERT INTO slot_tasks (slot_id, label, position) SELECT ?, label, position FROM slot_tasks WHERE slot_id = ?`, toSlot, fromSlot)
	return err
}

// ForceSignup adds the user to a slot regardless of capacity (manager action).
func (d *DB) ForceSignup(slotID, userID int64) error {
	if _, err := d.Exec(`INSERT OR IGNORE INTO signups (slot_id, user_id, created_at) VALUES (?, ?, ?)`, slotID, userID, Now()); err != nil {
		return err
	}
	_, err := d.Exec(`DELETE FROM waitlist WHERE slot_id = ? AND user_id = ?`, slotID, userID)
	return err
}

// --- waiting list --------------------------------------------------------

// JoinWaitlist records that the user wants the slot if a place frees up.
func (d *DB) JoinWaitlist(slotID, userID int64) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO waitlist (slot_id, user_id, created_at) VALUES (?, ?, ?)`,
		slotID, userID, Now())
	return err
}

// LeaveWaitlist removes the user from a slot's waiting list.
func (d *DB) LeaveWaitlist(slotID, userID int64) error {
	_, err := d.Exec(`DELETE FROM waitlist WHERE slot_id = ? AND user_id = ?`, slotID, userID)
	return err
}

// OnWaitlist tells whether the user is waiting for this slot.
func (d *DB) OnWaitlist(slotID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM waitlist WHERE slot_id = ? AND user_id = ?`, slotID, userID).Scan(&n)
	return n > 0
}

// ListWaitlist returns the users waiting for a slot, oldest request first.
func (d *DB) ListWaitlist(slotID int64) ([]User, error) {
	return d.queryUsers(`SELECT `+userCols+` FROM waitlist w JOIN users u ON u.id = w.user_id
		WHERE w.slot_id = ? ORDER BY w.created_at`, slotID)
}

// WaitlistCounts maps slot ids to the number of people waiting, for the slots
// of one pole. Managers see it next to each slot.
func (d *DB) WaitlistCounts(poleID int64) (map[int64]int, error) {
	rows, err := d.Query(`SELECT w.slot_id, COUNT(*) FROM waitlist w
		JOIN slots s ON s.id = w.slot_id WHERE s.pole_id = ? GROUP BY w.slot_id`, poleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// UserWaitlistInPole maps slot ids to true for the slots of a pole the user is
// waiting on, so a page can render many cards without one query per card.
func (d *DB) UserWaitlistInPole(poleID, userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT w.slot_id FROM waitlist w JOIN slots s ON s.id = w.slot_id
		WHERE s.pole_id = ? AND w.user_id = ?`, poleID, userID)
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
