package db

// --- event coordinators -------------------------------------------------

// IsEventCoordinator tells whether the user coordinates the event.
func (d *DB) IsEventCoordinator(eventID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM event_coordinators WHERE event_id = ? AND user_id = ?`, eventID, userID).Scan(&n)
	return n > 0
}

// ListEventCoordinators returns the coordinators of an event.
func (d *DB) ListEventCoordinators(eventID int64) ([]User, error) {
	return d.queryUsers(`SELECT `+userCols+` FROM users u WHERE u.id IN (SELECT user_id FROM event_coordinators WHERE event_id = ?) ORDER BY u.name COLLATE NOCASE, u.email`, eventID)
}

// AddEventCoordinator makes a user coordinator of an event. Coordinators are
// volunteers of the event too, so they also join its default pole.
func (d *DB) AddEventCoordinator(eventID, userID int64) error {
	if _, err := d.Exec(`INSERT OR IGNORE INTO event_coordinators (event_id, user_id) VALUES (?, ?)`, eventID, userID); err != nil {
		return err
	}
	def, err := d.DefaultPole(eventID)
	if err != nil {
		return err
	}
	return d.JoinPole(def.ID, userID)
}

// RemoveEventCoordinator takes a user off the coordination; they stay a
// volunteer.
func (d *DB) RemoveEventCoordinator(eventID, userID int64) error {
	_, err := d.Exec(`DELETE FROM event_coordinators WHERE event_id = ? AND user_id = ?`, eventID, userID)
	return err
}

// IsPoleCoordinator tells whether the user coordinates the event a pole
// belongs to.
func (d *DB) IsPoleCoordinator(poleID, userID int64) bool {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM event_coordinators ec JOIN poles p ON p.event_id = ec.event_id WHERE p.id = ? AND ec.user_id = ?`, poleID, userID).Scan(&n)
	return n > 0
}
