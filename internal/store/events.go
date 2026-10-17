package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

type Event struct {
	ID                int64
	Slug              string
	Name              string
	Year              int
	StartsOn          *time.Time
	EndsOn            *time.Time
	IsCurrent         bool
	LogoDeadline      *time.Time
	AttendeesDeadline *time.Time
	BrochureFRFile    string
	BrochureENFile    string
	GuideFRFile       string
	GuideENFile       string
	// PaymentInfo holds payment instructions (bank details, terms).
	PaymentInfo string
	// PaymentMention is a legal mention, e.g. the VAT exemption article.
	PaymentMention string
	CreatedAt      time.Time

	// The volunteer side of the edition. They are written by
	// SaveVolunteerSettings and SetEventLogo only, never by SaveEvent, so
	// the partner forms cannot overwrite them.
	VolunteerStatus   string
	VolunteerFreezeAt *time.Time
	LogoFile          string
	// PurgedAt is when the personal data of the edition was purged.
	PurgedAt *time.Time
	// What the edition provides its volunteers, one per line: t-shirt
	// sizes and meals. Empty asks nothing.
	TshirtSizes string
	Meals       string
}

// Volunteer statuses of an edition.
const (
	VolunteersDraft    = "draft"    // prepared by the staff, hidden from volunteers
	VolunteersOpen     = "open"     // volunteers see it and sign up
	VolunteersFrozen   = "frozen"   // visible, signups closed
	VolunteersArchived = "archived" // hidden
)

// ValidVolunteerStatus tells whether s is one of the volunteer statuses.
func ValidVolunteerStatus(s string) bool {
	switch s {
	case VolunteersDraft, VolunteersOpen, VolunteersFrozen, VolunteersArchived:
		return true
	}

	return false
}

// VisibleToVolunteers tells whether volunteers may browse the edition.
func (e *Event) VisibleToVolunteers() bool {
	return e.VolunteerStatus == VolunteersOpen || e.VolunteerStatus == VolunteersFrozen
}

// AttendeesLocked reports whether partners can no longer edit attendees.
func (e *Event) AttendeesLocked() bool {
	return e.AttendeesDeadline != nil && time.Now().After(e.AttendeesDeadline.Add(24*time.Hour))
}

const eventCols = `id, slug, name, year, starts_on, ends_on, is_current, logo_deadline, attendees_deadline,
	brochure_fr_file, brochure_en_file, guide_fr_file, guide_en_file, payment_info, payment_mention, created_at,
	volunteer_status, volunteer_freeze_at, logo_file, purged_at, tshirt_sizes, meals`

func scanEvent(row interface{ Scan(...any) error }) (*Event, error) {
	var (
		e                                                Event
		starts, ends, logoDL, attendeeDL, freeze, purged sql.NullTime
	)

	err := row.Scan(&e.ID, &e.Slug, &e.Name, &e.Year, &starts, &ends, &e.IsCurrent, &logoDL, &attendeeDL,
		&e.BrochureFRFile, &e.BrochureENFile, &e.GuideFRFile, &e.GuideENFile, &e.PaymentInfo, &e.PaymentMention, &e.CreatedAt,
		&e.VolunteerStatus, &freeze, &e.LogoFile, &purged, &e.TshirtSizes, &e.Meals)
	if err != nil {
		return nil, notFound(err)
	}

	e.StartsOn, e.EndsOn = timePtr(starts), timePtr(ends)
	e.VolunteerFreezeAt, e.PurgedAt = timePtr(freeze), timePtr(purged)
	e.LogoDeadline, e.AttendeesDeadline = timePtr(logoDL), timePtr(attendeeDL)

	return &e, nil
}

func (s *Store) ListEvents(ctx context.Context) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM events ORDER BY year DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Event

	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, e)
	}

	return out, rows.Err()
}

func (s *Store) GetEvent(ctx context.Context, id int64) (*Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events WHERE id = ?`, id))
}

func (s *Store) GetEventBySlug(ctx context.Context, slug string) (*Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events WHERE slug = ?`, slug))
}

// CurrentEvent returns the event flagged as current, falling back to the most
// recent one.
func (s *Store) CurrentEvent(ctx context.Context) (*Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events ORDER BY is_current DESC, year DESC, id DESC LIMIT 1`))
}

func (s *Store) SaveEvent(ctx context.Context, e *Event) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e.IsCurrent {
			if _, err := tx.ExecContext(ctx, `UPDATE events SET is_current = 0 WHERE id != ?`, e.ID); err != nil {
				return err
			}
		}

		args := []any{e.Slug, e.Name, e.Year, nullTime(e.StartsOn), nullTime(e.EndsOn), e.IsCurrent,
			nullTime(e.LogoDeadline), nullTime(e.AttendeesDeadline),
			e.BrochureFRFile, e.BrochureENFile, e.GuideFRFile, e.GuideENFile, e.PaymentInfo, e.PaymentMention}

		if e.ID == 0 {
			e.CreatedAt = now()

			res, err := tx.ExecContext(ctx, `INSERT INTO events (slug, name, year, starts_on, ends_on, is_current,
				logo_deadline, attendees_deadline, brochure_fr_file, brochure_en_file, guide_fr_file, guide_en_file,
				payment_info, payment_mention, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append(args, e.CreatedAt)...)
			if err != nil {
				return err
			}

			if e.ID, err = res.LastInsertId(); err != nil {
				return err
			}

			// Every edition has a default pole for the volunteer slots of no
			// team in particular.
			return createDefaultPole(ctx, tx, e.ID)
		}

		_, err := tx.ExecContext(ctx, `UPDATE events SET slug = ?, name = ?, year = ?, starts_on = ?, ends_on = ?, is_current = ?,
			logo_deadline = ?, attendees_deadline = ?, brochure_fr_file = ?, brochure_en_file = ?, guide_fr_file = ?, guide_en_file = ?,
			payment_info = ?, payment_mention = ? WHERE id = ?`, append(args, e.ID)...)

		return err
	})
}

// SaveVolunteerSettings saves the volunteer status and freeze date of an
// edition.
func (s *Store) SaveVolunteerSettings(ctx context.Context, e *Event) error {
	if !ValidVolunteerStatus(e.VolunteerStatus) {
		return ErrBadInput
	}

	_, err := s.db.ExecContext(ctx, `UPDATE events SET volunteer_status = ?, volunteer_freeze_at = ?, tshirt_sizes = ?, meals = ? WHERE id = ?`,
		e.VolunteerStatus, nullTime(e.VolunteerFreezeAt), cleanLines(e.TshirtSizes), cleanLines(e.Meals), e.ID)

	return err
}

// SetEventLogo sets or clears the logo of an edition. The caller owns the
// file in the files store.
func (s *Store) SetEventLogo(ctx context.Context, eventID int64, file string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET logo_file = ? WHERE id = ?`, file, eventID)

	return err
}

type Level struct {
	ID           int64
	UID          string
	EventID      int64
	Name         string
	Position     int
	Price        int
	MaxPartners  int
	MaxAttendees int
	// TaxMode is TaxExcluded, TaxIncluded or TaxNone.
	TaxMode string
	// Color is the medal color (#rrggbb), empty for the default.
	Color string
	// Taken is the number of confirmed partnerships at this level. It is
	// only filled by ListLevels.
	Taken int
}

// Tax modes of a level price.
const (
	TaxExcluded = "ht"
	TaxIncluded = "ttc"
	TaxNone     = "none"
)

var TaxModes = []string{TaxExcluded, TaxIncluded, TaxNone}

func ValidTaxMode(m string) bool {
	return m == TaxExcluded || m == TaxIncluded || m == TaxNone
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validColor(c string) bool { return colorRe.MatchString(c) }

// Remaining returns the number of available slots, or -1 when unlimited.
func (l *Level) Remaining() int {
	if l.MaxPartners <= 0 {
		return -1
	}

	return max(l.MaxPartners-l.Taken, 0)
}

func (s *Store) ListLevels(ctx context.Context, eventID int64) ([]*Level, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.uid, l.event_id, l.name, l.position, l.price, l.max_partners, l.max_attendees, l.tax_mode, l.color,
		(SELECT COUNT(*) FROM partnerships p WHERE p.level_id = l.id AND p.status = 'confirmed')
		FROM levels l WHERE l.event_id = ? ORDER BY l.position DESC, l.price DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Level

	for rows.Next() {
		var l Level
		if err := rows.Scan(&l.ID, &l.UID, &l.EventID, &l.Name, &l.Position, &l.Price, &l.MaxPartners, &l.MaxAttendees, &l.TaxMode, &l.Color, &l.Taken); err != nil {
			return nil, err
		}

		out = append(out, &l)
	}

	return out, rows.Err()
}

func (s *Store) GetLevel(ctx context.Context, id int64) (*Level, error) {
	return s.getLevel(ctx, `id = ?`, id)
}

// GetLevelByUID finds a level of an edition by its public identifier.
func (s *Store) GetLevelByUID(ctx context.Context, eventID int64, uid string) (*Level, error) {
	return s.getLevel(ctx, `event_id = ? AND uid = ?`, eventID, uid)
}

func (s *Store) getLevel(ctx context.Context, where string, args ...any) (*Level, error) {
	var l Level

	err := s.db.QueryRowContext(ctx, `SELECT id, uid, event_id, name, position, price, max_partners, max_attendees, tax_mode, color
		FROM levels WHERE `+where, args...).
		Scan(&l.ID, &l.UID, &l.EventID, &l.Name, &l.Position, &l.Price, &l.MaxPartners, &l.MaxAttendees, &l.TaxMode, &l.Color)
	if err != nil {
		return nil, notFound(err)
	}

	return &l, nil
}

func (s *Store) SaveLevel(ctx context.Context, l *Level) error {
	if !ValidTaxMode(l.TaxMode) {
		l.TaxMode = TaxExcluded
	}

	if !validColor(l.Color) {
		l.Color = ""
	}

	if l.ID == 0 {
		l.UID = NewUUID()

		res, err := s.db.ExecContext(ctx, `INSERT INTO levels (uid, event_id, name, position, price, max_partners, max_attendees, tax_mode, color, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, l.UID, l.EventID, l.Name, l.Position, l.Price, l.MaxPartners, l.MaxAttendees, l.TaxMode, l.Color, now())
		if err != nil {
			return err
		}

		l.ID, err = res.LastInsertId()

		return err
	}

	_, err := s.db.ExecContext(ctx, `UPDATE levels SET name = ?, position = ?, price = ?, max_partners = ?, max_attendees = ?, tax_mode = ?, color = ? WHERE id = ?`,
		l.Name, l.Position, l.Price, l.MaxPartners, l.MaxAttendees, l.TaxMode, l.Color, l.ID)

	return err
}

func (s *Store) DeleteLevel(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM levels WHERE id = ?`, id)

	return err
}

type Perk struct {
	ID       int64
	UID      string
	EventID  int64
	NameFR   string
	NameEN   string
	Position int
}

func (p *Perk) Name(lang string) string {
	if lang == "en" && p.NameEN != "" {
		return p.NameEN
	}

	return p.NameFR
}

func (s *Store) ListPerks(ctx context.Context, eventID int64) ([]*Perk, error) {
	return s.queryPerks(ctx, `SELECT id, uid, event_id, name_fr, name_en, position FROM perks WHERE event_id = ? ORDER BY position, id`, eventID)
}

// ListLevelPerks returns the perks included in a level.
func (s *Store) ListLevelPerks(ctx context.Context, levelID int64) ([]*Perk, error) {
	return s.queryPerks(ctx, `SELECT p.id, p.uid, p.event_id, p.name_fr, p.name_en, p.position FROM perks p
		JOIN level_perks lp ON lp.perk_id = p.id WHERE lp.level_id = ? ORDER BY p.position, p.id`, levelID)
}

func (s *Store) queryPerks(ctx context.Context, query string, args ...any) ([]*Perk, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Perk

	for rows.Next() {
		var p Perk
		if err := rows.Scan(&p.ID, &p.UID, &p.EventID, &p.NameFR, &p.NameEN, &p.Position); err != nil {
			return nil, err
		}

		out = append(out, &p)
	}

	return out, rows.Err()
}

// GetPerkByUID finds a perk of an edition by its public identifier.
func (s *Store) GetPerkByUID(ctx context.Context, eventID int64, uid string) (*Perk, error) {
	perks, err := s.queryPerks(ctx, `SELECT id, uid, event_id, name_fr, name_en, position FROM perks WHERE event_id = ? AND uid = ?`, eventID, uid)
	if err != nil {
		return nil, err
	}

	if len(perks) == 0 {
		return nil, ErrNotFound
	}

	return perks[0], nil
}

func (s *Store) SavePerk(ctx context.Context, p *Perk) error {
	if p.ID == 0 {
		p.UID = NewUUID()

		res, err := s.db.ExecContext(ctx, `INSERT INTO perks (uid, event_id, name_fr, name_en, position) VALUES (?, ?, ?, ?, ?)`,
			p.UID, p.EventID, p.NameFR, p.NameEN, p.Position)
		if err != nil {
			return err
		}

		p.ID, err = res.LastInsertId()

		return err
	}

	_, err := s.db.ExecContext(ctx, `UPDATE perks SET name_fr = ?, name_en = ?, position = ? WHERE id = ?`, p.NameFR, p.NameEN, p.Position, p.ID)

	return err
}

func (s *Store) DeletePerk(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM perks WHERE id = ?`, id)

	return err
}

// LevelPerkMatrix returns, for an event, the set of (level, perk) pairs that
// are enabled.
func (s *Store) LevelPerkMatrix(ctx context.Context, eventID int64) (map[[2]int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lp.level_id, lp.perk_id FROM level_perks lp
		JOIN levels l ON l.id = lp.level_id WHERE l.event_id = ?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[[2]int64]bool{}

	for rows.Next() {
		var k [2]int64
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return nil, err
		}

		out[k] = true
	}

	return out, rows.Err()
}

// SetLevelPerkMatrix replaces all level/perk associations of an event.
func (s *Store) SetLevelPerkMatrix(ctx context.Context, eventID int64, pairs [][2]int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM level_perks WHERE level_id IN (SELECT id FROM levels WHERE event_id = ?)`, eventID); err != nil {
			return err
		}

		for _, p := range pairs {
			// The sub-select guarantees both ids belong to this event.
			_, err := tx.ExecContext(ctx, `INSERT INTO level_perks (level_id, perk_id)
				SELECT l.id, p.id FROM levels l, perks p WHERE l.id = ? AND p.id = ? AND l.event_id = ? AND p.event_id = ?`,
				p[0], p[1], eventID, eventID)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// CloneResult counts what CloneLevelsAndPerks added.
type CloneResult struct {
	Levels, Perks, Pairs int
}

// CloneLevelsAndPerks copies the levels, perks and level/perk associations of
// one edition into another, in a single transaction. Levels and perks that
// already exist in the target (same name, case-insensitive) are kept as is and
// only receive the missing associations; nothing is removed.
func (s *Store) CloneLevelsAndPerks(ctx context.Context, srcEventID, dstEventID int64) (*CloneResult, error) {
	if srcEventID == dstEventID {
		return nil, errors.New("source and target editions are the same")
	}

	res := &CloneResult{}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		// Levels: source id -> target id.
		levelMap := map[int64]int64{}

		existingLevels := map[string]int64{}
		if err := collectIDs(ctx, tx, `SELECT id, name FROM levels WHERE event_id = ?`, dstEventID, existingLevels); err != nil {
			return err
		}

		rows, err := tx.QueryContext(ctx, `SELECT id, name, position, price, max_partners, max_attendees, tax_mode, color
			FROM levels WHERE event_id = ? ORDER BY position DESC`, srcEventID)
		if err != nil {
			return err
		}

		var levels []Level

		for rows.Next() {
			var l Level
			if err := rows.Scan(&l.ID, &l.Name, &l.Position, &l.Price, &l.MaxPartners, &l.MaxAttendees, &l.TaxMode, &l.Color); err != nil {
				rows.Close()

				return err
			}

			levels = append(levels, l)
		}

		rows.Close()

		for _, l := range levels {
			if id, ok := existingLevels[strings.ToLower(l.Name)]; ok {
				levelMap[l.ID] = id

				continue
			}

			r, err := tx.ExecContext(ctx, `INSERT INTO levels (uid, event_id, name, position, price, max_partners, max_attendees, tax_mode, color, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, NewUUID(), dstEventID, l.Name, l.Position, l.Price, l.MaxPartners, l.MaxAttendees, l.TaxMode, l.Color, now())
			if err != nil {
				return err
			}

			id, err := r.LastInsertId()
			if err != nil {
				return err
			}

			levelMap[l.ID] = id
			res.Levels++
		}

		// Perks: source id -> target id.
		perkMap := map[int64]int64{}

		existingPerks := map[string]int64{}
		if err := collectIDs(ctx, tx, `SELECT id, name_fr FROM perks WHERE event_id = ?`, dstEventID, existingPerks); err != nil {
			return err
		}

		rows, err = tx.QueryContext(ctx, `SELECT id, name_fr, name_en, position FROM perks WHERE event_id = ? ORDER BY position, id`, srcEventID)
		if err != nil {
			return err
		}

		var perks []Perk

		for rows.Next() {
			var p Perk
			if err := rows.Scan(&p.ID, &p.NameFR, &p.NameEN, &p.Position); err != nil {
				rows.Close()

				return err
			}

			perks = append(perks, p)
		}

		rows.Close()

		for _, p := range perks {
			if id, ok := existingPerks[strings.ToLower(p.NameFR)]; ok {
				perkMap[p.ID] = id

				continue
			}

			r, err := tx.ExecContext(ctx, `INSERT INTO perks (uid, event_id, name_fr, name_en, position) VALUES (?, ?, ?, ?, ?)`,
				NewUUID(), dstEventID, p.NameFR, p.NameEN, p.Position)
			if err != nil {
				return err
			}

			id, err := r.LastInsertId()
			if err != nil {
				return err
			}

			perkMap[p.ID] = id
			res.Perks++
		}

		// Associations.
		rows, err = tx.QueryContext(ctx, `SELECT lp.level_id, lp.perk_id FROM level_perks lp
			JOIN levels l ON l.id = lp.level_id WHERE l.event_id = ?`, srcEventID)
		if err != nil {
			return err
		}

		var pairs [][2]int64

		for rows.Next() {
			var k [2]int64
			if err := rows.Scan(&k[0], &k[1]); err != nil {
				rows.Close()

				return err
			}

			pairs = append(pairs, k)
		}

		rows.Close()

		for _, k := range pairs {
			r, err := tx.ExecContext(ctx, `INSERT INTO level_perks (level_id, perk_id) VALUES (?, ?) ON CONFLICT DO NOTHING`,
				levelMap[k[0]], perkMap[k[1]])
			if err != nil {
				return err
			}

			if n, _ := r.RowsAffected(); n > 0 {
				res.Pairs++
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// collectIDs fills out with lowercase name -> id from a (id, name) query.
func collectIDs(ctx context.Context, tx *sql.Tx, query string, arg any, out map[string]int64) error {
	rows, err := tx.QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id   int64
			name string
		)

		if err := rows.Scan(&id, &name); err != nil {
			return err
		}

		out[strings.ToLower(name)] = id
	}

	return rows.Err()
}
