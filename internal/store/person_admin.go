package store

import (
	"context"
	"strings"
)

// What the staff see and change about a person, on their page of the
// administration.

// UpdatePerson saves the name, address, phone and e-mail language of a
// person. It returns ErrDuplicate when another person has that address,
// and ErrBadInput for a language other than fr or en.
func (s *Store) UpdatePerson(ctx context.Context, p *Person) error {
	if p.Lang != "fr" && p.Lang != "en" {
		return ErrBadInput
	}

	_, err := s.db.ExecContext(ctx, `UPDATE people SET name = ?, email = ?, phone = ?, lang = ? WHERE id = ?`,
		p.Name, NormalizeEmail(p.Email), p.Phone, p.Lang, p.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrDuplicate
	}

	return err
}

// DeletePersonSessions logs a person out everywhere.
func (s *Store) DeletePersonSessions(ctx context.Context, personID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE person_id = ?`, personID)

	return err
}

// IsStaff tells whether a person is a staff member.
func (s *Store) IsStaff(ctx context.Context, personID int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM admins WHERE person_id = ?`, personID)
}

// PersonOrganizations returns the partners a person is a contact of.
func (s *Store) PersonOrganizations(ctx context.Context, personID int64) ([]*Organization, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+orgCols+` FROM organizations
		WHERE id IN (SELECT organization_id FROM managers WHERE person_id = ?) ORDER BY name COLLATE NOCASE`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Organization

	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, o)
	}

	return out, rows.Err()
}

// PersonPole is a pole a person belongs to, in any edition.
type PersonPole struct {
	EventName string
	Pole      *Pole
	Lead      bool
}

// ListPersonPoles returns the poles of a person, the latest editions first.
func (s *Store) ListPersonPoles(ctx context.Context, personID int64) ([]*PersonPole, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+poleCols+`, e.name,
			EXISTS (SELECT 1 FROM pole_managers pg WHERE pg.pole_id = p.id AND pg.person_id = pm.person_id)
		`+poleFrom+` JOIN pole_members pm ON pm.pole_id = p.id
		WHERE pm.person_id = ? ORDER BY e.year DESC, e.id DESC, p.position, p.name COLLATE NOCASE`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*PersonPole

	for rows.Next() {
		var pp PersonPole

		pp.Pole, err = scanPoleExtra(rows, &pp.EventName, &pp.Lead)
		if err != nil {
			return nil, err
		}

		out = append(out, &pp)
	}

	return out, rows.Err()
}

// ListAllPersonSlots returns the slots of a person in every edition, the
// latest first.
func (s *Store) ListAllPersonSlots(ctx context.Context, personID int64) ([]*Slot, error) {
	return s.querySlots(ctx, personID, `JOIN signups me ON me.slot_id = s.id AND me.person_id = ?
		ORDER BY s.starts_at DESC LIMIT 200`, personID)
}
