package importer

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"

	"manage/internal/store"
)

// djangoFixture builds a minimal database with the old Django schema.
func djangoFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "django.sqlite3")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE form_organization (id TEXT PRIMARY KEY, name TEXT, address_line_1 TEXT, address_line_2 TEXT, zip_code TEXT, city TEXT, country TEXT);
		CREATE TABLE form_event (id TEXT PRIMARY KEY, year INTEGER, name TEXT, slug TEXT, organizer_id TEXT);
		CREATE TABLE form_partnerlevel (id TEXT PRIMARY KEY, event_id TEXT, name TEXT, level INTEGER, price INTEGER, "limit" INTEGER);
		CREATE TABLE form_partnerleveloption (id TEXT PRIMARY KEY, description TEXT);
		CREATE TABLE form_partnerleveloptionassociation (id TEXT PRIMARY KEY, partner_level_option_id TEXT, partner_level_id TEXT, active BOOLEAN);
		CREATE TABLE form_partner (id TEXT PRIMARY KEY, organization_id TEXT, event_id TEXT, level_id TEXT);

		INSERT INTO form_organization VALUES ('o1', 'Mon association', '', '', '31500', 'Toulouse', ''),
			('o2', 'DataBene', '', '', '', 'Somewhere', ''), ('o3', 'Centreon', '1 rue X', '', '31000', 'Toulouse', 'France');
		INSERT INTO form_event VALUES ('e1', 2024, 'Example Conf 2024', 'ec-2024', 'o1');
		INSERT INTO form_partnerlevel VALUES ('l1', 'e1', 'Platine', 3, 2000, 0), ('l2', 'e1', 'Bronze', 0, 250, 0);
		INSERT INTO form_partnerleveloption VALUES ('p1', 'Logo sur le site web'), ('p2', 'Stand'), ('p3', 'Unused');
		INSERT INTO form_partnerleveloptionassociation VALUES ('a1', 'p1', 'l1', 1), ('a2', 'p1', 'l1', 1), ('a3', 'p1', 'l2', 1),
			('a4', 'p2', 'l1', 1), ('a5', 'p3', 'l2', 0);
		INSERT INTO form_partner VALUES ('x1', 'o1', 'e1', NULL), ('x2', 'o2', 'e1', 'l2'), ('x3', 'o3', 'e1', 'l1');
	`)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDjangoImport(t *testing.T) {
	ctx := context.Background()
	old := djangoFixture(t)

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// An organization already created under a slightly different name is
	// reused.
	existing := &store.Organization{Name: "Data Bene"}
	if err := st.CreateOrganization(ctx, existing); err != nil {
		t.Fatal(err)
	}

	// A dry run writes nothing.
	r, err := Django(ctx, st, old, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if r.PartnershipsCreated != 2 || r.OrgsCreated != 1 || r.OrgsReused != 1 || r.OrganizersSkipped != 1 {
		t.Errorf("dry run report: %+v", r)
	}

	if events, _ := st.ListEvents(ctx); len(events) != 0 {
		t.Fatal("dry run wrote an event")
	}

	r, err = Django(ctx, st, old, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if r.EventsCreated != 1 || r.LevelsCreated != 2 || r.PerksCreated != 2 || r.PartnershipsCreated != 2 {
		t.Errorf("import report: %+v", r)
	}

	ev, err := st.GetEventBySlug(ctx, "example-conf-2024")
	if err != nil {
		t.Fatal(err)
	}

	orgs, _ := st.ListOrganizations(ctx)
	if len(orgs) != 2 {
		t.Errorf("organizations: %d (organizer must be skipped, DataBene reused)", len(orgs))
	}

	for _, o := range orgs {
		if o.Name == "Centreon" && (o.City != "Toulouse" || o.AddressLine1 != "1 rue X") {
			t.Errorf("address not imported: %+v", o)
		}
	}

	parts, _ := st.ListOrgPartnerships(ctx, existing.ID)
	if len(parts) != 1 || parts[0].Status != store.StatusConfirmed || parts[0].LevelID == nil {
		t.Errorf("reused organization partnership: %+v", parts)
	}

	matrix, _ := st.LevelPerkMatrix(ctx, ev.ID)
	if len(matrix) != 3 {
		t.Errorf("level/perk pairs: %d, want 3 (duplicates merged, inactive ignored)", len(matrix))
	}

	// Running it again creates nothing.
	r, err = Django(ctx, st, old, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if r.EventsCreated+r.LevelsCreated+r.PerksCreated+r.OrgsCreated+r.PartnershipsCreated != 0 || r.PartnershipsExists != 2 {
		t.Errorf("second run: %+v", r)
	}
}
