package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Partnership statuses, in pipeline order.
const (
	StatusProspect    = "prospect"
	StatusContacted   = "contacted"
	StatusNegotiating = "negotiating"
	StatusConfirmed   = "confirmed"
	StatusDeclined    = "declined"
)

var Statuses = []string{StatusProspect, StatusContacted, StatusNegotiating, StatusConfirmed, StatusDeclined}

func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}

	return false
}

type Partnership struct {
	ID            int64
	OrgID         int64
	EventID       int64
	LevelID       *int64
	Status        string
	PONumber      string
	QuoteFile     string
	QuoteSentAt   *time.Time
	QuoteSignedAt *time.Time
	InvoiceFile   string
	InvoiceSentAt *time.Time
	InvoicePaidAt *time.Time
	PartnerNotes  string
	InternalNotes string
	FollowupAt    *time.Time
	// PriceOverride is a negotiated price replacing the level price.
	PriceOverride *int64
	// OwnerAdminID is the staff member following the partnership.
	OwnerAdminID *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// VisibleToPartner reports whether the partnership is shown on the partner
// dashboard. Early pipeline stages are internal to the staff.
func (p *Partnership) VisibleToPartner() bool {
	return p.Status == StatusNegotiating || p.Status == StatusConfirmed
}

const partnershipCols = `id, organization_id, event_id, level_id, status, po_number, quote_file, quote_sent_at, quote_signed_at,
	invoice_file, invoice_sent_at, invoice_paid_at, partner_notes, internal_notes, followup_at, price_override, owner_admin_id, created_at, updated_at`

func scanPartnership(row interface{ Scan(...any) error }) (*Partnership, error) {
	var (
		p                                      Partnership
		level, priceOverride, owner            sql.NullInt64
		qSent, qSigned, iSent, iPaid, followup sql.NullTime
	)

	err := row.Scan(&p.ID, &p.OrgID, &p.EventID, &level, &p.Status, &p.PONumber, &p.QuoteFile, &qSent, &qSigned,
		&p.InvoiceFile, &iSent, &iPaid, &p.PartnerNotes, &p.InternalNotes, &followup, &priceOverride, &owner, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}

	p.LevelID = int64Ptr(level)
	p.QuoteSentAt, p.QuoteSignedAt = timePtr(qSent), timePtr(qSigned)
	p.InvoiceSentAt, p.InvoicePaidAt = timePtr(iSent), timePtr(iPaid)
	p.FollowupAt = timePtr(followup)
	p.PriceOverride = int64Ptr(priceOverride)
	p.OwnerAdminID = int64Ptr(owner)

	return &p, nil
}

// Price returns the amount due for the partnership: the negotiated price
// when set, otherwise the level price. ok is false when neither is known.
func (p *Partnership) Price(l *Level) (amount int, ok bool) {
	if p.PriceOverride != nil {
		return int(*p.PriceOverride), true
	}

	if l != nil {
		return l.Price, true
	}

	return 0, false
}

// GetPartnershipByOrgEvent finds the partnership of an organization for an
// edition.
func (s *Store) GetPartnershipByOrgEvent(ctx context.Context, orgID, eventID int64) (*Partnership, error) {
	return scanPartnership(s.db.QueryRowContext(ctx, `SELECT `+partnershipCols+` FROM partnerships WHERE organization_id = ? AND event_id = ?`, orgID, eventID))
}

func (s *Store) GetPartnership(ctx context.Context, id int64) (*Partnership, error) {
	return scanPartnership(s.db.QueryRowContext(ctx, `SELECT `+partnershipCols+` FROM partnerships WHERE id = ?`, id))
}

func (s *Store) queryPartnerships(ctx context.Context, query string, args ...any) ([]*Partnership, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Partnership

	for rows.Next() {
		p, err := scanPartnership(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// ListOrgPartnerships returns an organization's partnerships, most recent
// event first.
func (s *Store) ListOrgPartnerships(ctx context.Context, orgID int64) ([]*Partnership, error) {
	return s.queryPartnerships(ctx, `SELECT `+prefixCols("p", partnershipCols)+` FROM partnerships p
		JOIN events e ON e.id = p.event_id WHERE p.organization_id = ? ORDER BY e.year DESC, e.id DESC`, orgID)
}

func (s *Store) ListEventPartnerships(ctx context.Context, eventID int64) ([]*Partnership, error) {
	return s.queryPartnerships(ctx, `SELECT `+partnershipCols+` FROM partnerships WHERE event_id = ?`, eventID)
}

func (s *Store) CreatePartnership(ctx context.Context, p *Partnership) error {
	if p.Status == "" {
		p.Status = StatusProspect
	}

	p.CreatedAt, p.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO partnerships (organization_id, event_id, level_id, status, owner_admin_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.OrgID, p.EventID, nullInt(p.LevelID), p.Status, nullInt(p.OwnerAdminID), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}

		return err
	}

	p.ID, err = res.LastInsertId()

	return err
}

// UpdatePartnership saves every field of a partnership.
func (s *Store) UpdatePartnership(ctx context.Context, p *Partnership) error {
	p.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE partnerships SET level_id = ?, status = ?, po_number = ?, quote_file = ?, quote_sent_at = ?,
		quote_signed_at = ?, invoice_file = ?, invoice_sent_at = ?, invoice_paid_at = ?, partner_notes = ?, internal_notes = ?,
		followup_at = ?, price_override = ?, owner_admin_id = ?, updated_at = ? WHERE id = ?`,
		nullInt(p.LevelID), p.Status, p.PONumber, p.QuoteFile, nullTime(p.QuoteSentAt), nullTime(p.QuoteSignedAt),
		p.InvoiceFile, nullTime(p.InvoiceSentAt), nullTime(p.InvoicePaidAt), p.PartnerNotes, p.InternalNotes,
		nullTime(p.FollowupAt), nullInt(p.PriceOverride), nullInt(p.OwnerAdminID), p.UpdatedAt, p.ID)

	return err
}

func (s *Store) DeletePartnership(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM partnerships WHERE id = ?`, id)

	return err
}

// DonePerks returns the ids of the perks already delivered for a partnership.
func (s *Store) DonePerks(ctx context.Context, partnershipID int64) (map[int64]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT perk_id, done_at FROM partnership_perks WHERE partnership_id = ?`, partnershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]time.Time{}

	for rows.Next() {
		var (
			id int64
			at time.Time
		)

		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}

		out[id] = at
	}

	return out, rows.Err()
}

// SetDonePerks replaces the set of delivered perks, keeping the original
// completion date of perks that stay done.
func (s *Store) SetDonePerks(ctx context.Context, partnershipID int64, perkIDs []int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		keep := map[int64]bool{}
		for _, id := range perkIDs {
			keep[id] = true

			if _, err := tx.ExecContext(ctx, `INSERT INTO partnership_perks (partnership_id, perk_id, done_at) VALUES (?, ?, ?)
				ON CONFLICT DO NOTHING`, partnershipID, id, now()); err != nil {
				return err
			}
		}

		rows, err := tx.QueryContext(ctx, `SELECT perk_id FROM partnership_perks WHERE partnership_id = ?`, partnershipID)
		if err != nil {
			return err
		}

		var remove []int64

		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()

				return err
			}

			if !keep[id] {
				remove = append(remove, id)
			}
		}

		rows.Close()

		for _, id := range remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM partnership_perks WHERE partnership_id = ? AND perk_id = ?`, partnershipID, id); err != nil {
				return err
			}
		}

		return nil
	})
}

type Attendee struct {
	ID            int64
	UID           string
	PartnershipID int64
	FirstName     string
	LastName      string
	Email         string
	Role          string
	Dietary       string
	CreatedAt     time.Time
}

const (
	RoleBooth   = "booth"
	RoleVisitor = "visitor"
)

func (s *Store) ListAttendees(ctx context.Context, partnershipID int64) ([]*Attendee, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, uid, partnership_id, first_name, last_name, email, role, dietary, created_at
		FROM attendees WHERE partnership_id = ? ORDER BY last_name COLLATE NOCASE, first_name COLLATE NOCASE`, partnershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Attendee

	for rows.Next() {
		var a Attendee
		if err := rows.Scan(&a.ID, &a.UID, &a.PartnershipID, &a.FirstName, &a.LastName, &a.Email, &a.Role, &a.Dietary, &a.CreatedAt); err != nil {
			return nil, err
		}

		out = append(out, &a)
	}

	return out, rows.Err()
}

func (s *Store) CountAttendees(ctx context.Context, partnershipID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attendees WHERE partnership_id = ?`, partnershipID).Scan(&n)

	return n, err
}

func (s *Store) AddAttendee(ctx context.Context, a *Attendee) error {
	a.CreatedAt = now()
	a.Email = NormalizeEmail(a.Email)

	a.UID = NewUUID()

	res, err := s.db.ExecContext(ctx, `INSERT INTO attendees (uid, partnership_id, first_name, last_name, email, role, dietary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.UID, a.PartnershipID, a.FirstName, a.LastName, a.Email, a.Role, a.Dietary, a.CreatedAt)
	if err != nil {
		return err
	}

	a.ID, err = res.LastInsertId()

	return err
}

// UpdateAttendee saves the fields of an attendee of the partnership,
// found by UID. ErrNotFound when it is not one of its attendees.
func (s *Store) UpdateAttendee(ctx context.Context, a *Attendee) error {
	res, err := s.db.ExecContext(ctx, `UPDATE attendees SET first_name = ?, last_name = ?, email = ?, role = ?, dietary = ?
		WHERE uid = ? AND partnership_id = ?`, a.FirstName, a.LastName, NormalizeEmail(a.Email), a.Role, a.Dietary, a.UID, a.PartnershipID)
	if err != nil {
		return err
	}

	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) DeleteAttendee(ctx context.Context, partnershipID int64, uid string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM attendees WHERE uid = ? AND partnership_id = ?`, uid, partnershipID)

	return err
}

func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(p)
	}

	return strings.Join(parts, ", ")
}
