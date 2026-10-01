package store

import (
	"context"
	"database/sql"
	"time"
)

type JobOffer struct {
	ID          int64
	UID         string
	OrgID       int64
	Title       string
	Location    string
	Contract    string
	URL         string
	Description string
	ExpiresAt   *time.Time
	Hidden      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (j *JobOffer) Expired() bool {
	return j.ExpiresAt != nil && time.Now().After(j.ExpiresAt.Add(24*time.Hour))
}

const jobCols = `id, uid, organization_id, title, location, contract, url, description, expires_at, hidden, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (*JobOffer, error) {
	var (
		j       JobOffer
		expires sql.NullTime
	)

	err := row.Scan(&j.ID, &j.UID, &j.OrgID, &j.Title, &j.Location, &j.Contract, &j.URL, &j.Description, &expires, &j.Hidden, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}

	j.ExpiresAt = timePtr(expires)

	return &j, nil
}

func (s *Store) GetJobOffer(ctx context.Context, orgID int64, uid string) (*JobOffer, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM job_offers WHERE uid = ? AND organization_id = ?`, uid, orgID))
}

func (s *Store) ListJobOffers(ctx context.Context, orgID int64) ([]*JobOffer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM job_offers WHERE organization_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*JobOffer

	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, j)
	}

	return out, rows.Err()
}

// PublicJob is a job offer together with the organization publishing it.
type PublicJob struct {
	Job   *JobOffer
	Org   *Organization
	Level string
}

// ListPublicJobs returns visible, non-expired offers of organizations with a
// confirmed partnership for the given event, highest levels first.
func (s *Store) ListPublicJobs(ctx context.Context, eventID int64) ([]PublicJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixCols("j", jobCols)+`, `+prefixCols("o", orgCols)+`, COALESCE(l.name, '')
		FROM job_offers j
		JOIN organizations o ON o.id = j.organization_id
		JOIN partnerships p ON p.organization_id = o.id AND p.event_id = ? AND p.status = 'confirmed'
		LEFT JOIN levels l ON l.id = p.level_id
		WHERE j.hidden = 0 AND (j.expires_at IS NULL OR j.expires_at >= ?)
		ORDER BY COALESCE(l.position, -1) DESC, COALESCE(l.price, 0) DESC, o.name COLLATE NOCASE, j.created_at DESC`,
		eventID, now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PublicJob

	for rows.Next() {
		var (
			j       JobOffer
			o       Organization
			expires sql.NullTime
			level   string
		)

		err := rows.Scan(&j.ID, &j.UID, &j.OrgID, &j.Title, &j.Location, &j.Contract, &j.URL, &j.Description, &expires, &j.Hidden, &j.CreatedAt, &j.UpdatedAt,
			&o.ID, &o.Slug, &o.Name, &o.LegalName, &o.AddressLine1, &o.AddressLine2, &o.ZipCode, &o.City, &o.Country,
			&o.SIRET, &o.VATNumber, &o.Website, &o.DescriptionFR, &o.DescriptionEN, &o.LinkedIn, &o.Mastodon, &o.Bluesky, &o.X,
			&o.LogoFile, &o.LogoDarkFile, &o.CreatedAt, &o.UpdatedAt, &level)
		if err != nil {
			return nil, err
		}

		j.ExpiresAt = timePtr(expires)
		out = append(out, PublicJob{Job: &j, Org: &o, Level: level})
	}

	return out, rows.Err()
}

func (s *Store) SaveJobOffer(ctx context.Context, j *JobOffer) error {
	j.UpdatedAt = now()

	if j.ID == 0 {
		j.CreatedAt = j.UpdatedAt

		j.UID = NewUUID()

		res, err := s.db.ExecContext(ctx, `INSERT INTO job_offers (uid, organization_id, title, location, contract, url, description, expires_at, hidden, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, j.UID, j.OrgID, j.Title, j.Location, j.Contract, j.URL, j.Description, nullTime(j.ExpiresAt), j.Hidden, j.CreatedAt, j.UpdatedAt)
		if err != nil {
			return err
		}

		j.ID, err = res.LastInsertId()

		return err
	}

	_, err := s.db.ExecContext(ctx, `UPDATE job_offers SET title = ?, location = ?, contract = ?, url = ?, description = ?, expires_at = ?, hidden = ?, updated_at = ?
		WHERE id = ? AND organization_id = ?`, j.Title, j.Location, j.Contract, j.URL, j.Description, nullTime(j.ExpiresAt), j.Hidden, j.UpdatedAt, j.ID, j.OrgID)

	return err
}

func (s *Store) DeleteJobOffer(ctx context.Context, orgID int64, uid string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM job_offers WHERE uid = ? AND organization_id = ?`, uid, orgID)

	return err
}
