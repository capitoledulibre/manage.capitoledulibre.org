package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Organization struct {
	ID            int64
	Slug          string
	Name          string
	LegalName     string
	AddressLine1  string
	AddressLine2  string
	ZipCode       string
	City          string
	Country       string
	SIRET         string
	VATNumber     string
	Website       string
	DescriptionFR string
	DescriptionEN string
	LinkedIn      string
	Mastodon      string
	Bluesky       string
	X             string
	LogoFile      string
	LogoDarkFile  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BillingComplete reports whether the information needed to issue an invoice
// has been filled.
func (o *Organization) BillingComplete() bool {
	return o.LegalName != "" && o.AddressLine1 != "" && o.ZipCode != "" && o.City != ""
}

// CommunicationComplete reports whether the information used on the website
// has been filled.
func (o *Organization) CommunicationComplete() bool {
	return o.Website != "" && (o.DescriptionFR != "" || o.DescriptionEN != "")
}

func (o *Organization) Description(lang string) string {
	if lang == "en" && o.DescriptionEN != "" {
		return o.DescriptionEN
	}

	if o.DescriptionFR != "" {
		return o.DescriptionFR
	}

	return o.DescriptionEN
}

const orgCols = `id, slug, name, legal_name, address_line1, address_line2, zip_code, city, country, siret, vat_number,
	website, description_fr, description_en, linkedin, mastodon, bluesky, x, logo_file, logo_dark_file, created_at, updated_at`

func scanOrg(row interface{ Scan(...any) error }) (*Organization, error) {
	var o Organization

	err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.LegalName, &o.AddressLine1, &o.AddressLine2, &o.ZipCode, &o.City, &o.Country,
		&o.SIRET, &o.VATNumber, &o.Website, &o.DescriptionFR, &o.DescriptionEN, &o.LinkedIn, &o.Mastodon, &o.Bluesky, &o.X,
		&o.LogoFile, &o.LogoDarkFile, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}

	return &o, nil
}

func (s *Store) GetOrganization(ctx context.Context, id int64) (*Organization, error) {
	return scanOrg(s.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM organizations WHERE id = ?`, id))
}

func (s *Store) ListOrganizations(ctx context.Context) ([]*Organization, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+orgCols+` FROM organizations ORDER BY name COLLATE NOCASE`)
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

// OrganizationByLogoFile finds the organization currently using a logo file.
func (s *Store) OrganizationByLogoFile(ctx context.Context, file string) (*Organization, error) {
	return scanOrg(s.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM organizations WHERE logo_file = ? OR logo_dark_file = ?`, file, file))
}

func (s *Store) CreateOrganization(ctx context.Context, o *Organization) error {
	slug, err := s.uniqueSlug(ctx, o.Name)
	if err != nil {
		return err
	}

	o.Slug = slug
	o.CreatedAt, o.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO organizations (slug, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		o.Slug, o.Name, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return err
	}

	o.ID, err = res.LastInsertId()
	if err != nil {
		return err
	}

	return s.UpdateOrganization(ctx, o)
}

// UpdateOrganization saves every editable field except logos and slug.
func (s *Store) UpdateOrganization(ctx context.Context, o *Organization) error {
	o.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE organizations SET name = ?, legal_name = ?, address_line1 = ?, address_line2 = ?,
		zip_code = ?, city = ?, country = ?, siret = ?, vat_number = ?, website = ?, description_fr = ?, description_en = ?,
		linkedin = ?, mastodon = ?, bluesky = ?, x = ?, updated_at = ? WHERE id = ?`,
		o.Name, o.LegalName, o.AddressLine1, o.AddressLine2, o.ZipCode, o.City, o.Country, o.SIRET, o.VATNumber,
		o.Website, o.DescriptionFR, o.DescriptionEN, o.LinkedIn, o.Mastodon, o.Bluesky, o.X, o.UpdatedAt, o.ID)

	return err
}

func (s *Store) DeleteOrganization(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM organizations WHERE id = ?`, id)

	return err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a name into a URL-friendly identifier.
func Slugify(name string) string {
	var b strings.Builder

	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}

		b.WriteRune(r)
	}

	slug := strings.Trim(nonSlug.ReplaceAllString(b.String(), "-"), "-")
	if slug == "" {
		slug = "org"
	}

	return slug
}

func (s *Store) uniqueSlug(ctx context.Context, name string) (string, error) {
	base := Slugify(name)
	slug := base

	for i := 2; ; i++ {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations WHERE slug = ?`, slug).Scan(&n); err != nil {
			return "", err
		}

		if n == 0 {
			return slug, nil
		}

		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

type Logo struct {
	ID         int64
	OrgID      int64
	Variant    string
	File       string
	UploadedBy string
	UploadedAt time.Time
}

const (
	LogoDefault = "default"
	LogoDark    = "dark"
)

// AddLogo records a new logo version and makes it the current one for its
// variant.
func (s *Store) AddLogo(ctx context.Context, l *Logo) error {
	col := "logo_file"
	if l.Variant == LogoDark {
		col = "logo_dark_file"
	}

	l.UploadedAt = now()

	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO logos (organization_id, variant, file, uploaded_by, uploaded_at) VALUES (?, ?, ?, ?, ?)`,
			l.OrgID, l.Variant, l.File, l.UploadedBy, l.UploadedAt); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, `UPDATE organizations SET `+col+` = ?, updated_at = ? WHERE id = ?`, l.File, l.UploadedAt, l.OrgID)

		return err
	})
}

func (s *Store) ListLogos(ctx context.Context, orgID int64) ([]*Logo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, organization_id, variant, file, uploaded_by, uploaded_at FROM logos
		WHERE organization_id = ? ORDER BY uploaded_at DESC, id DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Logo

	for rows.Next() {
		var l Logo
		if err := rows.Scan(&l.ID, &l.OrgID, &l.Variant, &l.File, &l.UploadedBy, &l.UploadedAt); err != nil {
			return nil, err
		}

		out = append(out, &l)
	}

	return out, rows.Err()
}

type Manager struct {
	ID          int64
	UID         string
	OrgID       int64
	Email       string
	Name        string
	Lang        string
	InvitedBy   string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

const managerCols = `id, uid, organization_id, email, name, lang, invited_by, created_at, last_login_at`

func scanManager(row interface{ Scan(...any) error }) (*Manager, error) {
	var (
		m         Manager
		lastLogin sql.NullTime
	)

	if err := row.Scan(&m.ID, &m.UID, &m.OrgID, &m.Email, &m.Name, &m.Lang, &m.InvitedBy, &m.CreatedAt, &lastLogin); err != nil {
		return nil, notFound(err)
	}

	m.LastLoginAt = timePtr(lastLogin)

	return &m, nil
}

func (s *Store) queryManagers(ctx context.Context, query string, args ...any) ([]*Manager, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Manager

	for rows.Next() {
		m, err := scanManager(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, m)
	}

	return out, rows.Err()
}

func (s *Store) GetManager(ctx context.Context, id int64) (*Manager, error) {
	return scanManager(s.db.QueryRowContext(ctx, `SELECT `+managerCols+` FROM managers WHERE id = ?`, id))
}

// GetManagerByUID finds a manager by its public identifier.
func (s *Store) GetManagerByUID(ctx context.Context, uid string) (*Manager, error) {
	return scanManager(s.db.QueryRowContext(ctx, `SELECT `+managerCols+` FROM managers WHERE uid = ?`, uid))
}

// GetOrganizationBySlug finds an organization by its public identifier.
func (s *Store) GetOrganizationBySlug(ctx context.Context, slug string) (*Organization, error) {
	return scanOrg(s.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM organizations WHERE slug = ?`, slug))
}

func (s *Store) ListManagers(ctx context.Context, orgID int64) ([]*Manager, error) {
	return s.queryManagers(ctx, `SELECT `+managerCols+` FROM managers WHERE organization_id = ? ORDER BY email`, orgID)
}

// ManagersByEmail returns every manager account (one per organization) that
// uses this email.
func (s *Store) ManagersByEmail(ctx context.Context, email string) ([]*Manager, error) {
	return s.queryManagers(ctx, `SELECT `+managerCols+` FROM managers WHERE email = ? ORDER BY id`, NormalizeEmail(email))
}

// AddManager creates a manager, or returns the existing one when the email
// is already a manager of the organization.
func (s *Store) AddManager(ctx context.Context, m *Manager) (created bool, err error) {
	m.Email = NormalizeEmail(m.Email)
	if m.Lang == "" {
		m.Lang = "fr"
	}

	m.CreatedAt = now()

	m.UID = NewUUID()

	res, err := s.db.ExecContext(ctx, `INSERT INTO managers (uid, organization_id, email, name, lang, invited_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (organization_id, email) DO NOTHING`,
		m.UID, m.OrgID, m.Email, m.Name, m.Lang, m.InvitedBy, m.CreatedAt)
	if err != nil {
		return false, err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		existing, err := scanManager(s.db.QueryRowContext(ctx, `SELECT `+managerCols+` FROM managers WHERE organization_id = ? AND email = ?`, m.OrgID, m.Email))
		if err != nil {
			return false, err
		}

		*m = *existing

		return false, nil
	}

	m.ID, err = res.LastInsertId()

	return true, err
}

func (s *Store) UpdateManager(ctx context.Context, m *Manager) error {
	_, err := s.db.ExecContext(ctx, `UPDATE managers SET name = ?, lang = ? WHERE id = ?`, m.Name, m.Lang, m.ID)

	return err
}

// UpdateManagerIdentity changes a manager's email, name and language. It
// returns ErrDuplicate when the email is already a manager of the same
// organization.
func (s *Store) UpdateManagerIdentity(ctx context.Context, m *Manager) error {
	m.Email = NormalizeEmail(m.Email)

	_, err := s.db.ExecContext(ctx, `UPDATE managers SET email = ?, name = ?, lang = ? WHERE id = ?`, m.Email, m.Name, m.Lang, m.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrDuplicate
	}

	return err
}

func (s *Store) TouchManagerLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE managers SET last_login_at = ? WHERE id = ?`, now(), id)

	return err
}

func (s *Store) DeleteManager(ctx context.Context, orgID, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM managers WHERE id = ? AND organization_id = ?`, id, orgID)

	return err
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// PruneLogos keeps the keep most recent versions of an organization's logo
// variant and deletes older ones, never the current logo files. It returns
// the names of the deleted files, to remove from disk.
func (s *Store) PruneLogos(ctx context.Context, orgID int64, variant string, keep int) ([]string, error) {
	var removed []string

	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT l.id, l.file FROM logos l JOIN organizations o ON o.id = l.organization_id
			WHERE l.organization_id = ? AND l.variant = ? AND l.file NOT IN (o.logo_file, o.logo_dark_file)
			ORDER BY l.uploaded_at DESC, l.id DESC LIMIT -1 OFFSET ?`, orgID, variant, max(keep-1, 0))
		if err != nil {
			return err
		}

		var ids []int64

		for rows.Next() {
			var (
				id   int64
				file string
			)

			if err := rows.Scan(&id, &file); err != nil {
				rows.Close()

				return err
			}

			ids = append(ids, id)
			removed = append(removed, file)
		}

		rows.Close()

		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM logos WHERE id = ?`, id); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return removed, nil
}
