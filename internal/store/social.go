package store

import (
	"context"
	"database/sql"
	"time"
)

// Social networks on which a partner can be announced.
var Networks = []string{"linkedin", "mastodon", "bluesky", "x", "other"}

func ValidNetwork(n string) bool {
	for _, v := range Networks {
		if v == n {
			return true
		}
	}

	return false
}

// SocialPost is a planned or published announcement of a partner on a social
// network.
type SocialPost struct {
	ID            int64
	UID           string
	PartnershipID int64
	Network       string
	PlannedAt     *time.Time
	// URL is set once the post is published.
	URL       string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (p *SocialPost) Published() bool { return p.URL != "" }

const socialCols = `id, uid, partnership_id, network, planned_at, url, note, created_at, updated_at`

func scanSocialPost(row interface{ Scan(...any) error }) (*SocialPost, error) {
	var (
		p       SocialPost
		planned sql.NullTime
	)

	if err := row.Scan(&p.ID, &p.UID, &p.PartnershipID, &p.Network, &planned, &p.URL, &p.Note, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, notFound(err)
	}

	p.PlannedAt = timePtr(planned)

	return &p, nil
}

// ListSocialPosts returns the posts of a partnership by planned date, undated
// posts last.
func (s *Store) ListSocialPosts(ctx context.Context, partnershipID int64) ([]*SocialPost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+socialCols+` FROM social_posts WHERE partnership_id = ?
		ORDER BY planned_at IS NULL, planned_at, id`, partnershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SocialPost

	for rows.Next() {
		p, err := scanSocialPost(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

func (s *Store) GetSocialPost(ctx context.Context, partnershipID int64, uid string) (*SocialPost, error) {
	return scanSocialPost(s.db.QueryRowContext(ctx, `SELECT `+socialCols+` FROM social_posts WHERE uid = ? AND partnership_id = ?`, uid, partnershipID))
}

func (s *Store) SaveSocialPost(ctx context.Context, p *SocialPost) error {
	p.UpdatedAt = now()

	if p.ID == 0 {
		p.CreatedAt = p.UpdatedAt

		p.UID = NewUUID()

		res, err := s.db.ExecContext(ctx, `INSERT INTO social_posts (uid, partnership_id, network, planned_at, url, note, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.UID, p.PartnershipID, p.Network, nullTime(p.PlannedAt), p.URL, p.Note, p.CreatedAt, p.UpdatedAt)
		if err != nil {
			return err
		}

		p.ID, err = res.LastInsertId()

		return err
	}

	_, err := s.db.ExecContext(ctx, `UPDATE social_posts SET network = ?, planned_at = ?, url = ?, note = ?, updated_at = ?
		WHERE id = ? AND partnership_id = ?`, p.Network, nullTime(p.PlannedAt), p.URL, p.Note, p.UpdatedAt, p.ID, p.PartnershipID)

	return err
}

func (s *Store) DeleteSocialPost(ctx context.Context, partnershipID int64, uid string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM social_posts WHERE uid = ? AND partnership_id = ?`, uid, partnershipID)

	return err
}

// OrganizationHasConfirmedPartnership reports whether an organization is (or
// was) a confirmed partner of any edition.
func (s *Store) OrganizationHasConfirmedPartnership(ctx context.Context, orgID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM partnerships WHERE organization_id = ? AND status = 'confirmed'`, orgID).Scan(&n)

	return n > 0, err
}
