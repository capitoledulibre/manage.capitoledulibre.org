package store

import (
	"context"
	"time"
)

type Activity struct {
	ID            int64
	OrgID         int64
	PartnershipID *int64
	Actor         string
	Action        string
	Details       string
	CreatedAt     time.Time
}

func (s *Store) LogActivity(ctx context.Context, a *Activity) error {
	a.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO activity (organization_id, partnership_id, actor, action, details, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.OrgID, nullInt(a.PartnershipID), a.Actor, a.Action, a.Details, a.CreatedAt)

	return err
}

func (s *Store) ListActivity(ctx context.Context, orgID int64, limit int) ([]*Activity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, organization_id, partnership_id, actor, action, details, created_at FROM activity
		WHERE organization_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Activity

	for rows.Next() {
		var a Activity
		var pid *int64

		if err := rows.Scan(&a.ID, &a.OrgID, &pid, &a.Actor, &a.Action, &a.Details, &a.CreatedAt); err != nil {
			return nil, err
		}

		a.PartnershipID = pid
		out = append(out, &a)
	}

	return out, rows.Err()
}

// ReminderSent reports whether a reminder of this kind was already sent.
func (s *Store) ReminderSent(ctx context.Context, partnershipID int64, kind string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reminders_sent WHERE partnership_id = ? AND kind = ?`, partnershipID, kind).Scan(&n)

	return n > 0, err
}

func (s *Store) MarkReminderSent(ctx context.Context, partnershipID int64, kind string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO reminders_sent (partnership_id, kind, sent_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		partnershipID, kind, now())

	return err
}
