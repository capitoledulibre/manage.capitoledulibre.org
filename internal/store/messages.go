package store

import (
	"context"
	"time"
)

// Message is a notice for the volunteers of an edition (PoleID and SlotID
// zero), of a pole (PoleID set) or of a slot (SlotID set).
type Message struct {
	ID        int64
	UID       string
	EventID   int64
	PoleID    int64
	SlotID    int64
	Body      string
	Author    string
	CreatedAt time.Time
}

const messageSelect = `SELECT m.id, m.uid, m.event_id, COALESCE(m.pole_id, 0), COALESCE(m.slot_id, 0), m.body,
	COALESCE(NULLIF(pe.name, ''), pe.email, ''), m.created_at
	FROM messages m LEFT JOIN people pe ON pe.id = m.created_by `

func (s *Store) queryMessages(ctx context.Context, tail string, args ...any) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx, messageSelect+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Message

	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.UID, &m.EventID, &m.PoleID, &m.SlotID, &m.Body, &m.Author, &m.CreatedAt); err != nil {
			return nil, err
		}

		out = append(out, &m)
	}

	return out, rows.Err()
}

// ListEventMessages returns the notices for every volunteer of an edition,
// newest first.
func (s *Store) ListEventMessages(ctx context.Context, eventID int64) ([]*Message, error) {
	return s.queryMessages(ctx, `WHERE m.event_id = ? AND m.pole_id IS NULL AND m.slot_id IS NULL ORDER BY m.created_at DESC, m.id DESC`, eventID)
}

// ListPoleMessages returns the notices of a pole (not those of its slots).
func (s *Store) ListPoleMessages(ctx context.Context, poleID int64) ([]*Message, error) {
	return s.queryMessages(ctx, `WHERE m.pole_id = ? AND m.slot_id IS NULL ORDER BY m.created_at DESC, m.id DESC`, poleID)
}

// ListSlotMessages returns the notices of a slot.
func (s *Store) ListSlotMessages(ctx context.Context, slotID int64) ([]*Message, error) {
	return s.queryMessages(ctx, `WHERE m.slot_id = ? ORDER BY m.created_at DESC, m.id DESC`, slotID)
}

// GetMessageByUID finds a notice of an edition by its public identifier.
func (s *Store) GetMessageByUID(ctx context.Context, eventID int64, uid string) (*Message, error) {
	msgs, err := s.queryMessages(ctx, `WHERE m.event_id = ? AND m.uid = ?`, eventID, uid)
	if err != nil {
		return nil, err
	}

	if len(msgs) == 0 {
		return nil, ErrNotFound
	}

	return msgs[0], nil
}

// CreateMessage posts a notice; m.PoleID and m.SlotID may be zero. authorID
// is the person posting it.
func (s *Store) CreateMessage(ctx context.Context, m *Message, authorID int64) error {
	m.UID, m.CreatedAt = NewUUID(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO messages (uid, event_id, pole_id, slot_id, body, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, m.UID, m.EventID, nullID(m.PoleID), nullID(m.SlotID), m.Body, nullID(authorID), m.CreatedAt)
	if err != nil {
		return err
	}

	m.ID, err = res.LastInsertId()

	return err
}

func (s *Store) DeleteMessage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id)

	return err
}
