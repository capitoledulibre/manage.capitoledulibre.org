package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

// lines splits a list typed one item per line, without the blank lines and
// the doubles.
func lines(v string) []string {
	var out []string

	for l := range strings.SplitSeq(v, "\n") {
		if l = strings.TrimSpace(l); l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}

	return out
}

func cleanLines(v string) string { return strings.Join(lines(v), "\n") }

// TshirtSizeList is the t-shirt sizes on offer, none when the edition does
// not ask.
func (e *Event) TshirtSizeList() []string { return lines(e.TshirtSizes) }

// MealList is the meals the edition provides its volunteers.
func (e *Event) MealList() []string { return lines(e.Meals) }

// AsksVolunteers tells whether the edition asks its volunteers anything for
// the logistics.
func (e *Event) AsksVolunteers() bool { return e.TshirtSizes != "" || e.Meals != "" }

// VolunteerDetails is what a volunteer answers for an edition.
type VolunteerDetails struct {
	EventID  int64
	PersonID int64
	Tshirt   string
	Meals    []string
	Dietary  string
	// Answered is false for details not saved yet (prefilled from an
	// earlier edition, or empty).
	Answered bool
}

// Takes tells whether the volunteer takes a meal.
func (d *VolunteerDetails) Takes(meal string) bool { return slices.Contains(d.Meals, meal) }

func scanDetails(row interface{ Scan(...any) error }) (*VolunteerDetails, error) {
	var (
		d     VolunteerDetails
		meals string
	)

	if err := row.Scan(&d.EventID, &d.PersonID, &d.Tshirt, &meals, &d.Dietary); err != nil {
		return nil, notFound(err)
	}

	d.Meals, d.Answered = lines(meals), true

	return &d, nil
}

const detailsCols = `event_id, person_id, tshirt, meals, dietary`

// GetVolunteerDetails returns what a volunteer answered for an edition. When
// they have not answered yet, their t-shirt size of the latest earlier
// edition is offered: it rarely changes. Meals and dietary needs are asked
// again each time.
func (s *Store) GetVolunteerDetails(ctx context.Context, eventID, personID int64) (*VolunteerDetails, error) {
	d, err := scanDetails(s.db.QueryRowContext(ctx, `SELECT `+detailsCols+` FROM volunteer_details WHERE event_id = ? AND person_id = ?`,
		eventID, personID))
	if err == nil || !errors.Is(err, ErrNotFound) {
		return d, err
	}

	d = &VolunteerDetails{EventID: eventID, PersonID: personID}

	err = s.db.QueryRowContext(ctx, `SELECT vd.tshirt FROM volunteer_details vd JOIN events e ON e.id = vd.event_id
		WHERE vd.person_id = ? AND vd.event_id != ? AND vd.tshirt != '' ORDER BY e.year DESC, e.id DESC LIMIT 1`,
		personID, eventID).Scan(&d.Tshirt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return d, nil
}

// SaveVolunteerDetails records what a volunteer answers for an edition.
func (s *Store) SaveVolunteerDetails(ctx context.Context, d *VolunteerDetails) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO volunteer_details (event_id, person_id, tshirt, meals, dietary, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id, person_id) DO UPDATE SET tshirt = excluded.tshirt, meals = excluded.meals,
			dietary = excluded.dietary, updated_at = excluded.updated_at`,
		d.EventID, d.PersonID, d.Tshirt, strings.Join(d.Meals, "\n"), d.Dietary, now())

	return err
}

// ListVolunteerDetails returns the answers of an edition, by person.
func (s *Store) ListVolunteerDetails(ctx context.Context, eventID int64) (map[int64]*VolunteerDetails, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+detailsCols+` FROM volunteer_details WHERE event_id = ?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]*VolunteerDetails{}

	for rows.Next() {
		d, err := scanDetails(rows)
		if err != nil {
			return nil, err
		}

		out[d.PersonID] = d
	}

	return out, rows.Err()
}
