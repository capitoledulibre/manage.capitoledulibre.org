package db

// --- event staff --------------------------------------------------------

// IsEventStaff tells whether the user is on the staff of the event.
func (d *DB) IsEventStaff(eventID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM event_staff WHERE event_id = ? AND user_id = ?`, eventID, userID).Scan(&n)
	return n > 0
}

// ListEventStaff returns the staff of an event.
func (d *DB) ListEventStaff(eventID int64) ([]User, error) {
	return d.queryUsers(`SELECT `+userCols+` FROM users u WHERE u.id IN (SELECT user_id FROM event_staff WHERE event_id = ?) ORDER BY u.name COLLATE NOCASE, u.email`, eventID)
}

// AddEventStaff puts a user on the staff of an event. Staff are volunteers of
// the event too, so they also join its default pole.
func (d *DB) AddEventStaff(eventID, userID int64) error {
	if _, err := d.Exec(`INSERT OR IGNORE INTO event_staff (event_id, user_id) VALUES (?, ?)`, eventID, userID); err != nil {
		return err
	}
	def, err := d.DefaultPole(eventID)
	if err != nil {
		return err
	}
	return d.JoinPole(def.ID, userID)
}

// RemoveEventStaff takes a user off the staff; they stay a volunteer.
func (d *DB) RemoveEventStaff(eventID, userID int64) error {
	_, err := d.Exec(`DELETE FROM event_staff WHERE event_id = ? AND user_id = ?`, eventID, userID)
	return err
}

// StaffEventIDs returns the ids of the events the user is on the staff of.
func (d *DB) StaffEventIDs(userID int64) (map[int64]bool, error) {
	rows, err := d.Query(`SELECT event_id FROM event_staff WHERE user_id = ?`, userID)
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

// IsPoleStaff tells whether the user is on the staff of the event a pole
// belongs to.
func (d *DB) IsPoleStaff(poleID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM event_staff es JOIN poles p ON p.event_id = es.event_id WHERE p.id = ? AND es.user_id = ?`, poleID, userID).Scan(&n)
	return n > 0
}
