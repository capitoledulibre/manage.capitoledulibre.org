package db

import (
	"database/sql"
	"time"
)

// Event statuses.
const (
	StatusDraft    = "draft"
	StatusOpen     = "open"
	StatusFrozen   = "frozen"
	StatusArchived = "archived"
)

// Event is one occurrence of the event (typically one year).
type Event struct {
	ID        int64
	Slug      string
	Name      string
	Status    string
	StartsOn  string // YYYY-MM-DD
	EndsOn    string // YYYY-MM-DD
	FreezeAt  *time.Time
	CreatedAt time.Time
	Image     string // optional logo, file name inside the upload directory
}

// LogoURL is the edition logo to display, or "" when there is none.
func (e Event) LogoURL() string {
	if e.Image == "" {
		return ""
	}
	return "/uploads/" + e.Image
}

// URL is the public page of the event.
func (e Event) URL() string { return "/" + e.Slug }

// AdminURL is the administration page of the event.
func (e Event) AdminURL() string { return "/admin/events/" + e.Slug }

// IsVisibleToVolunteers tells whether volunteers may browse this event.
func (e Event) IsVisibleToVolunteers() bool {
	return e.Status == StatusOpen || e.Status == StatusFrozen
}

// SignupsLocked tells whether volunteers can no longer change their signups.
// poleFreeze, when set, overrides the event freeze date.
func (e Event) SignupsLocked(poleFreeze *time.Time, now time.Time) bool {
	if e.Status != StatusOpen {
		return true
	}
	freeze := e.FreezeAt
	if poleFreeze != nil {
		freeze = poleFreeze
	}
	return freeze != nil && !now.Before(*freeze)
}

const eventCols = `id, slug, name, status, starts_on, ends_on, freeze_at, created_at, image`

func scanEvent(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var freeze sql.NullString
	var created string
	if err := row.Scan(&e.ID, &e.Slug, &e.Name, &e.Status, &e.StartsOn, &e.EndsOn, &freeze, &created, &e.Image); err != nil {
		return e, err
	}
	e.FreezeAt = NullTime(freeze)
	e.CreatedAt = ParseTime(created)
	return e, nil
}

// ListEvents returns all events, most recent first.
func (d *DB) ListEvents() ([]Event, error) {
	rows, err := d.Query(`SELECT ` + eventCols + ` FROM events ORDER BY starts_on DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEvent returns one event.
func (d *DB) GetEvent(id int64) (Event, error) {
	return scanEvent(d.QueryRow(`SELECT `+eventCols+` FROM events WHERE id = ?`, id))
}

// GetEventBySlug returns one event by its slug.
func (d *DB) GetEventBySlug(slug string) (Event, error) {
	return scanEvent(d.QueryRow(`SELECT `+eventCols+` FROM events WHERE slug = ?`, slug))
}

// CurrentEvent returns the event volunteers should see: the most recent
// open one, else the most recent frozen one. ErrNotFound if none.
func (d *DB) CurrentEvent() (Event, error) {
	return scanEvent(d.QueryRow(`SELECT ` + eventCols + ` FROM events
		WHERE status IN ('open', 'frozen')
		ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, starts_on DESC LIMIT 1`))
}

// CreateEvent creates an event together with its default pole.
func (d *DB) CreateEvent(name, startsOn, endsOn string, freeze *time.Time, defaultPoleName string) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO events (slug, name, status, starts_on, ends_on, freeze_at, created_at) VALUES (?, ?, 'draft', ?, ?, ?, ?)`,
		uniqueEventSlug(tx, name, 0), name, startsOn, endsOn, OptTime(freeze), Now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_, err = tx.Exec(`INSERT INTO poles (event_id, slug, name, description, is_default, position, created_at) VALUES (?, ?, ?, '', 1, -1, ?)`,
		id, uniqueSlug(Slugify(defaultPoleName), func(string) bool { return false }), defaultPoleName, Now())
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// DuplicateEvent creates a draft event and copies every pole of the
// source event into it (settings, locations, managers; no slots, no
// members), along with the edition logo. It returns the new event id.
func (d *DB) DuplicateEvent(sourceID int64, name, startsOn, endsOn string, defaultPoleName string) (int64, error) {
	poles, err := d.ListPoles(sourceID)
	if err != nil {
		return 0, err
	}
	newID, err := d.CreateEvent(name, startsOn, endsOn, nil, defaultPoleName)
	if err != nil {
		return 0, err
	}
	src, err := d.GetEvent(sourceID)
	if err != nil {
		return 0, err
	}
	if err := d.SetEventImage(newID, src.Image); err != nil {
		return 0, err
	}
	for _, p := range poles {
		if _, err := d.CopyPole(p.ID, newID); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

// UpdateEvent updates the editable fields of an event.
func (d *DB) UpdateEvent(e Event) error {
	base := e.Slug
	if !ValidSlug(base) {
		base = e.Name
	}
	e.Slug = d.UniqueEventSlug(base, e.ID)
	_, err := d.Exec(`UPDATE events SET slug = ?, name = ?, status = ?, starts_on = ?, ends_on = ?, freeze_at = ? WHERE id = ?`,
		e.Slug, e.Name, e.Status, e.StartsOn, e.EndsOn, OptTime(e.FreezeAt), e.ID)
	return err
}

// SetEventImage sets or clears the edition logo. The caller owns the file: it
// is the upload directory that keeps the bytes.
func (d *DB) SetEventImage(id int64, name string) error {
	_, err := d.Exec(`UPDATE events SET image = ? WHERE id = ?`, name, id)
	return err
}

// DeleteEvent removes an event and everything inside it.
func (d *DB) DeleteEvent(id int64) error {
	_, err := d.Exec(`DELETE FROM events WHERE id = ?`, id)
	return err
}

// EventStats is the coverage summary of one event.
type EventStats struct {
	Volunteers int
	Slots      int
	Capacity   int
	Filled     int
}

// GetEventStats computes global coverage figures.
func (d *DB) GetEventStats(eventID int64) (EventStats, error) {
	var s EventStats
	err := d.QueryRow(`SELECT COUNT(DISTINCT pm.user_id) FROM pole_members pm JOIN poles p ON p.id = pm.pole_id WHERE p.event_id = ?`, eventID).Scan(&s.Volunteers)
	if err != nil {
		return s, err
	}
	err = d.QueryRow(`SELECT COUNT(s.id), COALESCE(SUM(s.capacity), 0),
			COALESCE((SELECT COUNT(*) FROM signups sg JOIN slots s2 ON s2.id = sg.slot_id JOIN poles p2 ON p2.id = s2.pole_id WHERE p2.event_id = ?), 0)
		FROM slots s JOIN poles p ON p.id = s.pole_id WHERE p.event_id = ?`, eventID, eventID).
		Scan(&s.Slots, &s.Capacity, &s.Filled)
	return s, err
}

// EventVolunteer is a volunteer of an event with the poles they joined.
type EventVolunteer struct {
	User      User
	Poles     []string
	SlotCount int
}

// ListEventVolunteers returns everyone who joined at least one pole.
func (d *DB) ListEventVolunteers(eventID int64) ([]EventVolunteer, error) {
	rows, err := d.Query(`SELECT `+userCols+`, GROUP_CONCAT(p.name, '|'),
			(SELECT COUNT(*) FROM signups sg JOIN slots s ON s.id = sg.slot_id JOIN poles p2 ON p2.id = s.pole_id WHERE p2.event_id = ? AND sg.user_id = u.id)
		FROM users u
		JOIN pole_members pm ON pm.user_id = u.id
		JOIN poles p ON p.id = pm.pole_id AND p.event_id = ?
		GROUP BY u.id
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`, eventID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventVolunteer
	for rows.Next() {
		var v EventVolunteer
		var poles sql.NullString
		u, err := scanUser(rows, &poles, &v.SlotCount)
		if err != nil {
			return nil, err
		}
		v.User = u
		if poles.Valid {
			v.Poles = splitPipe(poles.String)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func splitPipe(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
