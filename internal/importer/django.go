// Package importer imports data from the previous Django application
// (manage.example.org.old): editions, levels, perks, organizations
// and partnerships. It is idempotent: existing rows are matched and reused,
// so running it twice creates nothing new.
package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"manage/internal/store"
)

// placeholderCities are default values of the old application, not real
// addresses.
var placeholderCities = map[string]bool{"somewhere": true, "somewhere": true}

// Report counts what the import created or reused.
type Report struct {
	EventsCreated, EventsReused             int
	LevelsCreated, LevelsReused             int
	PerksCreated, PerksReused               int
	OrgsCreated, OrgsReused                 int
	PartnershipsCreated, PartnershipsExists int
	OrganizersSkipped                       int
}

// Django imports the Django SQLite database at path into st. With dryRun,
// nothing is written and the report shows what would happen. Progress is
// written to log.
func Django(ctx context.Context, st *store.Store, path string, dryRun bool, log io.Writer) (*Report, error) {
	old, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer old.Close()

	if err := old.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	imp := &importer{ctx: ctx, st: st, old: old, dryRun: dryRun, log: log, report: &Report{}}

	return imp.report, imp.run()
}

type importer struct {
	ctx    context.Context
	st     *store.Store
	old    *sql.DB
	dryRun bool
	log    io.Writer
	report *Report
}

func (i *importer) logf(format string, args ...any) { fmt.Fprintf(i.log, format+"\n", args...) }

type oldEvent struct {
	id, name, organizerID string
	year                  int
}

func (i *importer) run() error {
	rows, err := i.old.QueryContext(i.ctx, `SELECT id, year, name, COALESCE(organizer_id, '') FROM form_event ORDER BY year`)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	var events []oldEvent

	for rows.Next() {
		var e oldEvent
		if err := rows.Scan(&e.id, &e.year, &e.name, &e.organizerID); err != nil {
			rows.Close()

			return err
		}

		events = append(events, e)
	}

	rows.Close()

	for _, e := range events {
		if err := i.importEvent(e); err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}
	}

	return nil
}

func (i *importer) importEvent(oe oldEvent) error {
	slug := store.Slugify(oe.name)

	ev, err := i.st.GetEventBySlug(i.ctx, slug)

	switch {
	case err == nil:
		i.report.EventsReused++
		i.logf("édition %q : existe déjà", oe.name)
	case errors.Is(err, store.ErrNotFound):
		ev = &store.Event{Slug: slug, Name: oe.name, Year: oe.year}
		i.report.EventsCreated++
		i.logf("édition %q : créée", oe.name)

		if !i.dryRun {
			if err := i.st.SaveEvent(i.ctx, ev); err != nil {
				return err
			}
		}
	default:
		return err
	}

	levels, err := i.importLevels(oe, ev)
	if err != nil {
		return err
	}

	if err := i.importPerks(oe, ev, levels); err != nil {
		return err
	}

	return i.importPartnerships(oe, ev, levels)
}

// importLevels returns the new level for each old level id.
func (i *importer) importLevels(oe oldEvent, ev *store.Event) (map[string]*store.Level, error) {
	var existing []*store.Level

	if ev.ID != 0 {
		var err error
		if existing, err = i.st.ListLevels(i.ctx, ev.ID); err != nil {
			return nil, err
		}
	}

	rows, err := i.old.QueryContext(i.ctx, `SELECT id, name, level, price, "limit" FROM form_partnerlevel WHERE event_id = ? ORDER BY level DESC`, oe.id)
	if err != nil {
		return nil, fmt.Errorf("read levels: %w", err)
	}
	defer rows.Close()

	out := map[string]*store.Level{}

	for rows.Next() {
		var (
			id string
			l  store.Level
		)

		if err := rows.Scan(&id, &l.Name, &l.Position, &l.Price, &l.MaxPartners); err != nil {
			return nil, err
		}

		found := false

		for _, e := range existing {
			if strings.EqualFold(e.Name, l.Name) {
				out[id], found = e, true
			}
		}

		if found {
			i.report.LevelsReused++

			continue
		}

		l.EventID = ev.ID
		i.report.LevelsCreated++
		i.logf("  niveau %s (%d €) : créé", l.Name, l.Price)

		if !i.dryRun {
			if err := i.st.SaveLevel(i.ctx, &l); err != nil {
				return nil, err
			}
		}

		out[id] = &l
	}

	return out, rows.Err()
}

func (i *importer) importPerks(oe oldEvent, ev *store.Event, levels map[string]*store.Level) error {
	var existing []*store.Perk

	matrix := map[[2]int64]bool{}

	if ev.ID != 0 {
		var err error
		if existing, err = i.st.ListPerks(i.ctx, ev.ID); err != nil {
			return err
		}

		if matrix, err = i.st.LevelPerkMatrix(i.ctx, ev.ID); err != nil {
			return err
		}
	}

	// Options are global in the old schema; only those associated with a
	// level of this event are imported.
	rows, err := i.old.QueryContext(i.ctx, `SELECT DISTINCT o.id, o.description, a.partner_level_id
		FROM form_partnerleveloptionassociation a
		JOIN form_partnerleveloption o ON o.id = a.partner_level_option_id
		JOIN form_partnerlevel l ON l.id = a.partner_level_id
		WHERE l.event_id = ? AND a.active = 1 ORDER BY o.description`, oe.id)
	if err != nil {
		return fmt.Errorf("read perks: %w", err)
	}
	defer rows.Close()

	perks := map[string]*store.Perk{}
	position := len(existing)

	for rows.Next() {
		var optionID, description, levelID string
		if err := rows.Scan(&optionID, &description, &levelID); err != nil {
			return err
		}

		p, ok := perks[optionID]
		if !ok {
			for _, e := range existing {
				if strings.EqualFold(e.NameFR, description) {
					p = e
				}
			}

			if p == nil {
				position++
				p = &store.Perk{EventID: ev.ID, NameFR: description, Position: position}
				i.report.PerksCreated++
				i.logf("  contrepartie %q : créée", description)

				if !i.dryRun {
					if err := i.st.SavePerk(i.ctx, p); err != nil {
						return err
					}
				}
			} else {
				i.report.PerksReused++
			}

			perks[optionID] = p
		}

		if l := levels[levelID]; l != nil {
			matrix[[2]int64{l.ID, p.ID}] = true
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if i.dryRun || ev.ID == 0 {
		return nil
	}

	pairs := make([][2]int64, 0, len(matrix))
	for k := range matrix {
		pairs = append(pairs, k)
	}

	return i.st.SetLevelPerkMatrix(i.ctx, ev.ID, pairs)
}

func (i *importer) importPartnerships(oe oldEvent, ev *store.Event, levels map[string]*store.Level) error {
	orgs, err := i.st.ListOrganizations(i.ctx)
	if err != nil {
		return err
	}

	byKey := map[string]*store.Organization{}
	for _, o := range orgs {
		byKey[nameKey(o.Name)] = o
	}

	rows, err := i.old.QueryContext(i.ctx, `SELECT o.id, o.name, o.address_line_1, o.address_line_2, o.zip_code, o.city, o.country,
		COALESCE(p.level_id, '')
		FROM form_partner p JOIN form_organization o ON o.id = p.organization_id
		WHERE p.event_id = ? ORDER BY o.name`, oe.id)
	if err != nil {
		return fmt.Errorf("read partners: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var oldOrgID, levelID string

		o := &store.Organization{}
		if err := rows.Scan(&oldOrgID, &o.Name, &o.AddressLine1, &o.AddressLine2, &o.ZipCode, &o.City, &o.Country, &levelID); err != nil {
			return err
		}

		if oldOrgID == oe.organizerID {
			i.report.OrganizersSkipped++
			i.logf("  %s : organisateur de l'édition, ignoré", o.Name)

			continue
		}

		if placeholderCities[strings.ToLower(strings.TrimSpace(o.City))] {
			o.City = ""
		}

		org, reused := byKey[nameKey(o.Name)]

		if reused {
			i.report.OrgsReused++
		} else {
			i.report.OrgsCreated++

			if !i.dryRun {
				if err := i.st.CreateOrganization(i.ctx, o); err != nil {
					return err
				}
			}

			org = o
			byKey[nameKey(o.Name)] = org
		}

		p := &store.Partnership{OrgID: org.ID, EventID: ev.ID, Status: store.StatusConfirmed}

		levelName := "sans niveau"
		if l := levels[levelID]; l != nil {
			p.LevelID, levelName = &l.ID, l.Name
		}

		what := "organisation existante"
		if !reused {
			what = "organisation créée"
		}

		if i.dryRun {
			exists, err := i.hasPartnership(org.ID, ev.ID)
			if err != nil {
				return err
			}

			if exists {
				i.report.PartnershipsExists++
				i.logf("  %s : partenariat déjà présent", org.Name)
			} else {
				i.report.PartnershipsCreated++
				i.logf("  %s (%s) : partenariat %s à créer", org.Name, what, levelName)
			}

			continue
		}

		err := i.st.CreatePartnership(i.ctx, p)

		switch {
		case errors.Is(err, store.ErrDuplicate):
			i.report.PartnershipsExists++
			i.logf("  %s : partenariat déjà présent", org.Name)
		case err != nil:
			return err
		default:
			i.report.PartnershipsCreated++
			i.logf("  %s (%s) : partenariat %s créé", org.Name, what, levelName)
		}
	}

	return rows.Err()
}

// hasPartnership reports whether the organization is already attached to the
// event; ids are zero for rows a dry run would create.
func (i *importer) hasPartnership(orgID, eventID int64) (bool, error) {
	if orgID == 0 || eventID == 0 {
		return false, nil
	}

	partnerships, err := i.st.ListOrgPartnerships(i.ctx, orgID)
	if err != nil {
		return false, err
	}

	for _, p := range partnerships {
		if p.EventID == eventID {
			return true, nil
		}
	}

	return false, nil
}

// nameKey compares organization names regardless of case, spaces and
// punctuation, so "DataBene" matches "Data Bene".
func nameKey(name string) string {
	return strings.ReplaceAll(store.Slugify(name), "-", "")
}
