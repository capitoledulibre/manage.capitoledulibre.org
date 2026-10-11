package store

import (
	"context"
	"database/sql"
	"time"
)

// Volunteer log actions.
const (
	LogSignup         = "signup"          // the subject signed up
	LogUnsignup       = "unsignup"        // the subject withdrew
	LogWaitlist       = "waitlist"        // the subject waits for a place
	LogWaitlistLeft   = "waitlist_left"   // the subject stopped waiting
	LogJoined         = "joined"          // the subject joined the pole
	LogLeft           = "left"            // the subject left the pole, Count slots cancelled
	LogSignupAdded    = "signup_added"    // the actor put the subject on a slot
	LogSignupRemoved  = "signup_removed"  // the actor took the subject off a slot
	LogMemberAdded    = "member_added"    // the actor added the subject to the pole
	LogMemberRemoved  = "member_removed"  // the actor removed the subject, Count slots cancelled
	LogLeadAdded      = "lead_added"      // the actor made the subject a lead
	LogLeadRemoved    = "lead_removed"    // the actor took the lead role away
	LogSlotsCreated   = "slots_created"   // the actor created Count slots
	LogSlotMoved      = "slot_moved"      // the actor moved a slot (the new time)
	LogSlotUpdated    = "slot_updated"    // the actor changed a slot otherwise
	LogSlotDeleted    = "slot_deleted"    // the actor deleted a slot, Count people on it
	LogPoleCreated    = "pole_created"    // the actor created the pole
	LogPoleDeleted    = "pole_deleted"    // the actor deleted the pole
	LogCoordinatorIn  = "coordinator_in"  // the actor added the subject to the coordination
	LogCoordinatorOut = "coordinator_out" // the actor removed the subject from it
	LogTaskDone       = "task_done"       // the actor ticked a task (Title) of a slot
	LogTaskUndone     = "task_undone"     // the actor unticked it
	LogStatus         = "status"          // the actor set the volunteer status, Title holds it
	LogRegistered     = "registered"      // the actor registered the subject
	LogMessagePosted  = "message_posted"  // the actor posted a message, Count people e-mailed
)

// LogEntry is a line of the volunteer log.
type LogEntry struct {
	ID          int64
	EventID     int64
	PoleID      int64
	ActorID     int64
	ActorName   string
	SubjectID   int64
	SubjectName string
	Action      string
	PoleName    string
	SlotStarts  *time.Time
	SlotEnds    *time.Time
	Place       string
	Title       string
	Count       int
	CreatedAt   time.Time

	// EventName is filled by ListPersonLog, which spans editions.
	EventName string
}

// SetSlot copies what names a slot into the entry.
func (e *LogEntry) SetSlot(sl *Slot) {
	starts, ends := sl.StartsAt, sl.EndsAt
	e.SlotStarts, e.SlotEnds, e.Place, e.Title = &starts, &ends, sl.LocationName, sl.Title
}

// SetPole copies what names a pole into the entry.
func (e *LogEntry) SetPole(p *Pole) { e.PoleID, e.PoleName, e.EventID = p.ID, p.Name, p.EventID }

// SetActor and SetSubject copy who into the entry.
func (e *LogEntry) SetActor(p *Person) {
	if p != nil {
		e.ActorID, e.ActorName = p.ID, p.Label()
	}
}

func (e *LogEntry) SetSubject(p *Person) {
	if p != nil {
		e.SubjectID, e.SubjectName = p.ID, p.Label()
	}
}

// Log records an entry of the volunteer log.
func (s *Store) Log(ctx context.Context, e *LogEntry) error {
	e.CreatedAt = now()

	_, err := s.db.ExecContext(ctx, `INSERT INTO volunteer_log (event_id, pole_id, actor_id, actor_name, subject_id, subject_name,
		action, pole_name, slot_starts, slot_ends, place, title, count, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.EventID, nullID(e.PoleID), nullID(e.ActorID), e.ActorName, nullID(e.SubjectID), e.SubjectName,
		e.Action, e.PoleName, nullTime(e.SlotStarts), nullTime(e.SlotEnds), e.Place, e.Title, e.Count, e.CreatedAt)

	return err
}

const logCols = `l.id, l.event_id, COALESCE(l.pole_id, 0), COALESCE(l.actor_id, 0), l.actor_name, COALESCE(l.subject_id, 0), l.subject_name,
	l.action, l.pole_name, l.slot_starts, l.slot_ends, l.place, l.title, l.count, l.created_at, e.name`

func (s *Store) queryLog(ctx context.Context, where string, args ...any) ([]*LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+logCols+` FROM volunteer_log l JOIN events e ON e.id = l.event_id
		WHERE `+where+` ORDER BY l.created_at DESC, l.id DESC LIMIT 300`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*LogEntry

	for rows.Next() {
		var (
			e          LogEntry
			start, end sql.NullTime
		)

		if err := rows.Scan(&e.ID, &e.EventID, &e.PoleID, &e.ActorID, &e.ActorName, &e.SubjectID, &e.SubjectName,
			&e.Action, &e.PoleName, &start, &end, &e.Place, &e.Title, &e.Count, &e.CreatedAt, &e.EventName); err != nil {
			return nil, err
		}

		e.SlotStarts, e.SlotEnds = timePtr(start), timePtr(end)
		out = append(out, &e)
	}

	return out, rows.Err()
}

// ListPoleLog returns the latest entries of a pole.
func (s *Store) ListPoleLog(ctx context.Context, poleID int64) ([]*LogEntry, error) {
	return s.queryLog(ctx, `l.pole_id = ?`, poleID)
}

// ListEventLog returns the latest entries of an edition.
func (s *Store) ListEventLog(ctx context.Context, eventID int64) ([]*LogEntry, error) {
	return s.queryLog(ctx, `l.event_id = ?`, eventID)
}

// ListPersonLog returns the latest entries about a person or by them, all
// editions.
func (s *Store) ListPersonLog(ctx context.Context, personID int64) ([]*LogEntry, error) {
	return s.queryLog(ctx, `l.subject_id = ?1 OR l.actor_id = ?1`, personID)
}

// forgetInLog wipes a person from the log, before they are deleted: their
// account goes, and so does what named them.
func forgetInLog(ctx context.Context, q querier, personID int64) error {
	if _, err := q.ExecContext(ctx, `UPDATE volunteer_log SET subject_name = '' WHERE subject_id = ?`, personID); err != nil {
		return err
	}

	_, err := q.ExecContext(ctx, `UPDATE volunteer_log SET actor_name = '' WHERE actor_id = ?`, personID)

	return err
}
