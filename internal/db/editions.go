package db

import (
	"database/sql"
	"time"
)

// Edition statuses.
const (
	StatusDraft    = "draft"
	StatusOpen     = "open"
	StatusFrozen   = "frozen"
	StatusArchived = "archived"
)

// Edition is one occurrence of the event (typically one year).
type Edition struct {
	ID        int64
	Name      string
	Status    string
	StartsOn  string // YYYY-MM-DD
	EndsOn    string // YYYY-MM-DD
	FreezeAt  *time.Time
	CreatedAt time.Time
}

// IsVisibleToVolunteers tells whether volunteers may browse this edition.
func (e Edition) IsVisibleToVolunteers() bool {
	return e.Status == StatusOpen || e.Status == StatusFrozen
}

// SignupsLocked tells whether volunteers can no longer change their signups.
// poleFreeze, when set, overrides the edition freeze date.
func (e Edition) SignupsLocked(poleFreeze *time.Time, now time.Time) bool {
	if e.Status != StatusOpen {
		return true
	}
	freeze := e.FreezeAt
	if poleFreeze != nil {
		freeze = poleFreeze
	}
	return freeze != nil && !now.Before(*freeze)
}

const editionCols = `id, name, status, starts_on, ends_on, freeze_at, created_at`

func scanEdition(row interface{ Scan(...any) error }) (Edition, error) {
	var e Edition
	var freeze sql.NullString
	var created string
	if err := row.Scan(&e.ID, &e.Name, &e.Status, &e.StartsOn, &e.EndsOn, &freeze, &created); err != nil {
		return e, err
	}
	e.FreezeAt = NullTime(freeze)
	e.CreatedAt = ParseTime(created)
	return e, nil
}

// ListEditions returns all editions, most recent first.
func (d *DB) ListEditions() ([]Edition, error) {
	rows, err := d.Query(`SELECT ` + editionCols + ` FROM editions ORDER BY starts_on DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edition
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEdition returns one edition.
func (d *DB) GetEdition(id int64) (Edition, error) {
	return scanEdition(d.QueryRow(`SELECT `+editionCols+` FROM editions WHERE id = ?`, id))
}

// CurrentEdition returns the edition volunteers should see: the most recent
// open one, else the most recent frozen one. ErrNotFound if none.
func (d *DB) CurrentEdition() (Edition, error) {
	return scanEdition(d.QueryRow(`SELECT ` + editionCols + ` FROM editions
		WHERE status IN ('open', 'frozen')
		ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, starts_on DESC LIMIT 1`))
}

// CreateEdition creates an edition together with its default pole.
func (d *DB) CreateEdition(name, startsOn, endsOn string, freeze *time.Time, defaultPoleName string) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO editions (name, status, starts_on, ends_on, freeze_at, created_at) VALUES (?, 'draft', ?, ?, ?, ?)`,
		name, startsOn, endsOn, OptTime(freeze), Now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_, err = tx.Exec(`INSERT INTO poles (edition_id, name, description, is_default, position, created_at) VALUES (?, ?, '', 1, -1, ?)`,
		id, defaultPoleName, Now())
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// DuplicateEdition creates a draft edition and copies every pole of the
// source edition into it (settings, locations, managers; no slots, no
// members). It returns the new edition id.
func (d *DB) DuplicateEdition(sourceID int64, name, startsOn, endsOn string, defaultPoleName string) (int64, error) {
	poles, err := d.ListPoles(sourceID)
	if err != nil {
		return 0, err
	}
	newID, err := d.CreateEdition(name, startsOn, endsOn, nil, defaultPoleName)
	if err != nil {
		return 0, err
	}
	for _, p := range poles {
		if _, err := d.CopyPole(p.ID, newID); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

// UpdateEdition updates the editable fields of an edition.
func (d *DB) UpdateEdition(e Edition) error {
	_, err := d.Exec(`UPDATE editions SET name = ?, status = ?, starts_on = ?, ends_on = ?, freeze_at = ? WHERE id = ?`,
		e.Name, e.Status, e.StartsOn, e.EndsOn, OptTime(e.FreezeAt), e.ID)
	return err
}

// DeleteEdition removes an edition and everything inside it.
func (d *DB) DeleteEdition(id int64) error {
	_, err := d.Exec(`DELETE FROM editions WHERE id = ?`, id)
	return err
}

// EditionStats is the coverage summary of one edition.
type EditionStats struct {
	Volunteers int
	Slots      int
	Capacity   int
	Filled     int
}

// GetEditionStats computes global coverage figures.
func (d *DB) GetEditionStats(editionID int64) (EditionStats, error) {
	var s EditionStats
	err := d.QueryRow(`SELECT COUNT(DISTINCT pm.user_id) FROM pole_members pm JOIN poles p ON p.id = pm.pole_id WHERE p.edition_id = ?`, editionID).Scan(&s.Volunteers)
	if err != nil {
		return s, err
	}
	err = d.QueryRow(`SELECT COUNT(s.id), COALESCE(SUM(s.capacity), 0),
			COALESCE((SELECT COUNT(*) FROM signups sg JOIN slots s2 ON s2.id = sg.slot_id JOIN poles p2 ON p2.id = s2.pole_id WHERE p2.edition_id = ?), 0)
		FROM slots s JOIN poles p ON p.id = s.pole_id WHERE p.edition_id = ?`, editionID, editionID).
		Scan(&s.Slots, &s.Capacity, &s.Filled)
	return s, err
}

// EditionVolunteer is a volunteer of an edition with the poles they joined.
type EditionVolunteer struct {
	User      User
	Poles     []string
	SlotCount int
}

// ListEditionVolunteers returns everyone who joined at least one pole.
func (d *DB) ListEditionVolunteers(editionID int64) ([]EditionVolunteer, error) {
	rows, err := d.Query(`SELECT `+userCols+`, GROUP_CONCAT(p.name, '|'),
			(SELECT COUNT(*) FROM signups sg JOIN slots s ON s.id = sg.slot_id JOIN poles p2 ON p2.id = s.pole_id WHERE p2.edition_id = ? AND sg.user_id = u.id)
		FROM users u
		JOIN pole_members pm ON pm.user_id = u.id
		JOIN poles p ON p.id = pm.pole_id AND p.edition_id = ?
		GROUP BY u.id
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`, editionID, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EditionVolunteer
	for rows.Next() {
		var v EditionVolunteer
		var created string
		var last, poles sql.NullString
		if err := rows.Scan(&v.User.ID, &v.User.Email, &v.User.Name, &v.User.Phone, &v.User.IsAdmin, &created, &last, &poles, &v.SlotCount); err != nil {
			return nil, err
		}
		v.User.CreatedAt = ParseTime(created)
		v.User.LastLoginAt = NullTime(last)
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
