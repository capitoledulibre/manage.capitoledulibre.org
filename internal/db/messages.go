package db

import (
	"database/sql"
	"time"
)

// Message is a notice displayed on a page. Scope is edition, pole or slot.
type Message struct {
	ID        int64
	EditionID int64
	PoleID    int64
	SlotID    int64
	Body      string
	Author    string
	CreatedAt time.Time
}

const messageCols = `m.id, m.edition_id, COALESCE(m.pole_id, 0), COALESCE(m.slot_id, 0), m.body, COALESCE(NULLIF(u.name, ''), u.email, ''), m.created_at`

func (d *DB) queryMessages(q string, args ...any) ([]Message, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.EditionID, &m.PoleID, &m.SlotID, &m.Body, &m.Author, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = ParseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListEditionMessages returns organizer-wide messages (no pole, no slot).
func (d *DB) ListEditionMessages(editionID int64) ([]Message, error) {
	return d.queryMessages(`SELECT `+messageCols+` FROM messages m LEFT JOIN users u ON u.id = m.created_by
		WHERE m.edition_id = ? AND m.pole_id IS NULL AND m.slot_id IS NULL ORDER BY m.created_at DESC`, editionID)
}

// ListPoleMessages returns messages attached to a pole (not to a slot).
func (d *DB) ListPoleMessages(poleID int64) ([]Message, error) {
	return d.queryMessages(`SELECT `+messageCols+` FROM messages m LEFT JOIN users u ON u.id = m.created_by
		WHERE m.pole_id = ? AND m.slot_id IS NULL ORDER BY m.created_at DESC`, poleID)
}

// ListSlotMessages returns messages attached to a slot.
func (d *DB) ListSlotMessages(slotID int64) ([]Message, error) {
	return d.queryMessages(`SELECT `+messageCols+` FROM messages m LEFT JOIN users u ON u.id = m.created_by
		WHERE m.slot_id = ? ORDER BY m.created_at DESC`, slotID)
}

// GetMessage returns one message.
func (d *DB) GetMessage(id int64) (Message, error) {
	msgs, err := d.queryMessages(`SELECT `+messageCols+` FROM messages m LEFT JOIN users u ON u.id = m.created_by WHERE m.id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	if len(msgs) == 0 {
		return Message{}, sql.ErrNoRows
	}
	return msgs[0], nil
}

// CreateMessage posts a message. poleID / slotID may be 0.
func (d *DB) CreateMessage(editionID, poleID, slotID, authorID int64, body string) (int64, error) {
	res, err := d.Exec(`INSERT INTO messages (edition_id, pole_id, slot_id, body, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		editionID, OptInt(poleID), OptInt(slotID), body, authorID, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteMessage removes a message.
func (d *DB) DeleteMessage(id int64) error {
	_, err := d.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}
