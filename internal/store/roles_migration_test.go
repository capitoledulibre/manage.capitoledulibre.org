package store

import (
	"context"
	"regexp"
	"slices"
	"testing"
	"time"
)

// TestTitlesToRoles runs 0010_titles_to_roles.sql again on slots made
// as before it: a slot's title becomes its pole's role.
func TestTitlesToRoles(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	e := newEvent(t, s, "ec-2026", 2026)

	pole := &Pole{EventID: e.ID, Name: "Logistique"}
	other := &Pole{EventID: e.ID, Name: "Buvette"}

	for _, p := range []*Pole{pole, other} {
		if err := s.CreatePole(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	hall, err := s.CreateLocation(ctx, pole.ID, "Hall")
	if err != nil {
		t.Fatal(err)
	}

	welcome, err := s.CreateLocation(ctx, pole.ID, "Accueil")
	if err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	slot := func(p *Pole, loc int64, title string, h int) *Slot {
		sl := &Slot{PoleID: p.ID, LocationID: loc, Title: title, StartsAt: at.Add(time.Duration(h) * time.Hour), EndsAt: at.Add(time.Duration(h+1) * time.Hour), Capacity: 1}
		if err := s.CreateSlot(ctx, sl); err != nil {
			t.Fatal(err)
		}

		return sl
	}

	badges := slot(pole, 0, "Badges", 2)
	badges2 := slot(pole, 0, "Badges", 3)
	vestiaire := slot(pole, 0, "Vestiaire", 1)
	toWelcome := slot(pole, 0, "Accueil", 4)       // a location already named so
	located := slot(pole, hall.ID, "Ouverture", 0) // keeps its location and title
	whole := slot(pole, 0, "", 5)                  // the whole pole, untouched
	bar := slot(other, 0, "Badges", 0)             // another pole, its own role

	body, err := migrationsFS.ReadFile("migrations/0010_titles_to_roles.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx, string(body)); err != nil {
		t.Fatal(err)
	}

	locs, err := s.ListLocations(ctx, pole.ID)
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	byName := map[string]*Location{}
	uid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	for _, l := range locs {
		names = append(names, l.Name)
		byName[l.Name] = l

		if !uid.MatchString(l.UID) {
			t.Errorf("location %q: uid %q is not a UUID", l.Name, l.UID)
		}
	}

	// The new roles come after the pole's, in the order they first run.
	if want := []string{"Hall", "Accueil", "Vestiaire", "Badges"}; !slices.Equal(names, want) {
		t.Fatalf("roles %v, want %v", names, want)
	}

	for _, c := range []struct {
		slot  *Slot
		loc   int64
		title string
	}{
		{badges, byName["Badges"].ID, ""},
		{badges2, byName["Badges"].ID, ""},
		{vestiaire, byName["Vestiaire"].ID, ""},
		{toWelcome, welcome.ID, ""},
		{located, hall.ID, "Ouverture"},
		{whole, 0, ""},
	} {
		got, err := s.GetSlot(ctx, c.slot.ID, 0)
		if err != nil {
			t.Fatal(err)
		}

		if got.LocationID != c.loc || got.Title != c.title {
			t.Errorf("slot at %s: location %d %q, want %d %q", got.StartsAt.Format("15:04"), got.LocationID, got.Title, c.loc, c.title)
		}
	}

	others, _ := s.ListLocations(ctx, other.ID)
	if got, _ := s.GetSlot(ctx, bar.ID, 0); len(others) != 1 || got.LocationID != others[0].ID {
		t.Errorf("the other pole's slot: %+v, roles %+v", got, others)
	}
}
